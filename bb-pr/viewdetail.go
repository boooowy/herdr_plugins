package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	tabOverview = iota
	tabFiles
	tabComments
	tabCount
)

// detailView shows one PR with three tabs: Overview / Files / Comments
// (atlas.nvim's five tabs collapsed — Conversation/Review duplicated
// comments there).
type detailView struct {
	prID int
	tab  int
	vp   [tabCount]Viewport

	// inlineThreadFor maps a comment root ID to its inline thread — how the
	// Comments tab's Enter finds the file/line to jump to in the diff view.
	inlineThreadFor map[int]CommentThread
	// threadFor covers every thread (general + inline) for the reply-target
	// footer; threadSpan is each thread's row range for the focus highlight
	// (rows of the tab viewport in the flat layout, rows of cmtPreview in
	// the split layout).
	threadFor  map[int]CommentThread
	threadSpan map[int][2]int

	// Comments-tab master-detail split (wide terminals): cmtPreview is the
	// right-hand read-only pane, cmtPreviewKey names what it currently shows
	// (a file path, or generalPreviewKey), and cmtFirstThread gives Enter's
	// jump target when the cursor sits on a file header row.
	cmtPreview     Viewport
	cmtPreviewKey  string
	cmtFirstThread map[string]int

	// Reply rows are selectable too: replyRoot maps a reply ID to its thread
	// root, commentFor maps any selectable comment ID (root or reply) to the
	// comment itself (C reply target, x delete target).
	replyRoot  map[int]int
	commentFor map[int]Comment

	// pendingDelete is a comment ID waiting for the y-key confirmation of
	// `x` (0 = none).
	pendingDelete int
}

// cmtGroup marks a Comments-tab master-list group header row: a file with
// inline comments, or (Path == "") the general PR-comment group.
type cmtGroup struct{ Path string }

// generalPreviewKey is cmtPreviewKey's value for the general PR-comment
// group ("\x00" cannot appear in a repo path).
const generalPreviewKey = "\x00general"

// commentsSplit reports whether the Comments tab uses the master-detail
// split; narrower terminals fall back to the flat single-column list.
func commentsSplit(a *app) bool { return a.w >= 90 }

// commentMasterWidth is the split's left-pane width.
func commentMasterWidth(w int) int {
	lw := w * 38 / 100
	if lw < 28 {
		lw = 28
	}
	if lw > 52 {
		lw = 52
	}
	return lw
}

func newDetailView(a *app, id int) *detailView {
	v := &detailView{prID: id}
	v.load(a, false)
	return v
}

// load fetches whatever is missing for this PR (detail, diffstat, comments,
// raw diff — the diff prefetches so opening a file feels instant).
func (v *detailView) load(a *app, force bool) {
	d := a.detailFor(v.prID)
	if force {
		*d = prDetail{}
	}
	id := v.prID
	if d.pr == nil {
		a.fetch(fmt.Sprintf("pr %d", id), func() (func(*app), error) {
			pr, err := a.client.getPR(a.ctx.Workspace, a.ctx.Repo, id)
			if err != nil {
				return nil, err
			}
			return func(a *app) { a.detailFor(id).pr = pr; v.rebuild(a) }, nil
		})
	}
	if d.diffstat == nil {
		a.fetch(fmt.Sprintf("diffstat %d", id), func() (func(*app), error) {
			st, err := a.client.diffStat(a.ctx.Workspace, a.ctx.Repo, id)
			if err != nil {
				return nil, err
			}
			if st == nil {
				st = []DiffStatEntry{} // nil marks "not loaded" — keep 0 files distinct
			}
			return func(a *app) { a.detailFor(id).diffstat = st; v.rebuild(a) }, nil
		})
	}
	if d.comments == nil {
		a.fetch(fmt.Sprintf("comments %d", id), func() (func(*app), error) {
			cs, err := a.client.comments(a.ctx.Workspace, a.ctx.Repo, id)
			if err != nil {
				return nil, err
			}
			if cs == nil {
				cs = []Comment{} // nil marks "not loaded" — keep 0 comments distinct
			}
			return func(a *app) {
				a.detailFor(id).comments = cs
				v.rebuild(a)
				rebuildTopDiffView(a, id)
			}, nil
		})
	}
	if d.diffText == "" {
		a.fetch(fmt.Sprintf("diff %d", id), func() (func(*app), error) {
			text, err := a.client.diff(a.ctx.Workspace, a.ctx.Repo, id)
			if err != nil {
				return nil, err
			}
			return func(a *app) {
				dd := a.detailFor(id)
				dd.diffText = text
				dd.files = ParseUnifiedDiff(text)
				v.rebuild(a)
				rebuildTopDiffView(a, id)
				if req := a.pendingDifftool; req != nil && req.prID == id {
					a.pendingDifftool = nil
					openInDiffTool(a, req.prID, req.focus)
				}
			}, nil
		})
	}
	v.rebuild(a)
}

