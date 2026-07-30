package main

import (
	"strings"
	"time"
)

// threadRows renders one comment thread as viewport rows. boxed draws the
// ┌/│/└ frame used inside the diff view; the Comments tab renders unframed.
// The root header row is selectable so the cursor can walk threads.
// anchorLabel ("L486–496" etc.) prefixes the header when non-empty — the
// Comments tab uses it; the diff view passes "" (the code line is visible).
func threadRows(t CommentThread, width int, boxed bool, now time.Time, anchorLabel string) []Row {
	var rows []Row
	bodyW := width - 4
	if boxed {
		bodyW = width - 6
	}
	if bodyW < 10 {
		bodyW = 10
	}

	prefix := func(s string, st StyleID) []Span {
		if boxed {
			return []Span{{" │ ", styleCommentBorder}, {s, st}}
		}
		return []Span{{"   ", styleNone}, {s, st}}
	}

	// Header: ┌─💬 author (time) [outdated] ────
	header := []Span{}
	if boxed {
		header = append(header, Span{" ┌─", styleCommentBorder})
	} else {
		header = append(header, Span{" ", styleNone})
	}
	if anchorLabel != "" {
		header = append(header, Span{anchorLabel + " ", styleMeta})
	}
	header = append(header,
		Span{"💬 ", styleNone},
		Span{t.Root.User.Name(), styleAuthor},
		Span{" (" + relTime(t.Root.CreatedOn, now) + ")", styleDim},
	)
	if t.Root.Inline != nil && t.Root.Inline.Outdated {
		header = append(header, Span{" [outdated]", styleOutdated})
	}
	if boxed {
		used := 0
		for _, sp := range header {
			used += displayWidth(sp.Text)
		}
		if pad := width - used - 2; pad > 0 {
			header = append(header, Span{" " + strings.Repeat("─", pad), styleCommentBorder})
		}
	}
	rows = append(rows, row(RowComment, t.Root.ID, true, header...))

	// prefixSpans is the per-row lead-in (box border + indent) that markdown
	// body spans get appended to.
	prefixSpans := func(indent string) []Span {
		if boxed {
			return []Span{{" │ ", styleCommentBorder}, {indent, styleNone}}
		}
		return []Span{{"   " + indent, styleNone}}
	}
	appendBody := func(c Comment, indent string) {
		if c.Deleted {
			rows = append(rows, Row{Kind: RowComment, Spans: prefix(indent+"(deleted comment)", styleDim)})
			return
		}
		for _, spans := range mdStyledLines(c.Content.Raw, bodyW-displayWidth(indent), styleComment) {
			rows = append(rows, Row{Kind: RowComment, Spans: append(prefixSpans(indent), spans...)})
		}
	}
	appendBody(t.Root, "")

	for _, r := range t.Replies {
		head := prefix(" └ ", styleDim)
		head = append(head,
			Span{r.User.Name(), styleAuthor},
			Span{" (" + relTime(r.CreatedOn, now) + ")", styleDim},
		)
		rows = append(rows, Row{Kind: RowComment, Spans: head})
		appendBody(r, "   ")
	}

	if boxed {
		rows = append(rows, Row{Kind: RowComment, Spans: []Span{
			{" └" + strings.Repeat("─", max(0, width-4)), styleCommentBorder},
		}})
	} else {
		rows = append(rows, Row{Kind: RowComment})
	}
	return rows
}
