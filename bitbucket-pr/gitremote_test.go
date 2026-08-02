package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseBitbucketRemote(t *testing.T) {
	cases := []struct {
		url      string
		ws, repo string
		wantErr  bool
	}{
		{url: "git@bitbucket.org:myworkspace/my-app.git", ws: "myworkspace", repo: "my-app"},
		{url: "git@bitbucket.org:myworkspace/my-app", ws: "myworkspace", repo: "my-app"},
		{url: "ssh://git@bitbucket.org/myworkspace/my-app.git", ws: "myworkspace", repo: "my-app"},
		{url: "ssh://git@bitbucket.org:22/myworkspace/my-app.git", ws: "myworkspace", repo: "my-app"},
		{url: "https://bitbucket.org/myworkspace/my-app.git", ws: "myworkspace", repo: "my-app"},
		{url: "https://someuser@bitbucket.org/myworkspace/my-app.git", ws: "myworkspace", repo: "my-app"},
		{url: "https://bitbucket.org/myworkspace/my-app", ws: "myworkspace", repo: "my-app"},
		{url: "https://bitbucket.org/myworkspace/my-app/", ws: "myworkspace", repo: "my-app"},
		{url: "https://bitbucket.org/ws/repo.name.git", ws: "ws", repo: "repo.name"},
		{url: "  git@bitbucket.org:ws/repo.git\n", ws: "ws", repo: "repo"},
		{url: "git@github.com:owner/repo.git", wantErr: true},
		{url: "https://gitlab.com/owner/repo.git", wantErr: true},
		{url: "", wantErr: true},
	}
	for _, c := range cases {
		ws, repo, err := parseBitbucketRemote(c.url)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseBitbucketRemote(%q) = %q/%q, want error", c.url, ws, repo)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseBitbucketRemote(%q): %v", c.url, err)
			continue
		}
		if ws != c.ws || repo != c.repo {
			t.Errorf("parseBitbucketRemote(%q) = %q/%q, want %q/%q", c.url, ws, repo, c.ws, c.repo)
		}
	}
}

func TestRepoRootFromDir(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := repoRootFromDir(nested)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(dir)
	if canonical, evalErr := filepath.EvalSymlinks(want); evalErr == nil {
		want = canonical
	}
	if got != want {
		t.Fatalf("repo root = %q, want %q", got, want)
	}
}
