package main

import "testing"

func vpRows(selectable ...bool) []Row {
	rows := make([]Row, len(selectable))
	for i, s := range selectable {
		rows[i] = Row{Kind: RowText, Selectable: s, Item: i}
	}
	return rows
}

func TestViewportResetSnapsToSelectable(t *testing.T) {
	v := Viewport{H: 5}
	v.Reset(vpRows(false, false, true, false, true))
	if v.Cursor != 2 {
		t.Errorf("cursor = %d, want 2", v.Cursor)
	}

	v = Viewport{H: 5}
	v.Reset(vpRows(false, false))
	if v.Cursor != -1 {
		t.Errorf("cursor = %d, want -1 when nothing is selectable", v.Cursor)
	}
}

func TestViewportMoveSkipsNonSelectable(t *testing.T) {
	v := Viewport{H: 5}
	v.Reset(vpRows(true, false, false, true, true))
	v.MoveCursor(1)
	if v.Cursor != 3 {
		t.Errorf("cursor = %d, want 3", v.Cursor)
	}
	v.MoveCursor(1)
	if v.Cursor != 4 {
		t.Errorf("cursor = %d, want 4", v.Cursor)
	}
	v.MoveCursor(1) // at the end: stays
	if v.Cursor != 4 {
		t.Errorf("cursor = %d, want 4 (clamped)", v.Cursor)
	}
	v.MoveCursor(-2)
	if v.Cursor != 0 {
		t.Errorf("cursor = %d, want 0", v.Cursor)
	}
}

func TestViewportScrollClamps(t *testing.T) {
	v := Viewport{H: 3}
	v.Reset(vpRows(true, true, true, true, true, true))
	v.Scroll(100)
	if v.Top != 3 {
		t.Errorf("top = %d, want 3 (len-H)", v.Top)
	}
	v.Scroll(-100)
	if v.Top != 0 {
		t.Errorf("top = %d, want 0", v.Top)
	}
}

func TestViewportEnsureVisible(t *testing.T) {
	v := Viewport{H: 3}
	v.Reset(vpRows(true, true, true, true, true, true))
	v.MoveCursor(5)
	if v.Cursor != 5 {
		t.Fatalf("cursor = %d", v.Cursor)
	}
	if v.Top != 3 {
		t.Errorf("top = %d, want 3 so the cursor is visible", v.Top)
	}
}

func TestViewportJumpTo(t *testing.T) {
	rows := vpRows(true, true, true, true)
	rows[2].Kind = RowHunk
	v := Viewport{H: 10}
	v.Reset(rows)
	if !v.JumpTo(func(r Row) bool { return r.Kind == RowHunk }, 1) || v.Cursor != 2 {
		t.Errorf("jump forward: cursor = %d", v.Cursor)
	}
	if v.JumpTo(func(r Row) bool { return r.Kind == RowHunk }, 1) {
		t.Error("no further hunk: jump must return false")
	}
}

func TestViewportResetBeforeFirstPaintKeepsTop(t *testing.T) {
	v := Viewport{} // H == 0: no Paint has happened yet
	v.Reset(vpRows(true, true, true, true))
	if v.Top != 0 || v.Cursor != 0 {
		t.Errorf("top=%d cursor=%d, want 0/0 before first paint", v.Top, v.Cursor)
	}
}

func TestViewportResetAfterFoldKeepsCursorValid(t *testing.T) {
	v := Viewport{H: 5}
	v.Reset(vpRows(true, true, true, true, true, true, true, true))
	v.MoveCursor(7)
	// Folding shrinks the row list under the cursor.
	v.Reset(vpRows(true, true, true))
	if v.Cursor < 0 || v.Cursor >= 3 {
		t.Errorf("cursor = %d out of bounds after shrink", v.Cursor)
	}
}
