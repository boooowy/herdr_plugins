package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPRCacheRoundTrip(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	prs := []PullRequest{{ID: 1, Title: "a"}, {ID: 2, Title: "b"}}
	savePRCache("ws", "repo", "OPEN", prs)
	got := loadPRCache("ws", "repo", "OPEN")
	if len(got) != 2 || got[0].ID != 1 || got[1].Title != "b" {
		t.Errorf("round trip: %+v", got)
	}
	if loadPRCache("ws", "repo", "MERGED") != nil {
		t.Error("cache miss must return nil")
	}
	if loadPRCache("ws", "other", "OPEN") != nil {
		t.Error("different repo must be a miss")
	}
}

func TestPRCacheCorruptFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	path := prCachePath("ws", "repo", "OPEN")
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte("{broken"), 0o600)
	if loadPRCache("ws", "repo", "OPEN") != nil {
		t.Error("corrupt cache must return nil")
	}
}

func TestMutatePRCacheUpdatesAndRemovesMatchingPR(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	prs := []PullRequest{{ID: 1, Title: "a"}, {ID: 2, Title: "b"}}
	savePRCache("ws", "repo", "OPEN", prs)

	mutatePRCache("ws", "repo", "OPEN", 1, func(pr *PullRequest) bool {
		pr.Title = "updated"
		return true
	})
	got := loadPRCache("ws", "repo", "OPEN")
	if len(got) != 2 || got[0].Title != "updated" {
		t.Fatalf("updated cache = %+v", got)
	}

	mutatePRCache("ws", "repo", "OPEN", 1, func(*PullRequest) bool { return false })
	got = loadPRCache("ws", "repo", "OPEN")
	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("removed cache = %+v", got)
	}
}
