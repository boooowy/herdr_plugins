package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDashboardBarClampsValues(t *testing.T) {
	if got := dashboardBar(0); got != strings.Repeat("□", dashboardBarWidth) {
		t.Errorf("zero bar = %q", got)
	}
	if got := dashboardBar(200); got != strings.Repeat("■", dashboardBarWidth) {
		t.Errorf("clamped bar = %q", got)
	}
}

func TestRenderDashboardShowsState(t *testing.T) {
	a := testApp(t)
	collectedAt := time.Date(2026, 7, 23, 12, 34, 56, 0, time.Local).Unix()
	if err := writeQuotaState(filepath.Join(a.stateDir, "claude.json"), quotaState{
		Agent:       "claude",
		OK:          true,
		CollectedAt: collectedAt,
		Windows: []quotaWindow{{
			Label: "5h", UsedPercent: floatPointer(89), ResetsAt: float64(collectedAt + 3600),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	output := a.renderDashboard()
	for _, wanted := range []string{"Agent Quota Meter", "Claude Code", "5h", "89.0%", "Codex", "(no data yet)"} {
		if !strings.Contains(output, wanted) {
			t.Errorf("dashboard missing %q:\n%s", wanted, output)
		}
	}
}

func TestFormatResetAcceptsMillisecondsAndRFC3339(t *testing.T) {
	instant := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	if got := formatReset(float64(instant.UnixMilli())); got != instant.Local().Format("01/02 15:04") {
		t.Errorf("millisecond reset = %q", got)
	}
	if got := formatReset(instant.Format(time.RFC3339)); got != instant.Local().Format("01/02 15:04") {
		t.Errorf("RFC3339 reset = %q", got)
	}
}
