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
	envWorkspace = "BITBUCKET_PR_WORKSPACE"
	envRepo      = "BITBUCKET_PR_REPO"
	envPRID      = "BITBUCKET_PR_PR_ID"
	envRepoDir   = "BITBUCKET_PR_REPO_DIR"
	envMode      = "BITBUCKET_PR_MODE" // "picker" starts at the repo picker
)

// runAction is the palette/keybinding/link-handler entrypoint. It runs
// server-side (no terminal): resolve which repo (and optionally which PR) to
// show, then ask herdr to open the viewer pane with that context in env.
// args[0] == "picker" skips repo resolution and opens the repo picker.
func runAction(args []string) {
	client, err := newHerdrClient()
	if err != nil {
		errExit(err)
	}
	cfg := loadConfig()
	picker := len(args) > 0 && args[0] == "picker"

	var workspace, repo, repoDir string
	prID := 0

	switch {
	case picker:
		// The current pane's repo (when there is one) only picks the
		// workspace; the user chooses the repository in the picker. Its
		// checkout still feeds the index so the picker can mark it local.
		local := resolveRepoContext(client, cfg)
		if local.Dir != "" {
			rememberRepoDir(local.Workspace, local.Repo, local.Dir)
		}
		workspace = local.Workspace
		if workspace == "" {
			workspace = cfg.workspaceFallback()
		}
		if workspace == "" {
			client.notify("Bitbucket PR",
				"workspaceを特定できません。config.toml の default_workspace を設定するか、bitbucket.org リポジトリ内のペインで実行してください。",
				"none")
			errExit("could not resolve workspace")
		}
	case os.Getenv("HERDR_PLUGIN_CLICKED_URL") != "":
		// Ctrl-clicked PR URL: the most reliable context — everything is in
		// the URL, no git remote needed.
		url := os.Getenv("HERDR_PLUGIN_CLICKED_URL")
		workspace, repo, prID, err = parsePRURL(url)
		if err != nil {
			client.notify("Bitbucket PR", "PR URLを解釈できません: "+url, "none")
			errExit(err)
		}
		// A clicked URL may target another repository. Reuse the focused
		// checkout only when its origin matches the URL; otherwise Outline
		// reports that no local checkout was attached.
		if local := resolveRepoContext(client, cfg); local.Workspace == workspace && local.Repo == repo {
			repoDir = local.Dir
		}
	default:
		local := resolveRepoContext(client, cfg)
		workspace, repo, repoDir = local.Workspace, local.Repo, local.Dir
		if workspace == "" || repo == "" {
			// No repo under the pane and no default_repo — fall back to the
			// picker when at least the workspace is known.
			if ws := cfg.workspaceFallback(); ws != "" {
				picker, workspace, repo, repoDir = true, ws, "", ""
				break
			}
			client.notify("Bitbucket PR",
				"リポジトリを特定できません。bitbucket.org リポジトリ内のペインで実行するか、config.toml の default_workspace/default_repo を設定してください。",
				"none")
			errExit("could not resolve workspace/repo")
		}
	}

	if repoDir != "" {
		// Teach the picker's checkout index in passing: normal launches are
		// how most existing checkouts become known.
		rememberRepoDir(workspace, repo, repoDir)
	}

	env := map[string]string{
		envWorkspace: workspace,
	}
	if repo != "" {
		env[envRepo] = repo
	}
	if picker {
		env[envMode] = "picker"
	}
	if repoDir != "" {
		env[envRepoDir] = repoDir
	}
	if prID > 0 {
		env[envPRID] = strconv.Itoa(prID)
	}
	debugf("action: open viewer %s/%s pr=%d picker=%v placement=%s", workspace, repo, prID, picker, cfg.Placement)
	pane, err := client.pluginPaneOpen(pluginID, "viewer", cfg.Placement, true, env, "", "")
	if err != nil {
		errExit("open viewer pane:", err)
	}
	// Label the fresh tab so it's findable in the tab bar. Only for tab
	// placement — with split/overlay the pane lands in an existing tab whose
	// label belongs to the user.
	if cfg.Placement == "tab" && pane != nil && pane.TabID != "" {
		title := "Bitbucket repos"
		if repo != "" {
			title = renderListTabTitle(cfg, workspace, repo)
		}
		if err := client.tabRename(pane.TabID, truncateWidth(title, 40)); err != nil {
			debugf("action: tab rename: %v", err)
		}
	}
}

// renderListTabTitle expands the list_tab_title placeholders. Shared by the
// action (initial open) and the picker (rename after selecting a repo).
func renderListTabTitle(cfg Config, workspace, repo string) string {
	return strings.NewReplacer("{repo}", repo, "{workspace}", workspace).Replace(cfg.ListTabTitle)
}

// resolveRepoContext derives {workspace, repo} from the pane the user
// launched from: its foreground process cwd first (the shell/editor the user
// is actually in), then the pane cwd, then the config defaults.
type repoContext struct {
	Workspace string
	Repo      string
	Dir       string
}

func resolveRepoContext(client *herdrClient, cfg Config) repoContext {
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
				root, rootErr := repoRootFromDir(dir)
				if rootErr != nil {
					debugf("action: %v", rootErr)
				}
				return repoContext{Workspace: ws, Repo: repo, Dir: root}
			} else {
				debugf("action: %v", err)
			}
		}
	}
	if ws := cfg.workspaceFallback(); ws != "" && cfg.DefaultRepo != "" {
		return repoContext{Workspace: ws, Repo: cfg.DefaultRepo}
	}
	return repoContext{}
}

// uiContext is the launch context the action passed via env, read by the ui
// subcommand.
type uiContext struct {
	Workspace string
	Repo      string // "" in picker mode until the user selects one
	RepoDir   string
	PRID      int    // 0 = start at the PR list
	Mode      string // "" | "picker"
}

func uiContextFromEnv() (uiContext, error) {
	ctx := uiContext{
		Workspace: os.Getenv(envWorkspace),
		Repo:      os.Getenv(envRepo),
		RepoDir:   os.Getenv(envRepoDir),
		Mode:      os.Getenv(envMode),
	}
	if ctx.Workspace == "" || (ctx.Repo == "" && ctx.Mode != "picker") {
		return ctx, fmt.Errorf("missing %s/%s; launch me via the action", envWorkspace, envRepo)
	}
	if s := os.Getenv(envPRID); s != "" {
		if id, err := strconv.Atoi(s); err == nil {
			ctx.PRID = id
		}
	}
	return ctx, nil
}