// rebuildTopDiffView refreshes a diff view sitting above this detail view
// when its PR's data (diff text, comments) arrives late — its rows were
// built from the incomplete cache and nothing else would regenerate them.
func rebuildTopDiffView(a *app, prID int) {
	if dv, ok := a.top().(*diffView); ok && dv.prID == prID {
		dv.rebuild(a)
	}
}

// rebuild regenerates the current tab's rows.
func (v *detailView) rebuild(a *app) {
	d := a.detailFor(v.prID)
	switch v.tab {
	case tabOverview:
		v.vp[v.tab].Reset(v.overviewRows(a, d))
	case tabFiles:
		v.vp[v.tab].Reset(v.fileRows(a, d))
	case tabComments:
		if commentsSplit(a) {
			v.vp[v.tab].Reset(v.commentMasterRows(a, d))
			v.cmtPreviewKey = "" // widths/content changed — rebuild the pane
			v.syncPreview(a)
		} else {
			v.vp[v.tab].Reset(v.commentRows(a, d))
		}
	}
}

func (v *detailView) overviewRows(a *app, d *prDetail) []Row {
	var rows []Row
	if d.pr == nil {
		return rows
	}
	desc := d.pr.Description()
	if desc == "" {
		rows = append(rows, textRow(Span{" (説明はありません)", styleDim}))
	} else {
		for _, spans := range mdStyledLines(desc, a.w-3, styleNone) {
			rows = append(rows, textRow(append([]Span{{" ", styleNone}}, spans...)...))
		}
	}

	// Reviewers grouped the way atlas.nvim does: Approved / Changes
	// requested / Pending.
	var approved, changes, pending []string
	for _, p := range d.pr.Participants {
		if p.Role != "REVIEWER" && !p.Approved && p.State == "" {
			continue // silent participants stay out of the reviewer block
		}
		name := p.User.Name()
		switch {
		case p.Approved || p.State == "approved":
			approved = append(approved, name)
		case p.State == "changes_requested":
			changes = append(changes, name)
		case p.Role == "REVIEWER":
			pending = append(pending, name)
		}
	}
	total := len(approved) + len(changes) + len(pending)
	if total > 0 {
		rows = append(rows, textRow(), textRow(
			Span{fmt.Sprintf(" Reviewers (%d/%d approved)", len(approved), total), styleTitle}))
		appendGroup := func(label string, names []string, st StyleID) {
			if len(names) == 0 {
				return
			}
			rows = append(rows, textRow(
				Span{"   " + label + ": ", st},
				Span{joinComma(names), styleNone}))
		}
		appendGroup("✓ Approved", approved, styleApproved)
		appendGroup("± Changes requested", changes, styleChangesReq)
		appendGroup("○ Pending", pending, styleDim)
	}
	return rows
}

func (v *detailView) fileRows(a *app, d *prDetail) []Row {
	var rows []Row
	if truncMsg := diffTruncated(d); truncMsg != "" {
		rows = append(rows, textRow(Span{" ⚠ " + truncMsg, styleOutdated}))
	}
	commentCount := map[string]int{}
	for _, c := range d.comments {
		if c.Inline != nil && !c.Deleted {
			commentCount[c.Inline.Path]++
		}
	}
	for i, st := range d.diffstat {
		mark, style := statusMark(st.Status)
		path := st.Path()
		if st.Status == "renamed" && st.OldPath() != "" {
			path = st.OldPath() + " → " + path
		}
		badge := ""
		if n := commentCount[st.Path()]; n > 0 {
			badge = fmt.Sprintf("  💬%d", n)
		}
		stats := fmt.Sprintf(" +%d -%d", st.LinesAdded, st.LinesRemoved)
		pathW := a.w - 5 - displayWidth(stats) - displayWidth(badge) - 1
		rows = append(rows, row(RowFile, i, true,
			Span{" " + mark + " ", style},
			Span{padRight(path, pathW), styleNone},
			Span{badge, styleDim},
			Span{fmt.Sprintf(" +%d", st.LinesAdded), styleApproved},
			Span{fmt.Sprintf(" -%d", st.LinesRemoved), styleChangesReq},
		))
	}
	if len(rows) == 0 && a.loading == 0 {
		rows = append(rows, textRow(Span{" (変更ファイルはありません)", styleDim}))
	}
	return rows
}

