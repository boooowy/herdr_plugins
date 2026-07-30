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

const descPreviewLines = 8

// detailView shows one PR with three tabs: Overview / Files / Comments
// (atlas.nvim's five tabs collapsed — Conversation/Review duplicated
// comments there).
type detailView struct {
	prID         int
	tab          int
	vp           [tabCount]Viewport
	descExpanded bool

	// inlineThreadFor maps a comment root ID to its inline thread — how the
	// Comments tab's Enter finds the file/line to jump to in the diff view.
	inlineThreadFor map[int]CommentThread
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
			return func(a *app) { a.detailFor(id).diffstat = st; v.rebuild(a) }, nil
		})
	}
	if d.comments == nil {
		a.fetch(fmt.Sprintf("comments %d", id), func() (func(*app), error) {
			cs, err := a.client.comments(a.ctx.Workspace, a.ctx.Repo, id)
			if err != nil {
				return nil, err
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
		v.vp[v.tab].Reset(v.commentRows(a, d))
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
		lines := mdStyledLines(desc, a.w-3, styleNone)
		truncated := !v.descExpanded && len(lines) > descPreviewLines
		if truncated {
			lines = lines[:descPreviewLines]
		}
		for _, spans := range lines {
			rows = append(rows, textRow(append([]Span{{" ", styleNone}}, spans...)...))
		}
		if truncated {
			rows = append(rows, textRow(Span{" … (e で全文表示 / m でMarkdownビューア)", styleDim}))
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

	if len(general) > 0 {
		section(fmt.Sprintf("PR コメント (%d)", len(general)))
		for _, t := range general {
			rows = append(rows, threadRows(t, a.w, false, now, "")...)
		}
	}
	if inlineTotal > 0 {
		section(fmt.Sprintf("インラインコメント (%d)", inlineTotal))
		for _, path := range orderedInlinePaths(byFile) {
			rows = append(rows, textRow(Span{" 📄 ", styleNone}, Span{path, styleTitle}), textRow())
			for _, t := range byFile[path] {
				v.inlineThreadFor[t.Root.ID] = t
				rows = append(rows, threadRows(t, a.w, false, now, t.lineLabel())...)
			}
		}
	}
	return rows
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

	// Tab bar.
	labels := []string{
		"1:Overview",
		fmt.Sprintf("2:Files (%d)", len(d.diffstat)),
		fmt.Sprintf("3:Comments (%d)", commentThreadCount(d.comments)),
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

	v.vp[v.tab].Paint(s, Rect{X: 0, Y: 4, W: a.w, H: a.h - 5})
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

func (v *detailView) handle(a *app, k Key) {
	vp := &v.vp[v.tab]
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
	case isKey(k, 'k') || k.Kind == KeyUp:
		if vp.Cursor >= 0 {
			vp.MoveCursor(-1)
		} else {
			vp.Scroll(-1)
		}
	case k.Kind == KeyCtrl && k.R == 'd':
		vp.Scroll(vp.H / 2)
	case k.Kind == KeyCtrl && k.R == 'u':
		vp.Scroll(-vp.H / 2)
	case isKey(k, 'g'):
		vp.Top = 0
		if i := vp.nextSelectable(0, 1); i >= 0 {
			vp.Cursor = i
		}
	case isKey(k, 'G'):
		if i := vp.nextSelectable(len(vp.Rows)-1, -1); i >= 0 {
			vp.Cursor = i
			vp.EnsureVisible()
		}
	case isKey(k, 'e'):
		v.descExpanded = !v.descExpanded
		v.rebuild(a)
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
			if r := vp.Current(); r != nil && r.Kind == RowComment {
				if id, ok := r.Item.(int); ok {
					v.openCommentInDiff(a, id)
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
	case isKey(k, 'm'):
		v.openMarkdown(a)
	case isKey(k, 'C'):
		// Reply when the cursor is on a Comments-tab thread, otherwise a new
		// general PR comment.
		parent := 0
		if v.tab == tabComments {
			if r := vp.Current(); r != nil && r.Kind == RowComment {
				if id, ok := r.Item.(int); ok {
					parent = id
				}
			}
		}
		openCommentEditor(a, v.prID, nil, parent)
	case isKey(k, 'D'):
		openInDiffTool(a, v.prID, "")
	case isKey(k, '?'):
		a.push(helpView{ctx: "detail"})
	}
}

// openMarkdown shows the PR description — or, on the Comments tab, the
// selected thread — in the external markdown viewer popup.
func (v *detailView) openMarkdown(a *app) {
	d := a.detailFor(v.prID)
	if v.tab == tabComments {
		if r := v.vp[v.tab].Current(); r != nil && r.Kind == RowComment {
			if id, ok := r.Item.(int); ok {
				for _, t := range buildThreads(d.comments, nil) {
					if t.Root.ID == id {
						openMarkdownView(a, v.prID, threadMarkdown(t))
						return
					}
				}
			}
		}
	}
	if d.pr != nil {
		openMarkdownView(a, v.prID, prMarkdown(d.pr))
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
	if v.tab == tabOverview {
		base += "e:全文  "
	}
	if v.tab == tabComments {
		base += "Enter:コードへ  "
	}
	return base + "m:MD表示  C:コメント  D:PR全体diff  r:再読込  o:ブラウザ  q:戻る"
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
