package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

// colorCode maps a color name, a 0-255 number, or a #rrggbb hex value to an
// SGR parameter.
func colorCode(name string, bg bool) string {
	if len(name) == 7 && name[0] == '#' {
		if v, err := strconv.ParseUint(name[1:], 16, 32); err == nil {
			base := "38"
			if bg {
				base = "48"
			}
			return fmt.Sprintf("%s;2;%d;%d;%d", base, (v>>16)&0xff, (v>>8)&0xff, v&0xff)
		}
	}
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

// viewWindow decides which slice of the captured lines fits a view of the
// given height (one row already excluded for the footer by callers): when
// the capture is taller, the bottom lines win.
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

// PaintTokens draws the captured text into r with hint labels on each
// candidate (tmux-thumbs style): the plain content first, then the same
// span/label overlay the composite path uses — one code path decides label
// placement everywhere. Lines render fully left-to-right so wide/CJK
// characters keep natural alignment; the bottom lines win when the capture
// is taller than the rect.
func PaintTokens(s *Screen, r Rect, text string, cands []Candidate, lm *LabelMap, prefix string) {
	lines := strings.Split(text, "\n")
	offset := 0
	if len(lines) > r.H {
		offset = len(lines) - r.H
	}
	for li := 0; li < r.H && offset+li < len(lines); li++ {
		s.WriteString(r.X, r.Y+li, lines[offset+li], styleNone, r.X+r.W)
	}
	PaintTokenOverlay(s, r, text, cands, lm, prefix)
}

// PaintTokenOverlay restyles each candidate span and stamps its hint label:
// the tmux-thumbs look on top of the already-painted pane pixels (the
// composite base, or PaintTokens' plain content). While a prefix is typed,
// candidates it rules out drop their span highlight entirely — only the
// still-reachable copy targets stay lit, and their labels' remaining keys
// stay bold.
func PaintTokenOverlay(s *Screen, r Rect, text string, cands []Candidate, lm *LabelMap, prefix string) {
	lines := strings.Split(text, "\n")
	offset := 0
	if len(lines) > r.H {
		offset = len(lines) - r.H
	}
	for _, c := range cands {
		if c.Line < offset || c.Line-offset >= r.H {
			continue
		}
		y := r.Y + c.Line - offset
		runes := []rune(lines[c.Line])
		x := r.X
		for i := 0; i < c.StartRune && i < len(runes); i++ {
			x += runewidth.RuneWidth(runes[i])
		}
		label := lm.LabelFor(c.Text)
		active := prefix == "" || (label != "" && strings.HasPrefix(label, prefix))
		cx := x
		for i := c.StartRune; i < c.EndRune && i < len(runes); i++ {
			wdt := runewidth.RuneWidth(runes[i])
			if cx+wdt > r.X+r.W {
				break
			}
			if active { // ruled-out spans keep their original pane colors
				s.SetStyle(cx, y, styleMatch)
				if wdt == 2 {
					s.SetStyle(cx+1, y, styleMatch)
				}
			}
			cx += wdt
		}
		if label != "" {
			st := styleHint
			consumed := 0
			if !active {
				st = styleDim
			} else {
				consumed = len(prefix) // typed keys reveal the content beneath
			}
			placeLabel(s, r, y, x, cx, label, consumed, st)
		}
	}
}

// placeLabel stamps a hint label without spilling onto unrelated content:
// over the span's leading cells when the span is wide enough (the
// tmux-thumbs look), otherwise into blank cells just before or after the
// span, otherwise not at all — the span highlight still marks the candidate.
// consumed marks how many leading label keys were already typed: those cells
// revert to the content beneath (reveal-as-you-type) while the remaining
// keys stay put — placement is decided on the FULL label width so typing
// never moves a label.
func placeLabel(s *Screen, r Rect, y, startX, endX int, label string, consumed int, st StyleID) {
	lw := runewidth.StringWidth(label)
	var x int
	switch {
	case endX-startX >= lw:
		x = startX
	case blankCells(s, r, y, startX-lw, startX):
		x = startX - lw
	case blankCells(s, r, y, endX, endX+lw):
		x = endX
	default:
		return
	}
	if consumed > 0 {
		if consumed >= len(label) {
			return
		}
		label = label[consumed:] // alphabet keys are 1-cell ASCII
		x += consumed
	}
	s.WriteString(x, y, label, st, r.X+r.W)
}

// PaintWordInsert is the word-mode painter: the target pane's colored
// content is repainted with each candidate's label INSERTED just before its
// word, shifting the rest of the line right — every character of the word
// stays readable (the same trade line mode makes with its label margin).
// The inserted slot always spans the full label width, so typing a prefix
// never reflows the layout: ruled-out labels just dim, and an active
// label's typed keys go dim while the remaining keys stay bold.
func PaintWordInsert(s *Screen, r Rect, styled [][]Cell, cands []Candidate, lm *LabelMap, prefix string) {
	offset := 0
	if len(styled) > r.H {
		offset = len(styled) - r.H
	}
	byLine := make(map[int][]Candidate)
	for _, c := range cands {
		byLine[c.Line] = append(byLine[c.Line], c) // ExtractWords emits in StartRune order
	}
	maxX := r.X + r.W
	for li := 0; li < r.H && offset+li < len(styled); li++ {
		abs := offset + li
		y := r.Y + li
		lineCands := byLine[abs]
		ci := 0
		x := r.X
		runeIdx := 0
		curEnd := -1 // rune end (exclusive) of the span being highlighted
		curActive := false
		for _, cell := range styled[abs] {
			if cell.R == 0 {
				continue // wide-rune continuation, re-emitted by Set
			}
			for ci < len(lineCands) && lineCands[ci].StartRune < runeIdx {
				ci++ // skip candidates that started inside a clipped region
			}
			if ci < len(lineCands) && lineCands[ci].StartRune == runeIdx {
				c := lineCands[ci]
				ci++
				if label := lm.LabelFor(c.Text); label != "" {
					curActive = prefix == "" || strings.HasPrefix(label, prefix)
					curEnd = c.EndRune
					for i, lr := range label {
						if x >= maxX {
							break
						}
						st := styleDim
						if curActive && i >= len(prefix) {
							st = styleHint
						}
						s.Set(x, y, lr, st)
						x++
					}
				}
			}
			if x >= maxX {
				break
			}
			wdt := runewidth.RuneWidth(cell.R)
			if x+wdt > maxX {
				break
			}
			st := cell.Style
			if runeIdx < curEnd && curActive {
				st = styleMatch
			}
			s.Set(x, y, cell.R, st)
			x += wdt
			runeIdx++
		}
	}
}

// blankCells reports whether every cell of [x1, x2) on row y is a plain
// space inside the rect — safe to host a label without hiding content.
func blankCells(s *Screen, r Rect, y, x1, x2 int) bool {
	if x1 < r.X || x2 > r.X+r.W || y < 0 || y >= s.H {
		return false
	}
	for x := x1; x < x2; x++ {
		if x < 0 || x >= s.W || s.at(x, y).R != ' ' {
			return false
		}
	}
	return true
}

// PaintLineModeStyled is the composite-mode line painter: labels in a left
// margin, the pane's colored cells shifted right, and the selection range
// [anchor, cursor] highlighted — the range in styleSel, the moving cursor row
// inverted so you can see which end j/k moves.
func PaintLineModeStyled(s *Screen, r Rect, styled [][]Cell, ll *LineLabels, prefix string, anchor, cursor int) {
	offset := 0
	if len(styled) > r.H {
		offset = len(styled) - r.H
	}
	margin := 0
	if ll.Width() > 0 {
		margin = ll.Width() + 1
	}
	lo, hi := selRange(anchor, cursor)
	maxX := r.X + r.W
	for li := 0; li < r.H && offset+li < len(styled); li++ {
		abs := offset + li
		y := r.Y + li
		label := ll.LabelFor(abs)
		x := r.X
		if label != "" {
			labelStyle := styleHint
			if prefix != "" && !strings.HasPrefix(label, prefix) {
				labelStyle = styleDim
			}
			x = s.WriteString(x, y, label, labelStyle, maxX)
			for ; x < r.X+margin && x < maxX; x++ {
				s.Set(x, y, ' ', styleNone)
			}
		}
		s.PutCells(x, y, styled[abs], maxX)
		if anchor >= 0 && abs >= lo && abs <= hi { // highlight the selection
			st := styleSel
			if abs == cursor {
				st = styleInverse
			}
			for ix := r.X + margin; ix < maxX; ix++ {
				s.SetStyle(ix, y, st)
			}
		}
	}
}

// selRange normalizes the anchor/cursor pair into an ordered inclusive range.
func selRange(anchor, cursor int) (int, int) {
	if cursor < 0 {
		cursor = anchor
	}
	if anchor > cursor {
		return cursor, anchor
	}
	return anchor, cursor
}

// PaintLineMode draws line-mode hints into r: a uniform left margin holds
// each non-empty line's label, content shifts right, and the selection range
// [anchor, cursor] is highlighted (range in styleSel, cursor row inverted).
func PaintLineMode(s *Screen, r Rect, text string, ll *LineLabels, prefix string, anchor, cursor int) {
	lines := strings.Split(text, "\n")
	offset := 0
	if len(lines) > r.H {
		offset = len(lines) - r.H
	}
	margin := 0
	if ll.Width() > 0 {
		margin = ll.Width() + 1
	}
	lo, hi := selRange(anchor, cursor)
	maxX := r.X + r.W
	for li := 0; li < r.H && offset+li < len(lines); li++ {
		abs := offset + li
		y := r.Y + li
		line := lines[abs]
		label := ll.LabelFor(abs)
		inSel := anchor >= 0 && abs >= lo && abs <= hi
		contentStyle := styleNone
		if inSel {
			contentStyle = styleSel
			if abs == cursor {
				contentStyle = styleInverse
			}
		}
		x := r.X
		if label != "" {
			labelStyle := styleHint
			if prefix != "" && !strings.HasPrefix(label, prefix) {
				labelStyle = styleDim
			}
			x = s.WriteString(x, y, label, labelStyle, maxX)
			for ; x < r.X+margin && x < maxX; x++ {
				s.Set(x, y, ' ', contentStyle)
			}
		}
		x = s.WriteString(x, y, line, contentStyle, maxX)
		if inSel { // extend the highlight across the rect width
			for ; x < maxX; x++ {
				s.Set(x, y, ' ', contentStyle)
			}
		}
	}
}

// PaintCopyMode draws the copy-mode viewport into r: buffer lines (styled
// cells from the ANSI capture when available, plain text otherwise), search
// highlights, the selection, and the cursor.
func PaintCopyMode(s *Screen, r Rect, cs *CopyState, styled [][]Cell) {
	for row := 0; row < r.H; row++ {
		li := cs.Top + row
		if li >= len(cs.Buf) {
			break
		}
		if styled != nil && li < len(styled) {
			s.PutCells(r.X, r.Y+row, styled[li], r.X+r.W)
		} else {
			s.WriteString(r.X, r.Y+row, string(cs.Buf[li]), styleNone, r.X+r.W)
		}
	}
	if cs.Search.QueryLen > 0 {
		for _, m := range cs.Search.Matches {
			styleSpan(s, r, cs, m.Line, m.Col, m.Col+cs.Search.QueryLen-1, styleSearch)
		}
	}
	if cs.Sel != nil {
		a, b := cs.Sel.Anchor, cs.Cur
		if a.Line > b.Line || (a.Line == b.Line && a.Col > b.Col) {
			a, b = b, a
		}
		for li := a.Line; li <= b.Line; li++ {
			c1, c2 := 0, len(cs.Buf[li])-1
			if !cs.Sel.Linewise {
				if li == a.Line {
					c1 = a.Col
				}
				if li == b.Line {
					c2 = b.Col
				}
			}
			styleSpan(s, r, cs, li, c1, c2, styleSel)
		}
	}
	styleSpan(s, r, cs, cs.Cur.Line, cs.Cur.Col, cs.Cur.Col, styleCursor)
	if len(cs.Buf[cs.Cur.Line]) == 0 { // empty line: paint a cursor cell
		if row := cs.Cur.Line - cs.Top; row >= 0 && row < r.H {
			s.Set(r.X, r.Y+row, ' ', styleCursor)
		}
	}
}

// styleSpan restyles the rune-index range [c1, c2] of buffer line li,
// walking display widths so CJK text highlights the right cells.
func styleSpan(s *Screen, r Rect, cs *CopyState, li, c1, c2 int, st StyleID) {
	row := li - cs.Top
	if row < 0 || row >= r.H {
		return
	}
	y := r.Y + row
	x := r.X
	for i, ch := range cs.Buf[li] {
		wdt := runewidth.RuneWidth(ch)
		if x+wdt > r.X+r.W {
			return
		}
		if i >= c1 && i <= c2 {
			s.SetStyle(x, y, st)
			if wdt == 2 {
				s.SetStyle(x+1, y, st)
			}
		}
		x += wdt
	}
}

// copyFooter renders the copy-mode status line.
func copyFooter(cs *CopyState) string {
	if cs.Search.Typing {
		pre := "/"
		if cs.Search.Backward {
			pre = "?"
		}
		return " " + pre + cs.Search.Query + "_"
	}
	pct := 100
	if len(cs.Lines) > 0 {
		pct = (cs.Cur.Line + 1) * 100 / len(cs.Lines)
	}
	sel := ""
	if cs.Sel != nil {
		sel = "V"
		if !cs.Sel.Linewise {
			sel = "v"
		}
		sel = " · -- " + sel + " --"
	}
	f := fmt.Sprintf(" copy · %d%%%s · v/V:select · y:copy · /:search · q/esc:quit", pct, sel)
	if cs.Msg != "" {
		f = " " + cs.Msg + " ·" + f
	}
	if cs.Trunc {
		f += " · scrollback capped"
	}
	return f
}

// PaintFooter writes the dim status line into the screen's bottom row,
// clearing it first (in composite mode the base frame has pane content
// there).
func PaintFooter(s *Screen, msg string) {
	if s.H < 2 {
		return
	}
	y := s.H - 1
	for x := 0; x < s.W; x++ {
		s.Set(x, y, ' ', styleNone)
	}
	s.WriteString(0, y, msg, styleDim, s.W)
}

// helpLines builds the help screen content: key reference plus the pattern
// inventory with its current on/off state from the user's config.
func helpLines(cfg Config) []string {
	var lines []string
	add := func(s string) { lines = append(lines, s) }

	add(" hint-copy " + version + " — help")
	add("")
	add(" token mode (URLs, paths, ...)")
	add("   a s d ...      copy the labeled string")
	add("   space          switch to line mode")
	add("   tab            word mode (labels every word)")
	add("   [              copy mode")
	add(" line mode (whole lines)")
	add("   label            select the first line (anchor)")
	add("   j k / ↑ ↓        extend the selection up / down")
	add("   another label    jump the other end to that line")
	add("   y / enter        copy the selected line(s)")
	add("   esc              clear selection (again to quit)")
	add(" copy mode ( [ or the copy-mode action; scrollback included )")
	add("   h j k l / arrows  move      w b e  words     { }  paragraphs")
	add("   0 ^ $   line      g G  top/bottom   ctrl+f/b page  ctrl+u/d half")
	add("   v/space select    V  line select    y/enter  copy")
	add("   / ?     search    n N  next/prev    q/esc    quit")
	add(" anywhere")
	add("   esc / ctrl-c   cancel")
	add("   ?              this help in token/line mode (any key returns)")
	add("")
	add(" patterns — toggle in " + configPathHint())

	var entries []string
	seen := make(map[string]bool)
	for _, p := range builtinPatterns {
		if seen[p.name] {
			continue
		}
		seen[p.name] = true
		state := "on"
		if !cfg.patternEnabled(p.name) {
			state = "off"
		}
		entries = append(entries, fmt.Sprintf("%-13s %-3s", p.name, state))
	}
	for _, cp := range cfg.CustomPatterns {
		entries = append(entries, fmt.Sprintf("%-13s %-3s", cp.Name, "on"))
	}
	for i := 0; i < len(entries); i += 3 {
		row := "   "
		for j := i; j < i+3 && j < len(entries); j++ {
			row += entries[j] + " "
		}
		add(row)
	}
	return lines
}

// configPathHint names the config file location for the help screen, using
// the concrete dir herdr injected when available.
func configPathHint() string {
	if dir := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); dir != "" {
		return dir + "/config.toml"
	}
	return "$HERDR_PLUGIN_CONFIG_DIR/config.toml"
}

