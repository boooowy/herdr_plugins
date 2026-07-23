package main

import (
	"slices"
	"testing"
	"time"
)

type optionPair struct {
	Option string
	Value  string
}

func optionPairs(args []string) []optionPair {
	result := make([]optionPair, 0, len(args)/2)
	for index := 0; index+1 < len(args); index += 2 {
		result = append(result, optionPair{args[index], args[index+1]})
	}
	return result
}

func hasPair(args []string, wanted optionPair) bool {
	return slices.Contains(optionPairs(args), wanted)
}

func TestContextStyleBoundaries(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{59.99, "normal"},
		{60, "caution"},
		{79.99, "caution"},
		{80, "warning"},
		{89.99, "warning"},
		{90, "danger"},
	}
	for _, test := range cases {
		if got := contextStyleLevel(test.value); got != test.want {
			t.Errorf("contextStyleLevel(%v) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestQuotaStyleBoundaries(t *testing.T) {
	cases := []struct {
		value float64
		want  string
	}{
		{60, "normal"},
		{59.99, "caution"},
		{30, "caution"},
		{29.99, "warning"},
		{15, "warning"},
		{14.99, "danger"},
	}
	for _, test := range cases {
		if got := quotaStyleLevel(test.value); got != test.want {
			t.Errorf("quotaStyleLevel(%v) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestStyledTokenArgsOnlySetActiveLevel(t *testing.T) {
	args := styledTokenArgs("context", "warning", "ctx 82%")
	for _, wanted := range []optionPair{
		{"--token", "context_warning=ctx 82%"},
		{"--clear-token", "context_normal"},
		{"--clear-token", "context_caution"},
		{"--clear-token", "context_danger"},
	} {
		if !hasPair(args, wanted) {
			t.Errorf("missing pair %#v from %#v", wanted, optionPairs(args))
		}
	}
}

func TestRenderQuotaWindowsKeepsOrderAndStyles(t *testing.T) {
	display := renderQuotaWindows([]quotaWindow{
		{Label: "5h", UsedPercent: floatPointer(67)},
		{Label: "1w", UsedPercent: floatPointer(34)},
	})
	if display == nil {
		t.Fatal("renderQuotaWindows returned nil")
	}
	if display.Legacy != "5h 33%, 1w 66% left" {
		t.Fatalf("legacy = %q", display.Legacy)
	}
	if len(display.Slots) != 2 {
		t.Fatalf("slot count = %d", len(display.Slots))
	}
	if display.Slots[0].Slot != 1 || display.Slots[0].Level != "caution" ||
		display.Slots[0].Value != "5h: 33%" {
		t.Errorf("first slot = %#v", display.Slots[0])
	}
	if display.Slots[1].Slot != 2 || display.Slots[1].Level != "normal" ||
		display.Slots[1].Value != "1w: 66% left" {
		t.Errorf("second slot = %#v", display.Slots[1])
	}
}

func TestRenderQuotaWindowsClampsAndSkipsInvalid(t *testing.T) {
	display := renderQuotaWindows([]quotaWindow{
		{Label: "invalid"},
		{Label: "low", UsedPercent: floatPointer(150)},
		{Label: "high", UsedPercent: floatPointer(-20)},
	})
	if len(display.Slots) != 2 {
		t.Fatalf("slot count = %d", len(display.Slots))
	}
	if display.Slots[0].Value != "low: 0%" || display.Slots[0].Level != "danger" {
		t.Errorf("low slot = %#v", display.Slots[0])
	}
	if display.Slots[1].Value != "high: 100% left" || display.Slots[1].Level != "normal" {
		t.Errorf("high slot = %#v", display.Slots[1])
	}
}

func TestQuotaArgsSetActiveSlotsAndClearOthers(t *testing.T) {
	display := renderQuotaWindows([]quotaWindow{
		{Label: "5h", UsedPercent: floatPointer(67)},
		{Label: "1w", UsedPercent: floatPointer(34)},
	})
	args := quotaTokenArgs(display)
	for _, wanted := range []optionPair{
		{"--token", "quota=5h 33%, 1w 66% left"},
		{"--token", "quota_1_caution=5h: 33%"},
		{"--token", "quota_2_normal=1w: 66% left"},
		{"--clear-token", "quota_1_normal"},
		{"--clear-token", "quota_2_" + "danger"},
		{"--clear-token", "quota_5_warning"},
	} {
		if !hasPair(args, wanted) {
			t.Errorf("missing pair %#v", wanted)
		}
	}
}

func TestSplitReportArgsAtHerdrLimit(t *testing.T) {
	var args []string
	for range 21 {
		args = append(args, "--clear-token", "token")
	}
	args = append(args, "--ttl-ms", "300000")
	chunks, err := splitReportArgs(args)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunk count = %d", len(chunks))
	}
	if len(optionPairs(chunks[0][:len(chunks[0])-2])) != 16 {
		t.Errorf("first patch count = %d", len(optionPairs(chunks[0][:len(chunks[0])-2])))
	}
	if len(optionPairs(chunks[1][:len(chunks[1])-2])) != 5 {
		t.Errorf("second patch count = %d", len(optionPairs(chunks[1][:len(chunks[1])-2])))
	}
	for _, chunk := range chunks {
		if !slices.Equal(chunk[len(chunk)-2:], []string{"--ttl-ms", "300000"}) {
			t.Errorf("missing common TTL in %#v", chunk)
		}
	}
}

func TestSplitReportArgsRejectsMalformedInput(t *testing.T) {
	if _, err := splitReportArgs([]string{"--clear-token"}); err == nil {
		t.Fatal("expected malformed args error")
	}
}

func TestIdleGraceTransitions(t *testing.T) {
	now := time.Unix(100, 0)
	mode, deadline := nextDisplayState("", "idle", time.Time{}, now)
	if mode != "" || !deadline.IsZero() {
		t.Errorf("initial idle = %q, %v", mode, deadline)
	}
	for _, previous := range []string{"done", "working"} {
		mode, deadline = nextDisplayState(previous, "idle", time.Time{}, now)
		if mode != "static" || !deadline.Equal(now.Add(idleGrace)) {
			t.Errorf("%s -> idle = %q, %v", previous, mode, deadline)
		}
	}
	originalDeadline := now.Add(idleGrace)
	mode, deadline = nextDisplayState("idle", "idle", originalDeadline, now.Add(time.Minute))
	if mode != "static" || !deadline.Equal(originalDeadline) {
		t.Errorf("repeated idle extended deadline: %q, %v", mode, deadline)
	}
	mode, deadline = nextDisplayState("idle", "idle", originalDeadline, originalDeadline)
	if mode != "" || !deadline.IsZero() {
		t.Errorf("expired grace = %q, %v", mode, deadline)
	}
	mode, deadline = nextDisplayState("idle", "working", originalDeadline, now)
	if mode != "working" || !deadline.IsZero() {
		t.Errorf("working did not cancel grace: %q, %v", mode, deadline)
	}
	mode, deadline = nextDisplayState("blocked", "idle", time.Time{}, now)
	if mode != "" || !deadline.IsZero() {
		t.Errorf("blocked -> idle = %q, %v", mode, deadline)
	}
}

func TestRemainingTTLUsesDeadline(t *testing.T) {
	now := time.Unix(100, 0)
	if got := remainingTTL(time.Unix(400, 0), now); got != "300000" {
		t.Errorf("remaining TTL = %q", got)
	}
	if got := remainingTTL(time.Unix(400, 0), time.Unix(399, 500_000_000)); got != "500" {
		t.Errorf("subsecond remaining TTL = %q", got)
	}
}

func TestPacmanASCIIAndContextText(t *testing.T) {
	if got := renderPacman(37, 0, true, true); got != "37% C..........@" {
		t.Errorf("ASCII pacman = %q", got)
	}
	if got := contextText(37); got != "ctx 37%" {
		t.Errorf("context text = %q", got)
	}
}