func (v *detailView) commentRows(a *app, d *prDetail) []Row {
	var rows []Row
	now := time.Now()
	general := generalThreads(d.comments)
	byFile := inlineThreadsByFile(d.comments)
	v.inlineThreadFor = map[int]CommentThread{}
	v.threadFor = map[int]CommentThread{}
	v.threadSpan = map[int][2]int{}
	v.replyRoot = nil // flat layout: only thread roots are selectable
	v.commentFor = nil

	inlineTotal := 0
	for _, ts := range byFile {
		inlineTotal += len(ts)
	}
	if len(general) == 0 && inlineTotal == 0 {
		if a.loading == 0 {
			rows = append(rows, textRow(Span{" (コメントはありません)", styleDim}))
		}
		return rows
	}

	section := func(title string) {
		line := "── " + title + " "
		if pad := a.w - displayWidth(line) - 1; pad > 0 {
			line += strings.Repeat("─", pad)
		}
		rows = append(rows, textRow(Span{line, styleDim}), textRow())
	}

	// addThread appends a thread's rows and records its span (sans the
	// trailing gap row) for the focus highlight.
	addThread := func(rows []Row, t CommentThread, label string) []Row {
		v.threadFor[t.Root.ID] = t
		start := len(rows)
		rows = append(rows, threadRows(t, a.w, now, label)...)
		v.threadSpan[t.Root.ID] = [2]int{start, len(rows) - 1}
		return rows
	}

	if len(general) > 0 {
		section(fmt.Sprintf("PR コメント (%d)", len(general)))
		for _, t := range general {
			rows = addThread(rows, t, "")
		}
	}
	if inlineTotal > 0 {
		section(fmt.Sprintf("インラインコメント (%d)", inlineTotal))
		for _, path := range orderedInlinePaths(byFile) {
			rows = append(rows, textRow(Span{" 📄 ", styleNone}, Span{path, styleTitle}), textRow())
			threads := byFile[path]
			for _, t := range threads {
				v.inlineThreadFor[t.Root.ID] = t
			}
			rows = appendFileCommentRows(rows, fileDiffFor(d.files, path), threads, a.w, addThread)
		}
	}
	return rows
}

// appendFileCommentRows renders one file's comment section: commented hunks
// diff-view style — full hunk context with each thread right under its
// anchor line. Hunks without comments are skipped; unanchorable threads
// (outdated, truncated diff, or the diff not loaded yet) land flat at the
// end. addThread appends one thread's rows and does the caller's
// bookkeeping (spans, selectability).
func appendFileCommentRows(rows []Row, f *FileDiff, threads []CommentThread, width int,
	addThread func([]Row, CommentThread, string) []Row) []Row {
	anchored, orphans := anchorThreads(f, threads)
	if f != nil {
		for hi := range f.Hunks {
			n := hunkCommentCount(anchored, hi)
			if n == 0 {
				continue
			}
			rows = append(rows, commentHunkHeader(&f.Hunks[hi], n, width))
			for li, l := range f.Hunks[hi].Lines {
				rows = append(rows, unselectableRows(diffLineRows(l, 4, width, false))...)
				for _, t := range anchored[[2]int{hi, li}] {
					rows = addThread(rows, t, t.lineLabel())
				}
			}
			rows = append(rows, textRow())
		}
	}
	for _, t := range orphans {
		rows = addThread(rows, t, t.lineLabel())
	}
	return rows
}

// commentMasterRows builds the split layout's left list: group headers
// (PR comments / one per file) with their threads indented underneath.
func (v *detailView) commentMasterRows(a *app, d *prDetail) []Row {
	var rows []Row
	general := generalThreads(d.comments)
	byFile := inlineThreadsByFile(d.comments)
	v.inlineThreadFor = map[int]CommentThread{}
	v.threadFor = map[int]CommentThread{}
	v.cmtFirstThread = map[string]int{}
	v.replyRoot = map[int]int{}
	v.commentFor = map[int]Comment{}
	width := commentMasterWidth(a.w)

	inlineTotal := 0
	for _, ts := range byFile {
		inlineTotal += len(ts)
	}
	if len(general) == 0 && inlineTotal == 0 {
		if a.loading == 0 {
			rows = append(rows, textRow(Span{" (コメントはありません)", styleDim}))
		}
		return rows
	}

	// addThreadRows lists one thread: its root line then every reply,
	// nested by depth — all selectable.
	addThreadRows := func(t CommentThread) {
		v.threadFor[t.Root.ID] = t
		v.commentFor[t.Root.ID] = t.Root
		rows = append(rows, masterThreadRow(t, width))
		for _, r := range t.Replies {
			v.replyRoot[r.ID] = t.Root.ID
			v.commentFor[r.ID] = r.Comment
			rows = append(rows, masterReplyRow(r, width))
		}
	}

	if len(general) > 0 {
		rows = append(rows, row(RowFile, cmtGroup{}, true,
			Span{" 💬 ", styleNone},
			Span{fmt.Sprintf("PR コメント (%d)", len(general)), styleTitle}))
		for _, t := range general {
			addThreadRows(t)
		}
		rows = append(rows, textRow())
	}
	for _, path := range orderedInlinePaths(byFile) {
		threads := byFile[path]
		v.cmtFirstThread[path] = threads[0].Root.ID
		rows = append(rows, row(RowFile, cmtGroup{Path: path}, true,
			Span{" 📄 ", styleNone},
			Span{truncateWidth(path, width-5), styleTitle}))
		for _, t := range threads {
			v.inlineThreadFor[t.Root.ID] = t
			addThreadRows(t)
		}
		rows = append(rows, textRow())
	}
	return rows
}

