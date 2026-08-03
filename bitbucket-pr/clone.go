package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Shared clone plumbing for the repo picker (C on a repo row) and the
// Outline tab's clone shortcut (C on the "no local checkout" error).

// cloneDestFor is where a clone of slug would land: clone_dir, falling back
// to the first repo_roots entry ("" = cloning unconfigured).
func cloneDestFor(cfg Config, slug string) string {
	base := cfg.cloneDir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, slug)
}

// cloneBlocked returns the user-facing reason cloning r cannot start right
// now ("" = it can). The "already local" case is intentionally not covered:
// both call sites handle a known checkout themselves (associate, not clone).
func cloneBlocked(a *app, r *Repository) string {
	switch {
	case a.cloneInFlight[r.FullName]:
		return "clone実行中です: " + r.FullName
	case cloneDestFor(a.cfg, r.Slug) == "":
		return "clone先が未設定です。config.toml の repo_roots か clone_dir を設定してください"
	case r.CloneURL(a.cfg.CloneProtocol) == "":
		return "clone URL（" + a.cfg.CloneProtocol + "）を取得できません"
	}
	return ""
}

// startClone runs `git clone` off the main loop and folds the result into
// the checkout index. done runs on the main goroutine afterwards with the
// checkout path ("" on failure; the error is already in a.status). When the
// destination already holds a checkout (cloned by hand) it is associated
// without cloning.
func startClone(a *app, r Repository, done func(*app, string)) {
	if done == nil {
		done = func(*app, string) {}
	}
	ws := a.ctx.Workspace
	key := repoDirKey(ws, r.Slug)
	dest := cloneDestFor(a.cfg, r.Slug)

	if looksLikeCheckout(dest) {
		a.localDirs[key] = dest
		rememberRepoDir(ws, r.Slug, dest)
		a.status = "既存のcheckoutを関連付けました: " + shortenHome(dest)
		done(a, dest)
		return
	}
	if entries, err := os.ReadDir(dest); err == nil && len(entries) > 0 {
		a.status = "clone先が空ではありません: " + shortenHome(dest)
		done(a, "")
		return
	}

	cloneURL := r.CloneURL(a.cfg.CloneProtocol)
	protocol := a.cfg.CloneProtocol
	extraArgs := a.cfg.CloneArgs
	a.cloneInFlight[r.FullName] = true
	a.fetch("clone "+r.FullName, func() (func(*app), error) {
		args := append([]string{"clone"}, extraArgs...)
		args = append(args, "--", cloneURL, dest)
		cmd := exec.Command("git", args...)
		// Never hang on an interactive credential prompt — fail fast and
		// explain instead.
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		return func(a *app) {
			delete(a.cloneInFlight, r.FullName)
			if err != nil {
				msg := "cloneに失敗: " + lastOutputLine(out, err)
				if protocol == "https" {
					msg += "（httpsは git credential helper の設定が必要です。clone_protocol = \"ssh\" も検討してください）"
				}
				a.status = msg
				debugf("clone %s: %v: %s", r.FullName, err, out)
				done(a, "")
				return
			}
			a.localDirs[key] = dest
			rememberRepoDir(ws, r.Slug, dest)
			a.status = "cloneしました: " + shortenHome(dest)
			// The user has likely moved on during a long clone — toast too.
			a.herdr.notify("Bitbucket PR", "clone完了: "+r.FullName, "done")
			done(a, dest)
		}, nil
	})
}

// shortenHome renders dir with the home directory as ~ for display.
func shortenHome(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || dir == "" {
		return dir
	}
	if dir == home {
		return "~"
	}
	if strings.HasPrefix(dir, home+string(filepath.Separator)) {
		return "~" + dir[len(home):]
	}
	return dir
}

// lastOutputLine extracts the most useful line from a failed subprocess.
func lastOutputLine(out []byte, err error) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return err.Error()
}