// PaintHelp draws the help content into r (top lines win — help is short).
func PaintHelp(s *Screen, r Rect, cfg Config) {
	lines := helpLines(cfg)
	for li := 0; li < r.H && li < len(lines); li++ {
		s.WriteString(r.X, r.Y+li, lines[li], styleNone, r.X+r.W)
	}
}

// ---- full-screen wrappers (kept signatures; used by the fallback path and
// the tests, which assert on their SGR-stripped output) ----

// tokenFooter renders the token/word-mode status line: the target count, the
// narrowing count while a prefix is typed, and — when only one candidate is
// still reachable — a preview of exactly what the next key will copy.
func tokenFooter(prefix string, lm *LabelMap, word bool) string {
	name := "type a label to copy"
	toggles := " · space: line mode · tab: word mode · ? help · esc to cancel"
	if word {
		name = "word mode: type a label"
		toggles = " · tab: token mode · ? help · esc to cancel"
	}
	if prefix != "" {
		if text, ok := lm.OnlyMatch(prefix); ok {
			return " " + prefix + "_ · copy: " + truncate(text, 48) + toggles
		}
		return fmt.Sprintf(" %s_ · %d targets%s", prefix, lm.MatchCount(prefix), toggles)
	}
	return fmt.Sprintf(" %d targets · %s%s", lm.Count(), name, toggles)
}