// masterThreadRow is one left-pane thread line: "   L46 tanaka 「抜粋…」".
func masterThreadRow(t CommentThread, width int) Row {
	spans := []Span{{"   ", styleNone}}
	if label := t.lineLabel(); label != "" {
		st := styleMeta
		if t.Root.Inline != nil && t.Root.Inline.Outdated {
			st = styleOutdated
		}
		spans = append(spans, Span{label + " ", st})
	}
	spans = append(spans, Span{t.Root.User.Name(), styleAuthor})
	if t.Root.Resolved() {
		spans = append(spans, Span{" ✓", styleApproved})
	}
	spans = appendExcerptSpan(spans, t.Root, width)
	return row(RowComment, t.Root.ID, true, spans...)
}

// masterReplyRow is one left-pane reply line: "     ↳ suzuki 「抜粋…」",
// shifted right by nesting depth.
func masterReplyRow(r Reply, width int) Row {
	indent := "     " + strings.Repeat("  ", r.Depth-1)
	spans := []Span{
		{indent + "↳ ", styleDim},
		{r.User.Name(), styleAuthor},
	}
	spans = appendExcerptSpan(spans, r.Comment, width)
	return row(RowComment, r.ID, true, spans...)
}

// appendExcerptSpan adds the 「…」 body excerpt when the remaining width
// allows it (" 「" + excerpt + "」" needs 6 cells of frame).
func appendExcerptSpan(spans []Span, c Comment, width int) []Span {
	used := 0
	for _, sp := range spans {
		used += displayWidth(sp.Text)
	}
	if w := width - used - 6; w > 1 {
		spans = append(spans, Span{" 「" + truncateWidth(commentExcerpt(c), w) + "」", styleDim})
	}
	return spans
}

