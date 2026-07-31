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
func buildAgentContext(comments []Comment, now time.Time) []byte {
	byFile := inlineThreadsByFile(comments)
	ctx := agentContext{Version: 1, Files: []agentCtxFile{}}
	for _, path := range orderedInlinePaths(byFile) {
		var notes []agentCtxNote
		for _, t := range byFile[path] {
			if n, ok := ctxNote(t, now); ok {
				notes = append(notes, n)
			}
		}
		if len(notes) > 0 {
			ctx.Files = append(ctx.Files, agentCtxFile{Path: path, Annotations: notes})
		}
	}
	data, err := json.Marshal(ctx)
	if err != nil {
		debugf("agentctx: marshal: %v", err)
		return []byte(`{"version":1,"files":[]}`)
	}
	return data
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
