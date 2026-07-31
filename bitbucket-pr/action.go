package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const pluginID = "boooowy.bitbucket-pr"

// Environment variables carrying the launch context into the viewer pane.
// The viewer's own HERDR_PANE_ID is the viewer pane itself, so everything it
// needs must travel explicitly.
const (
	envWorkspace = "BBPR_WORKSPACE"
	envRepo      = "BBPR_REPO"
	envPRID      = "BBPR_PR_ID"
)

// runAction is the palette/keybinding/link-handler entrypoint. It runs
// server-side (no terminal): resolve which repo (and optionally which PR) to
// show, then ask herdr to open the viewer pane with that context in env.
func runAction() {
	client, err := newHerdrClient()
	if err != nil {
		errExit(err)
	}
	cfg := loadConfig()

	var workspace, repo string
	prID := 0

	if url := os.Getenv("HERDR_PLUGIN_CLICKED_URL"); url != "" {
		// Ctrl-clicked PR URL: the most reliable context — everything is in
		// the URL, no git remote needed.
		workspace, repo, prID, err = parsePRURL(url)
		if err != nil {
			client.notify("Bitbucket PR", "PR URLを解釈できません: "+url, "none")
			errExit(err)
		}
	} else {
		workspace, repo = resolveRepoContext(client, cfg)
		if workspace == "" || repo == "" {
			client.notify("Bitbucket PR",
				"リポジトリを特定できません。bitbucket.org リポジトリ内のペインで実行するか、config.toml の default_workspace/default_repo を設定してください。",
				"none")
			errExit("could not resolve workspace/repo")
		}
	}

	env := map[string]string{
		envWorkspace: workspace,
		envRepo:      repo,
	}
	if prID > 0 {
		env[envPRID] = strconv.Itoa(prID)
	}
	debugf("action: open viewer %s/%s pr=%d placement=%s", workspace, repo, prID, cfg.Placement)
	pane, err := client.pluginPaneOpen(pluginID, "viewer", cfg.Placement, true, env, "", "")
	if err != nil {
		errExit("open viewer pane:", err)
	}
	// Label the fresh tab so it's findable in the tab bar. Only for tab
	// placement — with split/overlay the pane lands in an existing tab whose
	// label belongs to the user.
	if cfg.Placement == "tab" && pane != nil && pane.TabID != "" {
		title := strings.NewReplacer("{repo}", repo, "{workspace}", workspace).Replace(cfg.ListTabTitle)
		if err := client.tabRename(pane.TabID, truncateWidth(title, 40)); err != nil {
			debugf("action: tab rename: %v", err)
		}
	}
}

// resolveRepoContext derives {workspace, repo} from the pane the user
// launched from: its foreground process cwd first (the shell/editor the user
// is actually in), then the pane cwd, then the config defaults.
func resolveRepoContext(client *herdrClient, cfg Config) (string, string) {
	var pane *paneInfo
	if id := os.Getenv("HERDR_PANE_ID"); id != "" {
		pane, _ = client.paneByID(id)
	}
	if pane == nil {
		pane, _ = client.focusedPane()
	}
	if pane != nil {
		for _, dir := range []string{pane.ForegroundCwd, pane.Cwd} {
			if dir == "" {
				continue
			}
			if ws, repo, err := repoFromDir(dir); err == nil {
				return ws, repo
			} else {
				debugf("action: %v", err)
			}
		}
	}
	if ws := cfg.workspaceFallback(); ws != "" && cfg.DefaultRepo != "" {
		return ws, cfg.DefaultRepo
	}
	return "", ""
}

// uiContext is the launch context the action passed via env, read by the ui
// subcommand.
type uiContext struct {
	Workspace string
	Repo      string
	PRID      int // 0 = start at the PR list
}

func uiContextFromEnv() (uiContext, error) {
	ctx := uiContext{
		Workspace: os.Getenv(envWorkspace),
		Repo:      os.Getenv(envRepo),
	}
	if ctx.Workspace == "" || ctx.Repo == "" {
		return ctx, fmt.Errorf("missing %s/%s; launch me via the action", envWorkspace, envRepo)
	}
	if s := os.Getenv(envPRID); s != "" {
		if id, err := strconv.Atoi(s); err == nil {
			ctx.PRID = id
		}
	}
	return ctx, nil
}
