package main

import "strings"

// Pos is a buffer position: Line indexes Lines/Buf, Col is a rune index
// within the line.
type Pos struct {
	Line, Col int
}

// Sel is an active selection anchored at Anchor and extending to the cursor.
type Sel struct {
	Anchor   Pos
	Linewise bool
}

// searchState holds the incremental search: while Typing, printable keys
// edit Query; on commit the cursor jumps and n/N repeat.
type searchState struct {
	Query    string
	Backward bool
	Typing   bool
	Matches  []Pos
	QueryLen int // rune length, for highlighting
}

// CopyState is the pure copy-mode engine: a scrollback buffer, a viewport,
// a cursor, an optional selection, and a search. It performs no IO — the UI
// feeds it Keys and paints from its fields.
type CopyState struct {
	Lines []string // raw lines (yank source)
	Buf   [][]rune // rune view of Lines (motion/col math)
	W, H  int      // viewport size in cells
	Top   int      // first visible line
	Cur   Pos
	want  int // sticky column for vertical motion
	Sel   *Sel
	Search searchState
	Msg   string // one-frame footer flash
	Trunc bool   // scrollback was capped
}

// Action is what the UI must do after a key.
type Action int

const (
	ActNone Action = iota
	ActExit
	ActYank // the returned string goes to the clipboard
)

// NewCopyState starts at the tail of the buffer with the cursor on the last
// non-empty line, like entering herdr's copy mode.
func NewCopyState(text string, w, h int, truncated bool) *CopyState {
	lines := strings.Split(text, "\n")
	buf := make([][]rune, len(lines))
	for i, l := range lines {
		buf[i] = []rune(strings.TrimRight(l, "\r"))
	}
	s := &CopyState{Lines: lines, Buf: buf, W: w, H: h, Trunc: truncated}
	if s.Top = len(lines) - h; s.Top < 0 { // tail view
		s.Top = 0
	}
	cur := len(lines) - 1
	for cur > 0 && strings.TrimSpace(lines[cur]) == "" {
		cur--
	}
	s.Cur = Pos{Line: cur}
	s.scrollToCursor()
	return s
}

func (s *CopyState) lineLen(i int) int { return len(s.Buf[i]) }

// maxCol is the rightmost cursor column on a line (vim-style: on the last
// character, 0 on an empty line).
func (s *CopyState) maxCol(i int) int {
	if n := s.lineLen(i); n > 0 {
		return n - 1
	}
	return 0
}

func (s *CopyState) clampCol() {
	if s.Cur.Col > s.maxCol(s.Cur.Line) {
		s.Cur.Col = s.maxCol(s.Cur.Line)
	}
	if s.Cur.Col < 0 {
		s.Cur.Col = 0
	}
}

func (s *CopyState) scrollToCursor() {
	if s.Cur.Line < s.Top {
		s.Top = s.Cur.Line
	}
	if s.Cur.Line >= s.Top+s.H {
		s.Top = s.Cur.Line - s.H + 1
	}
	if max := len(s.Lines) - s.H; s.Top > max {
		s.Top = max
	}
	if s.Top < 0 {
		s.Top = 0
	}
}

func (s *CopyState) moveVert(dy int) {
	s.Cur.Line += dy
	if s.Cur.Line < 0 {
		s.Cur.Line = 0
	}
	if s.Cur.Line > len(s.Lines)-1 {
		s.Cur.Line = len(s.Lines) - 1
	}
	if s.want < s.Cur.Col {
		s.want = s.Cur.Col
	}
	s.Cur.Col = s.want
	s.clampCol()
}

func (s *CopyState) moveHoriz(dx int) {
	s.Cur.Col += dx
	s.clampCol()
	s.want = s.Cur.Col
}

// isWordRune uses a simple two-class model (non-space runs are words) —
// coarser than vim's three classes but predictable.
func isWordRune(r rune) bool { return r != ' ' && r != '\t' }

// nextWord moves to the start of the next word, crossing lines.
func (s *CopyState) nextWord() {
	l, c := s.Cur.Line, s.Cur.Col
	// Skip the rest of the current word, then whitespace.
	inWord := c < s.lineLen(l) && isWordRune(s.Buf[l][c])
	for {
		c++
		if c >= s.lineLen(l) {
			l++
			if l >= len(s.Lines) {
				s.Cur = Pos{Line: len(s.Lines) - 1, Col: s.maxCol(len(s.Lines) - 1)}
				s.want = s.Cur.Col
				return
			}
			c = 0
			inWord = false
			if s.lineLen(l) == 0 {
				continue
			}
		}
		r := s.Buf[l][c]
		if inWord && !isWordRune(r) {
			inWord = false
		}
		if !inWord && isWordRune(r) {
			s.Cur = Pos{Line: l, Col: c}
			s.want = c
			return
		}
	}
}

