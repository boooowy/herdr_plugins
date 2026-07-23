package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const tailInitialBytes int64 = 256 * 1024

type tailState struct {
	Position int64
	Usage    *claudeUsage
	Model    string
	Used     float64
	Window   float64
}

type claudeUsage struct {
	InputTokens              float64 `json:"input_tokens"`
	CacheCreationInputTokens float64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     float64 `json:"cache_read_input_tokens"`
}

type claudePathEntry struct {
	Path      string
	CheckedAt time.Time
}

type sessionReader struct {
	home             string
	now              func() time.Time
	claudePaths      map[string]claudePathEntry
	claudeTails      map[string]*tailState
	codexIndexAt     time.Time
	codexSessionByCW map[string]string
	codexTails       map[string]*tailState
}

func newSessionReader(home string, now func() time.Time) *sessionReader {
	return &sessionReader{
		home:             home,
		now:              now,
		claudePaths:      make(map[string]claudePathEntry),
		claudeTails:      make(map[string]*tailState),
		codexSessionByCW: make(map[string]string),
		codexTails:       make(map[string]*tailState),
	}
}

func tailLines(path string, state *tailState) ([][]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size < state.Position {
		*state = tailState{}
	}
	if state.Position == 0 && size > tailInitialBytes {
		state.Position = size - tailInitialBytes
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err := file.Seek(state.Position, io.SeekStart); err != nil {
		return nil, err
	}
	chunk, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	state.Position = size
	return bytes.Split(chunk, []byte{'\n'}), nil
}

func (reader *sessionReader) claudePercent(sessionID string) (float64, bool) {
	now := reader.now()
	entry, found := reader.claudePaths[sessionID]
	if !found || (entry.Path == "" && now.Sub(entry.CheckedAt) >= time.Minute) {
		pattern := filepath.Join(reader.home, ".claude", "projects", "*", sessionID+".jsonl")
		matches, _ := filepath.Glob(pattern)
		sort.Slice(matches, func(i, j int) bool {
			left, leftErr := os.Stat(matches[i])
			right, rightErr := os.Stat(matches[j])
			if leftErr != nil {
				return false
			}
			if rightErr != nil {
				return true
			}
			return left.ModTime().After(right.ModTime())
		})
		path := ""
		if len(matches) > 0 {
			path = matches[0]
		}
		entry = claudePathEntry{Path: path, CheckedAt: now}
		reader.claudePaths[sessionID] = entry
	}
	if entry.Path == "" {
		return 0, false
	}
	state := reader.claudeTails[entry.Path]
	if state == nil {
		state = &tailState{}
		reader.claudeTails[entry.Path] = state
	}
	lines, err := tailLines(entry.Path, state)
	if err != nil {
		delete(reader.claudePaths, sessionID)
		delete(reader.claudeTails, entry.Path)
		return 0, false
	}
	for _, line := range lines {
		if !bytes.Contains(line, []byte(`"usage"`)) {
			continue
		}
		var record struct {
			Type        string `json:"type"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Usage claudeUsage `json:"usage"`
				Model string      `json:"model"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &record) != nil ||
			record.Type != "assistant" ||
			record.IsSidechain {
			continue
		}
		var raw struct {
			Message struct {
				Usage map[string]any `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &raw) != nil {
			continue
		}
		if _, ok := raw.Message.Usage["input_tokens"]; !ok {
			continue
		}
		usage := record.Message.Usage
		state.Usage = &usage
		state.Model = record.Message.Model
	}
	if state.Usage == nil {
		return 0, false
	}
	used := state.Usage.InputTokens +
		state.Usage.CacheCreationInputTokens +
		state.Usage.CacheReadInputTokens
	return used / claudeContextWindow(state.Model) * 100, true
}

func claudeContextWindow(model string) float64 {
	lower := strings.ToLower(model)
	if strings.Contains(lower, "[1m]") {
		return 1_000_000
	}
	for _, marker := range []string{
		"fable",
		"mythos",
		"sonnet-5",
		"opus-4-6",
		"opus-4-7",
		"opus-4-8",
		"sonnet-4-6",
	} {
		if strings.Contains(lower, marker) {
			return 1_000_000
		}
	}
	return 200_000
}

func (reader *sessionReader) codexSessionMap() map[string]string {
	now := reader.now()
	if !reader.codexIndexAt.IsZero() && now.Sub(reader.codexIndexAt) < time.Minute {
		return reader.codexSessionByCW
	}
	pattern := filepath.Join(reader.home, ".codex", "sessions", "*", "*", "*", "rollout-*.jsonl")
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
	if len(files) > 30 {
		files = files[:30]
	}
	mapping := make(map[string]string)
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		line, readErr := bufio.NewReader(file).ReadBytes('\n')
		_ = file.Close()
		if readErr != nil && readErr != io.EOF {
			continue
		}
		var record struct {
			Payload struct {
				CWD string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &record) == nil &&
			record.Payload.CWD != "" &&
			mapping[record.Payload.CWD] == "" {
			mapping[record.Payload.CWD] = path
		}
	}
	reader.codexIndexAt = now
	reader.codexSessionByCW = mapping
	return mapping
}

func (reader *sessionReader) codexPercent(cwd string) (float64, bool) {
	path := reader.codexSessionMap()[cwd]
	if path == "" {
		return 0, false
	}
	state := reader.codexTails[path]
	if state == nil {
		state = &tailState{}
		reader.codexTails[path] = state
	}
	lines, err := tailLines(path, state)
	if err != nil {
		delete(reader.codexTails, path)
		return 0, false
	}
	for _, line := range lines {
		if !bytes.Contains(line, []byte(`"token_count"`)) {
			continue
		}
		var record struct {
			Payload struct {
				Type string `json:"type"`
				Info struct {
					LastTokenUsage struct {
						TotalTokens float64 `json:"total_tokens"`
						InputTokens float64 `json:"input_tokens"`
					} `json:"last_token_usage"`
					ModelContextWindow float64 `json:"model_context_window"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &record) != nil || record.Payload.Type != "token_count" {
			continue
		}
		used := record.Payload.Info.LastTokenUsage.TotalTokens
		if used == 0 {
			used = record.Payload.Info.LastTokenUsage.InputTokens
		}
		if used != 0 {
			state.Used = used
		}
		if record.Payload.Info.ModelContextWindow != 0 {
			state.Window = record.Payload.Info.ModelContextWindow
		}
	}
	if state.Used == 0 || state.Window == 0 {
		return 0, false
	}
	return state.Used / state.Window * 100, true
}

func (reader *sessionReader) gc(agents []herdrAgent) {
	liveClaude := make(map[string]bool)
	for _, agent := range agents {
		if agent.Agent == "claude" {
			liveClaude[agent.AgentSession.Value] = true
		}
	}
	for sessionID, entry := range reader.claudePaths {
		if !liveClaude[sessionID] {
			delete(reader.claudePaths, sessionID)
			delete(reader.claudeTails, entry.Path)
		}
	}
	liveCodex := make(map[string]bool)
	for _, agent := range agents {
		if agent.Agent == "codex" {
			liveCodex[reader.codexSessionByCW[agent.CWD]] = true
		}
	}
	for path := range reader.codexTails {
		if !liveCodex[path] {
			delete(reader.codexTails, path)
		}
	}
}
