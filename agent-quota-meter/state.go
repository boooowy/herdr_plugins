package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type quotaWindow struct {
	Label         string   `json:"label"`
	UsedPercent   *float64 `json:"used_percent,omitempty"`
	ResetsAt      any      `json:"resets_at,omitempty"`
	WindowMinutes *float64 `json:"window_minutes,omitempty"`
}

type quotaState struct {
	Agent        string        `json:"agent"`
	OK           bool          `json:"ok"`
	Windows      []quotaWindow `json:"windows"`
	CollectedAt  int64         `json:"collected_at"`
	AttemptedAt  int64         `json:"attempted_at,omitempty"`
	AsOf         int64         `json:"asof,omitempty"`
	Error        string        `json:"error,omitempty"`
	LastError    string        `json:"last_error,omitempty"`
	ResponseKeys []string      `json:"response_keys,omitempty"`
}

func floatPointer(value float64) *float64 {
	return &value
}

func readQuotaState(path string) (quotaState, error) {
	var state quotaState
	data, err := os.ReadFile(path)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	return state, nil
}

func writeQuotaState(path string, state quotaState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func touch(path string, nowUnix int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	when := unixTime(nowUnix)
	return os.Chtimes(path, when, when)
}

func unixTime(seconds int64) (resultTime time.Time) {
	return time.Unix(seconds, 0)
}

func numeric(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case json.Number:
		v, err := n.Float64()
		return v, err == nil
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
