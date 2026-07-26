package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudePercentUsesLatestMainAssistantUsage(t *testing.T) {
	home := t.TempDir()
	sessionID := "session-123"
	dir := filepath.Join(home, ".claude", "projects", "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	content := "" +
		`{"type":"assistant","isSidechain":false,"message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":10000,"cache_creation_input_tokens":1000,"cache_read_input_tokens":9000}}}` + "\n" +
		`{"type":"assistant","isSidechain":true,"message":{"model":"claude-sonnet-4-5","usage":{"input_tokens":190000}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := newSessionReader(home, time.Now)
	percent, ok := reader.claudePercent(sessionID)
	if !ok || percent != 10 {
		t.Fatalf("Claude percent = %v, %v", percent, ok)
	}
}

func TestClaudeContextWindowMarkers(t *testing.T) {
	cases := []struct {
		model string
		want  float64
	}{
		{"claude-opus-5", 1_000_000},
		{"claude-opus-4-6", 1_000_000},
		{"claude-opus-4-8", 1_000_000},
		{"claude-sonnet-5", 1_000_000},
		{"claude-fable-5", 1_000_000},
		{"claude-opus-6", 1_000_000},
		{"claude-sonnet-4-5", 200_000},
		{"claude-haiku-4-5-20251001", 200_000},
		{"<synthetic>", 200_000},
		{"claude-sonnet-4-5[1m]", 1_000_000},
	}
	for _, testCase := range cases {
		if got := claudeContextWindow(testCase.model); got != testCase.want {
			t.Errorf("claudeContextWindow(%q) = %v, want %v", testCase.model, got, testCase.want)
		}
	}
}

func TestClaudePercentSkipsSyntheticEntries(t *testing.T) {
	home := t.TempDir()
	sessionID := "session-synthetic"
	dir := filepath.Join(home, ".claude", "projects", "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	content := "" +
		`{"type":"assistant","isSidechain":false,"message":{"model":"claude-opus-5","usage":{"input_tokens":2,"cache_creation_input_tokens":4,"cache_read_input_tokens":492813}}}` + "\n" +
		`{"type":"assistant","isSidechain":false,"message":{"model":"<synthetic>","usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := newSessionReader(home, time.Now)
	percent, ok := reader.claudePercent(sessionID)
	if !ok {
		t.Fatal("claudePercent returned not ok")
	}
	if percent < 49 || percent > 50 {
		t.Errorf("Claude percent = %v, want ~49.3 (492819/1M)", percent)
	}
}

func TestCodexPercentMapsLatestSessionByCWD(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "workspace")
	sessionDir := filepath.Join(home, ".codex", "sessions", "2026", "07", "23")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionDir, "rollout-test.jsonl")
	content := "" +
		`{"type":"session_meta","payload":{"cwd":"` + cwd + `"}}` + "\n" +
		`{"payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":50000},"model_context_window":200000}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := newSessionReader(home, time.Now)
	percent, ok := reader.codexPercent(cwd)
	if !ok || percent != 25 {
		t.Fatalf("Codex percent = %v, %v", percent, ok)
	}
}

func TestTailLinesResetsAfterTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tail.jsonl")
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := &tailState{Usage: &claudeUsage{InputTokens: 10}, Used: 20}
	if _, err := tailLines(path, state); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := tailLines(path, state)
	if err != nil {
		t.Fatal(err)
	}
	if state.Usage != nil || state.Used != 0 {
		t.Fatalf("tail state was not reset: %#v", state)
	}
	if string(lines[0]) != "new" {
		t.Errorf("lines = %#v", lines)
	}
}

func TestTailLinesCarriesOverPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tail.jsonl")
	if err := os.WriteFile(path, []byte("first\npart"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := &tailState{}
	lines, err := tailLines(path, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || string(lines[0]) != "first" {
		t.Fatalf("first read lines = %#v", lines)
	}
	if state.Position != int64(len("first\n")) {
		t.Fatalf("position = %d, want %d", state.Position, len("first\n"))
	}
	if err := os.WriteFile(path, []byte("first\npartial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err = tailLines(path, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || string(lines[0]) != "partial" {
		t.Fatalf("second read lines = %#v", lines)
	}
}
