package main

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestBuildAgentContext(t *testing.T) {
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	now := base.Add(2 * time.Hour)

	to := 496
	startTo := 486
	ranged := mkComment(1, 0, "a.go", nil, &to, false, base)
	ranged.Inline.StartTo = &startTo
	ranged.Content.Raw = "range comment"
	ranged.User.DisplayName = "tanaka"

	reply := mkComment(2, 1, "", nil, nil, false, base.Add(time.Hour))
	reply.Content.Raw = "reply body"
	reply.User.DisplayName = "suzuki"

	oldSide := mkComment(3, 0, "b.go", iptr(12), nil, false, base)
	oldSide.Content.Raw = "old side"

	outdated := mkComment(4, 0, "a.go", nil, iptr(5), true, base)
	general := mkComment(5, 0, "", nil, nil, false, base)

	data := buildAgentContext([]Comment{ranged, reply, oldSide, outdated, general}, nil, "", now)
	var ctx agentContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Version != 1 || len(ctx.Files) != 2 {
		t.Fatalf("ctx = %+v", ctx)
	}

	byPath := map[string]agentCtxFile{}
	for _, f := range ctx.Files {
		byPath[f.Path] = f
	}
	a := byPath["a.go"].Annotations
	if len(a) != 1 { // outdated thread dropped
		t.Fatalf("a.go annotations = %+v", a)
	}
	if a[0].NewRange[0] != 486 || a[0].NewRange[1] != 496 {
		t.Errorf("newRange = %v", a[0].NewRange)
	}
	if a[0].Summary != "range comment" {
		t.Errorf("summary = %q", a[0].Summary)
	}
	if a[0].Rationale != "↳ suzuki: reply body" {
		t.Errorf("rationale = %q", a[0].Rationale)
	}
	if a[0].Author != "tanaka (2時間前)" {
		t.Errorf("author = %q", a[0].Author)
	}

	b := byPath["b.go"].Annotations
	if len(b) != 1 || len(b[0].OldRange) != 2 || b[0].OldRange[1] != 12 {
		t.Errorf("b.go annotations = %+v", b)
	}
	if len(b[0].NewRange) != 0 {
		t.Errorf("old-side note must not carry newRange: %+v", b[0])
	}
}

// hunk sorts the changeset by ctx.files, so the array has to list every file
// of the patch with the focused one first — otherwise its ordering overrides
// the patch reorderPatch produced.
func TestBuildAgentContextOrder(t *testing.T) {
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	files := []FileDiff{
		{OldPath: "a.go", NewPath: "a.go"},
		{OldPath: "b.go", NewPath: "b.go"},
		{OldPath: "c.go", NewPath: "c.go"},
	}
	cmt := mkComment(1, 0, "a.go", nil, iptr(10), false, now)
	cmt.Content.Raw = "note on a"

	paths := func(data []byte) []string {
		var ctx agentContext
		if err := json.Unmarshal(data, &ctx); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, f := range ctx.Files {
			out = append(out, f.Path)
		}
		return out
	}

	// Focused file first, the rest in patch order — even though only a.go
	// has comments.
	got := paths(buildAgentContext([]Comment{cmt}, files, "c.go", now))
	want := []string{"c.go", "a.go", "b.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("focus c.go: paths = %v, want %v", got, want)
	}

	// No focus: plain patch order, still every file.
	got = paths(buildAgentContext([]Comment{cmt}, files, "", now))
	want = []string{"a.go", "b.go", "c.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("no focus: paths = %v, want %v", got, want)
	}

	// Uncommented files carry an explicit empty annotation list, never null:
	// hunk needs the entry purely for its rank.
	var ctx agentContext
	if err := json.Unmarshal(buildAgentContext([]Comment{cmt}, files, "c.go", now), &ctx); err != nil {
		t.Fatal(err)
	}
	for _, f := range ctx.Files {
		if f.Path == "a.go" {
			if len(f.Annotations) != 1 {
				t.Errorf("a.go annotations = %+v", f.Annotations)
			}
			continue
		}
		if f.Annotations == nil || len(f.Annotations) != 0 {
			t.Errorf("%s annotations = %+v, want empty non-nil", f.Path, f.Annotations)
		}
	}
}

// A rename's old path must not add a second entry: hunk displays the new side
// and matches previousPath itself.
func TestBuildAgentContextRenameFocus(t *testing.T) {
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	files := []FileDiff{
		{OldPath: "keep.go", NewPath: "keep.go"},
		{OldPath: "old.go", NewPath: "new.go", IsRename: true},
	}
	var ctx agentContext
	if err := json.Unmarshal(buildAgentContext(nil, files, "old.go", now), &ctx); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range ctx.Files {
		got = append(got, f.Path)
	}
	want := []string{"new.go", "keep.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
}

// Threads on files the patch does not carry (truncated diffs) keep their
// annotations instead of being dropped.
func TestBuildAgentContextKeepsCommentsOutsidePatch(t *testing.T) {
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	files := []FileDiff{{OldPath: "a.go", NewPath: "a.go"}}
	cmt := mkComment(1, 0, "truncated.go", nil, iptr(3), false, now)
	cmt.Content.Raw = "note"

	var ctx agentContext
	if err := json.Unmarshal(buildAgentContext([]Comment{cmt}, files, "", now), &ctx); err != nil {
		t.Fatal(err)
	}
	if len(ctx.Files) != 2 || ctx.Files[0].Path != "a.go" || ctx.Files[1].Path != "truncated.go" {
		t.Fatalf("files = %+v", ctx.Files)
	}
	if len(ctx.Files[1].Annotations) != 1 {
		t.Errorf("truncated.go annotations = %+v", ctx.Files[1].Annotations)
	}
}

func TestBuildAgentContextEmpty(t *testing.T) {
	data := buildAgentContext(nil, nil, "", time.Now())
	var ctx agentContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Version != 1 || len(ctx.Files) != 0 {
		t.Errorf("empty ctx = %+v", ctx)
	}
}
