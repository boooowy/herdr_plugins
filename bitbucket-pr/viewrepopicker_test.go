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
		{Slug: "cloning", FullName: "ws/cloning"},
	}
	a.localDirs["ws/with-local"] = "/home/u/src/with-local"
	a.cloneInFlight["ws/cloning"] = true

	v := &repoPickerView{}
	v.rebuild(a)

	if got, want := len(v.vp.Rows), 3; got != want {
		t.Fatalf("rows = %d, want %d (one line per repo)", got, want)
	}
	if got := spansText(v.vp.Rows[0]); !strings.Contains(got, "●") {
		t.Errorf("local row = %q, want ● mark", got)
	}
	if got := spansText(v.vp.Rows[1]); strings.Contains(got, "●") || strings.Contains(got, "◌") {
		t.Errorf("non-local row = %q, want blank mark", got)
	}
	if got := spansText(v.vp.Rows[2]); !strings.Contains(got, "◌") {
		t.Errorf("cloning row = %q, want ◌ mark", got)
	}
}

func TestRepoPickerEmptyDescriptionStaysBlank(t *testing.T) {
	a := pickerTestApp()
	a.repos = []Repository{{Slug: "no-desc", FullName: "ws/no-desc"}}
	v := &repoPickerView{}
	v.rebuild(a)
	if got := spansText(v.vp.Rows[0]); strings.Contains(got, "説明なし") {
		t.Errorf("row = %q, empty description must stay blank", got)
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
	last := v.vp.Rows[len(v.vp.Rows)-1]
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

func TestRepoPickerCrossPRModeRowsAndFilter(t *testing.T) {
	a := pickerTestApp()
	a.myPRs.prs = []PullRequest{
		{ID: 100, Title: "fix timeout", UpdatedOn: time.Now()},
		{ID: 200, Title: "add cache layer"},
	}
	a.myPRs.prs[0].Destination.Repository.FullName = "ws/repo-a"
	a.myPRs.prs[1].Destination.Repository.FullName = "ws/repo-b"
	a.myPRs.loaded = true

	v := &repoPickerView{mode: pickerModeMine}
	v.rebuild(a)
	if len(v.vp.Rows) != 2 || v.shown != 2 {
		t.Fatalf("rows = %d shown = %d, want 2/2", len(v.vp.Rows), v.shown)
	}
	if got := spansText(v.vp.Rows[0]); !strings.Contains(got, "#100") || !strings.Contains(got, "repo-a") {
		t.Errorf("row = %q, want PR id and repo slug", got)
	}
	if pr := v.currentCrossPR(a); pr == nil || pr.ID != 100 {
		t.Errorf("currentCrossPR = %+v", pr)
	}

	// The filter matches the repo slug too.
	v.search.buf = []byte("repo-b")
	v.rebuild(a)
	if v.shown != 1 {
		t.Fatalf("shown = %d, want 1", v.shown)
	}
	if pr := v.currentCrossPR(a); pr == nil || pr.ID != 200 {
		t.Errorf("filtered currentCrossPR = %+v", pr)
	}
}

func TestRepoPickerCycleModeWraps(t *testing.T) {
	a := pickerTestApp()
	a.repos = []Repository{}
	a.myPRs.loaded, a.reviewPRs.loaded = true, true
	v := &repoPickerView{}
	v.cycleMode(a, 1)
	if v.mode != pickerModeMine {
		t.Fatalf("mode = %d, want mine", v.mode)
	}
	v.cycleMode(a, -1)
	if v.mode != pickerModeRepos {
		t.Fatalf("mode = %d, want repos", v.mode)
	}
	v.cycleMode(a, -1)
	if v.mode != pickerModeReviewing {
		t.Fatalf("mode = %d, want reviewing (wrap)", v.mode)
	}
}
