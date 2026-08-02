package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOutlineCacheRoundTripAndIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	result := &outlineResult{
		Schema:          outlineSchemaVersion,
		SourceHash:      "source-1",
		DestinationHash: "dest-1",
		Files:           []outlineFile{{Path: "main.go", Symbols: []outlineSymbol{{ID: "x", Name: "Run"}}}},
	}
	saveOutlineCache("team/name", "repo name", result)
	got := loadOutlineCache("team/name", "repo name", "source-1", "dest-1")
	if got == nil || got.symbolCount() != 1 {
		t.Fatalf("cache = %+v", got)
	}
	if got := loadOutlineCache("team/name", "repo name", "source-1", "other"); got != nil {
		t.Fatalf("destination mismatch returned %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "cache", "outline-v2-team_name__repo_name__source-1__dest-1.json")); err != nil {
		t.Fatal(err)
	}
}

func TestOutlineCacheRejectsCorruptData(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_STATE_DIR", dir)
	path := outlineCachePath("ws", "repo", "source", "dest")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadOutlineCache("ws", "repo", "source", "dest"); got != nil {
		t.Fatalf("corrupt cache = %+v", got)
	}
}
