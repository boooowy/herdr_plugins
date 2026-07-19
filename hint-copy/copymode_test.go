package main

import (
	"strings"
	"testing"
)

func runes(s *CopyState, keys string) (Action, string) {
	var act Action
	var text string
	for _, r := range keys {
		act, text = s.Handle(Key{Kind: KeyRune, R: r})
	}
	return act, text
}

func newCS(text string, w, h int) *CopyState { return NewCopyState(text, w, h, false) }

func TestCopyStateInitialPosition(t *testing.T) {
	s := newCS("one\ntwo\nthree\n\n", 80, 3)
	if s.Cur != (Pos{Line: 2}) {
		t.Errorf("cursor = %+v, want line 2 (last non-empty)", s.Cur)
	}
	if s.Top != 2 { // 5 lines, h=3 → tail view starts at 2
		t.Errorf("top = %d", s.Top)
	}
}

func TestCopyStateMoveClamps(t *testing.T) {
	s := newCS("abc\nde", 80, 10)
	runes(s, "g")
	runes(s, "kkhh") // beyond top-left stays put
	if s.Cur != (Pos{}) {
		t.Errorf("cur = %+v", s.Cur)
	}
	runes(s, "lllll") // clamps to last char of "abc"
	if s.Cur.Col != 2 {
		t.Errorf("col = %d", s.Cur.Col)
	}
}

func TestCopyStateStickyColumn(t *testing.T) {
	s := newCS("longline\nab\nlongline", 80, 10)
	runes(s, "g$") // col 7
	runes(s, "j")  // "ab" clamps to col 1
	if s.Cur.Col != 1 {
		t.Errorf("clamped col = %d", s.Cur.Col)
	}
	runes(s, "j") // back to a long line: sticky column restores 7
	if s.Cur.Col != 7 {
		t.Errorf("sticky col = %d", s.Cur.Col)
	}
}

func TestCopyStateWordMotions(t *testing.T) {
	s := newCS("foo bar\nbaz", 80, 10)
	runes(s, "g")
	runes(s, "w")
	if s.Cur != (Pos{Line: 0, Col: 4}) {
		t.Errorf("w → %+v", s.Cur)
	}
	runes(s, "w") // crosses to next line
	if s.Cur != (Pos{Line: 1, Col: 0}) {
		t.Errorf("w cross → %+v", s.Cur)
	}
	runes(s, "b") // back to bar
	if s.Cur != (Pos{Line: 0, Col: 4}) {
		t.Errorf("b → %+v", s.Cur)
	}
	runes(s, "e") // end of bar
	if s.Cur != (Pos{Line: 0, Col: 6}) {
		t.Errorf("e → %+v", s.Cur)
	}
}

func TestCopyStateParagraphAndLineMotions(t *testing.T) {
	s := newCS("a\nb\n\nc\nd", 80, 10)
	runes(s, "g}")
	if s.Cur.Line != 2 {
		t.Errorf("} → line %d", s.Cur.Line)
	}
	runes(s, "}")
	if s.Cur.Line != 4 {
		t.Errorf("}} → line %d", s.Cur.Line)
	}
	runes(s, "{")
	if s.Cur.Line != 2 {
		t.Errorf("{ → line %d", s.Cur.Line)
	}
	s2 := newCS("  indented text", 80, 10)
	runes(s2, "$")
	if s2.Cur.Col != 14 {
		t.Errorf("$ → %d", s2.Cur.Col)
	}
	runes(s2, "0")
	if s2.Cur.Col != 0 {
		t.Errorf("0 → %d", s2.Cur.Col)
	}
	runes(s2, "^")
	if s2.Cur.Col != 2 {
		t.Errorf("^ → %d", s2.Cur.Col)
	}
}

func TestCopyStateCharwiseYank(t *testing.T) {
	s := newCS("hello world", 80, 10)
	runes(s, "g")
	runes(s, "v")
	runes(s, "eeee") // to end of "world" (clamped)
	act, text := runes(s, "y")
	if act != ActYank || text != "hello world" {
		t.Errorf("yank = %v %q", act, text)
	}
}

