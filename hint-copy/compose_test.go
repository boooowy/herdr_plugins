package main

import (
	"strings"
	"testing"
)

// Composite painting helpers are exercised without a live herdr: paintBorder
// and paintPaneText are pure Screen operations.

func TestPaintBorderAndPaneText(t *testing.T) {
	s := NewScreen(10, 5)
	paintBorder(s, Rect{X: 0, Y: 0, W: 10, H: 5}, styleBorder)
	paintPaneText(s, Rect{X: 1, Y: 1, W: 8, H: 3}, "hello\nworld")
	rows := strings.Split(flushPlain(s), "\r\n")
	if rows[0] != "╭────────╮" {
		t.Errorf("top border = %q", rows[0])
	}
	if rows[1] != "│hello" { // trailing blanks trimmed, right border styled? no: border is styled → survives
		// right border cell is styleBorder (not styleNone) so it survives the trim
		t.Logf("row1 = %q", rows[1])
	}
	if !strings.Contains(rows[1], "hello") || !strings.Contains(rows[2], "world") {
		t.Errorf("content rows: %q %q", rows[1], rows[2])
	}
	if !strings.HasPrefix(rows[1], "│") || !strings.HasSuffix(rows[1], "│") {
		t.Errorf("side borders missing: %q", rows[1])
	}
	if rows[4] != "╰────────╯" {
		t.Errorf("bottom border = %q", rows[4])
	}
}

func TestPaintBorderClipsOffscreen(t *testing.T) {
	s := NewScreen(5, 3)
	// Rect partially outside the screen (tab-edge borders live under the
	// overlay's own ring) must not panic and must clip.
	paintBorder(s, Rect{X: -1, Y: -1, W: 8, H: 6}, styleBorder)
	out := flushPlain(s)
	if strings.Contains(out, "╭") {
		t.Errorf("offscreen corner drawn: %q", out)
	}
}

func TestPaintPaneTextPrefersBottom(t *testing.T) {
	s := NewScreen(10, 2)
	paintPaneText(s, Rect{X: 0, Y: 0, W: 10, H: 2}, "one\ntwo\nthree")
	rows := strings.Split(flushPlain(s), "\r\n")
	if rows[0] != "two" || rows[1] != "three" {
		t.Errorf("rows = %v", rows)
	}
}

// Painting hints into an offset rect keeps positions: a two-pane composite
// where the target rect starts at x=6 must show the label at x=6.
func TestPaintTokensIntoOffsetRect(t *testing.T) {
	s := NewScreen(20, 4)
	cfg := defaultConfig()
	text := "x /tmp/a"
	cands := Extract(text, cfg)
	lm := AssignLabels(cands, cfg)
	PaintTokens(s, Rect{X: 6, Y: 1, W: 10, H: 2}, text, cands, lm, "")
	rows := strings.Split(flushPlain(s), "\r\n")
	if rows[1] != "      x atmp/a" {
		t.Errorf("row1 = %q", rows[1])
	}
}
