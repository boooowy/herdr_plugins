package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoStoreRoundTrip(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	s := loadMemoStore("ws", "repo", 42)
	if len(s.memos) != 0 {
		t.Fatalf("fresh store must be empty: %+v", s.memos)
	}
	s.add(reviewMemo{ID: 1, Path: "a.go", Body: "one"})
	s.add(reviewMemo{ID: 2, Body: "pr-level"})

	got := loadMemoStore("ws", "repo", 42)
	if len(got.memos) != 2 || got.memos[0].Body != "one" || got.memos[1].Path != "" {
		t.Errorf("round trip: %+v", got.memos)
	}
	if other := loadMemoStore("ws", "repo", 43); len(other.memos) != 0 {
		t.Error("different PR must be a miss")
	}

	if !got.update(1, "edited") {
		t.Error("update existing must report true")
	}
	if got.update(99, "x") {
		t.Error("update missing must report false")
	}
	if !got.delete(2) {
		t.Error("delete existing must report true")
	}
	reloaded := loadMemoStore("ws", "repo", 42)
	if len(reloaded.memos) != 1 || reloaded.memos[0].Body != "edited" {
		t.Errorf("after update+delete: %+v", reloaded.memos)
	}
	if m := reloaded.byID(1); m == nil || m.Body != "edited" {
		t.Errorf("byID: %+v", m)
	}
	if reloaded.byID(2) != nil {
		t.Error("byID on deleted memo must be nil")
	}
}

func TestMemoStoreCorruptFile(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	path := memoPath("ws", "repo", 1)
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte("{broken"), 0o600)
	if s := loadMemoStore("ws", "repo", 1); len(s.memos) != 0 {
		t.Errorf("corrupt file must yield an empty store: %+v", s.memos)
	}
}

func TestMemoFromDiff(t *testing.T) {
	cases := []struct {
		name               string
		lines              []DiffLine
		oldRange, newRange []int
		excerpt            []string
		label              string
	}{
		{
			name:     "single added line",
			lines:    []DiffLine{{Kind: LineAdd, NewNo: 10, Text: "x := 1"}},
			newRange: []int{10, 10},
			excerpt:  []string{"+ x := 1"},
			label:    "L10",
		},
		{
			name:     "deleted only",
			lines:    []DiffLine{{Kind: LineDel, OldNo: 10, Text: "old"}},
			oldRange: []int{10, 10},
			excerpt:  []string{"- old"},
			label:    "旧L10",
		},
		{
			name: "mixed selection",
			lines: []DiffLine{
				{Kind: LineDel, OldNo: 9, Text: "before"},
				{Kind: LineAdd, NewNo: 9, Text: "after"},
				{Kind: LineContext, OldNo: 10, NewNo: 10, Text: "ctx"},
			},
			oldRange: []int{9, 10},
			newRange: []int{9, 10},
			excerpt:  []string{"- before", "+ after", "  ctx"},
			label:    "L9–10",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := memoFromDiff("src/foo.go", tc.lines, "body")
			if m.Path != "src/foo.go" || m.Body != "body" || m.ID == 0 {
				t.Errorf("basic fields: %+v", m)
			}
			if !intsEqual(m.OldRange, tc.oldRange) || !intsEqual(m.NewRange, tc.newRange) {
				t.Errorf("ranges: old=%v new=%v, want old=%v new=%v",
					m.OldRange, m.NewRange, tc.oldRange, tc.newRange)
			}
			if strings.Join(m.Excerpt, "\n") != strings.Join(tc.excerpt, "\n") {
				t.Errorf("excerpt: %q, want %q", m.Excerpt, tc.excerpt)
			}
			if m.lineLabel() != tc.label {
				t.Errorf("label: %q, want %q", m.lineLabel(), tc.label)
			}
		})
	}
}

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMemosMarkdown(t *testing.T) {
	pr := &PullRequest{ID: 123, Title: "Fix config loading"}
	pr.Source.Branch.Name = "feat/config"
	pr.Destination.Branch.Name = "main"
	memos := []reviewMemo{
		{Path: "src/foo.go", NewRange: []int{42, 42}, Body: "引数名を直したい",
			Excerpt: []string{"+ func NewFoo(cfg Config) *Foo {"}},
		{Path: "src/bar.go", OldRange: []int{10, 15}, Body: "エラーが握りつぶし",
			Excerpt: []string{"- if err != nil {", "-   return nil", "- }"}},
		{Body: "テスト追加を依頼したい"},
	}
	want := strings.Join([]string{
		"# PR #123 Fix config loading (feat/config → main) レビューメモ",
		"",
		"### src/foo.go L42",
		"引数名を直したい",
		"",
		"```diff",
		"+ func NewFoo(cfg Config) *Foo {",
		"```",
		"",
		"### src/bar.go 旧L10–15",
		"エラーが握りつぶし",
		"",
		"```diff",
		"- if err != nil {",
		"-   return nil",
		"- }",
		"```",
		"",
		"### (PR全体)",
		"テスト追加を依頼したい",
		"",
	}, "\n")
	if got := memosMarkdown(123, pr, memos); got != want {
		t.Errorf("markdown:\n%s\nwant:\n%s", got, want)
	}
}

