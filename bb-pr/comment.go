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
	envCommentFile   = "BBPR_COMMENT_FILE"
	envCommentPath   = "BBPR_COMMENT_PATH"
	envCommentTo     = "BBPR_COMMENT_TO"
	envCommentFrom   = "BBPR_COMMENT_FROM"
	envCommentParent = "BBPR_COMMENT_PARENT"
)

// openCommentEditor opens the comment-composer popup. inline anchors a code
// line (nil = general PR comment); parentID > 0 replies to a thread.
func openCommentEditor(a *app, prID int, inline *InlineAnchor, parentID int) {
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
	target := "PRへのコメント"
	if inline != nil {
		env[envCommentPath] = inline.Path
		if inline.To != nil {
			env[envCommentTo] = strconv.Itoa(*inline.To)
		}
		if inline.From != nil {
			env[envCommentFrom] = strconv.Itoa(*inline.From)
		}
		target = inline.Path
	}
	if parentID > 0 {
		env[envCommentParent] = strconv.Itoa(parentID)
		target = "スレッドへの返信"
	}
	if _, err := a.herdr.pluginPaneOpen(pluginID, "comment", "popup", true, env, "80%", "80%"); err != nil {
		a.status = "コメント編集を開けません: " + err.Error()
		return
	}
	a.status = target + " を編集中 — 保存して閉じると投稿、空のままなら中止（反映は r で再読込）"
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

	email, token, ok := cfg.credentials()
	if !ok {
		commentFail(cfg, "認証情報がありません (ATLASSIAN_USER_ID / ATLASSIAN_API_TOKEN)", file)
		return
	}
	client := newBBClient(email, token, time.Duration(cfg.HTTPTimeoutSec)*time.Second)

	var inline *InlineAnchor
	if p := os.Getenv(envCommentPath); p != "" {
		inline = &InlineAnchor{Path: p}
		if n, err := strconv.Atoi(os.Getenv(envCommentTo)); err == nil && n > 0 {
			inline.To = &n
		}
		if n, err := strconv.Atoi(os.Getenv(envCommentFrom)); err == nil && n > 0 {
			inline.From = &n
		}
	}
	parent, _ := strconv.Atoi(os.Getenv(envCommentParent))

	if _, err := client.postComment(ws, repo, prID, body, inline, parent); err != nil {
		commentFail(cfg, err.Error(), file)
		return
	}
	os.Remove(file)
}

// commentFail shows the post error full-screen (the draft survives on disk)
// and waits for a key so the popup doesn't vanish before it can be read.
func commentFail(cfg Config, msg, draft string) {
	w, h := termSize()
	s := NewScreen(w, h)
	s.WriteString(1, 0, "bb-pr — コメントを投稿できませんでした", styleTitle, w)
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
