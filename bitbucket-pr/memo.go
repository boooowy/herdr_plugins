package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Review memos: rinkaku-style local annotations written while reading a PR.
// A memo anchors to diff lines (or the whole PR), accumulates in a per-PR
// JSON file under stateDir(), and is never sent to Bitbucket — M lists them
// and y exports the whole set as Markdown for pasting into an AI agent
// prompt.

// reviewMemo is one local review note. Ranges are 1-based [start, end] pairs
// on each side of the diff (hunkNote-style); an empty Path marks a PR-level
// memo. Excerpt snapshots the anchored diff lines ("+ x" / "- x" / "  x") at
// creation time so the Markdown export never depends on the diff still being
// loaded.
type reviewMemo struct {
	ID        int64     `json:"id"`
	Path      string    `json:"path,omitempty"`
	OldRange  []int     `json:"oldRange,omitempty"`
	NewRange  []int     `json:"newRange,omitempty"`
	Body      string    `json:"body"`
	Excerpt   []string  `json:"excerpt,omitempty"`
	CreatedOn time.Time `json:"createdOn"`
}

// lineLabel renders the memo's location ("L486–496" / "旧L12"; "" for a
// PR-level memo). New side wins, like hunkNote.
func (m reviewMemo) lineLabel() string {
	if len(m.NewRange) > 0 {
		return rangeLabel("L", m.NewRange)
	}
	if len(m.OldRange) > 0 {
		return rangeLabel("旧L", m.OldRange)
	}
	return ""
}

// memoFromDiff builds a memo anchored to the given diff lines (the cursor
// line or a v/V selection). Both side ranges are kept — a selection can span
// added and deleted lines — and the label/jump side is chosen by lineLabel.
func memoFromDiff(path string, lines []DiffLine, body string) reviewMemo {
	m := reviewMemo{
		ID:        time.Now().UnixNano(),
		Path:      path,
		Body:      body,
		CreatedOn: time.Now(),
	}
	var oldNos, newNos []int
	for _, l := range lines {
		marker := "  "
		switch l.Kind {
		case LineAdd:
			marker = "+ "
		case LineDel:
			marker = "- "
		case LineNoNewline:
			marker = `\ `
		}
		m.Excerpt = append(m.Excerpt, marker+l.Text)
		if l.OldNo > 0 {
			oldNos = append(oldNos, l.OldNo)
		}
		if l.NewNo > 0 {
			newNos = append(newNos, l.NewNo)
		}
	}
	m.OldRange = minMaxRange(oldNos)
	m.NewRange = minMaxRange(newNos)
	return m
}

// newPRMemo builds an unanchored PR-level memo.
func newPRMemo(body string) reviewMemo {
	return reviewMemo{ID: time.Now().UnixNano(), Body: body, CreatedOn: time.Now()}
}

// fileMemo builds a memo anchored to a whole file (no line range).
func fileMemo(path string) reviewMemo {
	return reviewMemo{ID: time.Now().UnixNano(), Path: path, CreatedOn: time.Now()}
}

// memoFromAnchor builds a memo from a Bitbucket inline anchor (a comment
// thread's location), snapshotting the anchored diff lines when the file is
// in the loaded diff — same excerpt an m-key memo gets in the diff view.
func memoFromAnchor(files []FileDiff, in *InlineAnchor) reviewMemo {
	oldRange, newRange := anchorRanges(in)
	if lines := linesInRange(fileDiffFor(files, in.Path), oldRange, newRange); len(lines) > 0 {
		return memoFromDiff(in.Path, lines, "")
	}
	return reviewMemo{
		ID:        time.Now().UnixNano(),
		Path:      in.Path,
		OldRange:  oldRange,
		NewRange:  newRange,
		CreatedOn: time.Now(),
	}
}

