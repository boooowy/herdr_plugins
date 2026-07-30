package main

import (
	"fmt"
	"sort"
)

// CommentThread is one root comment with its replies flattened in
// chronological order (nested replies land under the same root).
type CommentThread struct {
	Root    Comment
	Replies []Comment
}

// buildThreads groups comments into threads. keep filters which ROOT
// comments start a thread (nil = all); replies always follow their root,
// wherever it is.
func buildThreads(comments []Comment, keep func(*Comment) bool) []CommentThread {
	byID := make(map[int]*Comment, len(comments))
	for i := range comments {
		byID[comments[i].ID] = &comments[i]
	}
	// rootOf follows parent links to the thread root (bounded against
	// malformed cycles).
	rootOf := func(c *Comment) *Comment {
		for hops := 0; c.Parent != nil && hops < 100; hops++ {
			p, ok := byID[c.Parent.ID]
			if !ok {
				break
			}
			c = p
		}
		return c
	}

	threads := map[int]*CommentThread{}
	var order []int
	for i := range comments {
		c := &comments[i]
		root := rootOf(c)
		if keep != nil && !keep(root) {
			continue
		}
		t, ok := threads[root.ID]
		if !ok {
			t = &CommentThread{Root: *root}
			threads[root.ID] = t
			order = append(order, root.ID)
		}
		if c.ID != root.ID {
			t.Replies = append(t.Replies, *c)
		}
	}

	out := make([]CommentThread, 0, len(order))
	for _, id := range order {
		t := threads[id]
		// Fully-deleted threads are tombstone noise; a deleted root with
		// live replies stays for context.
		alive := !t.Root.Deleted
		for _, r := range t.Replies {
			alive = alive || !r.Deleted
		}
		if !alive {
			continue
		}
		sort.SliceStable(t.Replies, func(i, j int) bool {
			return t.Replies[i].CreatedOn.Before(t.Replies[j].CreatedOn)
		})
		out = append(out, *t)
	}
	// Newest thread first (replies inside a thread stay chronological).
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Root.CreatedOn.After(out[j].Root.CreatedOn)
	})
	return out
}

// generalThreads returns the non-inline comment threads (the Comments tab).
func generalThreads(comments []Comment) []CommentThread {
	return buildThreads(comments, func(c *Comment) bool { return c.Inline == nil })
}

// commentThreadCount is the Comments tab's badge: general + inline threads.
func commentThreadCount(comments []Comment) int {
	n := len(generalThreads(comments))
	for _, ts := range inlineThreadsByFile(comments) {
		n += len(ts)
	}
	return n
}

// inlineThreadsByFile groups inline comment threads by file path.
func inlineThreadsByFile(comments []Comment) map[string][]CommentThread {
	all := buildThreads(comments, func(c *Comment) bool { return c.Inline != nil })
	byFile := map[string][]CommentThread{}
	for _, t := range all {
		byFile[t.Root.Inline.Path] = append(byFile[t.Root.Inline.Path], t)
	}
	return byFile
}

// anchor returns which diff side and line the thread points at: to (new
// side) wins; a comment with only from anchors a deleted line.
func (t *CommentThread) anchor() (newLine, oldLine int) {
	in := t.Root.Inline
	if in == nil {
		return 0, 0
	}
	if in.To != nil && *in.To > 0 {
		return *in.To, 0
	}
	if in.From != nil && *in.From > 0 {
		return 0, *in.From
	}
	return 0, 0
}

// lineLabel renders the anchor's line (range) for list display: "L486–496",
// "L496", or "旧L12" for old-side anchors. "" for non-inline threads. Ranges
// come only from the API's start_to/start_from — never guessed.
func (t *CommentThread) lineLabel() string {
	return anchorLabel(t.Root.Inline)
}

func anchorLabel(in *InlineAnchor) string {
	if in == nil {
		return ""
	}
	if in.To != nil && *in.To > 0 {
		if in.StartTo != nil && *in.StartTo > 0 && *in.StartTo != *in.To {
			return fmt.Sprintf("L%d–%d", *in.StartTo, *in.To)
		}
		return fmt.Sprintf("L%d", *in.To)
	}
	if in.From != nil && *in.From > 0 {
		if in.StartFrom != nil && *in.StartFrom > 0 && *in.StartFrom != *in.From {
			return fmt.Sprintf("旧L%d–%d", *in.StartFrom, *in.From)
		}
		return fmt.Sprintf("旧L%d", *in.From)
	}
	return ""
}

// matchesLine reports whether the thread anchors to this diff line.
func (t *CommentThread) matchesLine(l DiffLine) bool {
	if t.Root.Inline != nil && t.Root.Inline.Outdated {
		return false // outdated comments render in the orphaned section
	}
	newLine, oldLine := t.anchor()
	if newLine > 0 {
		return l.NewNo == newLine && l.Kind != LineDel
	}
	if oldLine > 0 {
		// from-only anchors point at the old side: a deleted line, or a
		// context line when the file has no new-side match.
		return l.OldNo == oldLine && l.Kind != LineAdd
	}
	return false
}
