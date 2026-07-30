package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// PR-list disk cache: the last fetched first page per (workspace, repo,
// state). Startup paints it instantly while the fresh page loads behind it
// (stale-while-revalidate) — the network round trip through the proxy costs
// ~1s regardless of page size, so the cache is what makes launch feel
// immediate. A missing or corrupt file just means a network-first start.

func prCachePath(ws, repo, state string) string {
	dir := os.Getenv("HERDR_PLUGIN_STATE_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "cache", "prs-"+ws+"__"+repo+"__"+state+".json")
}

// loadPRCache returns the cached first page, or nil when absent/unreadable.
func loadPRCache(ws, repo, state string) []PullRequest {
	data, err := os.ReadFile(prCachePath(ws, repo, state))
	if err != nil {
		return nil
	}
	var prs []PullRequest
	if err := json.Unmarshal(data, &prs); err != nil {
		debugf("cache: unmarshal %s: %v", prCachePath(ws, repo, state), err)
		return nil
	}
	return prs
}

// savePRCache stores the first page for the next startup; failures only log.
func savePRCache(ws, repo, state string, prs []PullRequest) {
	path := prCachePath(ws, repo, state)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		debugf("cache: mkdir: %v", err)
		return
	}
	data, err := json.Marshal(prs)
	if err != nil {
		debugf("cache: marshal: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		debugf("cache: write: %v", err)
	}
}
