package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// External diff tool integration (hunk by default, any tool via config):
// Enter on a file (or D for the whole PR) writes the PR's raw diff to a
// patch file — the selected file's section moved to the front so it shows
// first — and opens it with `diff_tool` in a dedicated herdr tab. The tab
// is reused per PR; tools watching the patch file (hunk's --watch) reload
// on rewrite.
const (
	envDiffFile   = "BITBUCKET_PR_DIFF_FILE"
	envHunkTitle  = "BITBUCKET_PR_DIFF_TITLE"
	envDiffCtx    = "BITBUCKET_PR_DIFF_CTX"    // hunk --agent-context sidecar path
	envDiffMarker = "BITBUCKET_PR_DIFF_MARKER" // touched after draft notes are posted
	envMemoImport = "BITBUCKET_PR_MEMO_IMPORT" // notes JSON written when m picks memo import
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
	t0 := time.Now()
	path := filepath.Join(stateDir(), fmt.Sprintf("pr-%d.patch", prID))
	patch := reorderPatch(d.diffText, focusPath)
	t1 := time.Now()
	if err := os.WriteFile(path, []byte(patch), 0o600); err != nil {
		a.status = "patch ファイルを書けません: " + err.Error()
		return
	}
	t2 := time.Now()
	// Existing inline comments ride along as hunk annotations ({ctx} in
	// diff_tool), and the same file fixes the display order (see
	// buildAgentContext); the marker file lets the pane signal posted draft
	// notes back so the viewer refreshes its comments.
	ctxPath := filepath.Join(stateDir(), fmt.Sprintf("pr-%d-ctx.json", prID))
	if err := os.WriteFile(ctxPath, buildAgentContext(d.comments, d.files, focusPath, time.Now()), 0o600); err != nil {
		debugf("difftool: agent context: %v", err)
		ctxPath = ""
	}
	debugf("difftool: prep reorder=%s patch=%s ctx=%s (%d files, %d bytes)",
		t1.Sub(t0), t2.Sub(t1), time.Since(t2), len(d.files), len(patch))
	marker := filepath.Join(stateDir(), fmt.Sprintf("dtpost-%d-%d", prID, time.Now().UnixNano()))
	memoImport := filepath.Join(stateDir(), fmt.Sprintf("dtmemo-%d-%d.json", prID, time.Now().UnixNano()))

	env := map[string]string{
		envDiffFile:   path,
		envHunkTitle:  diffTabTitle(a.cfg.DiffTabTitle, prID, d.pr, a.ctx.Repo),
		envDiffCtx:    ctxPath,
		envDiffMarker: marker,
		envMemoImport: memoImport,
		envWorkspace:  a.ctx.Workspace,
		envRepo:       a.ctx.Repo,
		envPRID:       strconv.Itoa(prID),
	}

	placement := a.cfg.DifftoolPlacement
	if placement != "tab" {
		// popup/overlay/split are one-shot: they close with the tool (popup
		// isn't even a pane), so there is nothing to reuse or rename.
		width, height := "", ""
		if placement == "popup" {
			width, height = a.cfg.DifftoolWidth, a.cfg.DifftoolHeight
		}
		open := time.Now()
		if _, err := a.herdr.pluginPaneOpen(pluginID, "difftool", placement, true, env, width, height); err != nil {
			a.status = "diff ツールを開けません: " + err.Error()
			return
		}
		debugf("difftool: opened %s for pr %d (pane.open=%s, total=%s)",
			placement, prID, time.Since(open), time.Since(t0))
		go watchPostMarker(a, prID, marker)
		go watchMemoImport(a, prID, memoImport)
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

	open := time.Now()
	pane, err := a.herdr.pluginPaneOpen(pluginID, "difftool", "tab", true, env, "", "")
	if err != nil {
		a.status = "diff ツールのペインを開けません: " + err.Error()
		return
	}
	debugf("difftool: pane.open=%s (total=%s)", time.Since(open), time.Since(t0))
	a.difftoolPanes[prID] = paneRef{PaneID: pane.PaneID, TabID: pane.TabID, Focus: focusPath}
	go watchPostMarker(a, prID, marker)
	go watchMemoImport(a, prID, memoImport)
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

// expandArgv replaces placeholder with value across argv; found reports
// whether any element contained it (callers pipe the file to stdin or
// append it as an argument when absent).
func expandArgv(argv []string, placeholder, value string) (out []string, found bool) {
	out = make([]string, len(argv))
	for i, a := range argv {
		if strings.Contains(a, placeholder) {
			found = true
		}
		out[i] = strings.ReplaceAll(a, placeholder, value)
	}
	return out, found
}

// diffToolArgv expands the configured argv: {patch} is replaced with the
// patch path; useStdin reports that no placeholder was present, so the
// caller should pipe the patch file to stdin instead.
func diffToolArgv(argv []string, patchPath string) (out []string, useStdin bool) {
	out, found := expandArgv(argv, "{patch}", patchPath)
	return out, !found
}

// expandCtxArg fills {ctx} with the agent-context sidecar path. Without a
// path, the {ctx} argument is dropped along with a preceding flag (so the
// default ["--agent-context", "{ctx}"] pair disappears cleanly).
func expandCtxArg(argv []string, ctxPath string) []string {
	if ctxPath != "" {
		out, _ := expandArgv(argv, "{ctx}", ctxPath)
		return out
	}
	var out []string
	for _, a := range argv {
		if strings.Contains(a, "{ctx}") {
			if len(out) > 0 && strings.HasPrefix(out[len(out)-1], "-") {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, a)
	}
	return out
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
	argv = expandCtxArg(argv, os.Getenv(envDiffCtx))
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
	// Draft-note harvesting only makes sense for hunk (it owns the session
	// daemon the notes live in). The daemon speaks loopback HTTP, so hunk
	// needs the proxy exemption too.
	isHunk := filepath.Base(argv[0]) == "hunk"
	if isHunk {
		cmd.Env = loopbackEnv()
	}
	if err := cmd.Start(); err != nil {
		errExit("run diff tool:", err)
	}
	var harvest *noteHarvester
	if isHunk {
		harvest = startNoteHarvest(cmd.Process.Pid)
	}
	err := cmd.Wait()
	if harvest != nil {
		if notes := harvest.finish(); len(notes) > 0 {
			offerNotePosting(cfg, notes)
		}
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		errExit("run diff tool:", err)
	}
}

// showDiffToolInstallHelp renders setup instructions when the configured
// tool binary is missing, then waits for a key.
func showDiffToolInstallHelp(cfg Config, missing string) {
	showToolInstallHelp(cfg, missing, []string{
		"デフォルトの hunk を使う場合:",
		"  brew install hunk        # または: npm i -g hunkdiff",
		"",
		"別のツールを使う場合は config.toml で指定できます（{patch} = patchファイル）:",
		`  diff_tool = ["delta", "{patch}"]`,
		`  diff_tool = ["nvim", "-c", "silent! e {patch}"]`,
	})
}

// showToolInstallHelp is the shared "external tool is missing" screen.
func showToolInstallHelp(cfg Config, missing string, lines []string) {
	w, h := termSize()
	s := NewScreen(w, h)
	s.WriteString(1, 0, fmt.Sprintf("bitbucket-pr — ツール %q が見つかりません", missing), styleTitle, w)
	for i, l := range lines {
		s.WriteString(1, i+2, l, styleNone, w)
	}
	s.WriteString(1, h-1, " 任意のキーで終了", styleDim, w)
	paintAndWaitKey(s, cfg)
}

// paintAndWaitKey flushes a full-screen message and blocks for one key.
func paintAndWaitKey(s *Screen, cfg Config) {
	out := bufio.NewWriter(os.Stdout)
	s.Flush(out, sgrTable(cfg))
	out.Flush()
	if old, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
		defer term.Restore(int(os.Stdin.Fd()), old)
	}
	newKeyReader(os.Stdin).Read()
}

// paintAndWaitChoice flushes a full-screen prompt and reads keys until one
// of accept appears (returned as-is) or n/q/Esc/Ctrl-C (0).
func paintAndWaitChoice(s *Screen, cfg Config, accept ...rune) rune {
	out := bufio.NewWriter(os.Stdout)
	s.Flush(out, sgrTable(cfg))
	out.Flush()
	if old, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
		defer term.Restore(int(os.Stdin.Fd()), old)
	}
	kr := newKeyReader(os.Stdin)
	for {
		k, err := kr.Read()
		if err != nil {
			return 0
		}
		for _, r := range accept {
			if isKey(k, r) {
				return r
			}
		}
		if isKey(k, 'n') || isKey(k, 'q') || k.Kind == KeyEsc ||
			(k.Kind == KeyCtrl && k.R == 'c') {
			return 0
		}
	}
}

// paintAndWaitYes flushes a full-screen prompt and reads keys until y
// (true) or n/q/Esc/Ctrl-C (false).
func paintAndWaitYes(s *Screen, cfg Config) bool {
	out := bufio.NewWriter(os.Stdout)
	s.Flush(out, sgrTable(cfg))
	out.Flush()
	if old, err := term.MakeRaw(int(os.Stdin.Fd())); err == nil {
		defer term.Restore(int(os.Stdin.Fd()), old)
	}
	kr := newKeyReader(os.Stdin)
	for {
		k, err := kr.Read()
		if err != nil {
			return false
		}
		switch {
		case isKey(k, 'y') || isKey(k, 'Y'):
			return true
		case isKey(k, 'n') || isKey(k, 'q') || k.Kind == KeyEsc ||
			(k.Kind == KeyCtrl && k.R == 'c'):
			return false
		}
	}
}
