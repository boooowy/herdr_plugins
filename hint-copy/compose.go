package main

import "strings"

// composite reproduces the whole tab inside the full-tab overlay so that
// opening hint-copy does not appear to move any pane: every pane's visible
// content is painted at its real position, and the hint interaction happens
// only inside the target pane's content rect.
//
// Geometry (measured against herdr 0.7.4): pane rects tile the tab area
// exactly in absolute terminal cells; each pane draws a 1-cell border ring
// just inside its own rect, so its content is rect inset by 1. The overlay
// itself is a zoomed pane with the same ring, so overlay-content coordinates
// are screen coordinates minus (area + 1); the tab-perimeter borders fall
// under the overlay's own border. Border colors are an approximation of the
// user's theme (accepted trade-off).
type composite struct {
	base   *Screen           // static: every pane's content + borders
	target Rect              // target pane's content region, overlay coords
	texts  map[string]string // pane id → visible text (target's reused by ui)
}

// buildComposite captures the tab. It returns nil — meaning "fall back to
// the plain full-screen frame" — when the tab has a single pane, the layout
// call fails, or the overlay's terminal size doesn't match the measured
// area-minus-border relation (dw/dh of 2-3 cells; anything else means the
// geometry assumptions don't hold on this setup).
func buildComposite(client *herdrClient, targetID string, w, h int) *composite {
	lay, err := client.paneLayout(targetID)
	if err != nil || len(lay.Panes) < 2 {
		return nil
	}
	dw, dh := lay.Area.Width-w, lay.Area.Height-h
	if dw < 1 || dw > 4 || dh < 1 || dh > 4 {
		return nil
	}
	// Overlay content origin in absolute screen coordinates.
	ox, oy := lay.Area.X+1, lay.Area.Y+1

	base := NewScreen(w, h)
	texts := make(map[string]string, len(lay.Panes))
	var target Rect
	found := false
	for _, p := range lay.Panes {
		text, err := client.paneRead(p.PaneID, "visible")
		if err != nil {
			text = ""
		}
		texts[p.PaneID] = text

		content := Rect{
			X: p.Rect.X + 1 - ox,
			Y: p.Rect.Y + 1 - oy,
			W: p.Rect.Width - 2,
			H: p.Rect.Height - 2,
		}
		// The focused (target) pane keeps a normal-intensity border so the
		// composite preserves the focus cue; the rest are dim.
		borderStyle := styleBorder
		if p.PaneID == targetID {
			borderStyle = styleNone
			target = content
			found = true
		}
		paintBorder(base, Rect{X: p.Rect.X - ox, Y: p.Rect.Y - oy, W: p.Rect.Width, H: p.Rect.Height}, borderStyle)
		paintPaneText(base, content, text)
	}
	if !found || target.W < 2 || target.H < 1 {
		return nil
	}
	return &composite{base: base, target: target, texts: texts}
}

// paintBorder draws a single-line box on the rect perimeter. Screen.Set
// clips out-of-bounds cells, which is exactly what we want at the tab edges
// (those borders live under the overlay's own ring).
func paintBorder(s *Screen, r Rect, st StyleID) {
	x2, y2 := r.X+r.W-1, r.Y+r.H-1
	for x := r.X + 1; x < x2; x++ {
		s.Set(x, r.Y, '─', st)
		s.Set(x, y2, '─', st)
	}
	for y := r.Y + 1; y < y2; y++ {
		s.Set(r.X, y, '│', st)
		s.Set(x2, y, '│', st)
	}
	s.Set(r.X, r.Y, '╭', st)
	s.Set(x2, r.Y, '╮', st)
	s.Set(r.X, y2, '╰', st)
	s.Set(x2, y2, '╯', st)
}

// paintPaneText fills a pane's content rect with its captured visible text,
// top-aligned (a live terminal viewport fills from the top); bottom lines
// win if the capture somehow exceeds the rect.
func paintPaneText(s *Screen, r Rect, text string) {
	lines := strings.Split(text, "\n")
	offset := 0
	if len(lines) > r.H {
		offset = len(lines) - r.H
	}
	for li := 0; li < r.H && offset+li < len(lines); li++ {
		s.WriteString(r.X, r.Y+li, lines[offset+li], styleNone, r.X+r.W)
	}
}