func lineFooter(prefix string, anchor, cursor int) string {
	footer := " line mode: type a label · space: token mode · ? help · esc to cancel"
	if anchor >= 0 {
		lo, hi := selRange(anchor, cursor)
		footer = fmt.Sprintf(" %d line(s) selected · j/k ↑↓: extend · label: jump · y/enter: copy · esc: clear", hi-lo+1)
	} else if prefix != "" {
		footer = " " + prefix + "_ ·" + footer
	}
	return footer
}

// Render paints one full-screen token-mode frame.
func Render(w io.Writer, screen string, cands []Candidate, lm *LabelMap, prefix string, width, height int, cfg Config) {
	s := NewScreen(width, height)
	_, view := viewWindow(len(strings.Split(screen, "\n")), height)
	PaintTokens(s, Rect{X: 0, Y: 0, W: width, H: view}, screen, cands, lm, prefix)
	PaintFooter(s, tokenFooter(prefix, lm, false))
	s.Flush(w, sgrTable(cfg))
}

// RenderLines paints one full-screen line-mode frame.
func RenderLines(w io.Writer, screen string, ll *LineLabels, prefix string, anchor, cursor, width, height int, cfg Config) {
	s := NewScreen(width, height)
	_, view := viewWindow(len(strings.Split(screen, "\n")), height)
	PaintLineMode(s, Rect{X: 0, Y: 0, W: width, H: view}, screen, ll, prefix, anchor, cursor)
	PaintFooter(s, lineFooter(prefix, anchor, cursor))
	s.Flush(w, sgrTable(cfg))
}

// RenderHelp paints the full-screen help frame.
func RenderHelp(w io.Writer, width, height int, cfg Config) {
	s := NewScreen(width, height)
	PaintHelp(s, Rect{X: 0, Y: 0, W: width, H: height - 1}, cfg)
	PaintFooter(s, " press any key to return")
	s.Flush(w, sgrTable(cfg))
}
