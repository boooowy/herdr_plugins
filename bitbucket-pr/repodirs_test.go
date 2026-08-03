package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoDirIndexRoundTrip(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	checkout := t.TempDir()
	os.MkdirAll(filepath.Join(checkout, ".git"), 0o700)

	rememberRepoDir("ws", "repo", checkout)
	got := loadRepoDirIndex()
	if got[repoDirKey("ws", "repo")] != checkout {
		t.Errorf("index = %v, want %s", got, checkout)
	}

	forgetRepoDir("ws", "repo")
	if got := loadRepoDirIndex(); len(got) != 0 {
		t.Errorf("after forget: %v", got)
	}
}

func TestLoadRepoDirIndexDropsDeadCheckouts(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	gone := filepath.Join(t.TempDir(), "removed")
	rememberRepoDir("ws", "repo", gone) // no .git underneath
	if got := loadRepoDirIndex(); len(got) != 0 {
		t.Errorf("dead entry survived: %v", got)
	}
}

func TestScanRepoRootsFindsBitbucketCheckouts(t *testing.T) {
	root := t.TempDir()
	write := func(dir, url string) {
		os.MkdirAll(filepath.Join(dir, ".git"), 0o700)
		os.WriteFile(filepath.Join(dir, ".git", "config"),
			[]byte("[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = "+url+"\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"), 0o600)
	}
	write(filepath.Join(root, "repo-a"), "git@bitbucket.org:ws/repo-a.git")
	write(filepath.Join(root, "repo-b"), "https://user@bitbucket.org/ws/repo-b")
	write(filepath.Join(root, "other"), "git@github.com:org/other.git")
	// Grouping directory one level deeper.
	write(filepath.Join(root, "group", "repo-c"), "ssh://git@bitbucket.org/ws2/repo-c.git")
	os.MkdirAll(filepath.Join(root, "not-a-repo"), 0o700)

	found := scanRepoRoots([]string{root, filepath.Join(root, "missing")})
	want := map[string]string{
		"ws/repo-a":  filepath.Join(root, "repo-a"),
		"ws/repo-b":  filepath.Join(root, "repo-b"),
		"ws2/repo-c": filepath.Join(root, "group", "repo-c"),
	}
	if len(found) != len(want) {
		t.Fatalf("found = %v, want %v", found, want)
	}
	for key, dir := range want {
		if found[key] != dir {
			t.Errorf("found[%s] = %s, want %s", key, found[key], dir)
		}
	}
}
