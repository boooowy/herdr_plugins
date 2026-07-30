package main

import (
	"strings"
	"testing"
	"time"
)

func hunkFixtureFile() *FileDiff {
	return &FileDiff{
		NewPath: "a.go",
		Hunks: []Hunk{
			{OldStart: 9, OldCount: 2, NewStart: 9, NewCount: 3, Lines: []DiffLine{
				{Kind: LineContext, OldNo: 9, NewNo: 9, Text: "ctx line"},
				{Kind: LineAdd, NewNo: 10, Text: "added line"},
				{Kind: LineDel, OldNo: 10, Text: "deleted line"},
			}},
			{OldStart: 50, OldCount: 1, NewStart: 51, NewCount: 2, Lines: []DiffLine{
				{Kind: LineAdd, NewNo: 52, Text: "uncommented hunk"},
			}},
		},
	}
}

func TestAnchorThreads(t *testing.T) {
	f := hunkFixtureFile()
	threads := []CommentThread{
		{Root: mkComment(1, 0, "a.go", nil, iptr(10), false, time.Now())},  // → hunk 0, line 1
		{Root: mkComment(2, 0, "a.go", iptr(10), nil, false, time.Now())},  // → hunk 0, line 2 (del)
		{Root: mkComment(3, 0, "a.go", nil, iptr(999), false, time.Now())}, // orphan
		{Root: mkComment(4, 0, "a.go", nil, iptr(10), true, time.Now())},   // outdated → orphan
	}
	anchored, orphans := anchorThreads(f, threads)
	if len(anchored[[2]int{0, 1}]) != 1 || anchored[[2]int{0, 1}][0].Root.ID != 1 {
		t.Errorf("add-line anchor = %+v", anchored)
	}
	if len(anchored[[2]int{0, 2}]) != 1 || anchored[[2]int{0, 2}][0].Root.ID != 2 {
		t.Errorf("del-line anchor = %+v", anchored)
	}
	if len(orphans) != 2 {
		t.Errorf("orphans = %+v", orphans)
	}
	// No diff at all: everything is an orphan.
	if a, o := anchorThreads(nil, threads); len(a) != 0 || len(o) != 4 {
		t.Errorf("nil file: anchored=%v orphans=%d", a, len(o))
	}
}

func TestFileDiffFor(t *testing.T) {
	files := []FileDiff{*hunkFixtureFile(), {OldPath: "old.py", NewPath: "new.py"}}
	if f := fileDiffFor(files, "a.go"); f == nil || f.Path() != "a.go" {
		t.Errorf("by new path: %+v", f)
	}
	if f := fileDiffFor(files, "old.py"); f == nil || f.Path() != "new.py" {
		t.Errorf("by old path: %+v", f)
	}
	if f := fileDiffFor(files, "missing.go"); f != nil {
		t.Errorf("missing: %+v", f)
	}
	if f := fileDiffFor(nil, "a.go"); f != nil {
		t.Errorf("no diff loaded: %+v", f)
	}
}

func TestCommentHunkHeader(t *testing.T) {
	f := hunkFixtureFile()
	r := commentHunkHeader(&f.Hunks[0], 2, 60)
	if r.Selectable {
		t.Error("hunk header must not be selectable")
	}
	text := ""
	for _, sp := range r.Spans {
		text += sp.Text
		if sp.Style != styleHunk {
			t.Errorf("span style = %d", sp.Style)
		}
	}
	if !strings.Contains(text, "@@ -9,2 +9,3 @@") || !strings.Contains(text, "💬2") {
		t.Errorf("header = %q", text)
	}
}

func TestUnselectableRows(t *testing.T) {
	rows := unselectableRows(diffLineRows(DiffLine{Kind: LineAdd, NewNo: 10, Text: "x"}, 4, 60, false))
	for _, r := range rows {
		if r.Selectable || r.Item != nil {
			t.Errorf("row still selectable: %+v", r)
		}
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
