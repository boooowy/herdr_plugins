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

// PaintTokens draws the captured text into r with hint labels replacing the
// leading cells of each candidate (tmux-thumbs style). Lines render fully
// left-to-right so wide/CJK characters keep natural alignment; the bottom
// lines win when the capture is taller than the rect.
func PaintTokens(s *Screen, r Rect, text string, cands []Candidate, lm *LabelMap, prefix string) {
	lines := strings.Split(text, "\n")
	offset := 0
	if len(lines) > r.H {
		offset = len(lines) - r.H
	}
	byLine := make(map[int][]Candidate)
	for _, c := range cands {
		if c.Line >= offset {
			byLine[c.Line-offset] = append(byLine[c.Line-offset], c)
		}
	}
	for li := 0; li < r.H && offset+li < len(lines); li++ {
		paintTokenLine(s, r, li, lines[offset+li], byLine[li], lm, prefix)
	}
}

// paintTokenLine draws one line at rect row li, overlaying candidate labels.
// lineCands are sorted by StartRune and non-overlapping (Extract guarantees
// both); candidate text is ASCII, so a label replaces exactly its rune count.
func paintTokenLine(s *Screen, r Rect, li int, line string, lineCands []Candidate, lm *LabelMap, prefix string) {
	runes := []rune(line)
	maxX := r.X + r.W
	y := r.Y + li
	x := r.X
	i, ci := 0, 0
	for i < len(runes) && x < maxX {
		for ci < len(lineCands) && lineCands[ci].StartRune < i {
			ci++
		}
		if ci < len(lineCands) && lineCands[ci].StartRune == i {
			c := lineCands[ci]
			ci++
			matchLen := c.EndRune - c.StartRune
			j := i
			if label := lm.LabelFor(c.Text); label != "" {
				labelStyle := styleHint
				if prefix != "" && !strings.HasPrefix(label, prefix) {
					labelStyle = styleDim
				}
				skipped := 0
				for _, lr := range label {
					if x >= maxX {
						break
					}
					s.Set(x, y, lr, labelStyle)
					x++
					if skipped < matchLen {
						skipped++
					}
				}
				j = i + skipped
			}
			for ; j < i+matchLen && j < len(runes); j++ {
				wdt := runewidth.RuneWidth(runes[j])
				if x+wdt > maxX {
					break
				}
				s.Set(x, y, runes[j], styleMatch)
				x += wdt
			}
			i += matchLen
			continue
		}
		if runes[i] != '\r' {
			wdt := runewidth.RuneWidth(runes[i])
			if x+wdt > maxX {
				break
			}
			s.Set(x, y, runes[i], styleNone)
			x += wdt
		}
		i++
	}
}

// PaintTokenOverlay is the composite-mode token painter: the pane's colored
// content is already in the base frame, so it only restyles each candidate
// span and stamps the hint label over the leading cells — the tmux-thumbs
// look on top of the real pane pixels.
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
		cx := x
		for i := c.StartRune; i < c.EndRune && i < len(runes); i++ {
			wdt := runewidth.RuneWidth(runes[i])
			if cx+wdt > r.X+r.W {
				break
			}
			s.SetStyle(cx, y, styleMatch)
			if wdt == 2 {
				s.SetStyle(cx+1, y, styleMatch)
			}
			cx += wdt
		}
		if label := lm.LabelFor(c.Text); label != "" {
			st := styleHint
			if prefix != "" && !strings.HasPrefix(label, prefix) {
				st = styleDim
			}
			lx := x
			for _, lr := range label {
				if lx >= r.X+r.W {
					break
				}
				s.Set(lx, y, lr, st)
				lx++
			}
		}
	}
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
	add("   [              copy mode")
	add(" line mode (whole lines)")
	add("   label            select the first line (anchor)")
	add("   j k / ↑ ↓        extend the selection up / down")
	add("   another label    jump the other end to that line")
	add("   enter            copy the selected line(s)")
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

func tokenFooter(prefix string) string {
	footer := " type a label to copy · space: line mode · ? help · esc to cancel"
	if prefix != "" {
		footer = " " + prefix + "_ ·" + footer
	}
	return footer
}

func lineFooter(prefix string, anchor, cursor int) string {
	footer := " line mode: type a label · space: token mode · ? help · esc to cancel"
	if anchor >= 0 {
		lo, hi := selRange(anchor, cursor)
		footer = fmt.Sprintf(" %d line(s) selected · j/k ↑↓: extend · label: jump · enter: copy · esc: clear", hi-lo+1)
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
	PaintFooter(s, tokenFooter(prefix))
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
