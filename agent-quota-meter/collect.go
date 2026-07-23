package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	updateDebounce       = 60 * time.Second
	claudeSuccessCache   = 5 * time.Minute
	claudeFailureBackoff = 10 * time.Minute
	claudeStaleRetention = 30 * time.Minute
)

var claudeLabels = map[string]string{
	"five_hour":        "5h",
	"seven_day":        "1w",
	"seven_day_opus":   "1w(op)",
	"seven_day_sonnet": "1w(so)",
	"thirty_day":       "1m",
	"monthly":          "1m",
}

func (a *app) updateQuota(force bool) error {
	if err := os.MkdirAll(a.stateDir, 0o755); err != nil {
		return err
	}
	stamp := filepath.Join(a.stateDir, "last_update")
	now := a.now()
	if !force {
		if info, err := os.Stat(stamp); err == nil && now.Sub(info.ModTime()) < updateDebounce {
			return nil
		}
	}
	if err := touch(stamp, now.Unix()); err != nil {
		return err
	}
	// Each collector records its own error state. A failure in one source must
	// not prevent the other source from updating.
	_ = a.collectCodex()
	_ = a.collectClaude()
	return nil
}

func (a *app) collectCodex() error {
	now := a.now().Unix()
	state := quotaState{
		Agent:       "codex",
		OK:          false,
		Windows:     []quotaWindow{},
		CollectedAt: now,
	}
	pattern := filepath.Join(a.home, ".codex", "sessions", "*", "*", "*", "*.jsonl")
	files, _ := filepath.Glob(pattern)
	sort.Slice(files, func(i, j int) bool {
		left, leftErr := os.Stat(files[i])
		right, rightErr := os.Stat(files[j])
		if leftErr != nil {
			return false
		}
		if rightErr != nil {
			return true
		}
		return left.ModTime().After(right.ModTime())
	})

	var rateLimits map[string]any
	for index, path := range files {
		if index >= 10 {
			break
		}
		found, err := lastRateLimits(path)
		if err != nil || found == nil {
			continue
		}
		rateLimits = found
		if info, err := os.Stat(path); err == nil {
			state.AsOf = info.ModTime().Unix()
		}
		break
	}

	if rateLimits == nil {
		state.Error = "no rate_limits snapshot found in ~/.codex/sessions"
	} else {
		for _, key := range []string{"primary", "secondary"} {
			rawWindow, ok := rateLimits[key].(map[string]any)
			if !ok {
				continue
			}
			used, ok := numeric(rawWindow["used_percent"])
			if !ok {
				continue
			}
			minutes, hasMinutes := numeric(rawWindow["window_minutes"])
			window := quotaWindow{
				Label:       windowLabel(minutes, hasMinutes),
				UsedPercent: floatPointer(used),
				ResetsAt:    rawWindow["resets_at"],
			}
			if hasMinutes {
				window.WindowMinutes = floatPointer(minutes)
			}
			state.Windows = append(state.Windows, window)
		}
		state.OK = len(state.Windows) > 0
		if !state.OK {
			state.Error = "rate_limits snapshot had no usable windows"
		}
	}
	return writeQuotaState(filepath.Join(a.stateDir, "codex.json"), state)
}

func lastRateLimits(path string) (map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var last map[string]any
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(`"rate_limits"`)) {
			continue
		}
		var value any
		if json.Unmarshal(line, &value) != nil {
			continue
		}
		if found := digRateLimits(value); found != nil {
			last = found
		}
	}
	return last, scanner.Err()
}

func digRateLimits(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		if found, ok := typed["rate_limits"].(map[string]any); ok {
			return found
		}
		for _, child := range typed {
			if found := digRateLimits(child); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := digRateLimits(child); found != nil {
				return found
			}
		}
	}
	return nil
}

func windowLabel(minutes float64, present bool) string {
	if !present {
		return "?"
	}
	switch {
	case minutes <= 330:
		return "5h"
	case minutes >= 9960 && minutes <= 10200:
		return "1w"
	case minutes >= 40000:
		return "1m"
	case minutes < 2880:
		return fmt.Sprintf("%dh", int(minutes/60))
	default:
		return fmt.Sprintf("%dd", int(minutes/1440))
	}
}

type claudeCredentials struct {
	ClaudeAIOAuth struct {
		AccessToken string  `json:"accessToken"`
		ExpiresAt   float64 `json:"expiresAt"`
	} `json:"claudeAiOauth"`
}

