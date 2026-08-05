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
// (id 2, newest, with reply id 4) and b.go (id 3).
func masterFixture() []Comment {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	return []Comment{
		mkComment(1, 0, "", nil, nil, false, base),
		mkComment(2, 0, "a.go", nil, iptr(10), false, base.Add(2*time.Hour)),
		mkComment(3, 0, "b.go", nil, iptr(5), false, base.Add(time.Hour)),
		mkComment(4, 2, "", nil, nil, false, base.Add(3*time.Hour)), // reply on thread 2
	}
}

func TestCommentMasterRows(t *testing.T) {
	a := &app{w: 120}
	d := &prDetail{comments: masterFixture()}
	v := &detailView{}
	rows := v.commentMasterRows(a, d)

	// [0] general header, [1] thread 1, [2] gap, [3] a.go header,
	// [4] thread 2, [5] reply 4, [6] gap, [7] b.go header, [8] thread 3.
	if len(rows) < 9 {
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
	if id, ok := rows[5].Item.(int); !ok || id != 4 || !rows[5].Selectable {
		t.Errorf("reply row = %+v", rows[5])
	}
	if !strings.Contains(spansText(rows[5]), "↳") {
		t.Errorf("reply row text = %q", spansText(rows[5]))
	}
	if g, ok := rows[7].Item.(cmtGroup); !ok || g.Path != "b.go" {
		t.Errorf("second file header = %+v", rows[7])
	}
	if v.cmtFirstThread["a.go"] != 2 || v.cmtFirstThread["b.go"] != 3 {
		t.Errorf("cmtFirstThread = %v", v.cmtFirstThread)
	}
	if len(v.threadFor) != 3 || len(v.inlineThreadFor) != 2 {
		t.Errorf("threadFor=%d inlineThreadFor=%d", len(v.threadFor), len(v.inlineThreadFor))
	}
	if v.replyRoot[4] != 2 {
		t.Errorf("replyRoot = %v", v.replyRoot)
	}
	if len(v.commentFor) != 4 {
		t.Errorf("commentFor = %v", v.commentFor)
	}
}

func TestMasterThreadRow(t *testing.T) {
	to := 15
	th := CommentThread{Root: mkComment(1, 0, "a.go", nil, &to, false, time.Now())}
	th.Root.User.DisplayName = "tanaka"
	th.Root.Content.Raw = "最初の行\n二行目"
	r := masterThreadRow(th, 40, false)
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
	if txt := spansText(masterThreadRow(th, 14, false)); strings.Contains(txt, "「") {
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

func TestOverviewReviewersAreVerticalOrderedAndDecorated(t *testing.T) {
	a := &app{w: 100, h: 30, avatars: &avatarGraphics{}}
	pr := &PullRequest{}
	makeParticipant := func(name, state string, approved bool, avatar bool) Participant {
		p := Participant{Role: "REVIEWER", State: state, Approved: approved}
		p.User.DisplayName = name
		p.User.AccountID = name
		if avatar {
			setAvatarURL(&p.User, "https://example.test/"+name+".png")
		}
		return p
	}
	pr.Participants = []Participant{
		makeParticipant("Pending User", "", false, false),
		makeParticipant("Approved User", "approved", true, true),
		makeParticipant("Changes User", "changes_requested", false, true),
	}
	rows := (&detailView{}).overviewRows(a, &prDetail{pr: pr})
	var reviewerRows []Row
	for _, r := range rows {
		text := spansText(r)
		if strings.Contains(text, "Approved") || strings.Contains(text, "Changes requested") || strings.Contains(text, "Pending") {
			reviewerRows = append(reviewerRows, r)
		}
	}
	if len(reviewerRows) != 3 {
		t.Fatalf("reviewer rows = %d: %+v", len(reviewerRows), reviewerRows)
	}
	want := []string{"Approved User", "Changes User", "Pending User"}
	for i, name := range want {
		if text := spansText(reviewerRows[i]); !strings.Contains(text, name) {
			t.Errorf("row %d = %q, want %q", i, text, name)
		}
	}
	if len(reviewerRows[0].Avatars) != 1 || reviewerRows[0].Avatars[0].Badge != AvatarBadgeApproved {
		t.Errorf("approved avatars = %+v", reviewerRows[0].Avatars)
	}
	if len(reviewerRows[1].Avatars) != 1 || reviewerRows[1].Avatars[0].Badge != AvatarBadgeNone {
		t.Errorf("changes avatars = %+v", reviewerRows[1].Avatars)
	}
	if len(reviewerRows[2].Avatars) != 0 || !strings.Contains(spansText(reviewerRows[2]), "○ Pending") {
		t.Errorf("pending row = %q avatars=%+v", spansText(reviewerRows[2]), reviewerRows[2].Avatars)
	}
}

func TestMasterReplyRowIndentByDepth(t *testing.T) {
	r := Reply{Comment: mkComment(9, 2, "", nil, nil, false, time.Now()), Depth: 1}
	r.User.DisplayName = "suzuki"
	r.Content.Raw = "対応します"
	d1 := spansText(masterReplyRow(r, 50, false))
	r.Depth = 2
	d2 := spansText(masterReplyRow(r, 50, false))
	if !strings.HasPrefix(d1, "     ↳ ") || !strings.HasPrefix(d2, "       ↳ ") {
		t.Errorf("indents: depth1=%q depth2=%q", d1, d2)
	}
	if !strings.Contains(d1, "suzuki") || !strings.Contains(d1, "「対応します」") {
		t.Errorf("reply row = %q", d1)
	}
	row := masterReplyRow(r, 50, false)
	if !row.Selectable || row.Kind != RowComment || row.Item.(int) != 9 {
		t.Errorf("reply row meta = %+v", row)
	}
}

func TestMasterThreadRowResolvedBadge(t *testing.T) {
	to := 15
	th := CommentThread{Root: mkComment(1, 0, "a.go", nil, &to, false, time.Now())}
	th.Root.Resolution = &CommentResolution{}
	if txt := spansText(masterThreadRow(th, 50, false)); !strings.Contains(txt, "✓") {
		t.Errorf("resolved row = %q", txt)
	}
	th.Root.Resolution = nil
	if txt := spansText(masterThreadRow(th, 50, false)); strings.Contains(txt, "✓") {
		t.Errorf("unresolved row = %q", txt)
	}
}

// masterViewFixture builds a detailView on the split layout with the cursor
// usable for selection tests.
func masterViewFixture(t *testing.T) (*app, *detailView) {
	t.Helper()
	a := &app{w: 120, h: 30, detail: map[int]*prDetail{}}
	d := a.detailFor(7)
	d.pr = &PullRequest{ID: 7, Title: "t"}
	d.comments = masterFixture()
	d.files = []FileDiff{*hunkFixtureFile()}
	v := &detailView{prID: 7, tab: tabComments}
	v.rebuild(a)
	return a, v
}

// D hands the diff tool the file under the cursor, so hunk opens on it
// instead of dumping the PR in natural order.
func TestCursorFilePath(t *testing.T) {
	a, v := masterViewFixture(t)

	// Thread on b.go — the second commented file, so this is exactly the case
	// that used to open on a.go.
	moveCursorTo(t, v, 3)
	if got := v.cursorFilePath(a); got != "b.go" {
		t.Errorf("thread on b.go: focus = %q, want b.go", got)
	}

	// A reply resolves through its root thread's file.
	moveCursorTo(t, v, 4)
	if got := v.cursorFilePath(a); got != "a.go" {
		t.Errorf("reply on a.go thread: focus = %q, want a.go", got)
	}

	// General thread has no file context — natural order.
	moveCursorTo(t, v, 1)
	if got := v.cursorFilePath(a); got != "" {
		t.Errorf("general thread: focus = %q, want empty", got)
	}

	// File header rows anchor to their own file.
	for i, r := range v.vp[tabComments].Rows {
		if g, ok := r.Item.(cmtGroup); ok && g.Path == "b.go" {
			v.vp[tabComments].Cursor = i
			if got := v.cursorFilePath(a); got != "b.go" {
				t.Errorf("b.go header row: focus = %q, want b.go", got)
			}
			break
		}
	}

	// Overview carries no file context at all.
	v.tab = tabOverview
	if got := v.cursorFilePath(a); got != "" {
		t.Errorf("overview: focus = %q, want empty", got)
	}
}

func moveCursorTo(t *testing.T, v *detailView, id int) {
	t.Helper()
	for i, r := range v.vp[tabComments].Rows {
		if got, ok := r.Item.(int); ok && got == id {
			v.vp[tabComments].Cursor = i
			return
		}
	}
	t.Fatalf("comment %d not found in master rows", id)
}

func TestCommentMasterCursorHighlightsFullRow(t *testing.T) {
	a, v := masterViewFixture(t)
	leftW := commentMasterWidth(a.w)
	paint := func() *Screen {
		s := NewScreen(a.w, a.h)
		v.vp[tabComments].Paint(s, Rect{X: 0, Y: 0, W: leftW, H: a.h})
		return s
	}

	// Group header, thread root, and reply rows all get the full-width
	// focus background — colored spans included, out to the pane edge.
	for _, id := range []int{1, 4} { // thread root, reply
		moveCursorTo(t, v, id)
		s := paint()
		y := v.vp[tabComments].Cursor - v.vp[tabComments].Top
		for _, x := range []int{0, 10, leftW - 1} {
			if got := s.at(x, y).Style; got < styleOnFocusBg {
				t.Errorf("comment %d: cell (%d,%d) style = %d, want focus-bg variant", id, x, y, got)
			}
		}
		if got := s.at(0, y+1).Style; got >= styleOnFocusBg {
			t.Errorf("comment %d: row below the cursor must not be focused (style=%d)", id, got)
		}
	}
}

func TestSelectedCommentOnReplyRow(t *testing.T) {
	_, v := masterViewFixture(t)
	moveCursorTo(t, v, 4) // reply on thread 2
	c, th, ok := v.selectedComment()
	if !ok || c.ID != 4 || th.Root.ID != 2 {
		t.Errorf("selectedComment = %+v thread=%+v ok=%v", c, th.Root, ok)
	}
	if tgt := replyTargetFor(c, th); !strings.Contains(tgt, "返信→") || !strings.Contains(tgt, "L10") {
		t.Errorf("target = %q", tgt)
	}
}

func TestPendingDeleteConfirmFlow(t *testing.T) {
	a, v := masterViewFixture(t)
	moveCursorTo(t, v, 4)
	v.handle(a, Key{Kind: KeyRune, R: 'x'})
	if v.pendingAction.kind != detailActionDeleteComment || v.pendingAction.commentID != 4 ||
		!strings.Contains(v.pendingAction.prompt, "削除しますか") {
		t.Fatalf("after x: pending=%+v", v.pendingAction)
	}
	// Any key but y cancels.
	v.handle(a, Key{Kind: KeyRune, R: 'n'})
	if v.pendingAction.kind != detailActionNone || !strings.Contains(a.status, "取り消し") {
		t.Errorf("after n: pending=%+v status=%q", v.pendingAction, a.status)
	}
}

func TestFooterNamesResolveTargetOnReplyRow(t *testing.T) {
	a, v := masterViewFixture(t)
	moveCursorTo(t, v, 2) // thread root
	if f := v.footer(a); !strings.Contains(f, "s:resolve") || strings.Contains(f, "親を") {
		t.Errorf("root footer = %q", f)
	}
	moveCursorTo(t, v, 4) // reply on thread 2
	if f := v.footer(a); !strings.Contains(f, "s:親をresolve") {
		t.Errorf("reply footer = %q", f)
	}
}

func TestResolveRejectsGeneralThread(t *testing.T) {
	a, v := masterViewFixture(t)
	moveCursorTo(t, v, 1) // general thread root
	v.handle(a, Key{Kind: KeyRune, R: 's'})
	if !strings.Contains(a.status, "resolve できません") {
		t.Errorf("status = %q", a.status)
	}
	if a.loading != 0 {
		t.Errorf("no API call must fire for general threads (loading=%d)", a.loading)
	}
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

func TestZeroCommentFilesGuideAdaptsToConfiguredViewer(t *testing.T) {
	newPath := func(path string) *struct {
		Path string `json:"path"`
	} {
		return &struct {
			Path string `json:"path"`
		}{Path: path}
	}
	cases := []struct {
		name       string
		cfg        Config
		wantGuide  string
		wantFooter string
	}{
		{"hunk", defaultConfig(), "Enterでhunk → + / cでコメント", "hunk内 +/c:コメント"},
		{"builtin", func() Config { c := defaultConfig(); c.FilesEnter = "builtin"; return c }(), "Enterで内蔵diff → cでコメント", "diff内 c:コメント"},
		{"other difftool", func() Config { c := defaultConfig(); c.DiffTool = []string{"delta"}; return c }(), "vで内蔵diff → cでコメント", "v:内蔵diff(c:コメント)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &prDetail{
				pr:       &PullRequest{ID: 7, Title: "title"},
				diffstat: []DiffStatEntry{{Status: "modified", New: newPath("a.go")}},
				comments: []Comment{},
			}
			a := &app{w: 120, h: 20, cfg: tc.cfg, detail: map[int]*prDetail{7: d}}
			v := &detailView{prID: 7, tab: tabFiles}
			v.rebuild(a)

			// The guide is painted above the viewport, so the cursor remains on
			// the actual file and Enter keeps its existing semantics.
			if r := v.vp[tabFiles].Current(); r == nil || r.Kind != RowFile || r.Item.(int) != 0 {
				t.Fatalf("current row = %+v, want first file", r)
			}
			s := NewScreen(a.w, a.h)
			v.render(a, s)
			if line := screenRow(s, 4); !strings.Contains(line, tc.wantGuide) {
				t.Errorf("guide = %q, want %q", line, tc.wantGuide)
			}
			if footer := v.footer(a); !strings.Contains(footer, tc.wantFooter) {
				t.Errorf("footer = %q, want %q", footer, tc.wantFooter)
			}
		})
	}
}

func TestFilesGuideOnlyAppearsForLoadedEmptyComments(t *testing.T) {
	path := &struct {
		Path string `json:"path"`
	}{Path: "a.go"}
	cases := []struct {
		name     string
		comments []Comment
		diffstat []DiffStatEntry
		want     bool
	}{
		{"loading comments", nil, []DiffStatEntry{{Status: "modified", New: path}}, false},
		{"no comments", []Comment{}, []DiffStatEntry{{Status: "modified", New: path}}, true},
		{"existing comment", masterFixture(), []DiffStatEntry{{Status: "modified", New: path}}, false},
		{"no changed files", []Comment{}, []DiffStatEntry{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &prDetail{comments: tc.comments, diffstat: tc.diffstat}
			if got := showReviewGuide(d); got != tc.want {
				t.Errorf("showReviewGuide = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEmptyCommentsGuideAndFilteredEmptyState(t *testing.T) {
	a := &app{w: 120}
	d := &prDetail{comments: []Comment{}}
	for _, rows := range [][]Row{
		(&detailView{}).commentRows(a, d),
		(&detailView{}).commentMasterRows(a, d),
	} {
		if len(rows) != 2 || !strings.Contains(spansText(rows[0]), "コメントはまだありません") ||
			!strings.Contains(spansText(rows[1]), "3:Files からレビューを開始できます") {
			t.Errorf("empty comment rows = %+v", rows)
		}
	}

	v := &detailView{}
	v.search[tabComments].buf = []byte("not-found")
	rows := v.commentRows(a, &prDetail{comments: masterFixture()})
	if len(rows) != 1 || !strings.Contains(spansText(rows[0]), "一致するコメントはありません") ||
		strings.Contains(spansText(rows[0]), "3:Files") {
		t.Errorf("filtered empty rows = %+v", rows)
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

func TestDetailUsesListSummaryWhileFullPRLoads(t *testing.T) {
	a := &app{w: 100, h: 24, detail: map[int]*prDetail{}}
	updated := time.Now().Add(-time.Hour)
	summary := &PullRequest{ID: 32, Title: "一覧から即時表示", State: "OPEN", UpdatedOn: updated}
	summary.Author.DisplayName = "深谷 颯佑"
	summary.Source.Branch.Name = "feature/fast-detail"
	summary.Destination.Branch.Name = "main"
	reviewer := Participant{Role: "REVIEWER", State: "approved", Approved: true}
	reviewer.User.DisplayName = "Reviewer"
	summary.Participants = []Participant{reviewer}

	v := &detailView{prID: 32, summary: summary}
	v.rebuild(a)
	s := NewScreen(a.w, a.h)
	v.render(a, s)

	if header := screenRow(s, 0); !strings.Contains(header, "#32 一覧から即時表示") ||
		!strings.Contains(header, "OPEN") || strings.Contains(header, "読み込み中") {
		t.Errorf("summary header = %q", header)
	}
	if meta := screenRow(s, 1); !strings.Contains(meta, "feature/fast-detail → main") ||
		!strings.Contains(meta, "by 深谷 颯佑") {
		t.Errorf("summary meta = %q", meta)
	}
	joined := ""
	for _, r := range v.vp[tabOverview].Rows {
		joined += spansText(r) + "\n"
	}
	if !strings.Contains(joined, "説明を読み込み中") || !strings.Contains(joined, "Reviewer") {
		t.Errorf("summary overview = %q", joined)
	}

	full := *summary
	full.Summary.Raw = "完全な説明"
	full.Source.Commit.Hash = "1234567890abcdef"
	a.detailFor(32).pr = &full
	v.rebuild(a)
	joined = ""
	for _, r := range v.vp[tabOverview].Rows {
		joined += spansText(r) + "\n"
	}
	if !strings.Contains(joined, "完全な説明") || strings.Contains(joined, "読み込み中") {
		t.Errorf("full overview = %q", joined)
	}
	s = NewScreen(a.w, a.h)
	v.render(a, s)
	if meta := screenRow(s, 1); !strings.Contains(meta, "1234567…") {
		t.Errorf("full meta = %q", meta)
	}
}

func TestDetailWithoutListSummaryKeepsLoadingScreen(t *testing.T) {
	a := &app{w: 80, h: 20, detail: map[int]*prDetail{}}
	v := &detailView{prID: 32}
	s := NewScreen(a.w, a.h)
	v.render(a, s)
	if header := screenRow(s, 0); !strings.Contains(header, "#32 を読み込み中") {
		t.Errorf("direct URL header = %q", header)
	}
	if tabs := screenRow(s, 2); strings.Contains(tabs, "Overview") {
		t.Errorf("tabs should wait for the first PR payload: %q", tabs)
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
