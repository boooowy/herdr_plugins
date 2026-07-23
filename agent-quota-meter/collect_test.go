package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func testApp(t *testing.T) *app {
	t.Helper()
	root := t.TempDir()
	return &app{
		herdr:      "herdr",
		home:       filepath.Join(root, "home"),
		stateDir:   filepath.Join(root, "state"),
		runtimeDir: filepath.Join(root, "runtime"),
		now:        time.Now,
		httpClient: &http.Client{Timeout: time.Second},
	}
}

func TestWindowLabel(t *testing.T) {
	cases := []struct {
		minutes float64
		present bool
		want    string
	}{
		{0, false, "?"},
		{300, true, "5h"},
		{10080, true, "1w"},
		{43200, true, "1m"},
		{720, true, "12h"},
		{4320, true, "3d"},
	}
	for _, test := range cases {
		if got := windowLabel(test.minutes, test.present); got != test.want {
			t.Errorf("windowLabel(%v, %v) = %q, want %q", test.minutes, test.present, got, test.want)
		}
	}
}

func TestLastRateLimitsUsesLastSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := "" +
		`{"payload":{"rate_limits":{"primary":{"used_percent":10}}}}` + "\n" +
		`{"payload":{"nested":[{"rate_limits":{"primary":{"used_percent":20}}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rateLimits, err := lastRateLimits(path)
	if err != nil {
		t.Fatal(err)
	}
	primary := rateLimits["primary"].(map[string]any)
	if primary["used_percent"] != float64(20) {
		t.Fatalf("last used_percent = %#v", primary["used_percent"])
	}
}

func TestCollectCodexWritesCompatibleState(t *testing.T) {
	a := testApp(t)
	sessionDir := filepath.Join(a.home, ".codex", "sessions", "2026", "07", "23")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionDir, "rollout-test.jsonl")
	line := `{"payload":{"type":"token_count","info":{"rate_limits":{"primary":{"used_percent":25,"window_minutes":300,"resets_at":1700000000},"secondary":{"used_percent":40,"window_minutes":10080}}}}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.collectCodex(); err != nil {
		t.Fatal(err)
	}
	state, err := readQuotaState(filepath.Join(a.stateDir, "codex.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !state.OK || len(state.Windows) != 2 {
		t.Fatalf("state = %#v", state)
	}
	if state.Windows[0].Label != "5h" || *state.Windows[0].UsedPercent != 25 {
		t.Errorf("primary = %#v", state.Windows[0])
	}
	if state.Windows[1].Label != "1w" || *state.Windows[1].UsedPercent != 40 {
		t.Errorf("secondary = %#v", state.Windows[1])
	}
}

func TestFindClaudeWindowsPreservesOrder(t *testing.T) {
	windows, err := findClaudeWindows(json.RawMessage(`{
		"five_hour":{"utilization":0.25,"resets_at":"2026-07-23T10:00:00Z"},
		"seven_day":{"nested":{"utilization":0.5}}
	}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 {
		t.Fatalf("window count = %d", len(windows))
	}
	if windows[0].Label != "5h" || *windows[0].UsedPercent != 0.25 {
		t.Errorf("first window = %#v", windows[0])
	}
	// The key hint follows the immediate object containing utilization, matching
	// the Python recursive collector's behavior.
	if windows[1].Label != "nested" || *windows[1].UsedPercent != 0.5 {
		t.Errorf("second window = %#v", windows[1])
	}
}

func TestCollectClaudeCachesAndScalesFractionalUsage(t *testing.T) {
	a := testApp(t)
	fixedNow := time.Unix(1_800_000_000, 0)
	a.now = func() time.Time { return fixedNow }
	var requests atomic.Int32
	a.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"five_hour":{"utilization":0.25},
				"seven_day":{"utilization":0.5}
			}`)),
			Header: make(http.Header),
		}, nil
	})}
	t.Setenv("AGENT_QUOTA_CLAUDE_USAGE_URL", "https://usage.test")
	a.credentials = func() (claudeCredentials, error) {
		var result claudeCredentials
		result.ClaudeAIOAuth.AccessToken = "secret"
		result.ClaudeAIOAuth.ExpiresAt = float64(fixedNow.Add(time.Hour).UnixMilli())
		return result, nil
	}

	if err := a.collectClaude(); err != nil {
		t.Fatal(err)
	}
	state, err := readQuotaState(filepath.Join(a.stateDir, "claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !state.OK || len(state.Windows) != 2 {
		t.Fatalf("state = %#v", state)
	}
	if *state.Windows[0].UsedPercent != 25 || *state.Windows[1].UsedPercent != 50 {
		t.Errorf("fractional usage was not scaled: %#v", state.Windows)
	}
	if err := a.collectClaude(); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want 1 due to cache", requests.Load())
	}
}

func TestCollectClaudeRetainsRecentSuccessOnFailure(t *testing.T) {
	a := testApp(t)
	fixedNow := time.Unix(1_800_000_000, 0)
	a.now = func() time.Time { return fixedNow }
	if err := writeQuotaState(filepath.Join(a.stateDir, "claude.json"), quotaState{
		Agent:       "claude",
		OK:          true,
		CollectedAt: fixedNow.Add(-6 * time.Minute).Unix(),
		Windows: []quotaWindow{{
			Label: "5h", UsedPercent: floatPointer(20),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	a.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader("rate limited")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Setenv("AGENT_QUOTA_CLAUDE_USAGE_URL", "https://usage.test")
	a.credentials = func() (claudeCredentials, error) {
		var result claudeCredentials
		result.ClaudeAIOAuth.AccessToken = "secret"
		return result, nil
	}
	if err := a.collectClaude(); err != nil {
		t.Fatal(err)
	}
	state, err := readQuotaState(filepath.Join(a.stateDir, "claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !state.OK || len(state.Windows) != 1 {
		t.Fatalf("recent success was not retained: %#v", state)
	}
	if state.LastError != "usage API HTTP 429" || state.AttemptedAt != fixedNow.Unix() {
		t.Errorf("failure metadata = %#v", state)
	}
}

func TestUpdateDebounceSkipsCollectors(t *testing.T) {
	a := testApp(t)
	now := time.Unix(1_800_000_000, 0)
	a.now = func() time.Time { return now }
	if err := touch(filepath.Join(a.stateDir, "last_update"), now.Unix()); err != nil {
		t.Fatal(err)
	}
	// No credentials callback is installed. A debounce hit must return before
	// either collector runs.
	if err := a.updateQuota(false); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(a.stateDir, "claude.json")) {
		t.Fatal("debounced update unexpectedly ran Claude collector")
	}
}