func (a *app) collectClaude() error {
	path := filepath.Join(a.stateDir, "claude.json")
	now := a.now()
	previous, previousErr := readQuotaState(path)
	hasPrevious := previousErr == nil
	if hasPrevious {
		age := now.Sub(time.Unix(previous.CollectedAt, 0))
		if previous.OK && age < claudeSuccessCache {
			return nil
		}
		attemptedAt := previous.AttemptedAt
		if attemptedAt == 0 {
			attemptedAt = previous.CollectedAt
		}
		if !previous.OK && now.Sub(time.Unix(attemptedAt, 0)) < claudeFailureBackoff {
			return nil
		}
	}

	failure := func(message string) error {
		if hasPrevious && previous.OK && now.Sub(time.Unix(previous.CollectedAt, 0)) < claudeStaleRetention {
			previous.LastError = message
			previous.AttemptedAt = now.Unix()
			return writeQuotaState(path, previous)
		}
		return writeQuotaState(path, quotaState{
			Agent:       "claude",
			OK:          false,
			Windows:     []quotaWindow{},
			CollectedAt: now.Unix(),
			AttemptedAt: now.Unix(),
			Error:       message,
		})
	}

	credentials, err := a.credentials()
	if err != nil {
		return failure("credentials unavailable: " + errorType(err))
	}
	token := credentials.ClaudeAIOAuth.AccessToken
	if token == "" {
		return failure("no accessToken in credentials")
	}
	if credentials.ClaudeAIOAuth.ExpiresAt > 0 &&
		credentials.ClaudeAIOAuth.ExpiresAt/1000 < float64(now.Unix()) {
		return failure("access token expired; open Claude Code once to refresh it")
	}

	endpoint := os.Getenv("AGENT_QUOTA_CLAUDE_USAGE_URL")
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/api/oauth/usage"
	}
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return failure("usage API request failed: " + errorType(err))
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("anthropic-beta", "oauth-2025-04-20")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "boooowy-agent-quota/0.3")
	response, err := a.httpClient.Do(request)
	if err != nil {
		return failure(fmt.Sprintf("usage API request failed: %s: %v", errorType(err), err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return failure(fmt.Sprintf("usage API HTTP %d", response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024))
	if err != nil {
		return failure(fmt.Sprintf("usage API request failed: %s: %v", errorType(err), err))
	}
	found, err := findClaudeWindows(json.RawMessage(body), "")
	if err != nil {
		return failure(fmt.Sprintf("usage API request failed: %s: %v", errorType(err), err))
	}
	if len(found) > 0 {
		allFractional := true
		for _, window := range found {
			if window.UsedPercent == nil || *window.UsedPercent > 1 {
				allFractional = false
				break
			}
		}
		if allFractional {
			for index := range found {
				value := *found[index].UsedPercent * 100
				found[index].UsedPercent = &value
			}
		}
	}
	state := quotaState{
		Agent:       "claude",
		OK:          len(found) > 0,
		Windows:     found,
		CollectedAt: now.Unix(),
		AttemptedAt: now.Unix(),
	}
	if !state.OK {
		state.Error = "usage API response had no utilization fields"
		var top map[string]json.RawMessage
		if json.Unmarshal(body, &top) == nil {
			for key := range top {
				state.ResponseKeys = append(state.ResponseKeys, key)
			}
			sort.Strings(state.ResponseKeys)
		}
	}
	return writeQuotaState(path, state)
}

func (a *app) loadClaudeCredentials() (claudeCredentials, error) {
	var credentials claudeCredentials
	var data []byte
	var err error
	if runtime.GOOS == "darwin" {
		data, err = exec.Command(
			"security", "find-generic-password",
			"-s", "Claude Code-credentials", "-w",
		).Output()
	} else {
		data, err = os.ReadFile(filepath.Join(a.home, ".claude", ".credentials.json"))
	}
	if err != nil {
		return credentials, err
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &credentials); err != nil {
		return credentials, err
	}
	return credentials, nil
}

func findClaudeWindows(raw json.RawMessage, keyHint string) ([]quotaWindow, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	switch trimmed[0] {
	case '{':
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		type entry struct {
			key string
			raw json.RawMessage
		}
		var entries []entry
		var utilization *float64
		var resetsAt any
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key := token.(string)
			var child json.RawMessage
			if err := decoder.Decode(&child); err != nil {
				return nil, err
			}
			entries = append(entries, entry{key: key, raw: child})
			if key == "utilization" {
				var value any
				jsonDecoder := json.NewDecoder(bytes.NewReader(child))
				jsonDecoder.UseNumber()
				if jsonDecoder.Decode(&value) == nil {
					if number, ok := numeric(value); ok {
						utilization = floatPointer(number)
					}
				}
			}
			if key == "resets_at" || key == "resetsAt" {
				_ = json.Unmarshal(child, &resetsAt)
			}
		}
		if utilization != nil {
			label := keyHint
			if mapped, ok := claudeLabels[label]; ok {
				label = mapped
			}
			if label == "" {
				label = "?"
			}
			return []quotaWindow{{
				Label:       label,
				UsedPercent: utilization,
				ResetsAt:    resetsAt,
			}}, nil
		}
		var windows []quotaWindow
		for _, item := range entries {
			children, err := findClaudeWindows(item.raw, item.key)
			if err != nil {
				return nil, err
			}
			windows = append(windows, children...)
		}
		return windows, nil
	case '[':
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		var windows []quotaWindow
		for decoder.More() {
			var child json.RawMessage
			if err := decoder.Decode(&child); err != nil {
				return nil, err
			}
			children, err := findClaudeWindows(child, keyHint)
			if err != nil {
				return nil, err
			}
			windows = append(windows, children...)
		}
		return windows, nil
	default:
		return nil, nil
	}
}

func errorType(err error) string {
	name := fmt.Sprintf("%T", err)
	name = strings.TrimPrefix(name, "*")
	if index := strings.LastIndex(name, "."); index >= 0 {
		name = name[index+1:]
	}
	return name
}
