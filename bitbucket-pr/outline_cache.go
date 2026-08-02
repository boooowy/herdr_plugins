package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var outlineCachePartRE = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func outlineCachePath(ws, repo, sourceHash, destinationHash string) string {
	clean := func(s string) string { return outlineCachePartRE.ReplaceAllString(s, "_") }
	return filepath.Join(stateDir(), "cache", fmt.Sprintf("outline-v%d-%s__%s__%s__%s.json",
		outlineSchemaVersion, clean(ws), clean(repo), clean(sourceHash), clean(destinationHash)))
}

func loadOutlineCache(ws, repo, sourceHash, destinationHash string) *outlineResult {
	data, err := os.ReadFile(outlineCachePath(ws, repo, sourceHash, destinationHash))
	if err != nil {
		return nil
	}
	var result outlineResult
	if err := json.Unmarshal(data, &result); err != nil {
		debugf("outline cache: unmarshal: %v", err)
		return nil
	}
	if result.Schema != outlineSchemaVersion || result.SourceHash != sourceHash || result.DestinationHash != destinationHash {
		return nil
	}
	return &result
}

func saveOutlineCache(ws, repo string, result *outlineResult) {
	if result == nil {
		return
	}
	path := outlineCachePath(ws, repo, result.SourceHash, result.DestinationHash)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		debugf("outline cache: mkdir: %v", err)
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		debugf("outline cache: marshal: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		debugf("outline cache: write: %v", err)
	}
}