func TestMemoRows(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	a := &app{w: 100, h: 20, detail: map[int]*prDetail{}}
	v := &detailView{prID: 1}
	store := a.memosFor(1)
	store.add(reviewMemo{ID: 1, Path: "src/foo.go", NewRange: []int{42, 42}, Body: "rename cfg"})
	store.add(reviewMemo{ID: 2, Body: "PR-level note"})

	rows := v.memoRows(a)
	if len(rows) != 2 {
		t.Fatalf("one dense row per memo: %d rows", len(rows))
	}
	for i, r := range rows {
		if r.Kind != RowMemo || !r.Selectable {
			t.Errorf("row %d must be a selectable RowMemo: %+v", i, r)
		}
	}
	if id, _ := rows[0].Item.(int64); id != 1 {
		t.Errorf("row 0 Item: %v", rows[0].Item)
	}
	if v.shownMemos != 2 {
		t.Errorf("shownMemos = %d", v.shownMemos)
	}

	// `/` filter narrows by path and body.
	v.search[tabMemo].buf = []byte("foo")
	rows = v.memoRows(a)
	if len(rows) != 1 || rows[0].Item.(int64) != 1 || v.shownMemos != 1 {
		t.Errorf("filtered rows: %+v (shown %d)", rows, v.shownMemos)
	}

	// Empty store: one unselectable hint row.
	empty := &detailView{prID: 2}
	rows = empty.memoRows(a)
	if len(rows) != 1 || rows[0].Selectable {
		t.Errorf("empty state: %+v", rows)
	}
}

func TestOpenMemoInDiffJumpTarget(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	newSide := func(path string) *struct {
		Path string `json:"path"`
	} {
		return &struct {
			Path string `json:"path"`
		}{Path: path}
	}
	a := &app{w: 100, h: 20, detail: map[int]*prDetail{}}
	a.detailFor(1).diffstat = []DiffStatEntry{
		{Status: "modified", New: newSide("a.go")},
		{Status: "modified", New: newSide("b.go")},
	}
	v := &detailView{prID: 1}
	store := a.memosFor(1)
	store.add(reviewMemo{ID: 1, Path: "b.go", NewRange: []int{7, 9}, Body: "n"})
	store.add(reviewMemo{ID: 2, Path: "b.go", OldRange: []int{3, 3}, Body: "o"})

	v.openMemoInDiff(a, 1)
	dv, ok := a.top().(*diffView)
	if !ok || dv.fileIdx != 1 || dv.pendingLine != 7 || dv.pendingOldLine {
		t.Errorf("new-range jump: %+v", dv)
	}

	v.openMemoInDiff(a, 2)
	dv, ok = a.top().(*diffView)
	if !ok || dv.pendingLine != 3 || !dv.pendingOldLine {
		t.Errorf("old-range jump: %+v", dv)
	}
}

