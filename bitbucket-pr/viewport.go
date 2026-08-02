package main

// The viewport implements atlas.nvim's line_map idea: every rendered row
// carries its semantic item ({Kind, Item}), so cursor movement, Enter,
// folding, and semantic jumps ([h / ]f) all resolve through one structure.

// RowKind classifies what a rendered row means.
type RowKind int

const (
	RowText          RowKind = iota // plain content (description, separators)
	RowPR                           // one PR in the list (Item: int index)
	RowFile                         // one file in the diffstat (Item: int index)
	RowHunk                         // a hunk header (Item: hunkRef)
	RowDiffLine                     // one diff line (Item: hunkRef)
	RowComment                      // a comment block line (Item: comment id)
	RowTab                          // the detail tab bar
	RowOutlineDir                   // Outline directory (Item: outlineRowRef)
	RowOutlineFile                  // Outline file/test group (Item: outlineRowRef)
	RowOutlineSymbol                // Outline symbol (Item: outlineRowRef)
)

// Span is one styled segment of a row, painted left to right.
type Span struct {
	Text  string
	Style StyleID
}

// RowAvatar places one account image relative to the beginning of a row.
// The row text reserves Cols cells at Col so the graphics layer never covers
// useful text. SelectedOnly is available to views that intentionally limit an
// image to the cursor row.
type RowAvatar struct {
	URL          string
	AccountID    string
	Col          int
	Cols         int
	Rows         int
	Badge        AvatarBadge
	SelectedOnly bool
}

// Row is one rendered line plus its semantic identity.
type Row struct {
	Kind       RowKind
	Spans      []Span
	Item       any
	Selectable bool
	Avatars    []RowAvatar
	// CursorRows groups this row with the following rows for cursor painting.
	// Zero keeps the default one-row inverse cursor.
	CursorRows int
}

// row is a convenience constructor.
func row(kind RowKind, item any, selectable bool, spans ...Span) Row {
	return Row{Kind: kind, Spans: spans, Item: item, Selectable: selectable}
}

func textRow(spans ...Span) Row { return Row{Kind: RowText, Spans: spans} }

// Viewport scrolls a Row list inside a fixed-height window and keeps a
// cursor that only rests on selectable rows.
type Viewport struct {
	Rows   []Row
	Cursor int // index into Rows; -1 when nothing is selectable
	Top    int // first visible row
	H      int // window height in rows
}

// Reset installs new rows, clamps the window, and snaps the cursor to a
// selectable row (keeping the previous cursor position when still valid).
func (v *Viewport) Reset(rows []Row) {
	v.Rows = rows
	if v.Cursor >= len(rows) {
		v.Cursor = len(rows) - 1
	}
	if v.Cursor < 0 {
		v.Cursor = 0
	}
	if !v.selectable(v.Cursor) {
		if i := v.nextSelectable(v.Cursor, 1); i >= 0 {
			v.Cursor = i
		} else if i := v.nextSelectable(v.Cursor, -1); i >= 0 {
			v.Cursor = i
		} else {
			v.Cursor = -1
		}
	}
	v.clampTop()
	v.EnsureVisible()
}

func (v *Viewport) selectable(i int) bool {
	return i >= 0 && i < len(v.Rows) && v.Rows[i].Selectable
}

// nextSelectable finds the nearest selectable row from i (exclusive when
// moving, inclusive fallback handled by Reset) in direction dir.
func (v *Viewport) nextSelectable(i, dir int) int {
	for j := i; j >= 0 && j < len(v.Rows); j += dir {
		if v.Rows[j].Selectable {
			return j
		}
	}
	return -1
}

// MoveCursor moves the cursor by delta selectable rows.
func (v *Viewport) MoveCursor(delta int) {
	if v.Cursor < 0 {
		return
	}
	dir := 1
	if delta < 0 {
		dir, delta = -1, -delta
	}
	for ; delta > 0; delta-- {
		if j := v.nextSelectable(v.Cursor+dir, dir); j >= 0 {
			v.Cursor = j
		} else {
			break
		}
	}
	v.EnsureVisible()
}

