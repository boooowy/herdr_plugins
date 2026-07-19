package main

import "strings"

// composite reproduces the whole tab inside the full-tab overlay so that
// opening hint-copy does not appear to move any pane: every pane's visible
// content is captured WITH its colors (ANSI read) and painted at its real
// position, borders use the theme-ish colors (focused pane keeps its focus
// color), and the hint interaction happens only inside the target pane's
// content rect.
//
// Geometry (measured against herdr 0.7.4): pane rects tile the tab area
// exactly in absolute terminal cells; each pane draws a 1-cell border ring
// just inside its own rect, so its content is rect inset by 1. The overlay
// itself is a zoomed pane with the same ring, so overlay-content coordinates
// are screen coordinates minus (area + 1); the tab-perimeter borders fall
// under the overlay's own border.
type composite struct {
	base   *Screen             // static: every pane's colored content + borders
	target Rect                // target pane's content region, overlay coords
	plain  map[string][]string // pane id → plain lines (target's reused by ui)
	styled map[string][][]Cell // pane id → styled lines
}

// buildComposite captures the tab. It returns nil — meaning "fall back to
// the plain full-screen frame" — when the tab has a single pane, the layout
// call fails, or the overlay's terminal size doesn't match the measured
// area-minus-border relation (dw/dh of 2-3 cells; anything else means the
// geometry assumptions don't hold on this setup).
func buildComposite(client *herdrClient, targetID string, w, h int, cfg Config, tbl *styleTable) *composite {
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
	base.styles = tbl
	titles := client.paneTitles()
	borderSt := tbl.id(colorCode(cfg.BorderFg, false))
	focusSt := tbl.id(colorCode(cfg.BorderFocusFg, false))

	comp := &composite{
		base:   base,
		plain:  make(map[string][]string, len(lay.Panes)),
		styled: make(map[string][][]Cell, len(lay.Panes)),
	}
	found := false
	for _, p := range lay.Panes {
		ansiText, _, rerr := client.paneReadFull(p.PaneID, "visible", 0, true)
		if rerr != nil {
			ansiText = ""
		}
		cells, plain := parseANSI(ansiText, tbl)
		comp.plain[p.PaneID] = plain
		comp.styled[p.PaneID] = cells

		content := Rect{
			X: p.Rect.X + 1 - ox,
			Y: p.Rect.Y + 1 - oy,
			W: p.Rect.Width - 2,
			H: p.Rect.Height - 2,
		}
		st := borderSt
		if p.PaneID == targetID {
			st = focusSt // the focused pane keeps its focus-colored ring
			comp.target = content
			found = true
		}
		box := Rect{X: p.Rect.X - ox, Y: p.Rect.Y - oy, W: p.Rect.Width, H: p.Rect.Height}
		paintBorder(base, box, st)
		if title := titles[p.PaneID]; title != "" {
			paintBorderTitle(base, box, title, st)
		}
		paintPaneCells(base, content, cells)
	}
	if !found || comp.target.W < 2 || comp.target.H < 1 {
		return nil
	}
	return comp
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

// paintBorderTitle writes the pane's caption into its top border, herdr
// style.
func paintBorderTitle(s *Screen, box Rect, title string, st StyleID) {
	if box.W < 8 {
		return
	}
	caption := " " + title + " "
	maxW := box.W - 4
	s.WriteString(box.X+2, box.Y, truncate(caption, maxW), st, box.X+2+maxW)
}

// paintPaneCells fills a pane's content rect with its captured styled cells,
// top-aligned (a live terminal viewport fills from the top); bottom lines
// win if the capture somehow exceeds the rect.
func paintPaneCells(s *Screen, r Rect, lines [][]Cell) {
	offset := 0
	if len(lines) > r.H {
		offset = len(lines) - r.H
	}
	for li := 0; li < r.H && offset+li < len(lines); li++ {
		s.PutCells(r.X, r.Y+li, lines[offset+li], r.X+r.W)
	}
}

// clearRect blanks a region back to the default style (used before painting
// line/copy mode over the colored composite base).
func clearRect(s *Screen, r Rect) {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			s.Set(x, y, ' ', styleNone)
		}
	}
}

// joinPlain reassembles the plain text of parsed lines.
func joinPlain(lines []string) string { return strings.Join(lines, "\n") }
