package main

import (
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/mattn/go-runewidth"
)

// The in-tab markdown renderer for descriptions and comment bodies. Block
// elements (headings, lists, quotes, fenced code with syntax highlighting,
// rules, tables) and inline ones (code, bold, links) are interpreted;
// anything ambiguous passes through verbatim.

// mdStyledLines renders markdown source as styled, wrapped display lines.
func mdStyledLines(text string, width int, base StyleID) [][]Span {
	if width < 10 {
		width = 10
	}
	var out [][]Span
	emit := func(spans []Span) { out = append(out, wrapSpans(spans, width)...) }
	// emitHanging wraps content at a marker-indented width, prefixing the
	// marker on the first line and matching spaces after.
	emitHanging := func(marker string, markerStyle StyleID, content []Span) {
		w := width - displayWidth(marker)
		if w < 10 {
			w = 10
		}
		for i, ws := range wrapSpans(content, w) {
			lead := Span{marker, markerStyle}
			if i > 0 {
				lead = Span{strings.Repeat(" ", displayWidth(marker)), styleNone}
			}
			out = append(out, append([]Span{lead}, ws...))
		}
	}
	inFence := false
	fenceLang := ""
	var fenceBuf []string
	closeFence := func() {
		out = append(out, highlightCode(fenceLang, strings.Join(fenceBuf, "\n"), width)...)
		fenceBuf, inFence = nil, false
	}
	var tableBuf []string
	tableIndent := ""
	closeTable := func() {
		out = append(out, renderTable(tableBuf, tableIndent, width, base)...)
		tableBuf = nil
	}

	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]
		if len(tableBuf) > 0 && (inFence || !strings.HasPrefix(trimmed, "|")) {
			closeTable()
		}
		switch {
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			// No fence bars: the block's background rectangle is delimiter
			// enough.
			if inFence {
				closeFence()
			} else {
				inFence = true
				fenceLang = strings.Trim(trimmed, "`~ ")
			}
		case inFence:
			fenceBuf = append(fenceBuf, line)
		case strings.HasPrefix(trimmed, "#"):
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			markers := [...]string{"◆ ", "◆ ", "◇ ", "◇ ", "・", "・"}
			emit([]Span{{markers[min(level, 6)-1] + strings.TrimSpace(trimmed[level:]), styleMDHead}})
		case strings.HasPrefix(trimmed, ">"):
			body := strings.TrimPrefix(strings.TrimPrefix(trimmed, ">"), " ")
			emitHanging(indent+"▎ ", styleDim, mdInlineSpans(body, styleDim))
		case isHRule(trimmed):
			emit([]Span{{strings.Repeat("─", width), styleDim}})
		case strings.HasPrefix(trimmed, "|"):
			if len(tableBuf) == 0 {
				tableIndent = indent
			}
			tableBuf = append(tableBuf, trimmed)
		default:
			if marker, rest, ok := splitBullet(line); ok {
				m := indent + "• "
				if len(indent) >= 2 {
					m = indent + "◦ "
				}
				if isNumberedMarker(marker) {
					m = marker // keep "12. "
				}
				switch {
				case strings.HasPrefix(rest, "[ ] "):
					m, rest = m+"☐ ", rest[4:]
				case strings.HasPrefix(rest, "[x] ") || strings.HasPrefix(rest, "[X] "):
					m, rest = m+"☑ ", rest[4:]
				}
				emitHanging(m, styleHunk, mdInlineSpans(rest, base))
				continue
			}
			emit(mdInlineSpans(line, base))
		}
	}
	if inFence {
		closeFence() // unterminated fence at end of input
	}
	if len(tableBuf) > 0 {
		closeTable()
	}
	return out
}

// highlightCode renders one fenced block as a full-width background
// rectangle with chroma syntax highlighting (single code color when the
// language is unknown).
func highlightCode(lang, code string, width int) [][]Span {
	var out [][]Span
	for _, lineSpans := range highlightTokens(lang, code) {
		w := width - 1
		if w < 10 {
			w = 10
		}
		for _, ws := range wrapSpans(lineSpans, w) {
			row := append([]Span{{" ", styleCodePad}}, ws...)
			used := 0
			for _, sp := range row {
				used += displayWidth(sp.Text)
			}
			if pad := width - used; pad > 0 {
				row = append(row, Span{strings.Repeat(" ", pad), styleCodePad})
			}
			out = append(out, row)
		}
	}
	return out
}

