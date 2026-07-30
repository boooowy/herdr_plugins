package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// External diff tool integration (hunk by default, any tool via config):
// Enter on a file (or D for the whole PR) writes the PR's raw diff to a
// patch file — the selected file's section moved to the front so it shows
// first — and opens it with `diff_tool` in a dedicated herdr tab. The tab
// is reused per PR; tools watching the patch file (hunk's --watch) reload
// on rewrite.
const (
	envDiffFile  = "BBPR_DIFF_FILE"
	envHunkTitle = "BBPR_DIFF_TITLE"
)

// paneRef remembers the tab a PR's diff tool runs in, and which file the
// patch was reordered for. Diff tools read the patch once at startup
// (hunk's --watch only reloads in working-tree mode), so a different focus
// file needs a fresh pane; the same focus just refocuses the tab.
type paneRef struct {
	PaneID string
	TabID  string
	Focus  string
}

// openInDiffTool shows the PR diff in the external diff tool. focusPath
// ("" = whole PR in natural order) selects the file to display first.
func openInDiffTool(a *app, prID int, focusPath string) {
	debugf("difftool: request pr=%d focus=%q", prID, focusPath)
	d := a.detailFor(prID)
	if d.diffText == "" {
		// Diff still downloading: queue the request; the fetch completion
		// in detailView.load fires it.
		a.pendingDifftool = &difftoolRequest{prID: prID, focus: focusPath}
		a.status = "diff を読み込み中 — 完了後に diff ツールを開きます"
		return
	}
	dir := os.Getenv("HERDR_PLUGIN_STATE_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("pr-%d.patch", prID))
	if err := os.WriteFile(path, []byte(reorderPatch(d.diffText, focusPath)), 0o600); err != nil {
		a.status = "patch ファイルを書けません: " + err.Error()
		return
	}

	env := map[string]string{
		envDiffFile:  path,
		envHunkTitle: diffTabTitle(a.cfg.DiffTabTitle, prID, d.pr, a.ctx.Repo),
	}

	placement := a.cfg.DifftoolPlacement
	if placement != "tab" {
		// popup/overlay/split are one-shot: they close with the tool (popup
		// isn't even a pane), so there is nothing to reuse or rename.
		width, height := "", ""
		if placement == "popup" {
			width, height = a.cfg.DifftoolWidth, a.cfg.DifftoolHeight
		}
		if _, err := a.herdr.pluginPaneOpen(pluginID, "difftool", placement, true, env, width, height); err != nil {
			a.status = "diff ツールを開けません: " + err.Error()
			return
		}
		debugf("difftool: opened %s for pr %d", placement, prID)
		return
	}

	// Tab placement: reuse the PR's existing diff tab when it already shows
	// this focus file; otherwise close it and start fresh so the tool
	// re-reads the reordered patch.
	if ref, ok := a.difftoolPanes[prID]; ok {
		if _, err := a.herdr.paneByID(ref.PaneID); err == nil {
			if ref.Focus == focusPath {
				if err := a.herdr.pluginPaneFocus(ref.PaneID); err == nil {
					debugf("difftool: focused existing pane %s for pr %d", ref.PaneID, prID)
					return
				}
			} else {
				if err := a.herdr.pluginPaneClose(ref.PaneID); err != nil {
					debugf("difftool: close stale pane %s: %v", ref.PaneID, err)
				}
			}
		}
		delete(a.difftoolPanes, prID)
	}

	pane, err := a.herdr.pluginPaneOpen(pluginID, "difftool", "tab", true, env, "", "")
	if err != nil {
		a.status = "diff ツールのペインを開けません: " + err.Error()
		return
	}
	a.difftoolPanes[prID] = paneRef{PaneID: pane.PaneID, TabID: pane.TabID, Focus: focusPath}
	title := diffTabTitle(a.cfg.DiffTabTitle, prID, d.pr, a.ctx.Repo)
	if err := a.herdr.tabRename(pane.TabID, title); err != nil {
		debugf("difftool: tab rename: %v", err)
	}
	debugf("difftool: opened %s (tab %s, %q) for pr %d", pane.PaneID, pane.TabID, title, prID)
}

// diffTabTitle renders the tab title template ({id} {title} {repo}).
func diffTabTitle(tmpl string, prID int, pr *PullRequest, repo string) string {
	title := ""
	if pr != nil {
		title = pr.Title
	}
	s := strings.NewReplacer(
		"{id}", strconv.Itoa(prID),
		"{title}", title,
		"{repo}", repo,
	).Replace(tmpl)
	return truncateWidth(s, 40)
}

// reorderPatch moves focusPath's file section to the front of a git-style
// unified diff so diff tools show it first. Unknown paths (or "") return
// the text unchanged.
func reorderPatch(text, focusPath string) string {
	if focusPath == "" {
		return text
	}
	const marker = "diff --git "
	var sections []string
	rest := text
	for {
		idx := strings.Index(rest, "\n"+marker)
		if idx < 0 {
			sections = append(sections, rest)
			break
		}
		sections = append(sections, rest[:idx+1])
		rest = rest[idx+1:]
	}
	for i, sec := range sections {
		if !strings.HasPrefix(sec, marker) {
			continue
		}
		files := ParseUnifiedDiff(sec)
		if len(files) == 1 && (files[0].Path() == focusPath || files[0].OldPath == focusPath) {
			if i == 0 {
				return text
			}
			reordered := make([]string, 0, len(sections))
			reordered = append(reordered, sec)
			reordered = append(reordered, sections[:i]...)
			reordered = append(reordered, sections[i+1:]...)
			return strings.Join(reordered, "")
		}
	}
	return text
}

// diffToolArgv expands the configured argv: {patch} is replaced with the
// patch path; useStdin reports that no placeholder was present, so the
// caller should pipe the patch file to stdin instead.
func diffToolArgv(argv []string, patchPath string) (out []string, useStdin bool) {
	out = make([]string, len(argv))
	useStdin = true
	for i, a := range argv {
		if strings.Contains(a, "{patch}") {
			useStdin = false
		}
		out[i] = strings.ReplaceAll(a, "{patch}", patchPath)
	}
	return out, useStdin
}

// runDiffToolUI is the difftool pane's entrypoint: run the configured tool
// on the patch file, inheriting the pane PTY.
func runDiffToolUI() {
	patch := os.Getenv(envDiffFile)
	if patch == "" {
		errExit("missing " + envDiffFile + "; launch me via the viewer")
	}
	if _, err := os.Stat(patch); err != nil {
		errExit("patch file missing:", err)
	}
	cfg := loadConfig()
	argv, useStdin := diffToolArgv(cfg.DiffTool, patch)
	if len(argv) == 0 {
		errExit("diff_tool is empty")
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		showDiffToolInstallHelp(cfg, argv[0])
		return
	}
	if title := os.Getenv(envHunkTitle); title != "" {
		fmt.Printf("\x1b]0;%s\x07", title)
	}
	debugf("difftool: run %v (stdin=%v)", argv, useStdin)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if useStdin {
		f, err := os.Open(patch)
		if err != nil {
			errExit("open patch:", err)
		}
		defer f.Close()
		cmd.Stdin = f
	} else {
		cmd.Stdin = os.Stdin
	}
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		errExit("run diff tool:", err)
	}
}

// showDiffToolInstallHelp renders setup instructions when the configured
// tool binary is missing, then waits for a key.
func showDiffToolInstallHelp(cfg Config, missing string) {
	w, h := termSize()
	out := bufio.NewWriter(os.Stdout)
	s := NewScreen(w, h)
	lines := []string{
		fmt.Sprintf("diff ツール %q が見つかりません。", missing),
		"",
		"デフォルトの hunk を使う場合:",
		"  brew install hunk        # または: npm i -g hunkdiff",
		"",
		"別のツールを使う場合は config.toml で指定できます（{patch} = patchファイル）:",
		`  diff_tool = ["delta", "{patch}"]`,
		`  diff_tool = ["nvim", "-c", "silent! e {patch}"]`,
	}
	s.WriteString(1, 0, "bb-pr — diff ツールが必要です", styleTitle, w)
	for i, l := range lines {
		s.WriteString(1, i+2, l, styleNone, w)
	}
	s.WriteString(1, h-1, " 任意のキーで終了", styleDim, w)
	s.Flush(out, sgrTable(cfg))
	out.Flush()
	if old, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
		defer term.Restore(int(os.Stdin.Fd()), old)
	}
	newKeyReader(os.Stdin).Read()
}
