package main

import (
	"os"
	"path/filepath"
	"strings"

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

	DefaultWorkspace string            `toml:"default_workspace"` // fallback: $BITBUCKET_WORKSPACE
	DefaultRepo      string            `toml:"default_repo"`
	DefaultState     string            `toml:"default_state"` // initial PR list filter

	// Repo picker: where existing checkouts live (scanned to mark repos as
	// local) and how the picker clones missing ones. clone_dir falls back to
	// the first repo_roots entry.
	RepoRoots     []string `toml:"repo_roots"`
	CloneDir      string   `toml:"clone_dir"`
	CloneProtocol string   `toml:"clone_protocol"` // ssh | https
	CloneArgs     []string `toml:"clone_args"`     // extra git clone flags, e.g. ["--filter=blob:none"]

	// ReviewScanRepos bounds the picker's Review view: Bitbucket has no
	// cross-repo "PRs I review" endpoint, so the N most recently updated
	// repositories are queried individually.
	ReviewScanRepos int `toml:"review_scan_repos"`

	Placement        string            `toml:"placement"`        // tab | split | zoomed | overlay
	ListTabTitle     string            `toml:"list_tab_title"`   // viewer tab label: {repo} {workspace}
	ShowComments     bool              `toml:"show_comments"`    // inline comments in the diff view
	ShowAvatars      bool              `toml:"show_avatars"`     // account avatars via Herdr's graphics layer
	AvatarOverrides  map[string]string `toml:"avatar_overrides"` // Atlassian account_id -> local image
	ContextFold      bool              `toml:"context_fold"`     // hunks start folded
	HTTPTimeoutSec   int               `toml:"http_timeout_sec"`

	// External diff tool (Enter on a file / D key). {patch} in the argv is
	// replaced with the patch file path; without the placeholder the patch
	// is piped to stdin instead (delta-style tools). {ctx} becomes the hunk
	// agent-context JSON (existing PR comments as inline annotations).
	DiffTool          []string `toml:"diff_tool"`
	DiffTabTitle      string   `toml:"diff_tab_title"`     // {id} {title} {repo}
	FilesEnter        string   `toml:"files_enter"`        // difftool | builtin
	DifftoolPlacement string   `toml:"difftool_placement"` // popup | overlay | tab | split
	DifftoolWidth     string   `toml:"difftool_width"`     // popup only: cells or "95%"
	DifftoolHeight    string   `toml:"difftool_height"`
	CommentEditor     []string `toml:"comment_editor"` // c key; empty = $EDITOR → nvim

	MDHeadingFg string `toml:"md_heading_fg"` // in-tab markdown headings
	MDCodeFg    string `toml:"md_code_fg"`    // in-tab markdown code
	MDCodeBg    string `toml:"md_code_bg"`    // code-block / inline-code background
	CommentBg   string `toml:"comment_bg"`    // comment-area background
	FocusBg     string `toml:"focus_bg"`      // focused thread / selected lines background
	AddFg       string `toml:"add_fg"`
	DelFg       string `toml:"del_fg"`
	HunkFg      string `toml:"hunk_fg"`
	CommentFg   string `toml:"comment_fg"`
	OutdatedFg  string `toml:"outdated_fg"`

	// loadErr carries a config parse problem so the UI can surface it once
	// instead of dying.
	loadErr string
}

func defaultConfig() Config {
	return Config{
		DefaultState:      "OPEN",
		CloneProtocol:     "ssh",
		ReviewScanRepos:   30,
		Placement:         "tab",
		ListTabTitle:      "PRs {repo}",
		ShowComments:      true,
		ShowAvatars:       true,
		ContextFold:       false,
		HTTPTimeoutSec:    20,
		DiffTool:          []string{"hunk", "patch", "{patch}", "--agent-context", "{ctx}", "--agent-notes"},
		DiffTabTitle:      "PR #{id}",
		FilesEnter:        "difftool",
		DifftoolPlacement: "popup",
		DifftoolWidth:     "95%",
		DifftoolHeight:    "95%",
		MDHeadingFg:       "#c792ea",
		MDCodeFg:          "#e5c07b",
		MDCodeBg:          "#161821",
		CommentBg:         "#20222e",
		FocusBg:           "#2e3350",
		AddFg:             "green",
		DelFg:             "red",
		HunkFg:            "cyan",
		CommentFg:         "white",
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
	cfg.AvatarOverrides = normalizeAvatarOverridePaths(cfg.AvatarOverrides, dir)
	cfg.RepoRoots = normalizeDirPaths(cfg.RepoRoots)
	if roots := normalizeDirPaths([]string{cfg.CloneDir}); len(roots) > 0 {
		cfg.CloneDir = roots[0]
	} else {
		cfg.CloneDir = ""
	}
	if cfg.CloneProtocol != "https" {
		cfg.CloneProtocol = defaultConfig().CloneProtocol
	}
	if cfg.ReviewScanRepos < 1 || cfg.ReviewScanRepos > 100 {
		cfg.ReviewScanRepos = defaultConfig().ReviewScanRepos
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
	if cfg.MDHeadingFg == "" {
		cfg.MDHeadingFg = defaultConfig().MDHeadingFg
	}
	if cfg.MDCodeFg == "" {
		cfg.MDCodeFg = defaultConfig().MDCodeFg
	}
	if cfg.MDCodeBg == "" {
		cfg.MDCodeBg = defaultConfig().MDCodeBg
	}
	if cfg.CommentBg == "" {
		cfg.CommentBg = defaultConfig().CommentBg
	}
	if cfg.FocusBg == "" {
		cfg.FocusBg = defaultConfig().FocusBg
	}
	return cfg
}

// normalizeAvatarOverridePaths turns config-relative paths into stable
// absolute paths. Empty account IDs and paths are ignored so a typo cannot
// suppress the Bitbucket-provided fallback image.
func normalizeAvatarOverridePaths(overrides map[string]string, configDir string) map[string]string {
	if len(overrides) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(overrides))
	for accountID, path := range overrides {
		accountID = strings.TrimSpace(accountID)
		path = strings.TrimSpace(path)
		if accountID == "" || path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(configDir, path)
		}
		normalized[accountID] = filepath.Clean(path)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// normalizeDirPaths expands ~ and cleans each directory path, dropping
// blanks. Relative paths are kept relative to the user's home (a plugin
// process cwd is the plugin directory, which is never what a user means).
func normalizeDirPaths(paths []string) []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "~" {
			p = home
		} else if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		} else if !filepath.IsAbs(p) {
			p = filepath.Join(home, p)
		}
		out = append(out, filepath.Clean(p))
	}
	return out
}

// cloneDir resolves where the picker clones into: clone_dir, falling back
// to the first repo_roots entry. "" means cloning is not configured.
func (c Config) cloneDir() string {
	if c.CloneDir != "" {
		return c.CloneDir
	}
	if len(c.RepoRoots) > 0 {
		return c.RepoRoots[0]
	}
	return ""
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