// highlightTokens lexes code into per-source-line styled spans.
func highlightTokens(lang, code string) [][]Span {
	lines := [][]Span{{}}
	appendText := func(text string, st StyleID) {
		for i, part := range strings.Split(text, "\n") {
			if i > 0 {
				lines = append(lines, []Span{})
			}
			if part != "" {
				n := len(lines) - 1
				lines[n] = append(lines[n], Span{strings.ReplaceAll(part, "\t", "    "), st})
			}
		}
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		appendText(code, styleMDCode)
		return lines
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		appendText(code, styleMDCode)
		return lines
	}
	for _, tok := range it.Tokens() {
		appendText(tok.Value, synStyle(tok.Type))
	}
	// Lexers often force a trailing newline; drop the empty last line.
	if n := len(lines); n > 1 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	return lines
}

// synStyle maps a chroma token type to one of the viewer's syntax styles.
func synStyle(tt chroma.TokenType) StyleID {
	switch {
	case tt.InCategory(chroma.Comment):
		return styleSynComment
	case tt.InCategory(chroma.Keyword):
		return styleSynKeyword
	case tt.InSubCategory(chroma.LiteralString):
		return styleSynString
	case tt.InSubCategory(chroma.LiteralNumber):
		return styleSynNumber
	case tt == chroma.NameFunction || tt == chroma.NameClass || tt == chroma.NameBuiltin || tt == chroma.NameDecorator:
		return styleSynFunc
	default:
		return styleMDCode
	}
}

// mdInlineSpans parses one line's inline markdown: `code`, **bold**,
// *italic*, [text](url) (URL concealed), and bare URLs. Unclosed markers
// pass through untouched; backslash escapes before punctuation are dropped.
func mdInlineSpans(line string, base StyleID) []Span {
	var spans []Span
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			spans = append(spans, Span{plain.String(), base})
			plain.Reset()
		}
	}
	i := 0
	for i < len(line) {
		rest := line[i:]
		switch {
		case rest[0] == '\\' && len(rest) > 1 && rest[1] < 0x80 && strings.ContainsRune("\\`*_{}[]()#+-.!|>", rune(rest[1])):
			plain.WriteByte(rest[1])
			i += 2
			continue
		case rest[0] == '`':
			if end := strings.Index(rest[1:], "`"); end >= 0 {
				flush()
				spans = append(spans, Span{rest[1 : end+1], styleMDCode})
				i += end + 2
				continue
			}
		case strings.HasPrefix(rest, "**"):
			if end := strings.Index(rest[2:], "**"); end > 0 {
				flush()
				spans = append(spans, Span{rest[2 : end+2], styleTitle})
				i += end + 4
				continue
			}
		case rest[0] == '*' && len(rest) > 2 && rest[1] != ' ' && rest[1] != '*':
			if end := strings.IndexByte(rest[1:], '*'); end > 0 && rest[end] != ' ' {
				flush()
				spans = append(spans, Span{rest[1 : end+1], styleItalic})
				i += end + 2
				continue
			}
		case rest[0] == '[':
			if ct := strings.Index(rest, "]("); ct > 0 {
				if cu := strings.IndexByte(rest[ct:], ')'); cu > 0 {
					flush()
					spans = append(spans, Span{rest[1:ct], styleMDLink})
					i += ct + cu + 1
					continue
				}
			}
		case strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://"):
			end := strings.IndexAny(rest, " \t)>」）")
			if end < 0 {
				end = len(rest)
			}
			flush()
			spans = append(spans, Span{rest[:end], styleMDLink})
			i += end
			continue
		}
		r, size := utf8.DecodeRuneInString(rest)
		plain.WriteRune(r)
		i += size
	}
	flush()
	if len(spans) == 0 {
		spans = []Span{{"", base}}
	}
	return spans
}

