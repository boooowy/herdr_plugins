package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// repo-dirs.json maps "workspace/repo" to the absolute path of a local
// checkout. It is how the repo picker knows which repositories can run
// Outline without a clone. Entries are learned from three sources: the
// action's normal repo resolution (launching from inside a checkout), a
// clone performed by the picker, and the repo_roots scan.

func repoDirIndexPath() string {
	return filepath.Join(stateDir(), "repo-dirs.json")
}

func repoDirKey(ws, repo string) string { return ws + "/" + repo }

// loadRepoDirIndex returns the persisted checkout index, dropping entries
// whose directory no longer looks like a git checkout.
func loadRepoDirIndex() map[string]string {
	data, err := os.ReadFile(repoDirIndexPath())
	if err != nil {
		return map[string]string{}
	}
	var index map[string]string
	if err := json.Unmarshal(data, &index); err != nil {
		debugf("repodirs: unmarshal: %v", err)
		return map[string]string{}
	}
	for key, dir := range index {
		if !looksLikeCheckout(dir) {
			delete(index, key)
		}
	}
	return index
}

// rememberRepoDir records one checkout location for the picker. Failures
// only log — the index is a convenience, never a requirement.
func rememberRepoDir(ws, repo, dir string) {
	if ws == "" || repo == "" || dir == "" {
		return
	}
	index := loadRepoDirIndex()
	key := repoDirKey(ws, repo)
	if index[key] == dir {
		return
	}
	index[key] = dir
	data, err := json.Marshal(index)
	if err != nil {
		debugf("repodirs: marshal: %v", err)
		return
	}
	if err := os.WriteFile(repoDirIndexPath(), data, 0o600); err != nil {
		debugf("repodirs: write: %v", err)
	}
}

// forgetRepoDir drops a stale entry (e.g. the recorded checkout's origin no
// longer matches the repository).
func forgetRepoDir(ws, repo string) {
	index := loadRepoDirIndex()
	key := repoDirKey(ws, repo)
	if _, ok := index[key]; !ok {
		return
	}
	delete(index, key)
	if data, err := json.Marshal(index); err == nil {
		if err := os.WriteFile(repoDirIndexPath(), data, 0o600); err != nil {
			debugf("repodirs: write: %v", err)
		}
	}
}

func looksLikeCheckout(dir string) bool {
	if dir == "" {
		return false
	}
	// .git is a directory in a normal clone and a file in a worktree; either
	// counts as a checkout.
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// originURLRe pulls the url out of a [remote "origin"] section without
// shelling out to git — the scan touches every directory under repo_roots,
// so one subprocess per candidate would be noticeable.
var originURLRe = regexp.MustCompile(`(?s)\[remote "origin"\].*?\n\s*url\s*=\s*(\S+)`)

// scanRepoRoots walks each configured root (two levels deep, covering both
// root/repo and root/group/repo layouts) and maps every bitbucket.org
// checkout it finds to "workspace/repo".
func scanRepoRoots(roots []string) map[string]string {
	found := map[string]string{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			debugf("repodirs: scan %s: %v", root, err)
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dir := filepath.Join(root, e.Name())
			if scanCheckout(dir, found) {
				continue
			}
			// Not a checkout itself — one level deeper (grouping directory).
			subs, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, s := range subs {
				if !s.IsDir() || strings.HasPrefix(s.Name(), ".") {
					continue
				}
				scanCheckout(filepath.Join(dir, s.Name()), found)
			}
		}
	}
	return found
}

// scanCheckout inspects one directory; when it is a bitbucket.org checkout
// it records it in found and reports true.
func scanCheckout(dir string, found map[string]string) bool {
	data, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		return looksLikeCheckout(dir) // worktrees etc.: a checkout, just not indexable
	}
	m := originURLRe.FindSubmatch(data)
	if m == nil {
		return true
	}
	ws, repo, err := parseBitbucketRemote(string(m[1]))
	if err != nil {
		return true // a checkout of some other host
	}
	key := repoDirKey(ws, repo)
	if _, ok := found[key]; !ok {
		found[key] = dir
	}
	return true
}
