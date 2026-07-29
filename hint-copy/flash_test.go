package main

import (
	"strings"
	"testing"
)

// styledFromLines makes a minimal [][]Cell (one unstyled cell per rune) so the
// line/copy flash builders have content to paint, standing in for an ANSI
// capture in tests.
func styledFromLines(lines []string) [][]Cell {
	out := make([][]Cell, len(lines))
	for i, l := range lines {
		row := make([]Cell, 0, len(l))
		for _, r := range l {
			row = append(row, Cell{R: r, Style: styleNone})
		}
		out[i] = row
	}
	return out
}

// flashFrameToken lights every cell of the copied candidate span and leaves
// the cell just before it untouched.
func TestFlashFrameTokenLightsCandidate(t *testing.T) {
	cfg := defaultConfig()
	text := "see /tmp/x.log ok"
	cands := Extract(text, cfg)
	if len(cands) == 0 {
		t.Fatal("expected a candidate")
	}
	v := &uiView{
		base:      NewScreen(80, 24),
		content:   Rect{X: 0, Y: 0, W: 80, H: 23},
		paintRect: Rect{X: 0, Y: 0, W: 80, H: 23},
	}
	c := cands[0] // "/tmp/x.log" starting at rune 4
	f := flashFrameToken(v, text, cands, c.Text)

	if got := f.at(c.StartRune, 0).Style; got != styleFlash {
		t.Errorf("candidate start cell style = %d, want styleFlash", got)
	}
	if got := f.at(c.EndRune-1, 0).Style; got != styleFlash {
		t.Errorf("candidate end cell style = %d, want styleFlash", got)
	}
	if got := f.at(c.StartRune-1, 0).Style; got == styleFlash {
		t.Error("cell before the candidate must not flash")
	}
}

// flashFrameToken only lights the picked string, not other candidates.
func TestFlashFrameTokenOnlyPicked(t *testing.T) {
	cfg := defaultConfig()
	text := "/tmp/a.log and /tmp/b.log"
	cands := Extract(text, cfg)
	var a, b Candidate
	for _, c := range cands {
		switch c.Text {
		case "/tmp/a.log":
			a = c
		case "/tmp/b.log":
			b = c
		}
	}
	if a.Text == "" || b.Text == "" {
		t.Fatalf("expected both candidates, got %+v", cands)
	}
	v := &uiView{
		base:      NewScreen(80, 24),
		content:   Rect{X: 0, Y: 0, W: 80, H: 23},
		paintRect: Rect{X: 0, Y: 0, W: 80, H: 23},
	}
	f := flashFrameToken(v, text, cands, a.Text)
	if f.at(a.StartRune, 0).Style != styleFlash {
		t.Error("picked candidate not flashed")
	}
	if f.at(b.StartRune, 0).Style == styleFlash {
		t.Error("non-picked candidate must not flash")
	}
}

// flashFrameLine lights the selected line range across the content width and
// leaves lines outside the range alone.
func TestFlashFrameLineLightsRange(t *testing.T) {
	cfg := defaultConfig()
	lines := []string{"one", "two", "three"}
	ll := AssignLineLabels(lines, 0, 23, cfg)
	v := &uiView{
		base:    NewScreen(80, 24),
		ll:      ll,
		content: Rect{X: 0, Y: 0, W: 80, H: 23},
	}
	margin := ll.Width() + 1
	f := flashFrameLine(v, styledFromLines(lines), 0, 1) // select rows 0 and 1

	for _, y := range []int{0, 1} {
		if got := f.at(margin, y).Style; got != styleFlash {
			t.Errorf("row %d content start style = %d, want styleFlash", y, got)
		}
	}
	if got := f.at(margin, 2).Style; got == styleFlash {
		t.Error("row outside the selection must not flash")
	}
}

// flashFrameCopy lights the yanked selection span.
func TestFlashFrameCopyLightsSelection(t *testing.T) {
	lines := []string{"hello world"}
	cs := NewCopyState(strings.Join(lines, "\n"), 80, 23, false)
	cs.Cur = Pos{Line: 0, Col: 0}
	cs.Sel = &Sel{Anchor: Pos{Line: 0, Col: 0}}
	cs.Cur = Pos{Line: 0, Col: 4} // select "hello"
	v := &uiView{
		base:    NewScreen(80, 24),
		content: Rect{X: 0, Y: 0, W: 80, H: 23},
	}
	f := flashFrameCopy(v, cs, nil)

	// The buffer's last line sits at the bottom of the viewport (tail view).
	row := cs.Cur.Line - cs.Top
	if got := f.at(0, v.content.Y+row).Style; got != styleFlash {
		t.Errorf("selection start style = %d, want styleFlash", got)
	}
	if got := f.at(4, v.content.Y+row).Style; got != styleFlash {
		t.Errorf("selection end style = %d, want styleFlash", got)
	}
	if got := f.at(6, v.content.Y+row).Style; got == styleFlash {
		t.Error("cell past the selection must not flash")
	}
}
