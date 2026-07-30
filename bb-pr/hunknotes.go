package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Draft-note harvesting: hunk registers every TUI with a local daemon whose
// `hunk session` CLI exposes the user's draft notes ("+" / c inside hunk) —
// but only while the TUI is alive; notes vanish the moment it exits. So the
// difftool pane polls the session while hunk runs, keeps the last good
// snapshot, and offers to post it to Bitbucket after hunk closes.

// hunkNote is one user draft note from `hunk session comment list --json`.
// Ranges are 1-based [start, end] pairs on one side of the diff.
type hunkNote struct {
	NoteID   string `json:"noteId"`
	Source   string `json:"source"`
	FilePath string `json:"filePath"`
	OldRange []int  `json:"oldRange"`
	NewRange []int  `json:"newRange"`
	Body     string `json:"body"`
	Title    string `json:"title"`
}

// anchor maps the note onto a Bitbucket inline anchor (nil when the note has
// no line range — hunk-level notes can't be anchored). Bitbucket's `to`/
// `from` is the range's last line.
func (n hunkNote) anchor() *InlineAnchor {
	in := &InlineAnchor{Path: n.FilePath}
	switch {
	case len(n.NewRange) > 0:
		to := n.NewRange[len(n.NewRange)-1]
		in.To = &to
	case len(n.OldRange) > 0:
		from := n.OldRange[len(n.OldRange)-1]
		in.From = &from
	default:
		return nil
	}
	return in
}

// lineLabel renders the note's location for the confirmation list.
func (n hunkNote) lineLabel() string {
	if len(n.NewRange) > 0 {
		return rangeLabel("L", n.NewRange)
	}
	if len(n.OldRange) > 0 {
		return rangeLabel("旧L", n.OldRange)
	}
	return ""
}

func rangeLabel(prefix string, r []int) string {
	if len(r) >= 2 && r[0] != r[1] {
		return fmt.Sprintf("%s%d–%d", prefix, r[0], r[1])
	}
	return fmt.Sprintf("%s%d", prefix, r[0])
}

// noteHarvester polls the hunk daemon for our hunk process's session (keyed
// by its PID) and keeps the latest user-note snapshot. Any CLI/daemon error
// just stops the harvest — the difftool then behaves exactly as before this
// feature existed.
type noteHarvester struct {
	pid   int
	mu    sync.Mutex
	notes []hunkNote
	stop  chan struct{}
	done  chan struct{}
}

func startNoteHarvest(pid int) *noteHarvester {
	h := &noteHarvester{pid: pid, stop: make(chan struct{}), done: make(chan struct{})}
	go h.run()
	return h
}

// finish stops polling and returns the last snapshot.
func (h *noteHarvester) finish() []hunkNote {
	close(h.stop)
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.notes
}

func (h *noteHarvester) run() {
	defer close(h.done)
	// The session appears once hunk finishes starting up; give it a while.
	id := ""
	for i := 0; i < 60 && id == ""; i++ {
		if h.sleep(500 * time.Millisecond) {
			return
		}
		id = findHunkSession(h.pid)
	}
	if id == "" {
		debugf("hunknotes: no session found for pid %d — harvest disabled", h.pid)
		return
	}
	debugf("hunknotes: watching session %s", id)
	for {
		if h.sleep(500 * time.Millisecond) {
			return
		}
		notes, err := listUserNotes(id)
		if err != nil {
			debugf("hunknotes: list: %v", err)
			continue // transient — keep the previous snapshot
		}
		h.mu.Lock()
		h.notes = notes
		h.mu.Unlock()
	}
}

// sleep waits d or until stop; true means stop was requested.
func (h *noteHarvester) sleep(d time.Duration) bool {
	select {
	case <-h.stop:
		return true
	case <-time.After(d):
		return false
	}
}

// findHunkSession returns the id of the session belonging to our hunk
// process ("" when the daemon is missing, the CLI is too old, or the session
// hasn't registered yet). The PID is the only launcher-side key that is
// unique — the session's source label is just the patch basename.
func findHunkSession(pid int) string {
	out, err := hunkCmd("session", "list", "--json").Output()
	if err != nil {
		debugf("hunknotes: session list: %v", err)
		return ""
	}
	return sessionIDForPID(out, pid)
}

