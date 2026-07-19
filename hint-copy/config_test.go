package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaultsWhenUnset(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
	cfg := loadConfig()
	if cfg.Alphabet == "" || cfg.MaxCandidates != 100 || cfg.HintBg != "yellow" {
		t.Errorf("bad defaults: %+v", cfg)
	}
	if cfg.ScrollbackLines != 5000 || cfg.SelBg != "blue" || cfg.SearchBg != "magenta" {
		t.Errorf("bad copy-mode defaults: %+v", cfg)
	}
	if !cfg.patternEnabled("url") || cfg.patternEnabled("ipv6") {
		t.Error("default pattern toggles wrong")
	}
}

func TestLoadConfigPartialFile(t *testing.T) {
	dir := t.TempDir()
	content := `
reverse = true
[patterns]
sha = false
ipv6 = true
[[custom_patterns]]
name = "jira"
regex = '[A-Z]+-\d+'
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	cfg := loadConfig()
	if !cfg.Reverse {
		t.Error("reverse not applied")
	}
	if cfg.Alphabet != defaultConfig().Alphabet || cfg.MaxCandidates != 100 {
		t.Error("unset fields lost their defaults")
	}
	if cfg.patternEnabled("sha") || !cfg.patternEnabled("ipv6") || !cfg.patternEnabled("url") {
		t.Error("pattern toggles wrong")
	}
	if len(cfg.CustomPatterns) != 1 || cfg.CustomPatterns[0].Name != "jira" {
		t.Errorf("custom patterns: %+v", cfg.CustomPatterns)
	}
}

func TestLoadConfigBrokenFileFallsBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("alphabet = ["), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	cfg := loadConfig()
	if cfg.loadErr == "" {
		t.Error("expected loadErr")
	}
	if cfg.Alphabet != defaultConfig().Alphabet {
		t.Error("broken file must fall back to defaults")
	}
}