// Current returns the row under the cursor (nil when none).
func (v *Viewport) Current() *Row {
	if v.Cursor < 0 || v.Cursor >= len(v.Rows) {
		return nil
	}
	return &v.Rows[v.Cursor]
}

// Scroll moves the window by delta rows and drags the cursor along so it
// stays visible.
func (v *Viewport) Scroll(delta int) {
	v.Top += delta
	v.clampTop()
	if v.Cursor >= 0 {
		if v.Cursor < v.Top {
			if j := v.nextSelectable(v.Top, 1); j >= 0 {
				v.Cursor = j
			}
		} else if v.Cursor >= v.Top+v.H {
			if j := v.nextSelectable(v.Top+v.H-1, -1); j >= 0 {
				v.Cursor = j
			}
		}
	}
}

// JumpTo moves the cursor to the next row (in direction dir) matching pred,
// scrolling it into view. Returns false when there is no match.
func (v *Viewport) JumpTo(pred func(Row) bool, dir int) bool {
	for j := v.Cursor + dir; j >= 0 && j < len(v.Rows); j += dir {
		if pred(v.Rows[j]) && v.Rows[j].Selectable {
			v.Cursor = j
			v.EnsureVisible()
			return true
		}
	}
	return false
}

// EnsureVisible scrolls the minimum needed to bring the cursor into view.
// Before the first Paint the window height is unknown (H == 0); scrolling
// then would push the cursor row out of the initial view, so wait for Paint.
func (v *Viewport) EnsureVisible() {
	if v.Cursor < 0 || v.H <= 0 {
		return
	}
	if v.Cursor < v.Top {
		v.Top = v.Cursor
	}
	cursorRows := 1
	if v.Cursor < len(v.Rows) && v.Rows[v.Cursor].CursorRows > cursorRows {
		cursorRows = v.Rows[v.Cursor].CursorRows
	}
	cursorEnd := v.Cursor + cursorRows
	if cursorEnd > len(v.Rows) {
		cursorEnd = len(v.Rows)
	}
	if cursorEnd > v.Top+v.H {
		v.Top = cursorEnd - v.H
	}
	// When the cursor group is taller than the viewport, keep its selectable
	// first row visible instead of showing only the trailing context rows.
	if v.Top > v.Cursor {
		v.Top = v.Cursor
	}
	v.clampTop()
}

func (v *Viewport) clampTop() {
	max := len(v.Rows) - v.H
	if max < 0 {
		max = 0
	}
	if v.Top > max {
		v.Top = max
	}
	if v.Top < 0 {
		v.Top = 0
	}
}

// Paint draws the visible rows into r, highlighting the cursor row by
// restyling its plain cells.
func (v *Viewport) Paint(s *Screen, r Rect) {
	v.H = r.H
	v.clampTop()
	for i := 0; i < r.H; i++ {
		idx := v.Top + i
		if idx >= len(v.Rows) {
			break
		}
		x := r.X
		for _, sp := range v.Rows[idx].Spans {
			x = s.WriteString(x, r.Y+i, sp.Text, sp.Style, r.X+r.W)
		}
		for _, avatar := range v.Rows[idx].Avatars {
			if avatar.SelectedOnly && idx != v.Cursor {
				continue
			}
			ax := r.X + avatar.Col
			ay := r.Y + i
			if ax < r.X || ay < r.Y || ax+avatar.Cols > r.X+r.W || ay+avatar.Rows > r.Y+r.H {
				continue
			}
			s.AddAvatar(ax, ay, avatar.Cols, avatar.Rows, avatar.URL, avatar.AccountID, avatar.Badge)
		}
		if idx == v.Cursor && v.Rows[idx].CursorRows <= 1 {
			s.StyleRow(r.Y+i, r.X, r.X+r.W, styleNone, styleCursor)
		}
	}
	if v.Cursor >= 0 && v.Cursor < len(v.Rows) && v.Rows[v.Cursor].CursorRows > 1 {
		end := v.Cursor + v.Rows[v.Cursor].CursorRows
		if end > len(v.Rows) {
			end = len(v.Rows)
		}
		focusViewportRows(s, v, r, v.Cursor, end)
	}
}
