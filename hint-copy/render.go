package main

import (
	"io"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

// styles holds the precomputed SGR sequences for one frame.
type styles struct {
	hint  string // the label cells
	match string // the rest of the matched text
	dim   string // labels ruled out by the typed prefix, footer
	reset string
}

func makeStyles(cfg Config) styles {
	return styles{
		hint:  "\x1b[1;" + colorCode(cfg.HintFg, false) + ";" + colorCode(cfg.HintBg, true) + "m",
		match: "\x1b[" + colorCode(cfg.MatchFg, false) + "m",
		dim:   "\x1b[2m",
		reset: "\x1b[0m",
	}
}

// colorCode maps a color name or a 0-255 number to an SGR parameter.
func colorCode(name string, bg bool) string {
	named := map[string]int{
		"black": 30, "red": 31, "green": 32, "yellow": 33,
		"blue": 34, "magenta": 35, "cyan": 36, "white": 37,
		"bright-black": 90, "gray": 90, "bright-red": 91, "bright-green": 92,
		"bright-yellow": 93, "bright-blue": 94, "bright-magenta": 95,
		"bright-cyan": 96, "bright-white": 97,
	}
	if code, ok := named[strings.ToLower(name)]; ok {
		if bg {
			code += 10
		}
		return strconv.Itoa(code)
	}
	if n, err := strconv.Atoi(name); err == nil && n >= 0 && n <= 255 {
		if bg {
			return "48;5;" + strconv.Itoa(n)
		}
		return "38;5;" + strconv.Itoa(n)
	}
	// Unknown name: default foreground / background.
	if bg {
		return "49"
	}
	return "39"
}

// viewWindow decides which slice of the captured lines fits the overlay: one
// row is reserved for the footer, and when the capture is taller than the
// view the bottom lines win. Renderers and the UI share it so they agree on
// what is visible.
func viewWindow(nLines, height int) (offset, view int) {
	view = height - 1
	if view < 1 {
		view = 1
	}
	if nLines > view {
		offset = nLines - view
	}
	return offset, view
}

// Render paints one full frame: the captured screen with hint labels replacing
// the leading cells of each candidate (tmux-thumbs style), plus a footer.
// Lines are emitted top-to-bottom in full, so wide/CJK characters keep their
// natural alignment without any column bookkeeping across the frame.
func Render(w io.Writer, screen string, cands []Candidate, lm *LabelMap, prefix string, width, height int, cfg Config) {
	st := makeStyles(cfg)
	lines := strings.Split(screen, "\n")
	offset, view := viewWindow(len(lines), height)
	byLine := make(map[int][]Candidate)
	for _, c := range cands {
		if c.Line >= offset {
			byLine[c.Line-offset] = append(byLine[c.Line-offset], c)
		}
	}

	var sb strings.Builder
	sb.WriteString("\x1b[H")
	shown := lines[offset:]
	for li := 0; li < view; li++ {
		if li < len(shown) {
			renderLine(&sb, shown[li], byLine[li], lm, prefix, width, st)
		}
		sb.WriteString("\x1b[K\r\n")
	}
	if height >= 2 {
		footer := " type a label to copy · space: line mode · esc to cancel"
		if prefix != "" {
			footer = " " + prefix + "_ ·" + footer
		}
		sb.WriteString(st.dim)
		sb.WriteString(runewidth.Truncate(footer, width, ""))
		sb.WriteString(st.reset)
	}
	sb.WriteString("\x1b[K")
	io.WriteString(w, sb.String())
}

// RenderLines paints a line-mode frame: every non-empty line gets its hint
// label in a uniform left margin (content shifts right, so columns across
// lines stay aligned), the anchor line is shown in inverse video, and the
// footer explains the pending state. It is deliberately separate from Render:
// token mode substitutes labels in place, line mode adds a margin — the two
// share almost no drawing logic.
func RenderLines(w io.Writer, screen string, ll *LineLabels, prefix string, anchor, width, height int, cfg Config) {
	st := makeStyles(cfg)
	lines := strings.Split(screen, "\n")
	offset, view := viewWindow(len(lines), height)
	margin := 0
	if ll.Width() > 0 {
		margin = ll.Width() + 1
	}

	var sb strings.Builder
	sb.WriteString("\x1b[H")
	for li := 0; li < view; li++ {
		abs := offset + li
		if abs < len(lines) {
			line := lines[abs]
			label := ll.LabelFor(abs)
			isAnchor := abs == anchor
			if isAnchor {
				sb.WriteString("\x1b[7m")
			}
			if label != "" {
				style := st.hint
				if prefix != "" && !strings.HasPrefix(label, prefix) {
					style = st.dim
				}
				sb.WriteString(style)
				sb.WriteString(label)
				sb.WriteString(st.reset)
				if isAnchor {
					sb.WriteString("\x1b[7m")
				}
				sb.WriteString(strings.Repeat(" ", margin-len(label)))
			}
			// Unlabeled lines are exactly the blank ones — no margin needed.
			writeClipped(&sb, line, width-margin)
			if isAnchor {
				sb.WriteString(st.reset)
			}
		}
		sb.WriteString("\x1b[K\r\n")
	}
	if height >= 2 {
		footer := " line mode: type a label · space: token mode · esc to cancel"
		if anchor >= 0 {
			footer = " enter/same label: copy this line · another label: copy range · esc: cancel"
		} else if prefix != "" {
			footer = " " + prefix + "_ ·" + footer
		}
		sb.WriteString(st.dim)
		sb.WriteString(runewidth.Truncate(footer, width, ""))
		sb.WriteString(st.reset)
	}
	sb.WriteString("\x1b[K")
	io.WriteString(w, sb.String())
}

// writeClipped emits line runes up to maxWidth display columns, skipping \r.
func writeClipped(sb *strings.Builder, line string, maxWidth int) {
	col := 0
	for _, r := range line {
		if r == '\r' {
			continue
		}
		wdt := runewidth.RuneWidth(r)
		if col+wdt > maxWidth {
			return
		}
		sb.WriteRune(r)
		col += wdt
	}
}

// renderLine emits one screen line, overlaying each candidate's label over the
// first cells of its match. lineCands must be sorted by StartRune and
// non-overlapping (Extract guarantees both). Candidate text is ASCII by
// construction, so replacing N leading runes costs exactly N columns.
func renderLine(sb *strings.Builder, line string, lineCands []Candidate, lm *LabelMap, prefix string, width int, st styles) {
	runes := []rune(line)
	col, i, ci := 0, 0, 0
	for i < len(runes) && col < width {
		for ci < len(lineCands) && lineCands[ci].StartRune < i {
			ci++
		}
		if ci < len(lineCands) && lineCands[ci].StartRune == i {
			c := lineCands[ci]
			ci++
			matchLen := c.EndRune - c.StartRune
			j := i
			if label := lm.LabelFor(c.Text); label != "" {
				labelStyle := st.hint
				if prefix != "" && !strings.HasPrefix(label, prefix) {
					labelStyle = st.dim
				}
				sb.WriteString(labelStyle)
				skipped := 0
				for _, lr := range label {
					if col >= width {
						break
					}
					sb.WriteRune(lr)
					col++
					if skipped < matchLen {
						skipped++
					}
				}
				sb.WriteString(st.reset)
				j = i + skipped
			}
			sb.WriteString(st.match)
			for ; j < i+matchLen && j < len(runes); j++ {
				wdt := runewidth.RuneWidth(runes[j])
				if col+wdt > width {
					break
				}
				sb.WriteRune(runes[j])
				col += wdt
			}
			sb.WriteString(st.reset)
			i += matchLen
			continue
		}
		wdt := runewidth.RuneWidth(runes[i])
		if col+wdt > width {
			break
		}
		if runes[i] != '\r' {
			sb.WriteRune(runes[i])
			col += wdt
		}
		i++
	}
}
