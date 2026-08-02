package main

import (
	"strings"
	"testing"
)

func TestListCursorHighlightsWholePRItem(t *testing.T) {
	a := &app{w: 80, h: 10}
	a.prs = []PullRequest{
		{ID: 1, Title: "selected"},
		{ID: 2, Title: "not selected"},
	}
	v := &listView{}
	v.rebuild(a)

	s := NewScreen(a.w, a.h)
	v.vp.Paint(s, Rect{X: 0, Y: 0, W: a.w, H: a.h})

	if got := s.at(0, 0).Style; got != onFocusBg(styleMeta) {
		t.Errorf("selected title style = %d, want %d", got, onFocusBg(styleMeta))
	}
	if got := s.at(0, 1).Style; got != onFocusBg(styleDim) {
		t.Errorf("selected metadata style = %d, want %d", got, onFocusBg(styleDim))
	}
	if got := s.at(0, 2).Style; got == onFocusBg(styleMeta) {
		t.Errorf("next PR unexpectedly focused: style=%d", got)
	}
}

func TestListUsesCompactDividerWithoutAddingRows(t *testing.T) {
	a := &app{w: 80, h: 10}
	a.prs = []PullRequest{{ID: 1, Title: "one"}, {ID: 2, Title: "two"}}
	v := &listView{}
	v.rebuild(a)

	if got, want := len(v.vp.Rows), len(a.prs)*2; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
	for i := 1; i < len(v.vp.Rows); i += 2 {
		if got := spansText(v.vp.Rows[i]); !strings.HasPrefix(got, listItemDivider) {
			t.Errorf("metadata row %d = %q, want compact divider prefix", i, got)
		}
	}
}

func TestListCursorKeepsBothItemRowsVisible(t *testing.T) {
	a := &app{w: 80, h: 4}
	a.prs = []PullRequest{{ID: 1, Title: "one"}, {ID: 2, Title: "two"}}
	v := &listView{}
	v.rebuild(a)

	// Establish a two-row viewport, then move from the first PR to the second.
	v.vp.Paint(NewScreen(a.w, 2), Rect{X: 0, Y: 0, W: a.w, H: 2})
	v.vp.MoveCursor(1)

	if v.vp.Cursor != 2 {
		t.Fatalf("cursor = %d, want second PR title at row 2", v.vp.Cursor)
	}
	if v.vp.Top != 2 {
		t.Fatalf("top = %d, want 2 so both selected rows are visible", v.vp.Top)
	}
}

func TestListCommentCountFollowsUpdatedAtFixedColumn(t *testing.T) {
	a := &app{w: 140, h: 10}
	a.prs = []PullRequest{
		{ID: 1, Title: "short", CommentCount: 0},
		{ID: 2, Title: strings.Repeat("long title ", 10), CommentCount: 7},
		{ID: 3, Title: "日本語タイトル", CommentCount: 1234},
	}
	for i := range a.prs {
		a.prs[i].Author.DisplayName = strings.Repeat("author", i+1)
	}

	v := &listView{}
	v.rebuild(a)

	wantText := []string{"", "💬 7", "💬 1234"}
	wantCol := -1
	for i := range a.prs {
		row := v.vp.Rows[i*2]
		comment := row.Spans[len(row.Spans)-1]
		if got := strings.TrimSpace(comment.Text); got != wantText[i] {
			t.Errorf("PR %d comment = %q, want %q", i, got, wantText[i])
		}
		if got := displayWidth(comment.Text); got != listCommentGap+listCommentWidth {
			t.Errorf("PR %d comment column width = %d, want %d", i, got, listCommentGap+listCommentWidth)
		}
		col := spansWidth(row.Spans[:len(row.Spans)-1]) + listCommentGap
		if wantCol < 0 {
			wantCol = col
		} else if col != wantCol {
			t.Errorf("PR %d comment column = %d, want %d", i, col, wantCol)
		}
	}
}