// anchorRanges maps an inline anchor onto [start, end] side ranges (new
// side wins, mirroring anchorLabel).
func anchorRanges(in *InlineAnchor) (oldRange, newRange []int) {
	switch {
	case in.To != nil && *in.To > 0:
		start := *in.To
		if in.StartTo != nil && *in.StartTo > 0 {
			start = *in.StartTo
		}
		newRange = []int{start, *in.To}
	case in.From != nil && *in.From > 0:
		start := *in.From
		if in.StartFrom != nil && *in.StartFrom > 0 {
			start = *in.StartFrom
		}
		oldRange = []int{start, *in.From}
	}
	return oldRange, newRange
}

// minMaxRange collapses line numbers into a [start, end] pair (nil when
// empty).
func minMaxRange(nos []int) []int {
	if len(nos) == 0 {
		return nil
	}
	lo, hi := nos[0], nos[0]
	for _, n := range nos[1:] {
		if n < lo {
			lo = n
		}
		if n > hi {
			hi = n
		}
	}
	return []int{lo, hi}
}

// memoStore holds one PR's memos and persists them eagerly on every
// mutation (cheap, and survives a crash or pane close).
type memoStore struct {
	path  string
	memos []reviewMemo
}

func memoPath(ws, repo string, prID int) string {
	return filepath.Join(stateDir(), "memos", fmt.Sprintf("memos-%s__%s__%d.json", ws, repo, prID))
}

// loadMemoStore reads the PR's memo file; absent or corrupt files just mean
// an empty store (same degradation as the PR-list cache).
func loadMemoStore(ws, repo string, prID int) *memoStore {
	s := &memoStore{path: memoPath(ws, repo, prID)}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, &s.memos); err != nil {
		debugf("memo: unmarshal %s: %v", s.path, err)
		s.memos = nil
	}
	return s
}

// save persists the store; failures only log (the memos stay in memory).
func (s *memoStore) save() {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		debugf("memo: mkdir: %v", err)
		return
	}
	data, err := json.Marshal(s.memos)
	if err != nil {
		debugf("memo: marshal: %v", err)
		return
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		debugf("memo: write: %v", err)
	}
}

func (s *memoStore) add(m reviewMemo) {
	s.memos = append(s.memos, m)
	s.save()
}

// update replaces the body of the memo with the given ID.
func (s *memoStore) update(id int64, body string) bool {
	for i := range s.memos {
		if s.memos[i].ID == id {
			s.memos[i].Body = body
			s.save()
			return true
		}
	}
	return false
}

func (s *memoStore) delete(id int64) bool {
	for i := range s.memos {
		if s.memos[i].ID == id {
			s.memos = append(s.memos[:i], s.memos[i+1:]...)
			s.save()
			return true
		}
	}
	return false
}

func (s *memoStore) byID(id int64) *reviewMemo {
	for i := range s.memos {
		if s.memos[i].ID == id {
			return &s.memos[i]
		}
	}
	return nil
}

// memosMarkdown renders every memo as one Markdown document for the
// clipboard: a PR header, then one "### path L範囲" section per memo with
// its body and the snapshotted diff excerpt. pr may still be nil (detail
// not fetched); the header then falls back to the bare number.
func memosMarkdown(prID int, pr *PullRequest, memos []reviewMemo) string {
	var b strings.Builder
	if pr != nil {
		fmt.Fprintf(&b, "# PR #%d %s (%s → %s) レビューメモ\n",
			pr.ID, pr.Title, pr.Source.Branch.Name, pr.Destination.Branch.Name)
	} else {
		fmt.Fprintf(&b, "# PR #%d レビューメモ\n", prID)
	}
	for _, m := range memos {
		b.WriteString("\n### ")
		if m.Path == "" {
			b.WriteString("(PR全体)")
		} else {
			b.WriteString(m.Path)
			if label := m.lineLabel(); label != "" {
				b.WriteString(" " + label)
			}
		}
		b.WriteString("\n" + m.Body + "\n")
		if len(m.Excerpt) > 0 {
			b.WriteString("\n```diff\n")
			for _, l := range m.Excerpt {
				b.WriteString(l + "\n")
			}
			b.WriteString("```\n")
		}
	}
	return b.String()
}
