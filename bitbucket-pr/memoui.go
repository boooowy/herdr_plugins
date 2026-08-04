package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Memo input: like comment posting, the viewer never does text input itself
// — m opens the user's editor on a draft file in a herdr popup, and this
// memo-ui entrypoint signals the saved body back through a ".saved" marker.
// Memos are local (never posted to Bitbucket), so unlike comment-ui there is
// no network side: the viewer owns the store and applies the body itself.

const envMemoFile = "BITBUCKET_PR_MEMO_FILE"

// openMemoEditor opens the memo-composer popup. seed prefills the draft (an
// existing body when re-editing); target labels the status line; onSave runs
// on the main loop with the saved body once the pane touches the marker.
// The anchor context lives in the onSave closure — nothing round-trips
// through env vars.
func openMemoEditor(a *app, prID int, seed, target string, onSave func(*app, string)) {
	draft := filepath.Join(stateDir(), fmt.Sprintf("memo-%d-%d.md", prID, time.Now().UnixNano()))
	if err := os.WriteFile(draft, []byte(seed), 0o600); err != nil {
		a.status = "下書きファイルを作れません: " + err.Error()
		return
	}
	env := map[string]string{envMemoFile: draft}
	if _, err := a.herdr.pluginPaneOpen(pluginID, "memo", "popup", true, env, "80%", "60%"); err != nil {
		a.status = "メモ編集を開けません: " + err.Error()
		return
	}
	a.status = target + " を編集中 — 保存して閉じると確定、空のままなら中止"
	go watchMemoMarker(a, draft, onSave)
}

// watchMemoMarker polls for memo-ui's ".saved" marker and hands the draft
// body to onSave on the main loop. A vanished draft without a marker means
// the user cancelled (same protocol as watchPostedMarker).
func watchMemoMarker(a *app, draft string, onSave func(*app, string)) {
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if _, err := os.Stat(draft + ".saved"); err == nil {
			data, err := os.ReadFile(draft)
			os.Remove(draft + ".saved")
			os.Remove(draft)
			body := strings.TrimSpace(string(data))
			if err != nil || body == "" {
				return
			}
			a.resultCh <- func(a *app) { onSave(a, body) }
			return
		}
		if _, err := os.Stat(draft); os.IsNotExist(err) {
			return // cancelled
		}
	}
}

// watchMemoImport polls for the difftool pane's memo-import file (written
// when the user picks m on the after-hunk prompt) and appends its notes to
// the PR's memo store as review memos. Diff tabs can stay open for hours,
// hence the long timeout (same as watchPostMarker).
func watchMemoImport(a *app, prID int, path string) {
	deadline := time.Now().Add(4 * time.Hour)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		os.Remove(path)
		var notes []hunkNote
		if err := json.Unmarshal(data, &notes); err != nil || len(notes) == 0 {
			debugf("memo import: %s: %v", path, err)
			return
		}
		a.resultCh <- func(a *app) {
			store := a.memosFor(prID)
			for _, m := range notesToMemos(a.detailFor(prID).files, notes) {
				store.add(m)
			}
			a.status = fmt.Sprintf("hunk の note %d件をメモに取り込みました (計%d件 — M:一覧)",
				len(notes), len(store.memos))
			rebuildMemoTab(a, prID)
		}
		return
	}
}

// notesToMemos converts harvested hunk draft notes into review memos. When
// the note's file is in the loaded diff, the anchored lines are snapshotted
// as the memo's excerpt (same as an m-key memo); otherwise the note's own
// ranges are kept and the excerpt is simply absent.
func notesToMemos(files []FileDiff, notes []hunkNote) []reviewMemo {
	base := time.Now().UnixNano() // sequential IDs: UnixNano alone can collide in a tight loop
	memos := make([]reviewMemo, 0, len(notes))
	for i, n := range notes {
		var m reviewMemo
		if lines := linesInRange(fileDiffFor(files, n.FilePath), n.OldRange, n.NewRange); len(lines) > 0 {
			m = memoFromDiff(n.FilePath, lines, n.Body)
		} else {
			m = reviewMemo{
				Path:      n.FilePath,
				OldRange:  minMaxRange(n.OldRange),
				NewRange:  minMaxRange(n.NewRange),
				Body:      n.Body,
				CreatedOn: time.Now(),
			}
		}
		m.ID = base + int64(i)
		memos = append(memos, m)
	}
	return memos
}

// linesInRange collects the diff lines whose side line numbers fall inside
// the note's [start, end] ranges (nil when the file is missing from the
// parsed diff or the note has no range).
func linesInRange(f *FileDiff, oldRange, newRange []int) []DiffLine {
	if f == nil {
		return nil
	}
	inRange := func(no int, r []int) bool {
		return len(r) > 0 && no >= r[0] && no <= r[len(r)-1]
	}
	var lines []DiffLine
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if inRange(l.NewNo, newRange) || inRange(l.OldNo, oldRange) {
				lines = append(lines, l)
			}
		}
	}
	return lines
}

// runMemoUI is the memo pane's entrypoint: edit the draft, then signal the
// viewer. The draft is left in place — the viewer reads its body after
// seeing the marker and removes both files itself.
func runMemoUI() {
	file := os.Getenv(envMemoFile)
	if file == "" {
		errExit("missing memo context; launch me via the viewer")
	}
	cfg := loadConfig()
	runDraftEditor(cfg, file)

	data, err := os.ReadFile(file)
	if err != nil {
		errExit("read draft:", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		os.Remove(file)
		return // empty draft = cancel, close quietly
	}
	if err := os.WriteFile(file+".saved", []byte("ok"), 0o600); err != nil {
		errExit("memo: marker:", err)
	}
}
