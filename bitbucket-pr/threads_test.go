package main

import (
	"testing"
	"time"
)

func TestThreadRowsBackgroundLayout(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := 15
	thread := CommentThread{
		Root: Comment{ID: 42, CreatedOn: base,
			Inline: &InlineAnchor{Path: "a.py", To: &to}},
		Replies: []Reply{{Comment: Comment{ID: 43, CreatedOn: base.Add(time.Hour)}, Depth: 1}},
	}
	thread.Root.Content.Raw = "root body"
	thread.Replies[0].Content.Raw = "reply body"
	thread.Root.User.DisplayName = "author"
	thread.Replies[0].User.DisplayName = "replier"

	rows := threadRows(thread, 60, base.Add(2*time.Hour), "L15", false)

	// Header: selectable, carries the root ID, painted on the comment bg.
	head := rows[0]
	if !head.Selectable || head.Item != 42 || head.Kind != RowComment {
		t.Fatalf("header row: %+v", head)
	}
	for _, sp := range head.Spans {
		if sp.Style < styleOnCommentBg {
			t.Errorf("header span not on comment bg: %+v", sp)
		}
	}

	// Every non-gap row pads to the full width.
	for i, r := range rows[:len(rows)-1] {
		w := 0
		for _, sp := range r.Spans {
			w += displayWidth(sp.Text)
		}
		if w != 60 {
			t.Errorf("row %d width = %d, want 60", i, w)
		}
	}

	// Reply header (↳) exists; last row is the plain gap.
	found := false
	for _, r := range rows {
		for _, sp := range r.Spans {
			if sp.Text == "   ↳ " {
				found = true
			}
		}
	}
	if !found {
		t.Error("reply header ↳ not found")
	}
	if gap := rows[len(rows)-1]; len(gap.Spans) != 0 {
		t.Errorf("last row must be a plain gap: %+v", gap)
	}
}