func sessionIDForPID(out []byte, pid int) string {
	var wrapped struct {
		Sessions []struct {
			SessionID string `json:"sessionId"`
			ID        string `json:"id"`
			PID       int    `json:"pid"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &wrapped); err != nil {
		return ""
	}
	for _, s := range wrapped.Sessions {
		if s.PID != pid {
			continue
		}
		if s.SessionID != "" {
			return s.SessionID
		}
		return s.ID
	}
	return ""
}

// listUserNotes fetches the session's user-typed draft notes.
func listUserNotes(sessionID string) ([]hunkNote, error) {
	out, err := hunkCmd("session", "comment", "list",
		sessionID, "--type", "user", "--json").Output()
	if err != nil {
		return nil, err
	}
	return parseHunkNotes(out)
}

// hunkCmd builds a `hunk session …` CLI invocation with the loopback
// proxy exemption (see loopbackEnv).
func hunkCmd(args ...string) *exec.Cmd {
	cmd := exec.Command("hunk", args...)
	cmd.Env = loopbackEnv()
	return cmd
}

// loopbackEnv is the current environment with 127.0.0.1/localhost added to
// NO_PROXY: corporate proxy settings otherwise swallow hunk's local daemon
// traffic (both the TUI's session registration and our session CLI calls),
// which disables the whole comment integration.
func loopbackEnv() []string {
	merged := os.Getenv("NO_PROXY")
	if merged == "" {
		merged = os.Getenv("no_proxy")
	}
	if merged != "" {
		merged += ","
	}
	merged += "127.0.0.1,localhost"
	var env []string
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "NO_PROXY") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "NO_PROXY="+merged, "no_proxy="+merged)
}

// parseHunkNotes accepts both a bare JSON array and an object wrapping one
// (hunk's exact shape varies by version).
func parseHunkNotes(out []byte) ([]hunkNote, error) {
	var notes []hunkNote
	if err := json.Unmarshal(out, &notes); err == nil {
		return userNotes(notes), nil
	}
	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal(out, &wrapped); err != nil {
		return nil, err
	}
	for _, key := range []string{"comments", "notes"} {
		if raw, ok := wrapped[key]; ok {
			if err := json.Unmarshal(raw, &notes); err != nil {
				return nil, err
			}
			return userNotes(notes), nil
		}
	}
	return nil, fmt.Errorf("unrecognized comment list shape")
}

// userNotes drops empty and non-user entries (the CLI already filters by
// --type user; this guards against versions that ignore the flag).
func userNotes(notes []hunkNote) []hunkNote {
	out := notes[:0]
	for _, n := range notes {
		if strings.TrimSpace(n.Body) == "" {
			continue
		}
		if n.Source != "" && n.Source != "user" {
			continue
		}
		out = append(out, n)
	}
	return out
}

// offerNotePosting is the after-hunk flow: list the harvested notes, ask
// y/n, and post them as Bitbucket inline comments. Runs in the difftool
// pane's terminal after the tool exits.
func offerNotePosting(cfg Config, notes []hunkNote) {
	ws, repo := os.Getenv(envWorkspace), os.Getenv(envRepo)
	prID, _ := strconv.Atoi(os.Getenv(envPRID))
	if ws == "" || repo == "" || prID == 0 {
		return // launched outside the viewer: nowhere to post
	}
	if !confirmNotePosting(cfg, notes, prID) {
		return
	}

	fmt.Print("\x1b[2J\x1b[H") // clear the prompt before the progress log
	fmt.Printf("投稿中… (%d件)\n", len(notes))
	email, token, ok := cfg.credentials()
	if !ok {
		noteFail(cfg, "認証情報がありません (ATLASSIAN_USER_ID / ATLASSIAN_API_TOKEN)", notes, prID)
		return
	}
	client := newBBClient(email, token, time.Duration(cfg.HTTPTimeoutSec)*time.Second)

	var failed []hunkNote
	var lastErr error
	for _, n := range notes {
		in := n.anchor()
		if in == nil {
			failed = append(failed, n)
			lastErr = fmt.Errorf("%s: 行が特定できない note は投稿できません", n.FilePath)
			continue
		}
		if _, err := client.postComment(ws, repo, prID, n.Body, in, 0); err != nil {
			failed = append(failed, n)
			lastErr = err
			continue
		}
		fmt.Printf("  ✓ %s %s\n", n.FilePath, n.lineLabel())
	}

	if len(failed) > 0 {
		noteFail(cfg, lastErr.Error(), failed, prID)
		return
	}
	if marker := os.Getenv(envDiffMarker); marker != "" {
		if err := os.WriteFile(marker, []byte("ok"), 0o600); err != nil {
			debugf("hunknotes: marker: %v", err)
		}
	}
	fmt.Printf("✓ %d件のコメントを投稿しました\n", len(notes))
	time.Sleep(700 * time.Millisecond) // let the message register before the popup closes
}

// confirmNotePosting paints the note list and waits for y (post) / other
// keys (discard).
func confirmNotePosting(cfg Config, notes []hunkNote, prID int) bool {
	w, h := termSize()
	s := NewScreen(w, h)
	s.WriteString(1, 0, fmt.Sprintf("bb-pr — draft note %d件を PR #%d にコメント投稿しますか？", len(notes), prID), styleTitle, w)
	paintSeparator(s, 1, w)
	y := 2
	for _, n := range notes {
		if y >= h-2 {
			s.WriteString(2, y, fmt.Sprintf("… 他 %d 件", len(notes)-(y-2)/2), styleDim, w)
			break
		}
		s.WriteString(2, y, "📄 "+n.FilePath+"  "+n.lineLabel(), styleMeta, w)
		y++
		body := strings.SplitN(strings.TrimSpace(n.Body), "\n", 2)[0]
		s.WriteString(4, y, truncateWidth(body, w-5), styleNone, w)
		y++
	}
	s.WriteString(1, h-1, " y:投稿する  n/q:破棄", styleDim, w)
	return paintAndWaitYes(s, cfg)
}

// noteFail shows the posting error and dumps the unposted notes to a rescue
// file so they survive (the hunk session that held them is already gone).
func noteFail(cfg Config, msg string, notes []hunkNote, prID int) {
	rescue := fmt.Sprintf("%s/notes-rescue-%d-%d.json", stateDir(), prID, time.Now().Unix())
	if data, err := json.MarshalIndent(notes, "", "  "); err == nil {
		if err := os.WriteFile(rescue, data, 0o600); err != nil {
			rescue = "(保存失敗: " + err.Error() + ")"
		}
	}
	w, h := termSize()
	s := NewScreen(w, h)
	s.WriteString(1, 0, "bb-pr — note を投稿できませんでした", styleTitle, w)
	y := 2
	for _, l := range wrapText(msg, w-4) {
		s.WriteString(2, y, l, styleError, w)
		y++
	}
	y++
	s.WriteString(2, y, fmt.Sprintf("未投稿の note %d件を退避しました: %s", len(notes), rescue), styleNone, w)
	s.WriteString(2, y+1, "APIトークンに pullrequest への write スコープが必要です。", styleDim, w)
	s.WriteString(1, h-1, " 任意のキーで終了", styleDim, w)
	paintAndWaitKey(s, cfg)
}
