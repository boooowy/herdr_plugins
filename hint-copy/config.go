package main

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the user configuration, read from
// $HERDR_PLUGIN_CONFIG_DIR/config.toml. Every field has a default; a missing
// file or a broken field never stops the plugin.
type Config struct {
	Alphabet      string          `toml:"alphabet"`
	Reverse       bool            `toml:"reverse"`
	OSC52         bool            `toml:"osc52"`
	MaxCandidates int             `toml:"max_candidates"`
	HintFg        string          `toml:"hint_fg"`
	HintBg        string          `toml:"hint_bg"`
	MatchFg       string          `toml:"match_fg"`
	Patterns      map[string]bool `toml:"patterns"`
	CustomPatterns []CustomPattern `toml:"custom_patterns"`

	// loadErr carries a config parse problem so the UI can surface it once
	// as a notification instead of dying.
	loadErr string
}

// CustomPattern is one user-supplied regex, e.g. a Jira ticket id. Group
// selects the capture group to copy (0 = whole match).
type CustomPattern struct {
	Name  string `toml:"name"`
	Regex string `toml:"regex"`
	Group int    `toml:"group"`
}

func defaultConfig() Config {
	return Config{
		Alphabet:      "asdfghjklqwertyuiopzxcvb",
		MaxCandidates: 100,
		HintFg:        "black",
		HintBg:        "yellow",
		MatchFg:       "green",
	}
}

// loadConfig reads the user config, falling back to defaults on any problem.
func loadConfig() Config {
	cfg := defaultConfig()
	dir := os.Getenv("HERDR_PLUGIN_CONFIG_DIR")
	if dir == "" {
		return cfg
	}
	path := filepath.Join(dir, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		bad := defaultConfig()
		bad.loadErr = "config.toml: " + err.Error()
		return bad
	}
	if cfg.Alphabet == "" {
		cfg.Alphabet = defaultConfig().Alphabet
	}
	if cfg.MaxCandidates <= 0 {
		cfg.MaxCandidates = defaultConfig().MaxCandidates
	}
	return cfg
}

// patternEnabled reports whether a built-in pattern should run. Unlisted
// patterns keep their default (everything on except ipv6).
func (c Config) patternEnabled(name string) bool {
	if v, ok := c.Patterns[name]; ok {
		return v
	}
	return name != "ipv6"
}
