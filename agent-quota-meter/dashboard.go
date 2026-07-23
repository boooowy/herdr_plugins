package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

const dashboardBarWidth = 24

func (a *app) runDashboard() error {
	var oldState *term.State
	if term.IsTerminal(int(os.Stdin.Fd())) {
		state, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err == nil {
			oldState = state
			defer term.Restore(int(os.Stdin.Fd()), state)
		}
	}

	keys := make(chan byte, 1)
	go func() {
		defer close(keys)
		buffer := make([]byte, 1)
		for {
			if _, err := os.Stdin.Read(buffer); err != nil {
				return
			}
			keys <- buffer[0]
		}
	}()

	for {
		_ = a.updateQuota(true)
		fmt.Print("\033[H\033[2J")
		rendered := a.renderDashboard()
		if oldState != nil {
			rendered = strings.ReplaceAll(rendered, "\n", "\r\n")
		}
		fmt.Print(rendered)
		timer := time.NewTimer(30 * time.Second)
		select {
		case key, ok := <-keys:
			if !timer.Stop() {
				<-timer.C
			}
			if !ok {
				keys = nil
				continue
			}
			if key == 'q' || key == 'Q' {
				if oldState != nil {
					fmt.Print("\r\n")
				}
				return nil
			}
		case <-timer.C:
		}
	}
}

func (a *app) renderDashboard() string {
	var output strings.Builder
	output.WriteString("\n  Agent Quota Meter\n")
	output.WriteString("  " + strings.Repeat("=", 44) + "\n")
	for _, item := range []struct {
		kind  string
		title string
	}{
		{kind: "claude", title: "Claude Code"},
		{kind: "codex", title: "Codex"},
	} {
		output.WriteString("\n  " + item.title + "\n")
		state, err := readQuotaState(filepath.Join(a.stateDir, item.kind+".json"))
		if err != nil {
			output.WriteString("    (no data yet)\n")
			continue
		}
		if !state.OK {
			message := state.Error
			if message == "" {
				message = "unknown"
			}
			output.WriteString("    取得失敗: " + message + "\n")
			continue
		}
		for _, window := range state.Windows {
			if window.UsedPercent == nil {
				continue
			}
			output.WriteString(fmt.Sprintf(
				"    %-8s %s %5.1f%%  リセット: %s\n",
				window.Label,
				dashboardBar(*window.UsedPercent),
				*window.UsedPercent,
				formatReset(window.ResetsAt),
			))
		}
		asOf := state.AsOf
		if asOf == 0 {
			asOf = state.CollectedAt
		}
		if asOf != 0 {
			output.WriteString(fmt.Sprintf(
				"    (取得: %s)\n",
				time.Unix(asOf, 0).Local().Format("15:04:05"),
			))
		}
	}
	output.WriteString("\n  [q] 閉じる / 30秒ごとに自動更新\n")
	return output.String()
}

func dashboardBar(percent float64) string {
	filled := int(math.Round(percent / 100 * dashboardBarWidth))
	filled = max(0, min(dashboardBarWidth, filled))
	return strings.Repeat("■", filled) + strings.Repeat("□", dashboardBarWidth-filled)
}

func formatReset(value any) string {
	if value == nil {
		return "-"
	}
	if number, ok := numeric(value); ok {
		if number > 1e12 {
			number /= 1000
		}
		return time.Unix(int64(number), 0).Local().Format("01/02 15:04")
	}
	text := stringValue(value)
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05-07:00",
	} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.Local().Format("01/02 15:04")
		}
	}
	if number, err := strconv.ParseFloat(text, 64); err == nil {
		if number > 1e12 {
			number /= 1000
		}
		return time.Unix(int64(number), 0).Local().Format("01/02 15:04")
	}
	return text
}