func TestDetailMemoContext(t *testing.T) {
	newSide := func(path string) *struct {
		Path string `json:"path"`
	} {
		return &struct {
			Path string `json:"path"`
		}{Path: path}
	}
	a := &app{w: 100, h: 20, detail: map[int]*prDetail{}}
	d := a.detailFor(1)
	d.diffstat = []DiffStatEntry{{Status: "modified", New: newSide("a.go")}}
	d.files = []FileDiff{{
		NewPath: "a.go",
		Hunks:   []Hunk{{Lines: []DiffLine{{Kind: LineAdd, NewNo: 10, Text: "added"}}}},
	}}

	// Files tab: the file under the cursor, no line range.
	v := &detailView{prID: 1, tab: tabFiles}
	v.vp[tabFiles].Reset([]Row{row(RowFile, 0, true, Span{"a.go", styleNone})})
	m, target := v.memoContext(a)
	if m.Path != "a.go" || m.lineLabel() != "" || !strings.Contains(target, "a.go") {
		t.Errorf("files-tab context: %+v %q", m, target)
	}

	// Comments tab: the selected inline thread's file + line, with excerpt.
	to := 10
	v.tab = tabComments
	v.threadFor = map[int]CommentThread{7: {Root: Comment{ID: 7, Inline: &InlineAnchor{Path: "a.go", To: &to}}}}
	v.vp[tabComments].Reset([]Row{row(RowComment, 7, true, Span{"x", styleNone})})
	m, _ = v.memoContext(a)
	if m.Path != "a.go" || !intsEqual(m.NewRange, []int{10, 10}) {
		t.Errorf("comments-tab context: %+v", m)
	}
	if strings.Join(m.Excerpt, "\n") != "+ added" {
		t.Errorf("comments-tab excerpt: %q", m.Excerpt)
	}

	// Outline tab: a symbol row anchors to its definition line.
	v.tab = tabOutline
	d.outline = &outlineResult{Files: []outlineFile{{
		Path:    "a.go",
		Symbols: []outlineSymbol{{ID: "s1", Path: "a.go", Name: "NewFoo", StartLine: 10}},
	}}}
	v.vp[tabOutline].Reset([]Row{row(RowOutlineSymbol,
		outlineRowRef{Kind: "symbol", Path: "a.go", ID: "s1"}, true, Span{"x", styleNone})})
	m, target = v.memoContext(a)
	if m.Path != "a.go" || !intsEqual(m.NewRange, []int{10, 10}) {
		t.Errorf("outline symbol context: %+v", m)
	}
	if strings.Join(m.Excerpt, "\n") != "+ added" {
		t.Errorf("outline symbol excerpt: %q", m.Excerpt)
	}
	if !strings.Contains(target, "NewFoo") {
		t.Errorf("outline symbol target: %q", target)
	}

	// Outline tab: a file row anchors to the file.
	v.vp[tabOutline].Reset([]Row{row(RowOutlineFile,
		outlineRowRef{Kind: "file", Path: "a.go"}, true, Span{"x", styleNone})})
	if m, _ = v.memoContext(a); m.Path != "a.go" || m.lineLabel() != "" {
		t.Errorf("outline file context: %+v", m)
	}

	// Elsewhere: PR-level fallback.
	v.tab = tabOverview
	if m, _ = v.memoContext(a); m.Path != "" {
		t.Errorf("overview fallback: %+v", m)
	}
}

func TestNotesToMemos(t *testing.T) {
	files := []FileDiff{{
		NewPath: "a.go",
		Hunks: []Hunk{{Lines: []DiffLine{
			{Kind: LineContext, OldNo: 9, NewNo: 9, Text: "ctx"},
			{Kind: LineAdd, NewNo: 10, Text: "added"},
			{Kind: LineDel, OldNo: 10, Text: "removed"},
		}}},
	}}
	notes := []hunkNote{
		{FilePath: "a.go", NewRange: []int{10, 10}, Body: "note on add"},
		{FilePath: "missing.go", OldRange: []int{5, 7}, Body: "file not in diff"},
	}

	memos := notesToMemos(files, notes)
	if len(memos) != 2 {
		t.Fatalf("memo count: %d", len(memos))
	}
	if memos[0].ID == 0 || memos[0].ID == memos[1].ID {
		t.Errorf("IDs must be unique and set: %d, %d", memos[0].ID, memos[1].ID)
	}

	// Anchored in the loaded diff: excerpt snapshotted like an m-key memo.
	m := memos[0]
	if m.Path != "a.go" || m.Body != "note on add" {
		t.Errorf("anchored memo: %+v", m)
	}
	if strings.Join(m.Excerpt, "\n") != "+ added" {
		t.Errorf("excerpt: %q", m.Excerpt)
	}
	if !intsEqual(m.NewRange, []int{10, 10}) {
		t.Errorf("new range: %v", m.NewRange)
	}

	// File missing from the diff: ranges survive, no excerpt.
	m = memos[1]
	if m.Path != "missing.go" || len(m.Excerpt) != 0 || !intsEqual(m.OldRange, []int{5, 7}) {
		t.Errorf("fallback memo: %+v", m)
	}
	if m.lineLabel() != "旧L5–7" {
		t.Errorf("fallback label: %q", m.lineLabel())
	}
}

func TestMemosMarkdownNilPR(t *testing.T) {
	got := memosMarkdown(7, nil, []reviewMemo{{Body: "note"}})
	if !strings.HasPrefix(got, "# PR #7 レビューメモ\n") {
		t.Errorf("nil-pr header: %q", got)
	}
}
