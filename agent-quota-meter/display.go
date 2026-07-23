package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	idleGrace                = 5 * time.Minute
	maxQuotaSlots            = 5
	maxTokenPatchesPerReport = 16
	pacmanCells              = 10
)

var styleLevels = []string{"normal", "caution", "warning", "danger"}

type renderedQuotaWindow struct {
	Slot      int
	Level     string
	Value     string
	Remaining float64
	Label     string
}

type quotaDisplay struct {
	Legacy string
	Slots  []renderedQuotaWindow
}

func asciiMode(stateDir string) bool {
	return os.Getenv("AGENT_QUOTA_ASCII") == "1" ||
		fileExists(filepath.Join(stateDir, "ascii"))
}

func renderPacman(percent float64, frame int, animate bool, ascii bool) string {
	mouthOpen, mouthClosed, dot, ghost := "ᗧ", "●", "·", "ᗣ"
	if ascii {
		mouthOpen, mouthClosed, dot, ghost = "C", "O", ".", "@"
	}
	position := 0
	pacman := mouthOpen
	if animate {
		position = frame % (pacmanCells + 1)
		if frame%2 != 0 {
			pacman = mouthClosed
		}
	}
	shown := clamp(percent, 0, 999)
	return fmt.Sprintf("%.0f%% %s%s%s%s",
		shown,
		strings.Repeat(" ", position),
		pacman,
		strings.Repeat(dot, pacmanCells-position),
		ghost,
	)
}

func contextText(percent float64) string {
	return fmt.Sprintf("ctx %.0f%%", clamp(percent, 0, 999))
}

func contextStyleLevel(percent float64) string {
	switch {
	case percent < 60:
		return "normal"
	case percent < 80:
		return "caution"
	case percent < 90:
		return "warning"
	default:
		return "danger"
	}
}

func quotaStyleLevel(remaining float64) string {
	switch {
	case remaining >= 60:
		return "normal"
	case remaining >= 30:
		return "caution"
	case remaining >= 15:
		return "warning"
	default:
		return "danger"
	}
}

func styledTokenArgs(prefix, activeLevel, value string) []string {
	var args []string
	for _, level := range styleLevels {
		token := prefix + "_" + level
		if level == activeLevel && value != "" {
			args = append(args, "--token", token+"="+value)
		} else {
			args = append(args, "--clear-token", token)
		}
	}
	return args
}

func allDisplayTokens() []string {
	tokens := []string{"context", "quota"}
	for _, level := range styleLevels {
		tokens = append(tokens, "context_"+level)
	}
	for slot := 1; slot <= maxQuotaSlots; slot++ {
		for _, level := range styleLevels {
			tokens = append(tokens, fmt.Sprintf("quota_%d_%s", slot, level))
		}
	}
	return tokens
}

func clearDisplayTokenArgs() []string {
	var args []string
	for _, token := range allDisplayTokens() {
		args = append(args, "--clear-token", token)
	}
	return args
}

func displayMode(status string) string {
	switch status {
	case "working":
		return "working"
	case "done", "blocked":
		return "static"
	default:
		return ""
	}
}

func nextDisplayState(previousStatus, status string, graceDeadline, now time.Time) (string, time.Time) {
	if status == "idle" {
		if previousStatus == "working" || previousStatus == "done" {
			graceDeadline = now.Add(idleGrace)
		}
		if !graceDeadline.IsZero() && now.Before(graceDeadline) {
			return "static", graceDeadline
		}
		return "", time.Time{}
	}
	return displayMode(status), time.Time{}
}

func remainingTTL(deadline, now time.Time) string {
	milliseconds := deadline.Sub(now).Milliseconds()
	if milliseconds < 1 {
		milliseconds = 1
	}
	return fmt.Sprintf("%d", milliseconds)
}

func renderQuotaWindows(windows []quotaWindow) *quotaDisplay {
	var rendered []renderedQuotaWindow
	for _, window := range windows {
		if window.UsedPercent == nil {
			continue
		}
		remaining := clamp(100-*window.UsedPercent, 0, 100)
		label := window.Label
		if label == "" {
			label = "?"
		}
		rendered = append(rendered, renderedQuotaWindow{
			Label:     label,
			Remaining: remaining,
			Level:     quotaStyleLevel(remaining),
		})
	}
	if len(rendered) == 0 {
		return nil
	}
	legacyParts := make([]string, 0, len(rendered))
	for _, window := range rendered {
		legacyParts = append(legacyParts, fmt.Sprintf("%s %.0f%%", window.Label, window.Remaining))
	}
	display := &quotaDisplay{Legacy: strings.Join(legacyParts, ", ") + " left"}
	count := min(len(rendered), maxQuotaSlots)
	for index := range count {
		window := rendered[index]
		window.Slot = index + 1
		window.Value = fmt.Sprintf("%s: %.0f%%", window.Label, window.Remaining)
		if index == count-1 {
			window.Value += " left"
		}
		display.Slots = append(display.Slots, window)
	}
	return display
}

func quotaTokenArgs(display *quotaDisplay) []string {
	args := []string{"--token", "quota=" + display.Legacy}
	bySlot := make(map[int]renderedQuotaWindow, len(display.Slots))
	for _, slot := range display.Slots {
		bySlot[slot.Slot] = slot
	}
	for index := 1; index <= maxQuotaSlots; index++ {
		slot, ok := bySlot[index]
		if ok {
			args = append(args, styledTokenArgs(fmt.Sprintf("quota_%d", index), slot.Level, slot.Value)...)
		} else {
			args = append(args, styledTokenArgs(fmt.Sprintf("quota_%d", index), "", "")...)
		}
	}
	return args
}

func splitReportArgs(args []string) ([][]string, error) {
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("report args must be option/value pairs")
	}
	var patches [][]string
	var common []string
	for index := 0; index < len(args); index += 2 {
		pair := args[index : index+2]
		if pair[0] == "--token" || pair[0] == "--clear-token" {
			patches = append(patches, append([]string(nil), pair...))
		} else {
			common = append(common, pair...)
		}
	}
	if len(patches) == 0 {
		if len(common) == 0 {
			return nil, nil
		}
		return [][]string{common}, nil
	}
	var chunks [][]string
	for start := 0; start < len(patches); start += maxTokenPatchesPerReport {
		end := min(start+maxTokenPatchesPerReport, len(patches))
		var chunk []string
		for _, patch := range patches[start:end] {
			chunk = append(chunk, patch...)
		}
		chunk = append(chunk, common...)
		chunks = append(chunks, chunk)
	}
	return chunks, nil
}

func clamp(value, minimum, maximum float64) float64 {
	return math.Min(math.Max(value, minimum), maximum)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
