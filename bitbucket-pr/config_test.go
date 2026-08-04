package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaultsWhenMissing(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", t.TempDir())
	cfg := loadConfig()
	if cfg.Placement != "tab" || cfg.DefaultState != "OPEN" || !cfg.ShowComments || !cfg.ShowAvatars {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.loadErr != "" {
		t.Errorf("missing file must not set loadErr, got %q", cfg.loadErr)
	}
	if cfg.DifftoolPlacement != "popup" || cfg.DifftoolWidth != "95%" || cfg.DifftoolHeight != "95%" {
		t.Errorf("difftool defaults: %+v", cfg)
	}
	if cfg.ListTabTitle != "PRs {repo}" {
		t.Errorf("list_tab_title default: %q", cfg.ListTabTitle)
	}
}

func TestLoadConfigBadValuesFallBack(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(
		"placement = \"floating\"\ndefault_state = \"open\"\nhttp_timeout_sec = -1\nshow_comments = false\nshow_avatars = false\ndifftool_placement = \"floating\"\n"), 0o644)
	cfg := loadConfig()
	if cfg.Placement != "tab" {
		t.Errorf("invalid placement should fall back to tab, got %q", cfg.Placement)
	}
	if cfg.DefaultState != "OPEN" {
		t.Errorf("invalid state should fall back to OPEN, got %q", cfg.DefaultState)
	}
	if cfg.HTTPTimeoutSec != 20 {
		t.Errorf("invalid timeout should fall back to 20, got %d", cfg.HTTPTimeoutSec)
	}
	if cfg.ShowComments {
		t.Error("show_comments=false must stick")
	}
	if cfg.ShowAvatars {
		t.Error("show_avatars=false must stick")
	}
	if cfg.DifftoolPlacement != "popup" {
		t.Errorf("invalid difftool_placement should fall back to popup, got %q", cfg.DifftoolPlacement)
	}
}

func TestLoadConfigNormalizesAvatarOverridePaths(t *testing.T) {
	dir := t.TempDir()
	absolute := filepath.Join(t.TempDir(), "absolute.png")
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	data := "[avatar_overrides]\n" +
		"\"account-relative\" = \"avatars/fukaya.png\"\n" +
		"\"account-absolute\" = \"" + absolute + "\"\n" +
		"\"account-empty\" = \"  \"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := loadConfig()
	if got := cfg.AvatarOverrides["account-relative"]; got != filepath.Join(dir, "avatars", "fukaya.png") {
		t.Errorf("relative override = %q", got)
	}
	if got := cfg.AvatarOverrides["account-absolute"]; got != absolute {
		t.Errorf("absolute override = %q", got)
	}
	if _, ok := cfg.AvatarOverrides["account-empty"]; ok {
		t.Error("empty override must be ignored")
	}
}

func TestLoadConfigParseErrorSurfaces(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", dir)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("placement = [broken"), 0o644)
	cfg := loadConfig()
	if cfg.loadErr == "" {
		t.Error("broken toml must set loadErr")
	}
	if cfg.Placement != "tab" {
		t.Errorf("broken toml must keep defaults, got placement %q", cfg.Placement)
	}
}

func TestCredentialsResolution(t *testing.T) {
	t.Setenv("ATLASSIAN_USER_ID", "env@example.com")
	t.Setenv("ATLASSIAN_API_TOKEN", "env-token")
	cfg := Config{}
	email, token, ok := cfg.credentials()
	if !ok || email != "env@example.com" || token != "env-token" {
		t.Errorf("env credentials: got %q/%q ok=%v", email, token, ok)
	}

	cfg = Config{Email: "cfg@example.com", APIToken: "cfg-token"}
	email, token, ok = cfg.credentials()
	if !ok || email != "cfg@example.com" || token != "cfg-token" {
		t.Errorf("config credentials must win: got %q/%q ok=%v", email, token, ok)
	}

	t.Setenv("ATLASSIAN_USER_ID", "")
	t.Setenv("ATLASSIAN_API_TOKEN", "")
	cfg = Config{}
	if _, _, ok = cfg.credentials(); ok {
		t.Error("missing credentials must report ok=false")
	}
}

func TestNormalizeDirPathsExpandsHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := normalizeDirPaths([]string{"~/src", " ", "/abs/path", "rel"})
	want := []string{filepath.Join(home, "src"), "/abs/path", filepath.Join(home, "rel")}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestCloneDirFallsBackToFirstRepoRoot(t *testing.T) {
	cfg := Config{RepoRoots: []string{"/roots/a", "/roots/b"}}
	if got := cfg.cloneDir(); got != "/roots/a" {
		t.Errorf("cloneDir = %q, want first repo root", got)
	}
	cfg.CloneDir = "/explicit"
	if got := cfg.cloneDir(); got != "/explicit" {
		t.Errorf("cloneDir = %q, want explicit clone_dir", got)
	}
	if got := (Config{}).cloneDir(); got != "" {
		t.Errorf("cloneDir = %q, want empty when unconfigured", got)
	}
}
