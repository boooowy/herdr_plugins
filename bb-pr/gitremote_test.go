package main

import "testing"

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