func TestCopyStateCharwiseYankMultiline(t *testing.T) {
	s := newCS("first line  \nmiddle\nlast line", 80, 10)
	runes(s, "g")
	runes(s, "wv") // anchor at "line" (col 6)
	runes(s, "G")  // cursor line 2 col 0
	act, text := runes(s, "y")
	want := "line\nmiddle\nl"
	if act != ActYank || text != want {
		t.Errorf("yank = %q, want %q", text, want)
	}
}

func TestCopyStateLinewiseYank(t *testing.T) {
	s := newCS("aaa\n  bbb\nccc", 80, 10)
	runes(s, "g")
	runes(s, "Vj")
	act, text := runes(s, "y")
	if act != ActYank || text != "aaa\n  bbb" {
		t.Errorf("yank = %v %q", act, text)
	}
}

func TestCopyStateYankNeedsSelection(t *testing.T) {
	s := newCS("abc", 80, 10)
	act, _ := runes(s, "y")
	if act != ActNone || s.Msg == "" {
		t.Errorf("act=%v msg=%q", act, s.Msg)
	}
}

func TestCopyStateSearchSmartCase(t *testing.T) {
	s := newCS("Foo foo FOO\nbar", 80, 10)
	runes(s, "g")
	s.Handle(Key{Kind: KeyRune, R: '/'})
	for _, r := range "foo" {
		s.Handle(Key{Kind: KeyRune, R: r})
	}
	if len(s.Search.Matches) != 3 { // lowercase query: case-insensitive
		t.Fatalf("matches = %d", len(s.Search.Matches))
	}
	s.Handle(Key{Kind: KeyEnter})
	if s.Cur != (Pos{Line: 0, Col: 4}) { // first match after col 0
		t.Errorf("cur = %+v", s.Cur)
	}
	runes(s, "n")
	if s.Cur.Col != 8 {
		t.Errorf("n → %+v", s.Cur)
	}
	runes(s, "n") // wraps around
	if s.Cur != (Pos{Line: 0, Col: 0}) {
		t.Errorf("wrap → %+v", s.Cur)
	}
	// Uppercase query: exact case.
	s.Handle(Key{Kind: KeyRune, R: '/'})
	for _, r := range "FOO" {
		s.Handle(Key{Kind: KeyRune, R: r})
	}
	if len(s.Search.Matches) != 1 {
		t.Errorf("smart-case matches = %d", len(s.Search.Matches))
	}
}

func TestCopyStateStagedEsc(t *testing.T) {
	s := newCS("abc def", 80, 10)
	runes(s, "g/")
	// typing → Esc cancels typing only
	s.Handle(Key{Kind: KeyRune, R: 'a'})
	if act, _ := s.Handle(Key{Kind: KeyEsc}); act != ActNone || s.Search.Typing {
		t.Fatal("esc should cancel typing")
	}
	runes(s, "v")
	if act, _ := s.Handle(Key{Kind: KeyEsc}); act != ActNone || s.Sel != nil {
		t.Fatal("esc should clear selection")
	}
	if act, _ := s.Handle(Key{Kind: KeyEsc}); act != ActExit {
		t.Fatal("esc should exit")
	}
}

func TestCopyStatePaging(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("line\n")
	}
	s := newCS(strings.TrimRight(b.String(), "\n"), 80, 10)
	startLine := s.Cur.Line
	s.Handle(Key{Kind: KeyCtrl, R: 'b'})
	if s.Cur.Line != startLine-10 {
		t.Errorf("ctrl+b → %d (start %d)", s.Cur.Line, startLine)
	}
	s.Handle(Key{Kind: KeyCtrl, R: 'u'})
	if s.Cur.Line != startLine-15 {
		t.Errorf("ctrl+u → %d", s.Cur.Line)
	}
	for i := 0; i < 50; i++ {
		s.Handle(Key{Kind: KeyCtrl, R: 'b'})
	}
	if s.Top != 0 || s.Cur.Line != 0 {
		t.Errorf("top clamp: top=%d line=%d", s.Top, s.Cur.Line)
	}
}
