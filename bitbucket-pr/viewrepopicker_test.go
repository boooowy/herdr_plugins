package main

import (
	"strings"
	"testing"
	"time"
)

func pickerTestApp() *app {
	return &app{
		w: 100, h: 12,
		ctx:           uiContext{Workspace: "ws", Mode: "picker"},
		localDirs:     map[string]string{},
		cloneInFlight: map[string]bool{},
	}
}

func TestRepoPickerRebuildMarksLocalCheckouts(t *testing.T) {
	a := pickerTestApp()
	a.repos = []Repository{
		{Slug: "with-local", FullName: "ws/with-local", UpdatedOn: time.Now()},
		{Slug: "without", FullName: "ws/without"},
	}
	a.localDirs["ws/with-local"] = "/home/u/src/with-local"

	v := &repoPickerView{}
	v.rebuild(a)

	if got, want := len(v.vp.Rows), 4; got != want {
		t.Fatalf("rows = %d, want %d", got, want)
	}
	if got := spansText(v.vp.Rows[1]); !strings.Contains(got, "●") {
		t.Errorf("local meta row = %q, want ● mark", got)
	}
	if got := spansText(v.vp.Rows[3]); !strings.Contains(got, "ローカルなし") {
		t.Errorf("missing meta row = %q, want ローカルなし", got)
	}
}

func TestRepoPickerFilterMatchesSlugNameDescription(t *testing.T) {
	a := pickerTestApp()
	a.repos = []Repository{
		{Slug: "api-gateway", FullName: "ws/api-gateway"},
		{Slug: "webapp", Name: "Web App", FullName: "ws/webapp"},
		{Slug: "tool", Description: "gateway helper", FullName: "ws/tool"},
	}
	v := &repoPickerView{}
	v.search.buf = []byte("gateway")
	v.rebuild(a)

	if v.shown != 2 {
		t.Fatalf("shown = %d, want 2 (slug + description matches)", v.shown)
	}
	for i := 0; i < len(v.vp.Rows); i += 2 {
		if got := spansText(v.vp.Rows[i]); strings.Contains(got, "webapp") {
			t.Errorf("webapp must be filtered out, rows: %q", got)
		}
	}
}

func TestRepoPickerRemoteResultsAppendDeduped(t *testing.T) {
	a := pickerTestApp()
	a.repos = []Repository{{Slug: "gateway", FullName: "ws/gateway"}}
	a.reposNext = "https://api/next" // list not fully loaded → remote results shown
	v := &repoPickerView{
		remoteQuery: "gate",
		remoteResults: []Repository{
			{Slug: "gateway", FullName: "ws/gateway"},       // duplicate of the local page
			{Slug: "gate-keeper", FullName: "ws/gate-keeper"}, // genuinely remote-only
		},
	}
	v.search.buf = []byte("gate")
	v.rebuild(a)

	if v.shown != 2 {
		t.Fatalf("shown = %d, want 2 (local + deduped remote)", v.shown)
	}
	last := v.vp.Rows[len(v.vp.Rows)-2]
	if got := spansText(last); !strings.Contains(got, "gate-keeper") {
		t.Errorf("remote hit missing: %q", got)
	}
	if item := last.Item.(int); item >= 0 {
		t.Errorf("remote row item = %d, want negative remote index", item)
	}
	if r := v.repoAt(a, last.Item.(int)); r == nil || r.Slug != "gate-keeper" {
		t.Errorf("repoAt(remote) = %+v", r)
	}
}

func TestRepoPickerEmptyStates(t *testing.T) {
	a := pickerTestApp()
	a.repos = []Repository{}
	v := &repoPickerView{}
	v.rebuild(a)
	if got := spansText(v.vp.Rows[0]); !strings.Contains(got, "リポジトリがありません") {
		t.Errorf("empty state = %q", got)
	}

	a.reposErr = "boom"
	v.rebuild(a)
	if got := spansText(v.vp.Rows[0]); !strings.Contains(got, "再読込") {
		t.Errorf("error state = %q", got)
	}
}
