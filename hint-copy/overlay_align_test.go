package main

import (
	"strings"
	"testing"
)

// Regression: in composite (split) mode the base pane content is painted with
// paintPaneCells at the full content rect, while the footer clamp shrinks the
// rect the UI paints modes into. Token mode overlays labels onto the already-
// painted base, so it must map candidate lines with the SAME offset the base
// used — otherwise every label shifts up onto the wrong line (labels appearing
// mid-word on unrelated text). This asserts the label lands on the exact row
// and column of the content it labels when both use the same paint rect.
func TestTokenOverlayLabelAlignsWithContentRow(t *testing.T) {
	cfg := defaultConfig()
	lines := []string{
		"line0", "line1", "line2", "line3",
		"line4", "line5", "grab /tmp/x.log here",
	} // 7 lines
	text := strings.Join(lines, "\n")
	cands := Extract(text, cfg)
	var path Candidate
	for _, c := range cands {
		if c.Text == "/tmp/x.log" {
			path = c
		}
	}
	if path.Text == "" {
		t.Fatal("expected a path candidate")
	}
	lm := AssignLabels(cands, cfg)

	// 7 lines into a 6-row rect: the bottom lines win, offset = 1.
	paint := Rect{X: 0, Y: 0, W: 40, H: 6}
	base := NewScreen(40, 7)
	base.styles = newStyleTable()
	paintPaneCells(base, paint, styledFromLines(lines))

	frame := base.Clone()
	PaintTokenOverlay(frame, paint, text, cands, lm, "")

	offset := len(lines) - paint.H // 1
	y := paint.Y + path.Line - offset
	x := paint.X + path.StartRune // "/tmp/x.log" begins after "grab "
	lab := []rune(lm.LabelFor(path.Text))[0]

	if got := frame.at(x, y).R; got != lab {
		t.Errorf("label at (%d,%d) = %q, want %q on the content row", x, y, string(got), string(lab))
	}
	if got := frame.at(x, y-1).R; got == lab {
		t.Error("label leaked onto the row above its content (offset mismatch)")
	}
}
