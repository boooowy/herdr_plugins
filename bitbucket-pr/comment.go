package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Comment posting: the viewer never does text input itself (the key reader
// can't take Japanese input) — instead `C` opens the user's editor on a
// draft file in a herdr popup, and this comment-ui entrypoint posts the
// saved body to Bitbucket when the editor exits.

const (
	envCommentFile      = "BITBUCKET_PR_COMMENT_FILE"
	envCommentPath      = "BITBUCKET_PR_COMMENT_PATH"
	envCommentTo        = "BITBUCKET_PR_COMMENT_TO"
	envCommentFrom      = "BITBUCKET_PR_COMMENT_FROM"
	envCommentStartTo   = "BITBUCKET_PR_COMMENT_START_TO"
	envCommentStartFrom = "BITBUCKET_PR_COMMENT_START_FROM"
	envCommentParent    = "BITBUCKET_PR_COMMENT_PARENT"
)

// openCommentEditor opens the comment-composer popup. inline anchors a code
// line or range (nil = general PR comment); parentID > 0 replies to a
// thread; target overrides the status label so the user sees exactly where
// the comment will land. A background poller watches for comment-ui's
// success marker and refreshes the comments in place — no manual reload
// needed.
func openCommentEditor(a *app, prID int, inline *InlineAnchor, parentID int, target string) {
	path := filepath.Join(stateDir(), fmt.Sprintf("comment-%d-%d.md", prID, time.Now().UnixNano()))
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		a.status = "下書きファイルを作れません: " + err.Error()
		return
	}
	env := map[string]string{
		envWorkspace:   a.ctx.Workspace,
		envRepo:        a.ctx.Repo,
		envPRID:        strconv.Itoa(prID),
		envCommentFile: path,
	}
	if target == "" {
		target = "PRへのコメント"
	}
	if inline != nil {
		env[envCommentPath] = inline.Path
		for _, f := range []struct {
			key string
			val *int
		}{
			{envCommentTo, inline.To},
			{envCommentFrom, inline.From},
			{envCommentStartTo, inline.StartTo},
			{envCommentStartFrom, inline.StartFrom},
		} {
			if f.val != nil {
				env[f.key] = strconv.Itoa(*f.val)
			}
		}
	}
	if parentID > 0 {
		env[envCommentParent] = strconv.Itoa(parentID)
	}
	if _, err := a.herdr.pluginPaneOpen(pluginID, "comment", "popup", true, env, "80%", "80%"); err != nil {
		a.status = "コメント編集を開けません: " + err.Error()
		return
	}
	a.status = target + " を編集中 — 保存して閉じると投稿、空のままなら中止"
	go watchCommentMarker(a, prID, path)
}

// watchCommentMarker polls for comment-ui's success marker (draft+".posted")
// and refetches the PR's comments when it appears. A vanished draft without
// a marker means the user cancelled.
func watchCommentMarker(a *app, prID int, draft string) {
	watchPostedMarker(a, prID, draft+".posted", draft, 15*time.Minute, "コメントを投稿しました")
}

// watchPostMarker is the difftool variant: the pane touches the marker after
// posting hunk draft notes. Diff tabs can stay open for hours, hence the
// long timeout; there is no draft file to signal cancellation.
func watchPostMarker(a *app, prID int, marker string) {
	watchPostedMarker(a, prID, marker, "", 4*time.Hour, "hunk の note を投稿しました")
}

func watchPostedMarker(a *app, prID int, marker, cancelFile string, maxWait time.Duration, msg string) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if _, err := os.Stat(marker); err == nil {
			os.Remove(marker)
			a.resultCh <- func(a *app) {
				a.status = msg
				a.detailFor(prID).comments = nil
				for _, vw := range a.stack {
					if dv, ok := vw.(*detailView); ok && dv.prID == prID {
						dv.load(a, false) // refetches only the comments
						break
					}
				}
			}
			return
		}
		if cancelFile != "" {
			if _, err := os.Stat(cancelFile); os.IsNotExist(err) {
				return // cancelled
			}
		}
	}
}

// runCommentUI is the comment pane's entrypoint: edit the draft, then post.
func runCommentUI() {
	file := os.Getenv(envCommentFile)
	ws, repo := os.Getenv(envWorkspace), os.Getenv(envRepo)
	prID, _ := strconv.Atoi(os.Getenv(envPRID))
	if file == "" || ws == "" || repo == "" || prID == 0 {
		errExit("missing comment context; launch me via the viewer")
	}
	cfg := loadConfig()

	argv := cfg.CommentEditor
	if len(argv) == 0 {
		if ed := strings.Fields(os.Getenv("EDITOR")); len(ed) > 0 {
			argv = ed
		} else {
			argv = []string{"nvim"}
		}
	}
	argv, hasFile := expandArgv(argv, "{file}", file)
	if !hasFile {
		argv = append(argv, file)
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		errExit("editor not found:", argv[0])
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		errExit("editor:", err)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		errExit("read draft:", err)
	}
	body := strings.TrimSpace(string(data))
	if body == "" {
		os.Remove(file)
		return // empty draft = cancel, close quietly
	}
	fmt.Println("投稿中…") // the POST takes ~1s; show why the popup lingers

	email, token, ok := cfg.credentials()
	if !ok {
		commentFail(cfg, "認証情報がありません (ATLASSIAN_USER_ID / ATLASSIAN_API_TOKEN)", file)
		return
	}
	client := newBBClient(email, token, time.Duration(cfg.HTTPTimeoutSec)*time.Second)

	var inline *InlineAnchor
	if p := os.Getenv(envCommentPath); p != "" {
		inline = &InlineAnchor{Path: p}
		lineEnv := func(key string) *int {
			if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
				return &n
			}
			return nil
		}
		inline.To = lineEnv(envCommentTo)
		inline.From = lineEnv(envCommentFrom)
		inline.StartTo = lineEnv(envCommentStartTo)
		inline.StartFrom = lineEnv(envCommentStartFrom)
	}
	parent, _ := strconv.Atoi(os.Getenv(envCommentParent))

	if _, err := client.postComment(ws, repo, prID, body, inline, parent); err != nil {
		commentFail(cfg, err.Error(), file)
		return
	}
	// Marker first (the viewer polls it), then the draft.
	if err := os.WriteFile(file+".posted", []byte("ok"), 0o600); err != nil {
		debugf("comment: marker: %v", err)
	}
	os.Remove(file)
	fmt.Println("✓ 投稿しました")
	time.Sleep(700 * time.Millisecond) // let the message register before the popup closes
}

// commentFail shows the post error full-screen (the draft survives on disk)
// and waits for a key so the popup doesn't vanish before it can be read.
func commentFail(cfg Config, msg, draft string) {
	w, h := termSize()
	s := NewScreen(w, h)
	s.WriteString(1, 0, "bitbucket-pr — コメントを投稿できませんでした", styleTitle, w)
	y := 2
	for _, l := range wrapText(msg, w-4) {
		s.WriteString(2, y, l, styleError, w)
		y++
	}
	y++
	s.WriteString(2, y, "下書きは残っています: "+draft, styleNone, w)
	s.WriteString(2, y+1, "APIトークンに pullrequest への write スコープが必要です。", styleDim, w)
	s.WriteString(1, h-1, " 任意のキーで終了", styleDim, w)
	paintAndWaitKey(s, cfg)
}
