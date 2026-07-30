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
	// Auth: when empty, the environment variables the company bitbucket-cli
	// already uses (ATLASSIAN_USER_ID / ATLASSIAN_API_TOKEN) fill them in.
	Email    string `toml:"email"`
	APIToken string `toml:"api_token"`

	DefaultWorkspace string `toml:"default_workspace"` // fallback: $BITBUCKET_WORKSPACE
	DefaultRepo      string `toml:"default_repo"`
	DefaultState     string `toml:"default_state"`  // initial PR list filter
	Placement        string `toml:"placement"`      // tab | split | zoomed | overlay
	ListTabTitle     string `toml:"list_tab_title"` // viewer tab label: {repo} {workspace}
	ShowComments     bool   `toml:"show_comments"`  // inline comments in the diff view
	ContextFold      bool   `toml:"context_fold"`   // hunks start folded
	HTTPTimeoutSec   int    `toml:"http_timeout_sec"`

	// External diff tool (Enter on a file / D key). {patch} in the argv is
	// replaced with the patch file path; without the placeholder the patch
	// is piped to stdin instead (delta-style tools).
	DiffTool          []string `toml:"diff_tool"`
	DiffTabTitle      string   `toml:"diff_tab_title"`     // {id} {title} {repo}
	FilesEnter        string   `toml:"files_enter"`        // difftool | builtin
	DifftoolPlacement string   `toml:"difftool_placement"` // popup | overlay | tab | split
	DifftoolWidth     string   `toml:"difftool_width"`     // popup only: cells or "95%"
	DifftoolHeight    string   `toml:"difftool_height"`
	MarkdownViewer    []string `toml:"markdown_viewer"` // m key; {file} = md file
	CommentEditor     []string `toml:"comment_editor"`  // C key; empty = $EDITOR → nvim

	MDHeadingFg     string `toml:"md_heading_fg"` // in-tab markdown headings
	MDCodeFg        string `toml:"md_code_fg"`    // in-tab markdown code
	AddFg           string `toml:"add_fg"`
	DelFg           string `toml:"del_fg"`
	HunkFg          string `toml:"hunk_fg"`
	CommentFg       string `toml:"comment_fg"`
	CommentBorderFg string `toml:"comment_border_fg"`
	OutdatedFg      string `toml:"outdated_fg"`

	// loadErr carries a config parse problem so the UI can surface it once
	// instead of dying.
	loadErr string
}

func defaultConfig() Config {
	return Config{
		DefaultState:      "OPEN",
		Placement:         "tab",
		ListTabTitle:      "PRs {repo}",
		ShowComments:      true,
		ContextFold:       false,
		HTTPTimeoutSec:    20,
		DiffTool:          []string{"hunk", "patch", "{patch}"},
		DiffTabTitle:      "PR #{id}",
		FilesEnter:        "difftool",
		DifftoolPlacement: "popup",
		DifftoolWidth:     "95%",
		DifftoolHeight:    "95%",
		MarkdownViewer:    []string{"glow", "-p", "{file}"},
		MDHeadingFg:       "#c792ea",
		MDCodeFg:          "#e5c07b",
		AddFg:             "green",
		DelFg:             "red",
		HunkFg:            "cyan",
		CommentFg:         "white",
		CommentBorderFg:   "#8be9fd",
		OutdatedFg:        "yellow",
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
	switch cfg.Placement {
	case "tab", "split", "zoomed", "overlay":
	default:
		cfg.Placement = defaultConfig().Placement
	}
	switch cfg.DefaultState {
	case "OPEN", "MERGED", "DECLINED", "SUPERSEDED":
	default:
		cfg.DefaultState = defaultConfig().DefaultState
	}
	if cfg.HTTPTimeoutSec <= 0 {
		cfg.HTTPTimeoutSec = defaultConfig().HTTPTimeoutSec
	}
	if len(cfg.DiffTool) == 0 {
		cfg.DiffTool = defaultConfig().DiffTool
	}
	if cfg.DiffTabTitle == "" {
		cfg.DiffTabTitle = defaultConfig().DiffTabTitle
	}
	if cfg.FilesEnter != "builtin" {
		cfg.FilesEnter = "difftool"
	}
	switch cfg.DifftoolPlacement {
	case "popup", "overlay", "tab", "split":
	default:
		cfg.DifftoolPlacement = defaultConfig().DifftoolPlacement
	}
	if cfg.ListTabTitle == "" {
		cfg.ListTabTitle = defaultConfig().ListTabTitle
	}
	if len(cfg.MarkdownViewer) == 0 {
		cfg.MarkdownViewer = defaultConfig().MarkdownViewer
	}
	if cfg.MDHeadingFg == "" {
		cfg.MDHeadingFg = defaultConfig().MDHeadingFg
	}
	if cfg.MDCodeFg == "" {
		cfg.MDCodeFg = defaultConfig().MDCodeFg
	}
	return cfg
}

// credentials resolves the Bitbucket auth pair: config wins, then the
// environment the company bitbucket-cli already uses. ok is false when
// either half is missing.
func (c Config) credentials() (email, token string, ok bool) {
	email, token = c.Email, c.APIToken
	if email == "" {
		email = os.Getenv("ATLASSIAN_USER_ID")
	}
	if token == "" {
		token = os.Getenv("ATLASSIAN_API_TOKEN")
	}
	return email, token, email != "" && token != ""
}

// workspaceFallback resolves the default workspace: config, then the
// environment variable the company bitbucket-cli uses.
func (c Config) workspaceFallback() string {
	if c.DefaultWorkspace != "" {
		return c.DefaultWorkspace
	}
	return os.Getenv("BITBUCKET_WORKSPACE")
}
