package main

import (
	"strings"
	"testing"
	"time"
)

func excerptFixtureFiles() []FileDiff {
	return []FileDiff{{
		NewPath: "a.go",
		Hunks: []Hunk{{
			Lines: []DiffLine{
				{Kind: LineContext, OldNo: 9, NewNo: 9, Text: "ctx line"},
				{Kind: LineAdd, NewNo: 10, Text: "added line"},
				{Kind: LineDel, OldNo: 10, Text: "deleted line"},
			},
		}},
	}}
}

func TestCommentExcerptRows(t *testing.T) {
	thread := CommentThread{Root: mkComment(1, 0, "a.go", nil, iptr(10), false, time.Now())}
	rows := commentExcerptRows(thread, excerptFixtureFiles(), 60)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	var text string
	for _, sp := range rows[0].Spans {
		text += sp.Text
		if sp.Style < styleOnCommentBg {
			t.Errorf("excerpt span not on comment bg: %+v", sp)
		}
	}
	if want := "  10 + added line"; !strings.Contains(text, want) {
		t.Errorf("excerpt = %q, want it to contain %q", text, want)
	}

	// Old-side anchor hits the deleted line.
	del := CommentThread{Root: mkComment(2, 0, "a.go", iptr(10), nil, false, time.Now())}
	rows = commentExcerptRows(del, excerptFixtureFiles(), 60)
	if len(rows) != 1 {
		t.Fatalf("del rows = %+v", rows)
	}
}

func TestReplyTarget(t *testing.T) {
	to := 15
	inline := CommentThread{Root: mkComment(1, 0, "a.go", nil, &to, false, time.Now())}
	inline.Root.User.DisplayName = "tanaka"
	if got := replyTarget(inline); got != "返信→tanaka L15" {
		t.Errorf("inline target = %q", got)
	}
	general := CommentThread{Root: mkComment(2, 0, "", nil, nil, false, time.Now())}
	general.Root.User.DisplayName = "sato"
	if got := replyTarget(general); got != "返信→sato" {
		t.Errorf("general target = %q", got)
	}
}

func TestCommentExcerptRowsFallsBack(t *testing.T) {
	// Diff not loaded yet.
	thread := CommentThread{Root: mkComment(1, 0, "a.go", nil, iptr(10), false, time.Now())}
	if rows := commentExcerptRows(thread, nil, 60); rows != nil {
		t.Errorf("no-diff rows = %+v", rows)
	}
	// Outdated threads never anchor.
	out := CommentThread{Root: mkComment(2, 0, "a.go", nil, iptr(10), true, time.Now())}
	if rows := commentExcerptRows(out, excerptFixtureFiles(), 60); rows != nil {
		t.Errorf("outdated rows = %+v", rows)
	}
	// Line missing from the diff.
	miss := CommentThread{Root: mkComment(3, 0, "a.go", nil, iptr(999), false, time.Now())}
	if rows := commentExcerptRows(miss, excerptFixtureFiles(), 60); rows != nil {
		t.Errorf("missing-line rows = %+v", rows)
	}
}
