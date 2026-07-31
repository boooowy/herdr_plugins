package main

import (
	"encoding/json"
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

	data := buildAgentContext([]Comment{ranged, reply, oldSide, outdated, general}, now)
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

func TestBuildAgentContextEmpty(t *testing.T) {
	data := buildAgentContext(nil, time.Now())
	var ctx agentContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Version != 1 || len(ctx.Files) != 0 {
		t.Errorf("empty ctx = %+v", ctx)
	}
}