// wrapSpans wraps a styled line at width cells, preserving span styles
// across the breaks.
func wrapSpans(spans []Span, width int) [][]Span {
	if width < 1 {
		width = 1
	}
	var out [][]Span
	var cur []Span
	curW := 0
	for _, sp := range spans {
		var b strings.Builder
		for _, r := range sp.Text {
			rw := runewidth.RuneWidth(r)
			if curW+rw > width {
				if b.Len() > 0 {
					cur = append(cur, Span{b.String(), sp.Style})
					b.Reset()
				}
				out = append(out, cur)
				cur, curW = nil, 0
			}
			b.WriteRune(r)
			curW += rw
		}
		if b.Len() > 0 {
			cur = append(cur, Span{b.String(), sp.Style})
		}
	}
	return append(out, cur)
}

// isHRule reports a thematic break: a line of only -, * or _ (3+ of one kind).
func isHRule(t string) bool {
	t = strings.ReplaceAll(t, " ", "")
	if len(t) < 3 {
		return false
	}
	c := t[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for i := 1; i < len(t); i++ {
		if t[i] != c {
			return false
		}
	}
	return true
}

// Cell alignment from a delimiter cell (":--" / ":-:" / "--:").
const (
	alignLeft = iota
	alignCenter
	alignRight
)

// renderTable renders buffered pipe-table lines as a column-aligned table:
// header cells bold, │/┼ borders dim, cell contents through the inline
// parser. Anything without a delimiter second row is not a table — those
// lines keep the old per-line │ substitution.
func renderTable(lines []string, indent string, width int, base StyleID) [][]Span {
	if len(lines) < 2 || !isTableDelimiter(lines[1]) {
		var out [][]Span
		for _, l := range lines {
			out = append(out, wrapSpans([]Span{{indent + mdTableLine(l), tableStyle(l, base)}}, width)...)
		}
		return out
	}

	// Parse: rows of inline-styled cells (the delimiter row only feeds
	// alignment), sized by their display width.
	var aligns []int
	for _, c := range splitTableCells(lines[1]) {
		aligns = append(aligns, delimAlign(c))
	}
	type cell struct {
		spans []Span
		w     int
	}
	var rows [][]cell
	cols := len(aligns)
	for i, l := range lines {
		if i == 1 {
			continue
		}
		st := base
		if i == 0 {
			st = styleTitle
		}
		var row []cell
		for _, c := range splitTableCells(l) {
			spans := mdInlineSpans(c, st)
			w := 0
			for _, sp := range spans {
				w += displayWidth(sp.Text)
			}
			row = append(row, cell{spans, w})
		}
		if len(row) > cols {
			cols = len(row)
		}
		rows = append(rows, row)
	}
	for len(aligns) < cols {
		aligns = append(aligns, alignLeft)
	}

	colW := make([]int, cols)
	for _, row := range rows {
		for j, c := range row {
			if c.w > colW[j] {
				colW[j] = c.w
			}
		}
	}
	// Shrink the widest columns (never below 3 cells) until the table fits;
	// overflowing cells are truncated with an ellipsis.
	avail := width - displayWidth(indent) - 3*cols - 1 // "│ cell " per column + closing "│"
	for {
		total, widest := 0, 0
		for j, w := range colW {
			total += w
			if w > colW[widest] {
				widest = j
			}
		}
		if total <= avail || colW[widest] <= 3 {
			break
		}
		colW[widest]--
	}

	var out [][]Span
	pad := func(spans []Span, n int, side int) []Span {
		if n <= 0 {
			return spans
		}
		sp := Span{strings.Repeat(" ", n), styleNone}
		if side == alignRight {
			return append([]Span{sp}, spans...)
		}
		return append(spans, sp)
	}
	lead := func() []Span {
		if indent == "" {
			return nil
		}
		return []Span{{indent, styleNone}}
	}
	emitRow := func(row []cell) {
		spans := lead()
		for j := 0; j < cols; j++ {
			c := cell{[]Span{}, 0}
			if j < len(row) {
				c = row[j]
			}
			cs, w := fitSpans(c.spans, colW[j])
			switch aligns[j] {
			case alignRight:
				cs = pad(cs, colW[j]-w, alignRight)
			case alignCenter:
				l := (colW[j] - w) / 2
				cs = pad(pad(cs, l, alignRight), colW[j]-w-l, alignLeft)
			default:
				cs = pad(cs, colW[j]-w, alignLeft)
			}
			spans = append(spans, Span{"│ ", styleDim})
			spans = append(spans, cs...)
			spans = append(spans, Span{" ", styleNone})
		}
		out = append(out, append(spans, Span{"│", styleDim}))
	}
	delim := func() {
		var b strings.Builder
		for j := 0; j < cols; j++ {
			b.WriteString("┼")
			b.WriteString(strings.Repeat("─", colW[j]+2))
		}
		b.WriteString("┼")
		out = append(out, append(lead(), Span{b.String(), styleDim}))
	}
	emitRow(rows[0])
	delim()
	for _, row := range rows[1:] {
		emitRow(row)
	}
	return out
}

// splitTableCells splits a pipe-table row into trimmed cell texts, keeping
// \| escapes intact for the inline parser to unescape.
func splitTableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, "\\|") {
		line = line[:len(line)-1]
	}
	var cells []string
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			b.WriteString(`\|`)
			i++
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteByte(line[i])
		}
	}
	return append(cells, strings.TrimSpace(b.String()))
}

