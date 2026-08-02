package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// bitbucketRemoteRe matches the bitbucket.org remote URL forms git produces:
//
//	git@bitbucket.org:workspace/repo.git
//	ssh://git@bitbucket.org/workspace/repo.git
//	ssh://git@bitbucket.org:22/workspace/repo.git
//	https://bitbucket.org/workspace/repo.git
//	https://user@bitbucket.org/workspace/repo
var bitbucketRemoteRe = regexp.MustCompile(
	`^(?:(?:ssh|https?)://)?(?:[\w.-]+@)?bitbucket\.org(?::\d+)?[:/]([\w.-]+)/([\w.-]+?)(?:\.git)?/?$`)

// parseBitbucketRemote extracts (workspace, repo_slug) from a git remote URL.
func parseBitbucketRemote(url string) (workspace, repo string, err error) {
	m := bitbucketRemoteRe.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", "", fmt.Errorf("not a bitbucket.org remote: %q", url)
	}
	return m[1], m[2], nil
}

// repoFromDir resolves the Bitbucket workspace/repo for the git repository
// containing dir, via its origin remote.
func repoFromDir(dir string) (workspace, repo string, err error) {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", "", fmt.Errorf("git remote get-url origin (in %s): %w", dir, err)
	}
	return parseBitbucketRemote(string(out))
}

// repoRootFromDir returns the stable top-level path of the repository that
// contains dir. Outline analysis must keep the launch repository: the viewer
// pane itself runs from the plugin directory and its cwd is unrelated to the
// PR being inspected.
func repoRootFromDir(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel (in %s): %w", dir, err)
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", fmt.Errorf("git returned an empty repository root for %s", dir)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("absolute repository root: %w", err)
	}
	return filepath.Clean(abs), nil
}
