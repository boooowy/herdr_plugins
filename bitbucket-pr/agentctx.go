package main

import (
	"encoding/json"
	"strings"
	"time"
)

// hunk's --agent-context sidecar: inline annotations rendered inside the
// diff (Next/Previous Comment jumps between them). bitbucket-pr converts the PR's
// existing inline comment threads into it so hunk shows them in place.
// Format: https://github.com/modem-dev/hunk (docs/agent-workflows.md).
type agentContext struct {
	Version int            `json:"version"`
	Files   []agentCtxFile `json:"files"`
}

type agentCtxFile struct {
	Path        string         `json:"path"`
	Annotations []agentCtxNote `json:"annotations"`
}

// agentCtxNote is one annotation. Ranges are 1-based [start, end] line pairs
// on the new (newRange) or old (oldRange) side of the diff.
type agentCtxNote struct {
	NewRange  []int  `json:"newRange,omitempty"`
	OldRange  []int  `json:"oldRange,omitempty"`
	Summary   string `json:"summary"`
	Rationale string `json:"rationale,omitempty"`
	Author    string `json:"author,omitempty"`
}

// buildAgentContext renders the PR's inline comment threads as hunk
// agent-context JSON. Outdated threads are skipped (their lines no longer
// exist in the diff); replies join the rationale as "↳ author: body" lines.
//
// files/focusPath also decide the display order: hunk sorts the changeset by
// this array (its orderDiffFiles ranks each diff file by its index here, with
// unlisted files last), so the array has to carry *every* file of the patch —
// the focused one first, the rest in patch order — or hunk's ordering would
// fight the patch reorderPatch already produced.
func buildAgentContext(comments []Comment, files []FileDiff, focusPath string, now time.Time) []byte {
	byFile := inlineThreadsByFile(comments)
	ctx := agentContext{Version: 1, Files: []agentCtxFile{}}
	seen := map[string]bool{}
	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		notes := []agentCtxNote{}
		for _, t := range byFile[path] {
			if n, ok := ctxNote(t, now); ok {
				notes = append(notes, n)
			}
		}
		ctx.Files = append(ctx.Files, agentCtxFile{Path: path, Annotations: notes})
	}
	add(displayPathFor(files, focusPath))
	for i := range files {
		add(files[i].Path())
	}
	// Threads on files the patch does not carry (Bitbucket truncates large
	// diffs) still ride along so their annotations are not dropped.
	for _, path := range orderedInlinePaths(byFile) {
		add(path)
	}
	data, err := json.Marshal(ctx)
	if err != nil {
		debugf("agentctx: marshal: %v", err)
		return []byte(`{"version":1,"files":[]}`)
	}
	return data
}

// displayPathFor maps a path to the one hunk displays (the new side), so a
// rename's old path does not add a second entry for the same file. Paths the
// patch does not carry come back unchanged.
func displayPathFor(files []FileDiff, path string) string {
	if path == "" {
		return ""
	}
	for i := range files {
		if files[i].Path() == path {
			return path
		}
		if files[i].OldPath == path && files[i].Path() != "" {
			return files[i].Path()
		}
	}
	return path
}

func ctxNote(t CommentThread, now time.Time) (agentCtxNote, bool) {
	in := t.Root.Inline
	if in == nil || in.Outdated {
		return agentCtxNote{}, false
	}
	n := agentCtxNote{
		Summary: threadBody(t.Root),
		Author:  t.Root.User.Name() + " (" + relTime(t.Root.CreatedOn, now) + ")",
	}
	switch {
	case in.To != nil && *in.To > 0:
		start := *in.To
		if in.StartTo != nil && *in.StartTo > 0 && *in.StartTo < start {
			start = *in.StartTo
		}
		n.NewRange = []int{start, *in.To}
	case in.From != nil && *in.From > 0:
		start := *in.From
		if in.StartFrom != nil && *in.StartFrom > 0 && *in.StartFrom < start {
			start = *in.StartFrom
		}
		n.OldRange = []int{start, *in.From}
	default:
		return agentCtxNote{}, false
	}
	var replies []string
	for _, r := range t.Replies {
		replies = append(replies, "↳ "+r.User.Name()+": "+threadBody(r.Comment))
	}
	n.Rationale = strings.Join(replies, "\n")
	return n, true
}

func threadBody(c Comment) string {
	if c.Deleted {
		return "(削除済みコメント)"
	}
	return strings.TrimSpace(c.Content.Raw)
}
