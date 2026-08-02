package main

import (
	"strings"
	"time"
)

// threadRows renders one comment thread as viewport rows: no border
// characters — each comment area reads as a block via the comment
// background (onCmtBg), replies are indented under a ↳ header, and a plain
// blank row separates threads. The root header row is selectable so the
// cursor can walk threads. anchorLabel ("L486–496" etc.) prefixes the
// header when non-empty.
func threadRows(t CommentThread, width int, now time.Time, anchorLabel string, showAvatars bool) []Row {
	var rows []Row
	bg := func(spans []Span, avatars []RowAvatar, kind RowKind, item any, selectable bool) {
		rows = append(rows, Row{Kind: kind, Item: item, Selectable: selectable,
			Spans: padBgRow(spans, width), Avatars: avatars})
	}

	// Root header: 💬 author (time) [anchor] [outdated]
	header := []Span{{" ", onCmtBg(styleNone)}}
	if anchorLabel != "" {
		header = append(header, Span{anchorLabel + " ", onCmtBg(styleMeta)})
	}
	header = append(header, Span{"💬 ", onCmtBg(styleNone)})
	header, avatars := appendAccountName(header, t.Root.User, t.Root.User.Name(),
		onCmtBg(styleAuthor), onCmtBg(styleNone), showAvatars)
	header = append(header, Span{" (" + relTime(t.Root.CreatedOn, now) + ")", onCmtBg(styleDim)})
	if t.Root.Inline != nil && t.Root.Inline.Outdated {
		header = append(header, Span{" [outdated]", onCmtBg(styleOutdated)})
	}
	if t.Root.Resolved() {
		header = append(header, Span{" [resolved]", onCmtBg(styleApproved)})
	}
	bg(header, avatars, RowComment, t.Root.ID, true)

	appendBody := func(c Comment, indent string) {
		if c.Deleted {
			bg([]Span{{indent + "(deleted comment)", onCmtBg(styleDim)}}, nil, RowComment, nil, false)
			return
		}
		w := width - displayWidth(indent) - 1
		for _, spans := range mdStyledLines(c.Content.Raw, w, styleComment) {
			row := []Span{{indent, onCmtBg(styleNone)}}
			for _, sp := range spans {
				row = append(row, Span{sp.Text, onCmtBg(sp.Style)})
			}
			bg(row, nil, RowComment, nil, false)
		}
	}
	appendBody(t.Root, "   ")

	for _, r := range t.Replies {
		// Nesting: each depth level shifts the ↳ two cells right.
		indent := "   " + strings.Repeat("  ", r.Depth-1)
		head := []Span{{indent + "↳ ", onCmtBg(styleDim)}}
		head, avatars := appendAccountName(head, r.User, r.User.Name(),
			onCmtBg(styleAuthor), onCmtBg(styleNone), showAvatars)
		head = append(head, Span{" (" + relTime(r.CreatedOn, now) + ")", onCmtBg(styleDim)})
		bg(head, avatars, RowComment, nil, false)
		appendBody(r.Comment, indent+"  ")
	}

	rows = append(rows, Row{Kind: RowComment}) // plain gap between threads
	return rows
}

// padBgRow extends a comment-area row to the full width so the background
// forms a clean rectangle.
func padBgRow(spans []Span, width int) []Span {
	used := 0
	for _, sp := range spans {
		used += displayWidth(sp.Text)
	}
	if pad := width - used; pad > 0 {
		spans = append(spans, Span{strings.Repeat(" ", pad), onCmtBg(styleNone)})
	}
	return spans
}
