package main

import (
	"encoding/json"
	"testing"
	"time"
)

func mkComment(id int, parent int, path string, from, to *int, outdated bool, created time.Time) Comment {
	c := Comment{ID: id, CreatedOn: created}
	c.Content.Raw = "body"
	if parent > 0 {
		c.Parent = &struct {
			ID int `json:"id"`
		}{ID: parent}
	}
	if path != "" {
		c.Inline = &InlineAnchor{Path: path, From: from, To: to, Outdated: outdated}
	}
	return c
}

func iptr(n int) *int { return &n }

func TestBuildThreadsNesting(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	comments := []Comment{
		mkComment(1, 0, "", nil, nil, false, base),
		mkComment(3, 2, "", nil, nil, false, base.Add(3*time.Hour)), // reply to a reply
		mkComment(2, 1, "", nil, nil, false, base.Add(1*time.Hour)),
		mkComment(10, 0, "", nil, nil, false, base.Add(-1*time.Hour)),
	}
	threads := generalThreads(comments)
	if len(threads) != 2 {
		t.Fatalf("got %d threads, want 2", len(threads))
	}
	// Sorted by root creation: id 10 first.
	if threads[0].Root.ID != 10 || threads[1].Root.ID != 1 {
		t.Errorf("thread order: %d, %d", threads[0].Root.ID, threads[1].Root.ID)
	}
	// The 3-level chain flattens under root 1 in chronological order.
	if len(threads[1].Replies) != 2 || threads[1].Replies[0].ID != 2 || threads[1].Replies[1].ID != 3 {
		t.Errorf("replies: %+v", threads[1].Replies)
	}
}

func TestGeneralExcludesInline(t *testing.T) {
	comments := []Comment{
		mkComment(1, 0, "", nil, nil, false, time.Now()),
		mkComment(2, 0, "a.go", nil, iptr(5), false, time.Now()),
	}
	if threads := generalThreads(comments); len(threads) != 1 || threads[0].Root.ID != 1 {
		t.Errorf("generalThreads: %+v", threads)
	}
	byFile := inlineThreadsByFile(comments)
	if len(byFile["a.go"]) != 1 || byFile["a.go"][0].Root.ID != 2 {
		t.Errorf("inlineThreadsByFile: %+v", byFile)
	}
}

func TestInlineReplyFollowsRootFile(t *testing.T) {
	// A reply usually repeats the inline anchor, but must land in its
	// root's thread either way.
	comments := []Comment{
		mkComment(1, 0, "a.go", nil, iptr(5), false, time.Now()),
		mkComment(2, 1, "", nil, nil, false, time.Now().Add(time.Minute)),
	}
	byFile := inlineThreadsByFile(comments)
	if len(byFile["a.go"]) != 1 || len(byFile["a.go"][0].Replies) != 1 {
		t.Errorf("reply not attached: %+v", byFile)
	}
}

func TestLineLabel(t *testing.T) {
	mk := func(in *InlineAnchor) *CommentThread {
		th := &CommentThread{}
		th.Root.Inline = in
		return th
	}
	cases := []struct {
		in   *InlineAnchor
		want string
	}{
		{nil, ""},
		{&InlineAnchor{To: iptr(496)}, "L496"},
		{&InlineAnchor{To: iptr(496), StartTo: iptr(486)}, "L486–496"},
		{&InlineAnchor{To: iptr(496), StartTo: iptr(496)}, "L496"}, // degenerate range
		{&InlineAnchor{From: iptr(12)}, "旧L12"},
		{&InlineAnchor{From: iptr(12), StartFrom: iptr(10)}, "旧L10–12"},
		{&InlineAnchor{}, ""},
	}
	for i, c := range cases {
		if got := mk(c.in).lineLabel(); got != c.want {
			t.Errorf("case %d: got %q, want %q", i, got, c.want)
		}
	}
}

func TestInlineAnchorDecodesStartRange(t *testing.T) {
	var in InlineAnchor
	raw := `{"from": null, "to": 496, "path": "a.py", "start_from": null, "start_to": 486}`
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	if in.StartTo == nil || *in.StartTo != 486 || in.To == nil || *in.To != 496 {
		t.Errorf("anchor = %+v", in)
	}
}

func TestCommentThreadCount(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	comments := []Comment{
		mkComment(1, 0, "", nil, nil, false, base),          // general root
		mkComment(2, 1, "", nil, nil, false, base),          // reply (same thread)
		mkComment(3, 0, "a.py", nil, iptr(10), false, base), // inline root
		mkComment(4, 0, "b.py", nil, iptr(20), false, base), // inline root
		mkComment(5, 3, "a.py", nil, iptr(10), false, base), // inline reply
	}
	if n := commentThreadCount(comments); n != 3 {
		t.Errorf("count = %d, want 3", n)
	}
}

func TestMatchesLine(t *testing.T) {
	addLine := DiffLine{Kind: LineAdd, NewNo: 15}
	ctxLine := DiffLine{Kind: LineContext, OldNo: 20, NewNo: 22}
	delLine := DiffLine{Kind: LineDel, OldNo: 30}

	toThread := CommentThread{Root: mkComment(1, 0, "a.go", nil, iptr(15), false, time.Now())}
	if !toThread.matchesLine(addLine) {
		t.Error("to=15 must match the added line NewNo=15")
	}
	if toThread.matchesLine(delLine) {
		t.Error("to anchor must not match a deleted line")
	}

	bothThread := CommentThread{Root: mkComment(2, 0, "a.go", iptr(20), iptr(22), false, time.Now())}
	if !bothThread.matchesLine(ctxLine) {
		t.Error("to=22 must match the context line NewNo=22")
	}

	fromThread := CommentThread{Root: mkComment(3, 0, "a.go", iptr(30), nil, false, time.Now())}
	if !fromThread.matchesLine(delLine) {
		t.Error("from=30 must match the deleted line OldNo=30")
	}
	if fromThread.matchesLine(addLine) {
		t.Error("from anchor must not match an added line")
	}

	outdated := CommentThread{Root: mkComment(4, 0, "a.go", nil, iptr(15), true, time.Now())}
	if outdated.matchesLine(addLine) {
		t.Error("outdated comments must never anchor (they go to the orphaned section)")
	}
}
