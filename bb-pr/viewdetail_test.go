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

func TestCommentMasterWidth(t *testing.T) {
	cases := []struct{ w, want int }{
		{70, 28},  // clamped up
		{120, 45}, // 38%
		{200, 52}, // clamped down
	}
	for _, c := range cases {
		if got := commentMasterWidth(c.w); got != c.want {
			t.Errorf("commentMasterWidth(%d) = %d, want %d", c.w, got, c.want)
		}
	}
}

// masterFixture: one general thread (id 1) plus inline threads in a.go
// (id 2, newest) and b.go (id 3).
func masterFixture() []Comment {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	return []Comment{
		mkComment(1, 0, "", nil, nil, false, base),
		mkComment(2, 0, "a.go", nil, iptr(10), false, base.Add(2*time.Hour)),
		mkComment(3, 0, "b.go", nil, iptr(5), false, base.Add(time.Hour)),
	}
}

func TestCommentMasterRows(t *testing.T) {
	a := &app{w: 120}
	d := &prDetail{comments: masterFixture()}
	v := &detailView{}
	rows := v.commentMasterRows(a, d)

	// [0] general header, [1] thread 1, [2] gap,
	// [3] a.go header, [4] thread 2, [5] gap, [6] b.go header, [7] thread 3.
	if len(rows) < 8 {
		t.Fatalf("got %d rows", len(rows))
	}
	if g, ok := rows[0].Item.(cmtGroup); !ok || g.Path != "" || !rows[0].Selectable {
		t.Errorf("general header = %+v", rows[0])
	}
	if id, ok := rows[1].Item.(int); !ok || id != 1 || rows[1].Kind != RowComment || !rows[1].Selectable {
		t.Errorf("general thread row = %+v", rows[1])
	}
	if g, ok := rows[3].Item.(cmtGroup); !ok || g.Path != "a.go" {
		t.Errorf("first file header = %+v", rows[3])
	}
	if g, ok := rows[6].Item.(cmtGroup); !ok || g.Path != "b.go" {
		t.Errorf("second file header = %+v", rows[6])
	}
	if v.cmtFirstThread["a.go"] != 2 || v.cmtFirstThread["b.go"] != 3 {
		t.Errorf("cmtFirstThread = %v", v.cmtFirstThread)
	}
	if len(v.threadFor) != 3 || len(v.inlineThreadFor) != 2 {
		t.Errorf("threadFor=%d inlineThreadFor=%d", len(v.threadFor), len(v.inlineThreadFor))
	}
}

func TestMasterThreadRow(t *testing.T) {
	to := 15
	th := CommentThread{Root: mkComment(1, 0, "a.go", nil, &to, false, time.Now())}
	th.Root.User.DisplayName = "tanaka"
	th.Root.Content.Raw = "最初の行\n二行目"
	r := masterThreadRow(th, 40)
	if !r.Selectable || r.Kind != RowComment || r.Item.(int) != 1 {
		t.Errorf("row = %+v", r)
	}
	text := ""
	for _, sp := range r.Spans {
		text += sp.Text
	}
	if !strings.Contains(text, "L15") || !strings.Contains(text, "tanaka") ||
		!strings.Contains(text, "「最初の行」") {
		t.Errorf("text = %q", text)
	}
	if strings.Contains(text, "二行目") {
		t.Errorf("excerpt must be the first line only: %q", text)
	}
	// Too narrow for an excerpt: the frame alone doesn't fit.
	if txt := spansText(masterThreadRow(th, 14)); strings.Contains(txt, "「") {
		t.Errorf("narrow row still has excerpt: %q", txt)
	}
}

func spansText(r Row) string {
	s := ""
	for _, sp := range r.Spans {
		s += sp.Text
	}
	return s
}

func TestCommentDetailRows(t *testing.T) {
	a := &app{w: 120}
	d := &prDetail{comments: masterFixture(), files: []FileDiff{*hunkFixtureFile()}}
	v := &detailView{}

	rows := v.commentDetailRows(a, d, "a.go", 60)
	joined := ""
	for _, r := range rows {
		if r.Selectable {
			t.Errorf("preview row must be unselectable: %+v", r)
		}
		joined += spansText(r) + "\n"
	}
	if !strings.Contains(joined, "a.go") || !strings.Contains(joined, "@@ -9,2 +9,3 @@") {
		t.Errorf("file preview = %q", joined)
	}
	span, ok := v.threadSpan[2]
	if !ok || span[0] < 0 || span[1] >= len(rows) || span[0] > span[1] {
		t.Errorf("threadSpan[2] = %v (rows=%d)", span, len(rows))
	}

	rows = v.commentDetailRows(a, d, generalPreviewKey, 60)
	joined = ""
	for _, r := range rows {
		if r.Selectable {
			t.Errorf("general preview row must be unselectable: %+v", r)
		}
		joined += spansText(r) + "\n"
	}
	if !strings.Contains(joined, "PR コメント") {
		t.Errorf("general preview = %q", joined)
	}
	if _, ok := v.threadSpan[1]; !ok {
		t.Errorf("threadSpan[1] missing: %v", v.threadSpan)
	}
}