func delimAlign(cell string) int {
	l, r := strings.HasPrefix(cell, ":"), strings.HasSuffix(cell, ":")
	switch {
	case l && r:
		return alignCenter
	case r:
		return alignRight
	default:
		return alignLeft
	}
}

// fitSpans truncates styled spans to at most w cells (ellipsis when cut) and
// returns the resulting display width.
func fitSpans(spans []Span, w int) ([]Span, int) {
	used := 0
	for _, sp := range spans {
		used += displayWidth(sp.Text)
	}
	if used <= w {
		return spans, used
	}
	var out []Span
	cur := 0
	for _, sp := range spans {
		var b strings.Builder
		for _, r := range sp.Text {
			rw := runewidth.RuneWidth(r)
			if cur+rw > w-1 { // reserve the last cell for the ellipsis
				if b.Len() > 0 {
					out = append(out, Span{b.String(), sp.Style})
				}
				return append(out, Span{"…", sp.Style}), cur + 1
			}
			b.WriteRune(r)
			cur += rw
		}
		out = append(out, Span{b.String(), sp.Style})
	}
	return out, cur
}

// mdTableLine restyles a pipe-table row: │ borders, ─ in delimiter rows.
func mdTableLine(t string) string {
	if isTableDelimiter(t) {
		t = strings.Map(func(r rune) rune {
			switch r {
			case '|':
				return '┼'
			case '-', ':':
				return '─'
			}
			return r
		}, t)
		return t
	}
	return strings.ReplaceAll(t, "|", "│")
}

func isTableDelimiter(t string) bool {
	seen := false
	for _, r := range t {
		switch r {
		case '|', ' ':
		case '-', ':':
			seen = true
		default:
			return false
		}
	}
	return seen
}

func tableStyle(t string, base StyleID) StyleID {
	if isTableDelimiter(t) {
		return styleDim
	}
	return base
}

// isNumberedMarker reports whether a splitBullet marker is "12. "-style.
func isNumberedMarker(m string) bool {
	m = strings.TrimLeft(m, " \t")
	return len(m) > 0 && m[0] >= '0' && m[0] <= '9'
}

// splitBullet splits "  - item" (or "12. item") into marker and rest;
// ok=false when the line is not a list item.
func splitBullet(line string) (marker, rest string, ok bool) {
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	t := line[indent:]
	for _, m := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(t, m) {
			return line[:indent+2], t[2:], true
		}
	}
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(t) && t[i] == '.' && t[i+1] == ' ' {
		return line[:indent+i+2], t[i+2:], true
	}
	return "", "", false
}
