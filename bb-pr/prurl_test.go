package main

import "testing"

func TestParsePRURL(t *testing.T) {
	cases := []struct {
		url      string
		ws, repo string
		id       int
		wantErr  bool
	}{
		{url: "https://bitbucket.org/myworkspace/my-app/pull-requests/482", ws: "myworkspace", repo: "my-app", id: 482},
		{url: "https://bitbucket.org/ws/repo/pull-requests/1/", ws: "ws", repo: "repo", id: 1},
		{url: "https://bitbucket.org/ws/repo/pull-requests/12/feat-title/diff", ws: "ws", repo: "repo", id: 12},
		{url: "https://bitbucket.org/ws/repo/pull-requests/12?w=1", ws: "ws", repo: "repo", id: 12},
		{url: "https://bitbucket.org/ws/repo/pull-requests/12#comment-5", ws: "ws", repo: "repo", id: 12},
		{url: "https://bitbucket.org/ws/repo/branches", wantErr: true},
		{url: "https://bitbucket.org/ws/repo/pull-requests/", wantErr: true},
		{url: "https://github.com/o/r/pull/12", wantErr: true},
	}
	for _, c := range cases {
		ws, repo, id, err := parsePRURL(c.url)
		if c.wantErr {
			if err == nil {
				t.Errorf("parsePRURL(%q) = %q/%q#%d, want error", c.url, ws, repo, id)
			}
			continue
		}
		if err != nil {
			t.Errorf("parsePRURL(%q): %v", c.url, err)
			continue
		}
		if ws != c.ws || repo != c.repo || id != c.id {
			t.Errorf("parsePRURL(%q) = %q/%q#%d, want %q/%q#%d", c.url, ws, repo, id, c.ws, c.repo, c.id)
		}
	}
}
