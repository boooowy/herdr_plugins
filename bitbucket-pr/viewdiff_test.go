package main

import (
	"strings"
	"testing"
)

func TestRangeAnchorNewSide(t *testing.T) {
	lines := []DiffLine{
		{Kind: LineContext, OldNo: 11, NewNo: 12},
		{Kind: LineAdd, NewNo: 13},
		{Kind: LineAdd, NewNo: 14},
	}
	in := rangeAnchor("a.go", lines)
	if in == nil || in.To == nil || *in.To != 14 || in.StartTo == nil || *in.StartTo != 12 {
		t.Fatalf("anchor = %+v", in)
	}
	if in.From != nil || in.StartFrom != nil {
		t.Errorf("new-side anchor must not set from: %+v", in)
	}
}

func TestRangeAnchorMixedPrefersNewSide(t *testing.T) {
	lines := []DiffLine{
		{Kind: LineDel, OldNo: 20},
		{Kind: LineAdd, NewNo: 20},
	}
	in := rangeAnchor("a.go", lines)
	if in == nil || in.To == nil || *in.To != 20 || in.StartTo != nil {
		t.Fatalf("anchor = %+v", in) // single new line: no start_to
	}
}

func TestRangeAnchorDeletionsOnly(t *testing.T) {
	lines := []DiffLine{
		{Kind: LineDel, OldNo: 30},
		{Kind: LineDel, OldNo: 31},
	}
	in := rangeAnchor("a.go", lines)
	if in == nil || in.From == nil || *in.From != 31 || in.StartFrom == nil || *in.StartFrom != 30 {
		t.Fatalf("anchor = %+v", in)
	}
}

func TestRangeAnchorSingleLine(t *testing.T) {
	in := rangeAnchor("a.go", []DiffLine{{Kind: LineAdd, NewNo: 5}})
	if in == nil || *in.To != 5 || in.StartTo != nil {
		t.Fatalf("anchor = %+v", in)
	}
	if got := anchorLabel(in); got != "L5" {
		t.Errorf("label = %q", got)
	}
}

func TestRangeAnchorNothingCommentable(t *testing.T) {
	if in := rangeAnchor("a.go", []DiffLine{{Kind: LineNoNewline}}); in != nil {
		t.Errorf("anchor = %+v", in)
	}
	if in := rangeAnchor("a.go", nil); in != nil {
		t.Errorf("nil lines anchor = %+v", in)
	}
}

func TestAnchorLabelRange(t *testing.T) {
	in := rangeAnchor("a.go", []DiffLine{
		{Kind: LineAdd, NewNo: 12}, {Kind: LineAdd, NewNo: 15},
	})
	if got := anchorLabel(in); got != "L12–15" {
		t.Errorf("label = %q", got)
	}
}

func TestDiffViewCommentLabelAndJumpPosition(t *testing.T) {
	a := &app{w: 100, h: 30, detail: map[int]*prDetail{}}
	d := a.detailFor(7)
	d.diffstat = []DiffStatEntry{{Status: "modified", New: &struct {
		Path string `json:"path"`
	}{Path: "a.go"}}}
	d.files = []FileDiff{*hunkFixtureFile()}
	d.comments = masterFixture() // thread 2 anchors a.go L10 (hunk 0, add line)

	v := &diffView{prID: 7, fileIdx: 0, folded: map[int]bool{}, showCmt: true,
		selAnchor: -1, pendingJump: 2}
	v.rebuild(a)

	idx := -1
	for i, r := range v.vp.Rows {
		if r.Kind == RowComment && r.Selectable {
			if id, ok := r.Item.(int); ok && id == 2 {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		t.Fatal("comment row for thread 2 not found")
	}
	// The header carries the anchor label now (parity with the Comments tab).
	if txt := spansText(v.vp.Rows[idx]); !strings.Contains(txt, "L10") {
		t.Errorf("comment header = %q, want L10 label", txt)
	}

	v.tryPendingJump(20)
	if v.vp.Cursor != idx || v.pendingJump != 0 {
		t.Errorf("cursor = %d (want %d), pendingJump = %d", v.vp.Cursor, idx, v.pendingJump)
	}
	if want := max(idx-3, 0); v.vp.Top != want {
		t.Errorf("Top = %d, want %d (comment parked near the top)", v.vp.Top, want)
	}
}

func TestDiffViewPendingOutlineLineOldSide(t *testing.T) {
	a := &app{w: 100, h: 30, detail: map[int]*prDetail{}}
	d := a.detailFor(8)
	d.diffstat = []DiffStatEntry{{Status: "modified", Old: &struct {
		Path string `json:"path"`
	}{Path: "a.go"}, New: &struct {
		Path string `json:"path"`
	}{Path: "a.go"}}}
	d.files = []FileDiff{*hunkFixtureFile()}
	d.comments = []Comment{}
	v := newDiffView(a, 8, 0)
	v.pendingLine, v.pendingOldLine = 10, true
	v.tryPendingJump(20)
	line, ok := v.vp.Current().Item.(DiffLine)
	if !ok || line.Kind != LineDel || line.OldNo != 10 || v.pendingLine != 0 {
		t.Fatalf("old-side jump = row:%#v pending:%d", v.vp.Current(), v.pendingLine)
	}
}

func TestSelectionAndInterceptEsc(t *testing.T) {
	v := &diffView{selAnchor: -1}
	v.vp.Rows = []Row{
		row(RowDiffLine, DiffLine{Kind: LineAdd, NewNo: 1}, true),
		row(RowDiffLine, DiffLine{Kind: LineAdd, NewNo: 2}, true),
		{Kind: RowDiffLine, Spans: []Span{{"wrap cont", styleNone}}}, // not selectable
		row(RowDiffLine, DiffLine{Kind: LineAdd, NewNo: 3}, true),
	}
	v.vp.Cursor = 3
	v.selAnchor = 0
	lines := v.selectedLines()
	if len(lines) != 3 || lines[0].NewNo != 1 || lines[2].NewNo != 3 {
		t.Fatalf("selected = %+v", lines)
	}
	// Cursor above the anchor still selects the same range.
	v.selAnchor, v.vp.Cursor = 3, 0
	if lines := v.selectedLines(); len(lines) != 3 {
		t.Fatalf("reversed selected = %+v", lines)
	}
	if !v.interceptEsc(nil) || v.selAnchor != -1 {
		t.Error("Esc must cancel the selection")
	}
	if v.interceptEsc(nil) {
		t.Error("Esc without selection must fall through to pop")
	}
}
