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

// Render paints one full frame: the captured screen with hint labels replacing
// the leading cells of each candidate (tmux-thumbs style), plus a footer.
// Lines are emitted top-to-bottom in full, so wide/CJK characters keep their
// natural alignment without any column bookkeeping across the frame. When the
// capture is taller than the overlay the bottom lines win.
func Render(w io.Writer, screen string, cands []Candidate, lm *LabelMap, prefix string, width, height int, cfg Config) {
	st := makeStyles(cfg)
	lines := strings.Split(screen, "\n")
	view := height - 1 // one row reserved for the footer
	if view < 1 {
		view = 1
	}
	offset := 0
	if len(lines) > view {
		offset = len(lines) - view
	}
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
		footer := " type a label to copy · esc to cancel"
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