// prevWord moves to the start of the previous word, crossing lines.
func (s *CopyState) prevWord() {
	l, c := s.Cur.Line, s.Cur.Col
	for {
		c--
		if c < 0 {
			l--
			if l < 0 {
				s.Cur = Pos{}
				s.want = 0
				return
			}
			c = s.lineLen(l) - 1
			if c < 0 {
				continue
			}
		}
		if !isWordRune(s.Buf[l][c]) {
			continue
		}
		// Walk back to this word's start.
		for c > 0 && isWordRune(s.Buf[l][c-1]) {
			c--
		}
		s.Cur = Pos{Line: l, Col: c}
		s.want = c
		return
	}
}

// endWord moves to the end of the current/next word, crossing lines.
func (s *CopyState) endWord() {
	l, c := s.Cur.Line, s.Cur.Col
	for {
		c++
		if c >= s.lineLen(l) {
			l++
			if l >= len(s.Lines) {
				s.Cur = Pos{Line: len(s.Lines) - 1, Col: s.maxCol(len(s.Lines) - 1)}
				s.want = s.Cur.Col
				return
			}
			c = -1
			continue
		}
		if !isWordRune(s.Buf[l][c]) {
			continue
		}
		for c+1 < s.lineLen(l) && isWordRune(s.Buf[l][c+1]) {
			c++
		}
		s.Cur = Pos{Line: l, Col: c}
		s.want = c
		return
	}
}

// paragraph moves to the previous/next empty line (or buffer edge).
func (s *CopyState) paragraph(dir int) {
	l := s.Cur.Line + dir
	for l > 0 && l < len(s.Lines)-1 && strings.TrimSpace(s.Lines[l]) != "" {
		l += dir
	}
	if l < 0 {
		l = 0
	}
	if l > len(s.Lines)-1 {
		l = len(s.Lines) - 1
	}
	s.Cur = Pos{Line: l}
	s.want = 0
}

// runSearch recomputes matches for the current query (literal, smart-case).
func (s *CopyState) runSearch() {
	s.Search.Matches = nil
	q := s.Search.Query
	s.Search.QueryLen = len([]rune(q))
	if q == "" {
		return
	}
	fold := !strings.ContainsFunc(q, func(r rune) bool { return r >= 'A' && r <= 'Z' })
	if fold {
		q = strings.ToLower(q)
	}
	for li, line := range s.Lines {
		hay := strings.TrimRight(line, "\r")
		if fold {
			hay = strings.ToLower(hay)
		}
		from := 0
		for {
			i := strings.Index(hay[from:], q)
			if i < 0 {
				break
			}
			byteIdx := from + i
			s.Search.Matches = append(s.Search.Matches, Pos{Line: li, Col: len([]rune(hay[:byteIdx]))})
			from = byteIdx + len(q)
		}
	}
}

// jumpMatch moves the cursor to the next match in direction dir (+1 forward,
// -1 backward) with wrap-around.
func (s *CopyState) jumpMatch(dir int) {
	ms := s.Search.Matches
	if len(ms) == 0 {
		s.Msg = "no matches"
		return
	}
	if dir >= 0 {
		for _, m := range ms {
			if m.Line > s.Cur.Line || (m.Line == s.Cur.Line && m.Col > s.Cur.Col) {
				s.Cur = m
				s.want = m.Col
				s.scrollToCursor()
				return
			}
		}
		s.Cur = ms[0] // wrap
	} else {
		for i := len(ms) - 1; i >= 0; i-- {
			if ms[i].Line < s.Cur.Line || (ms[i].Line == s.Cur.Line && ms[i].Col < s.Cur.Col) {
				s.Cur = ms[i]
				s.want = ms[i].Col
				s.scrollToCursor()
				return
			}
		}
		s.Cur = ms[len(ms)-1] // wrap
	}
	s.want = s.Cur.Col
	s.scrollToCursor()
}

// yankText returns the selected text (empty when no selection).
func (s *CopyState) yankText() string {
	if s.Sel == nil {
		return ""
	}
	a, b := s.Sel.Anchor, s.Cur
	if a.Line > b.Line || (a.Line == b.Line && a.Col > b.Col) {
		a, b = b, a
	}
	if s.Sel.Linewise {
		return RangeText(s.Lines, a.Line, b.Line)
	}
	if a.Line == b.Line {
		line := s.Buf[a.Line]
		if len(line) == 0 {
			return ""
		}
		end := b.Col + 1
		if end > len(line) {
			end = len(line)
		}
		return string(line[a.Col:end])
	}
	var parts []string
	first := s.Buf[a.Line]
	if a.Col < len(first) {
		parts = append(parts, strings.TrimRight(string(first[a.Col:]), " \t"))
	} else {
		parts = append(parts, "")
	}
	for l := a.Line + 1; l < b.Line; l++ {
		parts = append(parts, strings.TrimRight(s.Lines[l], " \t\r"))
	}
	last := s.Buf[b.Line]
	end := b.Col + 1
	if end > len(last) {
		end = len(last)
	}
	parts = append(parts, string(last[:end]))
	return strings.Join(parts, "\n")
}

