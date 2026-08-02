package main

import (
	"strings"
	"testing"
)

func TestPaintModalCentersAndPreservesOutsideCells(t *testing.T) {
	s := NewScreen(80, 24)
	s.WriteString(0, 0, "background", styleNone, s.W)
	result := paintModal(s, modalSpec{
		Title:          "承認",
		Lines:          []modalLine{{Spans: []Span{{Text: "このPRを承認しますか？", Style: styleTitle}}, Center: true}},
		Footer:         modalLine{Spans: []Span{{Text: "y 実行", Style: styleDim}}, Center: true},
		PreferredWidth: 54,
	})

	if result.Rect.X != (s.W-result.Rect.W)/2 || result.Rect.Y != (s.H-result.Rect.H)/2 {
		t.Fatalf("modal rect is not centered: %+v", result.Rect)
	}
	if got := strings.TrimSpace(screenRow(s, 0)); got != "background" {
		t.Errorf("outside row = %q", got)
	}
	painted := ""
	for y := result.Rect.Y; y < result.Rect.Y+result.Rect.H; y++ {
		painted += screenRow(s, y)
	}
	if !strings.Contains(painted, "このPRを承認しますか？") || !strings.Contains(painted, "y 実行") {
		t.Errorf("modal content = %q", painted)
	}
	if got := s.at(result.Rect.X, result.Rect.Y).R; got != '┌' {
		t.Errorf("top-left border = %q", got)
	}
}

func TestPaintModalWrapsInNarrowScreen(t *testing.T) {
	s := NewScreen(24, 10)
	result := paintModal(s, modalSpec{
		Title:          "承認",
		Lines:          []modalLine{{Spans: []Span{{Text: "このPRを承認しますか？", Style: styleNone}}}},
		PreferredWidth: 54,
	})
	if result.ContentRows < 2 {
		t.Fatalf("content rows = %d, want wrapped message", result.ContentRows)
	}
	if result.Rect.X < 0 || result.Rect.Y < 0 || result.Rect.X+result.Rect.W > s.W || result.Rect.Y+result.Rect.H > s.H {
		t.Fatalf("modal escaped screen: screen=%dx%d rect=%+v", s.W, s.H, result.Rect)
	}
}

func TestPaintModalHidesOnlyIntersectingAvatars(t *testing.T) {
	s := NewScreen(80, 24)
	s.AddAvatar(1, 1, 2, 2, "outside", "outside", AvatarBadgeNone)
	s.AddAvatar(39, 12, 2, 2, "inside", "inside", AvatarBadgeNone)
	paintModal(s, modalSpec{
		Title:          "確認",
		Lines:          []modalLine{{Spans: []Span{{Text: "実行しますか？", Style: styleNone}}}},
		PreferredWidth: 40,
	})
	if got := s.Avatars(); len(got) != 1 || got[0].URL != "outside" {
		t.Fatalf("remaining avatars = %+v", got)
	}
}

func TestHelpOverlayScrollAndClose(t *testing.T) {
	a := &app{w: 60, h: 12}
	a.openHelp("detail")
	s := NewScreen(a.w, a.h)
	paintHelpOverlay(a, s)
	if a.help == nil || a.help.maxTop == 0 || a.help.pageRows == 0 {
		t.Fatalf("help after paint = %+v", a.help)
	}

	a.help.handle(a, Key{Kind: KeyCtrl, R: 'd'})
	if a.help.top == 0 {
		t.Error("Ctrl-d did not scroll help")
	}
	a.help.handle(a, Key{Kind: KeyRune, R: '?'})
	if a.help != nil {
		t.Error("? did not close help")
	}
}

func TestHelpRowsKeepFixedColumnsAndModalFrameAcrossWidths(t *testing.T) {
	for _, width := range []int{56, 60, 72, 84, 88, 100} {
		a := &app{w: width, h: 30}
		a.openHelp("detail")
		s := NewScreen(a.w, a.h)
		paintHelpOverlay(a, s)

		modalW := modalWidth(width, 84)
		x := (width - modalW) / 2
		// Every visible inner row must retain both frame edges after the
		// variable-width Japanese descriptions have been painted.
		for y := 3; y < a.h-3; y++ {
			if s.at(x, y).R != '│' || s.at(x+modalW-1, y).R != '│' {
				t.Fatalf("width=%d row=%d frame=(%q,%q)", width, y, s.at(x, y).R, s.at(x+modalW-1, y).R)
			}
		}

		contentWidth := modalContentWidth(width, 84)
		for _, line := range helpModalLines(helpByCtx["detail"].lines, contentWidth) {
			lineWidth := 0
			for _, span := range line.Spans {
				lineWidth += displayWidth(span.Text)
			}
			if !line.NoWrap || lineWidth > contentWidth {
				t.Fatalf("width=%d content=%d lineWidth=%d line=%+v", width, contentWidth, lineWidth, line)
			}
		}
	}
}

func TestDetailConfirmationModalIsPaintedLast(t *testing.T) {
	a, v := prActionFixture("OPEN")
	a.stack = []view{v}
	v.beginPRAction(a, detailActionApprove)
	s := NewScreen(a.w, a.h)
	v.render(a, s)
	paintFooter(a, s)
	paintOverlay(a, s)

	var painted strings.Builder
	for y := 0; y < s.H; y++ {
		painted.WriteString(screenRow(s, y))
	}
	if !strings.Contains(painted.String(), "このPRを承認しますか？") {
		t.Fatalf("confirmation modal not painted: %q", painted.String())
	}
}