func TestSyncPreview(t *testing.T) {
	a := &app{w: 120, detail: map[int]*prDetail{}}
	d := a.detailFor(7)
	d.comments = masterFixture()
	d.files = []FileDiff{*hunkFixtureFile()}
	v := &detailView{prID: 7, tab: tabComments}
	v.vp[tabComments].Reset(v.commentMasterRows(a, d))

	// Reset lands the cursor on the first selectable row: the general header.
	v.syncPreview(a)
	if v.cmtPreviewKey != generalPreviewKey {
		t.Fatalf("initial preview key = %q", v.cmtPreviewKey)
	}

	// Move onto the a.go thread (id 2): the pane switches to that file and
	// scrolls the thread into view.
	for i, r := range v.vp[tabComments].Rows {
		if id, ok := r.Item.(int); ok && id == 2 {
			v.vp[tabComments].Cursor = i
			break
		}
	}
	v.syncPreview(a)
	if v.cmtPreviewKey != "a.go" {
		t.Fatalf("preview key = %q, want a.go", v.cmtPreviewKey)
	}
	span, ok := v.threadSpan[2]
	if !ok {
		t.Fatalf("threadSpan[2] missing")
	}
	if want := max(span[0]-1, 0); v.cmtPreview.Top != want {
		t.Errorf("preview Top = %d, want %d", v.cmtPreview.Top, want)
	}

	// Narrow terminal: syncPreview is a no-op (flat layout).
	a.w = 80
	v.cmtPreviewKey = ""
	v.syncPreview(a)
	if v.cmtPreviewKey != "" {
		t.Errorf("narrow layout must not build a preview: %q", v.cmtPreviewKey)
	}
}

func TestDetailRenderSplitSmoke(t *testing.T) {
	a := &app{w: 120, h: 30, detail: map[int]*prDetail{}}
	d := a.detailFor(7)
	d.pr = &PullRequest{ID: 7, Title: "title"}
	d.comments = masterFixture()
	d.files = []FileDiff{*hunkFixtureFile()}
	v := &detailView{prID: 7, tab: tabComments}
	v.rebuild(a)

	s := NewScreen(a.w, a.h)
	v.render(a, s)
	leftW := commentMasterWidth(a.w)
	if c := s.at(leftW, 4); c.R != '│' {
		t.Errorf("separator cell = %q", c.R)
	}
	// The preview pane actually painted something right of the separator.
	painted := false
	for y := 4; y < a.h-1 && !painted; y++ {
		for x := leftW + 1; x < a.w; x++ {
			if c := s.at(x, y); c.R != ' ' && c.R != 0 {
				painted = true
				break
			}
		}
	}
	if !painted {
		t.Error("preview pane is empty")
	}
}

// screenRow reads one rendered row back as a plain string.
func screenRow(s *Screen, y int) string {
	var b strings.Builder
	for x := 0; x < s.W; x++ {
		if c := s.at(x, y); c.R != 0 {
			b.WriteRune(c.R)
		}
	}
	return b.String()
}

func TestTabBadgesShowLoadingUntilFetched(t *testing.T) {
	a := &app{w: 100, h: 20, detail: map[int]*prDetail{}}
	d := a.detailFor(1)
	d.pr = &PullRequest{ID: 1, Title: "title"}
	v := &detailView{prID: 1}

	// Nothing fetched yet: the badges must not read as 0-file / 0-comment.
	s := NewScreen(a.w, a.h)
	v.render(a, s)
	line := screenRow(s, 2)
	if !strings.Contains(line, "Files (…)") || !strings.Contains(line, "Comments (…)") {
		t.Errorf("loading tab bar = %q", line)
	}
	if strings.Contains(line, "(0)") {
		t.Errorf("loading tab bar shows a zero count: %q", line)
	}

	// Loaded but genuinely empty: real counts appear.
	d.diffstat = []DiffStatEntry{}
	d.comments = []Comment{}
	s = NewScreen(a.w, a.h)
	v.render(a, s)
	line = screenRow(s, 2)
	if !strings.Contains(line, "Files (0)") || !strings.Contains(line, "Comments (0)") {
		t.Errorf("loaded tab bar = %q", line)
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