// Handle processes one key and reports what the UI should do. When it
// returns ActYank, the string is the text to copy.
func (s *CopyState) Handle(k Key) (Action, string) {
	s.Msg = ""

	if s.Search.Typing {
		switch k.Kind {
		case KeyEsc:
			s.Search.Typing = false
			s.Search.Query = ""
			s.runSearch()
		case KeyEnter:
			s.Search.Typing = false
			s.jumpMatch(searchDir(s.Search.Backward))
		case KeyBackspace:
			q := []rune(s.Search.Query)
			if len(q) == 0 {
				s.Search.Typing = false
				break
			}
			s.Search.Query = string(q[:len(q)-1])
			s.runSearch()
		case KeyRune:
			s.Search.Query += string(k.R)
			s.runSearch()
		}
		return ActNone, ""
	}

	switch k.Kind {
	case KeyEsc:
		switch {
		case s.Sel != nil:
			s.Sel = nil
		case s.Search.Query != "":
			s.Search = searchState{}
		default:
			return ActExit, ""
		}
		return ActNone, ""
	case KeyEnter:
		if text := s.yankText(); text != "" {
			return ActYank, text
		}
		s.Msg = "select with v or V first"
		return ActNone, ""
	case KeyUp:
		s.moveVert(-1)
	case KeyDown:
		s.moveVert(1)
	case KeyLeft:
		s.moveHoriz(-1)
	case KeyRight:
		s.moveHoriz(1)
	case KeyCtrl:
		switch k.R {
		case 'c':
			return ActExit, ""
		case 'f':
			s.Top += s.H
			s.moveVert(s.H)
		case 'b':
			s.Top -= s.H
			s.moveVert(-s.H)
		case 'd':
			s.Top += s.H / 2
			s.moveVert(s.H / 2)
		case 'u':
			s.Top -= s.H / 2
			s.moveVert(-s.H / 2)
		}
	case KeyRune:
		switch k.R {
		case 'q':
			return ActExit, ""
		case 'h':
			s.moveHoriz(-1)
		case 'l':
			s.moveHoriz(1)
		case 'j':
			s.moveVert(1)
		case 'k':
			s.moveVert(-1)
		case 'w':
			s.nextWord()
		case 'b':
			s.prevWord()
		case 'e':
			s.endWord()
		case '{':
			s.paragraph(-1)
		case '}':
			s.paragraph(1)
		case '0':
			s.moveHoriz(-s.Cur.Col - 1) // clamps to 0
		case '^':
			line := s.Buf[s.Cur.Line]
			c := 0
			for c < len(line) && !isWordRune(line[c]) {
				c++
			}
			if c >= len(line) {
				c = 0
			}
			s.Cur.Col = c
			s.want = c
		case '$':
			s.Cur.Col = s.maxCol(s.Cur.Line)
			s.want = s.Cur.Col
		case 'g':
			s.Cur = Pos{}
			s.want = 0
		case 'G':
			l := len(s.Lines) - 1
			for l > 0 && strings.TrimSpace(s.Lines[l]) == "" {
				l--
			}
			s.Cur = Pos{Line: l}
			s.want = 0
		case 'v', ' ':
			if s.Sel != nil && !s.Sel.Linewise {
				s.Sel = nil
			} else {
				s.Sel = &Sel{Anchor: s.Cur}
			}
		case 'V':
			if s.Sel != nil && s.Sel.Linewise {
				s.Sel = nil
			} else {
				s.Sel = &Sel{Anchor: s.Cur, Linewise: true}
			}
		case 'y':
			if text := s.yankText(); text != "" {
				return ActYank, text
			}
			s.Msg = "select with v or V first"
		case '/':
			s.Search = searchState{Typing: true}
		case '?':
			s.Search = searchState{Typing: true, Backward: true}
		case 'n':
			s.jumpMatch(searchDir(s.Search.Backward))
		case 'N':
			s.jumpMatch(-searchDir(s.Search.Backward))
		}
	}
	s.scrollToCursor()
	return ActNone, ""
}

func searchDir(backward bool) int {
	if backward {
		return -1
	}
	return 1
}