// commentExcerpt is the first non-empty body line, for one-line lists.
func commentExcerpt(c Comment) string {
	if c.Deleted {
		return "(deleted comment)"
	}
	for _, line := range strings.Split(c.Content.Raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// commentDetailRows builds the right-hand preview for one master entry:
// the general threads, or one file's hunk-context comment view. Every row
// is unselectable — the pane only scrolls (Ctrl-d/u), focus stays left.
func (v *detailView) commentDetailRows(a *app, d *prDetail, key string, width int) []Row {
	var rows []Row
	now := time.Now()
	v.threadSpan = map[int][2]int{}
	addThread := func(rows []Row, t CommentThread, label string) []Row {
		start := len(rows)
		rows = append(rows, unselectableRows(threadRows(t, width, now, label))...)
		v.threadSpan[t.Root.ID] = [2]int{start, len(rows) - 1}
		return rows
	}

	if key == generalPreviewKey {
		rows = append(rows, textRow(Span{" 💬 ", styleNone}, Span{"PR コメント", styleTitle}), textRow())
		for _, t := range generalThreads(d.comments) {
			rows = addThread(rows, t, "")
		}
		return rows
	}
	rows = append(rows, textRow(Span{" 📄 ", styleNone}, Span{truncateWidth(key, width-5), styleTitle}), textRow())
	return appendFileCommentRows(rows, fileDiffFor(d.files, key),
		inlineThreadsByFile(d.comments)[key], width, addThread)
}

// syncPreview aligns the right pane with the master cursor: rebuilds it
// when the selected group changes, and scrolls the selected thread into
// view (render tints its rows via threadSpan).
func (v *detailView) syncPreview(a *app) {
	if v.tab != tabComments || !commentsSplit(a) {
		return
	}
	key, rootID := "", 0
	if r := v.vp[tabComments].Current(); r != nil {
		switch it := r.Item.(type) {
		case cmtGroup:
			if it.Path == "" {
				key = generalPreviewKey
			} else {
				key = it.Path
			}
		case int:
			id := it
			if root, ok := v.replyRoot[id]; ok {
				id = root // a reply scrolls to its thread
			}
			if t, ok := v.threadFor[id]; ok {
				rootID = id
				if t.Root.Inline != nil {
					key = t.Root.Inline.Path
				} else {
					key = generalPreviewKey
				}
			}
		}
	}
	if key == "" {
		v.cmtPreview = Viewport{}
		v.cmtPreviewKey = ""
		return
	}
	if key != v.cmtPreviewKey {
		d := a.detailFor(v.prID)
		v.cmtPreview = Viewport{}
		v.cmtPreview.Reset(v.commentDetailRows(a, d, key, a.w-commentMasterWidth(a.w)-1))
		v.cmtPreviewKey = key
	}
	if rootID != 0 {
		if span, ok := v.threadSpan[rootID]; ok {
			v.cmtPreview.Top = max(span[0]-1, 0)
			v.cmtPreview.clampTop()
		}
	} else {
		v.cmtPreview.Top = 0
	}
}

// fileDiffFor finds the parsed diff for path (nil when the diff is still
// loading or the file is missing from a truncated diff).
func fileDiffFor(files []FileDiff, path string) *FileDiff {
	for i := range files {
		if files[i].Path() == path || files[i].OldPath == path {
			return &files[i]
		}
	}
	return nil
}

// commentHunkHeader is the Comments-tab hunk banner:
// ─── @@ -a,b +c,d @@ section ─── 💬n ───
func commentHunkHeader(h *Hunk, count, width int) Row {
	head := fmt.Sprintf("─── @@ -%d,%d +%d,%d @@ %s ", h.OldStart, h.OldCount, h.NewStart, h.NewCount,
		truncateWidth(h.Section, width/2))
	tail := fmt.Sprintf(" 💬%d ───", count)
	pad := width - displayWidth(head) - displayWidth(tail) - 1
	if pad < 0 {
		pad = 0
	}
	return textRow(
		Span{head + strings.Repeat("─", pad), styleHunk},
		Span{tail, styleHunk},
	)
}

// unselectableRows strips the cursor stops off diff-line rows: on the
// Comments tab j/k walks threads, not code.
func unselectableRows(rows []Row) []Row {
	for i := range rows {
		rows[i].Selectable = false
		rows[i].Item = nil
	}
	return rows
}

// currentThread returns the thread under the Comments-tab cursor (a cursor
// on a reply row resolves to its enclosing thread).
func (v *detailView) currentThread() (CommentThread, bool) {
	if v.tab != tabComments {
		return CommentThread{}, false
	}
	r := v.vp[v.tab].Current()
	if r == nil || r.Kind != RowComment {
		return CommentThread{}, false
	}
	id, ok := r.Item.(int)
	if !ok {
		return CommentThread{}, false
	}
	if root, ok := v.replyRoot[id]; ok {
		id = root
	}
	t, ok := v.threadFor[id]
	return t, ok
}

// selectedComment returns the exact comment under the Comments-tab cursor —
// a thread root or an individual reply — plus its thread.
func (v *detailView) selectedComment() (Comment, CommentThread, bool) {
	t, ok := v.currentThread()
	if !ok {
		return Comment{}, CommentThread{}, false
	}
	if id, ok := v.vp[v.tab].Current().Item.(int); ok {
		if c, ok := v.commentFor[id]; ok {
			return c, t, true
		}
	}
	return t.Root, t, true // flat layout: only roots are selectable
}

// orderedInlinePaths sorts the inline-comment files so the one holding the
// newest thread comes first (threads within a file are already newest-first
// via buildThreads).
func orderedInlinePaths(byFile map[string][]CommentThread) []string {
	paths := make([]string, 0, len(byFile))
	for p := range byFile {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		ti, tj := byFile[paths[i]][0].Root.CreatedOn, byFile[paths[j]][0].Root.CreatedOn
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return paths[i] < paths[j]
	})
	return paths
}

// openCommentInDiff pushes the built-in diff view scrolled to an inline
// comment thread (Enter on the Comments tab).
func (v *detailView) openCommentInDiff(a *app, rootID int) {
	t, ok := v.inlineThreadFor[rootID]
	if !ok {
		return // general comment: nothing to jump to
	}
	d := a.detailFor(v.prID)
	path := t.Root.Inline.Path
	idx := -1
	for i := range d.diffstat {
		if d.diffstat[i].Path() == path || d.diffstat[i].OldPath() == path {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.status = "diff にファイルが見つかりません: " + path
		return
	}
	dv := newDiffView(a, v.prID, idx)
	dv.showCmt = true // the jump target is a comment box — force them visible
	dv.pendingJump = rootID
	dv.rebuild(a)
	a.push(dv)
}

// statusMark maps a diffstat status to its letter and color.
func statusMark(status string) (string, StyleID) {
	switch status {
	case "added":
		return "A", styleApproved
	case "removed":
		return "D", styleChangesReq
	case "renamed":
		return "R", styleMeta
	default:
		return "M", styleMeta
	}
}

// diffTruncated reports a warning when the API diff looks cut off against
// the diffstat (Bitbucket truncates diffs at 200 files / 8000 lines).
func diffTruncated(d *prDetail) string {
	if d.diffText == "" || d.diffstat == nil {
		return ""
	}
	if len(d.files) < len(d.diffstat) {
		return fmt.Sprintf("diff が切り詰められています (%d/%d ファイル) — o でブラウザ確認", len(d.files), len(d.diffstat))
	}
	return ""
}

func (v *detailView) render(a *app, s *Screen) {
	d := a.detailFor(v.prID)
	if d.pr == nil {
		s.WriteString(1, 0, fmt.Sprintf("#%d を読み込み中…", v.prID), styleDim, a.w)
		paintSeparator(s, 1, a.w)
		return
	}
	pr := d.pr

	s.WriteString(1, 0, fmt.Sprintf("#%d %s", pr.ID, truncateWidth(pr.Title, a.w-14)), styleTitle, a.w)
	s.WriteString(a.w-1-displayWidth(pr.State), 0, pr.State, stateStyle(pr.State), a.w)

	meta := fmt.Sprintf("%s → %s (%s)  by %s  updated %s",
		pr.Source.Branch.Name, pr.Destination.Branch.Name,
		truncateWidth(pr.Source.Commit.Hash, 8),
		pr.Author.Name(), relTime(pr.UpdatedOn, time.Now()))
	s.WriteString(1, 1, truncateWidth(meta, a.w-2), styleDim, a.w)

	// Tab bar. Counts show "…" until their fetch lands — a flash of
	// "Files (0)" reads as an empty PR.
	filesCount, commentsCount := "…", "…"
	if d.diffstat != nil {
		filesCount = fmt.Sprintf("%d", len(d.diffstat))
	}
	if d.comments != nil {
		commentsCount = fmt.Sprintf("%d", commentThreadCount(d.comments))
	}
	labels := []string{
		"1:Overview",
		"2:Files (" + filesCount + ")",
		"3:Comments (" + commentsCount + ")",
	}
	x := 1
	for i, l := range labels {
		style := styleTabInactive
		if i == v.tab {
			style = styleTabActive
		}
		x = s.WriteString(x, 2, l, style, a.w)
		x = s.WriteString(x, 2, "   ", styleNone, a.w)
	}
	paintSeparator(s, 3, a.w)

	rect := Rect{X: 0, Y: 4, W: a.w, H: a.h - 5}
	if v.tab == tabComments && commentsSplit(a) {
		leftW := commentMasterWidth(a.w)
		left := Rect{X: 0, Y: rect.Y, W: leftW, H: rect.H}
		right := Rect{X: leftW + 1, Y: rect.Y, W: a.w - leftW - 1, H: rect.H}
		v.vp[v.tab].Paint(s, left)
		paintVSeparator(s, leftW, rect.Y, rect.Y+rect.H-1)
		v.cmtPreview.Paint(s, right)
		// The reply-target thread lights up so C never posts somewhere invisible.
		if t, ok := v.currentThread(); ok {
			if span, ok := v.threadSpan[t.Root.ID]; ok {
				focusViewportRows(s, &v.cmtPreview, right, span[0], span[1])
			}
		}
		return
	}
	v.vp[v.tab].Paint(s, rect)
	// The reply-target thread lights up so C never posts somewhere invisible.
	if t, ok := v.currentThread(); ok {
		if span, ok := v.threadSpan[t.Root.ID]; ok {
			focusViewportRows(s, &v.vp[v.tab], rect, span[0], span[1])
		}
	}
}

// paintVSeparator draws a vertical │ column from y0 to y1 (inclusive).
func paintVSeparator(s *Screen, x, y0, y1 int) {
	for y := y0; y <= y1; y++ {
		s.Set(x, y, '│', styleDim)
	}
}

func stateStyle(state string) StyleID {
	switch state {
	case "OPEN":
		return styleApproved
	case "DECLINED":
		return styleChangesReq
	default:
		return styleMeta
	}
}

// interceptEsc cancels a pending x-delete instead of popping the view.
func (v *detailView) interceptEsc(a *app) bool {
	if v.pendingDelete == 0 {
		return false
	}
	v.pendingDelete = 0
	a.status = "削除を取り消しました"
	return true
}

func (v *detailView) handle(a *app, k Key) {
	vp := &v.vp[v.tab]
	// A pending x-delete consumes the next key: y confirms, anything else
	// cancels.
	if v.pendingDelete != 0 {
		id := v.pendingDelete
		v.pendingDelete = 0
		if isKey(k, 'y') {
			v.runCommentAction(a, "コメントを削除しました", func(cl *bbClient) error {
				return cl.deleteComment(a.ctx.Workspace, a.ctx.Repo, v.prID, id)
			})
		} else {
			a.status = "削除を取り消しました"
		}
		return
	}
	switch {
	case k.Kind == KeyTab || (k.Kind == KeyRune && k.R == '\t'):
		v.tab = (v.tab + 1) % tabCount
		v.rebuild(a)
	case k.Kind == KeyShiftTab:
		v.tab = (v.tab + tabCount - 1) % tabCount
		v.rebuild(a)
	case isKey(k, 'l') || k.Kind == KeyRight:
		v.tab = (v.tab + 1) % tabCount
		v.rebuild(a)
	case isKey(k, 'h') || k.Kind == KeyLeft:
		v.tab = (v.tab + tabCount - 1) % tabCount
		v.rebuild(a)
	case isKey(k, '1'):
		v.tab = tabOverview
		v.rebuild(a)
	case isKey(k, '2'):
		v.tab = tabFiles
		v.rebuild(a)
	case isKey(k, '3'):
		v.tab = tabComments
		v.rebuild(a)
	case isKey(k, 'j') || k.Kind == KeyDown:
		if vp.Cursor >= 0 {
			vp.MoveCursor(1)
		} else {
			vp.Scroll(1)
		}
		v.syncPreview(a)
	case isKey(k, 'k') || k.Kind == KeyUp:
		if vp.Cursor >= 0 {
			vp.MoveCursor(-1)
		} else {
			vp.Scroll(-1)
		}
		v.syncPreview(a)
	case k.Kind == KeyCtrl && k.R == 'd':
		if v.tab == tabComments && commentsSplit(a) {
			v.cmtPreview.Scroll(v.cmtPreview.H / 2)
		} else {
			vp.Scroll(vp.H / 2)
		}
	case k.Kind == KeyCtrl && k.R == 'u':
		if v.tab == tabComments && commentsSplit(a) {
			v.cmtPreview.Scroll(-v.cmtPreview.H / 2)
		} else {
			vp.Scroll(-vp.H / 2)
		}
	case isKey(k, 'g'):
		vp.Top = 0
		if i := vp.nextSelectable(0, 1); i >= 0 {
			vp.Cursor = i
		}
		v.syncPreview(a)
	case isKey(k, 'G'):
		if i := vp.nextSelectable(len(vp.Rows)-1, -1); i >= 0 {
			vp.Cursor = i
			vp.EnsureVisible()
		}
		v.syncPreview(a)
	case k.Kind == KeyEnter:
		switch v.tab {
		case tabFiles:
			if r := vp.Current(); r != nil && r.Kind == RowFile {
				if a.cfg.FilesEnter == "builtin" {
					a.push(newDiffView(a, v.prID, r.Item.(int)))
				} else {
					d := a.detailFor(v.prID)
					openInDiffTool(a, v.prID, d.diffstat[r.Item.(int)].Path())
				}
			}
		case tabComments:
			if r := vp.Current(); r != nil {
				switch it := r.Item.(type) {
				case int:
					if root, ok := v.replyRoot[it]; ok {
						it = root // a reply jumps to its thread
					}
					v.openCommentInDiff(a, it)
				case cmtGroup:
					// A file header jumps to its first (newest) thread; the
					// general group has no code to jump to.
					if id, ok := v.cmtFirstThread[it.Path]; ok {
						v.openCommentInDiff(a, id)
					}
				}
			}
		}
	case isKey(k, 'v'):
		// The built-in diff view (inline comments) stays reachable even
		// when Enter launches the external tool.
		if v.tab == tabFiles {
			if r := vp.Current(); r != nil && r.Kind == RowFile {
				a.push(newDiffView(a, v.prID, r.Item.(int)))
			}
		}
	case isKey(k, 'r'):
		v.load(a, true)
	case isKey(k, 'o'):
		d := a.detailFor(v.prID)
		if d.pr != nil {
			openInBrowser(a, d.pr.WebURL())
		}
	case isKey(k, 'y'):
		d := a.detailFor(v.prID)
		if d.pr != nil {
			copyToClipboard(a, d.pr.WebURL())
		}
	case isKey(k, 'b'):
		d := a.detailFor(v.prID)
		if d.pr != nil {
			copyToClipboard(a, d.pr.Source.Branch.Name)
		}
	case isKey(k, 'C'):
		// Reply to the exact comment under the Comments-tab cursor (a reply
		// row nests under that reply), otherwise a new general PR comment.
		// A file header is ambiguous — ask for a thread.
		if v.tab == tabComments {
			if r := vp.Current(); r != nil {
				if g, ok := r.Item.(cmtGroup); ok && g.Path != "" {
					a.status = "返信するスレッドを j/k で選択してください"
					return
				}
			}
		}
		parent, target := 0, ""
		if c, t, ok := v.selectedComment(); ok {
			parent = c.ID
			target = replyTargetFor(c, t)
		}
		openCommentEditor(a, v.prID, nil, parent, target)
	case isKey(k, 'x') && v.tab == tabComments:
		if c, _, ok := v.selectedComment(); ok {
			if c.Deleted {
				a.status = "削除済みコメントです"
			} else {
				v.pendingDelete = c.ID
				a.status = fmt.Sprintf("%s のコメントを削除しますか？ y で確定 / 他キーで取消", c.User.Name())
			}
		}
	case isKey(k, 's') && v.tab == tabComments:
		// Resolve toggles on the thread root; the API only accepts inline
		// (diff) threads.
		if _, t, ok := v.selectedComment(); ok {
			root := t.Root
			switch {
			case root.Inline == nil:
				a.status = "一般コメントは resolve できません（インラインスレッドのみ）"
			case root.Resolved():
				v.runCommentAction(a, "スレッドを再オープンしました", func(cl *bbClient) error {
					return cl.unresolveComment(a.ctx.Workspace, a.ctx.Repo, v.prID, root.ID)
				})
			default:
				v.runCommentAction(a, "スレッドを resolve しました", func(cl *bbClient) error {
					return cl.resolveComment(a.ctx.Workspace, a.ctx.Repo, v.prID, root.ID)
				})
			}
		}
	case isKey(k, 'D'):
		openInDiffTool(a, v.prID, "")
	case isKey(k, '?'):
		a.push(helpView{ctx: "detail"})
	}
}

func (v *detailView) footer(a *app) string {
	base := "Tab/h/l:タブ  j/k:移動  "
	if v.tab == tabFiles {
		if a.cfg.FilesEnter == "builtin" {
			base += "Enter:diff  "
		} else {
			base += "Enter:diffツール  v:内蔵diff  "
		}
	}
	cKey := "C:コメント"
	if v.tab == tabComments {
		base += "Enter:コードへ  "
		if commentsSplit(a) {
			base += "Ctrl-d/u:プレビュー  "
		}
		sKey := "s:resolve"
		if t, ok := v.currentThread(); ok && t.Root.Resolved() {
			sKey = "s:再オープン"
		}
		base += "x:削除  " + sKey + "  "
		if c, t, ok := v.selectedComment(); ok {
			cKey = "C:" + replyTargetFor(c, t)
		}
	}
	return base + cKey + "  D:PR全体diff  r:再読込  o:ブラウザ  q:戻る"
}

// replyTarget names where a reply will land: "返信→tanaka L15".
func replyTarget(t CommentThread) string {
	return replyTargetFor(t.Root, t)
}

// replyTargetFor is replyTarget for a specific comment inside the thread
// (nested replies name the reply's author, not the root's).
func replyTargetFor(c Comment, t CommentThread) string {
	s := "返信→" + c.User.Name()
	if label := t.lineLabel(); label != "" {
		s += " " + label
	}
	return s
}

// runCommentAction fires one comment mutation (delete / resolve / reopen)
// off the main loop and refetches the PR's comments on success — the same
// reload path the comment editor uses.
func (v *detailView) runCommentAction(a *app, okMsg string, call func(*bbClient) error) {
	prID := v.prID
	client := a.client
	a.fetch(fmt.Sprintf("comment action %d", prID), func() (func(*app), error) {
		if err := call(client); err != nil {
			return nil, err
		}
		return func(a *app) {
			a.status = okMsg
			a.detailFor(prID).comments = nil
			v.load(a, false) // refetches only the comments
		}, nil
	})
}

func joinComma(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
