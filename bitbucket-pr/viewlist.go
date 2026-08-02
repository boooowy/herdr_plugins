package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// prStates is the `s` key's filter cycle.
var prStates = []string{"OPEN", "MERGED", "DECLINED", "SUPERSEDED"}

const (
	listReviewerLimit   = 4
	listTitleMaxWidth   = 64
	listIDWidth         = 8
	listCommentGap      = 2
	listCommentWidth    = 7
	listRoleWidth       = 3
	listAuthorNameWidth = 16
	listUpdatedWidth    = 10
	listItemDivider     = "  ────  "
)

// listView is the PR list: one selectable row per PR plus a muted meta row
// with the branch flow underneath.
type listView struct {
	vp Viewport

	// search is the `/` filter over the pages fetched so far; shown counts
	// the PRs it lets through (for the header's "12/48件").
	search searchBox
	shown  int
}

func newListView(a *app) *listView {
	v := &listView{}
	a.prsState = a.cfg.DefaultState
	v.load(a, false)
	return v
}

// load fetches the first page of the PR list for the current state filter
// (unless cached); later pages arrive on demand via loadMore.
func (v *listView) load(a *app, force bool) {
	state := a.prsState
	if !force && a.prs != nil {
		v.rebuild(a)
		return
	}
	a.prs, a.prsNext, a.prsErr = nil, "", ""
	a.prsMoreLoading = false
	a.prsGen++
	gen := a.prsGen
	if !force {
		// Paint the previous fetch instantly while the fresh page loads
		// (prsNext stays "" so loadMore waits for real data).
		if cached := loadPRCache(a.ctx.Workspace, a.ctx.Repo, state); len(cached) > 0 {
			a.prs = cached
		}
	}
	first := a.client.listPRsFirstURL(a.ctx.Workspace, a.ctx.Repo, state)
	// fetch bumps a.loading before the rebuild below runs, so the empty
	// placeholder reads 読み込み中 instead of claiming there are no PRs.
	a.fetch("prs "+state, func() (func(*app), error) {
		prs, next, err := a.client.listPRsPage(first)
		return func(a *app) {
			if a.prsGen != gen {
				return // superseded by a reload or state switch
			}
			if err != nil {
				// Surface the failure in the list body too: the footer
				// status vanishes on the next keypress, and a permanent
				// 読み込み中 placeholder would read as "cannot switch".
				a.prsErr = err.Error()
				a.status = a.prsErr
				v.rebuild(a)
				return
			}
			if prs == nil {
				prs = []PullRequest{} // non-nil marks "loaded, empty"
			}
			a.prsErr = ""
			a.prs = prs
			a.prsNext = next
			savePRCache(a.ctx.Workspace, a.ctx.Repo, state, prs)
			v.rebuild(a)
		}, nil
	})
	v.rebuild(a)
}

// loadMore appends the next PR-list page when the cursor nears the end of
// what's loaded so far.
func (v *listView) loadMore(a *app) {
	if a.prsNext == "" || a.prsMoreLoading {
		return
	}
	a.prsMoreLoading = true
	gen := a.prsGen
	next := a.prsNext
	a.fetch("prs more", func() (func(*app), error) {
		prs, nn, err := a.client.listPRsPage(next)
		if err != nil {
			// Clear the in-flight flag ourselves so the next cursor move can
			// retry; fetch's error path only sets a.status.
			return func(a *app) {
				if a.prsGen == gen {
					a.prsMoreLoading = false
				}
				a.status = err.Error()
			}, nil
		}
		return func(a *app) {
			if a.prsGen != gen {
				return
			}
			a.prsMoreLoading = false
			a.prs = append(a.prs, prs...)
			a.prsNext = nn
			v.rebuild(a)
		}, nil
	})
}

// rebuild regenerates the viewport rows from the cached list, honouring the
// `/` filter. Row.Item stays the index into a.prs — Enter/o/y/b resolve the
// PR through it, so filtered rows must not be renumbered.
func (v *listView) rebuild(a *app) {
	now := time.Now()
	q := v.search.query()
	rows := make([]Row, 0, len(a.prs)*2)
	v.shown = 0
	for i := range a.prs {
		pr := &a.prs[i]
		if !matchQuery(q, "#"+strconv.Itoa(pr.ID), pr.Title, pr.Author.Name(),
			pr.Source.Branch.Name, pr.Destination.Branch.Name) {
			continue
		}
		v.shown++
		author := padRight(truncateWidth(pr.Author.Name(), listAuthorNameWidth), listAuthorNameWidth)
		updated := padRight(truncateWidth(relTime(pr.UpdatedOn, now), listUpdatedWidth), listUpdatedWidth)
		commentCount := ""
		if pr.CommentCount > 0 {
			commentCount = fmt.Sprintf("💬 %d", pr.CommentCount)
		}
		titleW := listTitleWidth(a)
		spans := []Span{
			{fmt.Sprintf(" #%-5d ", pr.ID), styleMeta},
			{padRight(truncateWidth(pr.Title, titleW), titleW), styleNone},
			{"A: ", styleDim},
		}
		spans, avatars := appendAccountName(spans, pr.Author, author, styleDim, styleDim, a.avatarsEnabled())
		spans = append(spans,
			Span{"  " + updated, styleDim},
			Span{strings.Repeat(" ", listCommentGap) + padRight(truncateWidth(commentCount, listCommentWidth), listCommentWidth), styleNone},
		)
		prRow := row(RowPR, i, true, spans...)
		prRow.Avatars = avatars
		prRow.CursorRows = 2
		rows = append(rows, prRow)
		rows = append(rows, listMetaRow(a, pr))
	}
	if v.shown == 0 {
		switch {
		case a.loading > 0:
			rows = append(rows, textRow(Span{" 読み込み中…", styleDim}))
		case a.prsErr != "":
			rows = append(rows, textRow(Span{" 取得に失敗しました（r で再読込）: " + a.prsErr, styleDim}))
		case q != "":
			rows = append(rows, textRow(Span{" (一致する PR はありません)", styleDim}))
		default:
			rows = append(rows, textRow(Span{" (" + a.prsState + " のPRはありません)", styleDim}))
		}
	}
	v.vp.Reset(rows)
	v.fillFilter(a)
}

func listTitleWidth(a *app) int {
	avatarWidth := 0
	if a.avatarsEnabled() {
		avatarWidth = compactAvatarCols
	}
	width := a.w - listIDWidth - listRoleWidth - avatarWidth - listAuthorNameWidth - 2 - listUpdatedWidth - listCommentGap - listCommentWidth
	if width > listTitleMaxWidth {
		width = listTitleMaxWidth
	}
	if width < 1 {
		width = 1
	}
	return width
}

func accountKey(account Account) string {
	if account.AccountID != "" {
		return "id:" + account.AccountID
	}
	if account.UUID != "" {
		return "uuid:" + account.UUID
	}
	if name := account.Name(); name != "Unknown" {
		return "name:" + name
	}
	return ""
}

func listMetaRow(a *app, pr *PullRequest) Row {
	branch := pr.Source.Branch.Name + " → " + pr.Destination.Branch.Name
	titleWidth := listTitleWidth(a)
	if !a.avatarsEnabled() {
		return textRow(
			Span{listItemDivider, styleDim},
			Span{padRight(truncateWidth(branch, titleWidth), titleWidth), styleDim},
		)
	}

	reviewers := pullRequestReviewers(pr)
	var candidates []reviewerInfo
	for _, reviewer := range reviewers {
		if reviewer.User.AvatarURL() != "" {
			candidates = append(candidates, reviewer)
		}
	}
	shown := len(candidates)
	if shown > listReviewerLimit {
		shown = listReviewerLimit
	}

	spans := []Span{
		{listItemDivider, styleDim},
		{padRight(truncateWidth(branch, titleWidth), titleWidth), styleDim},
		{"R: ", styleDim},
	}
	available := a.w - spansWidth(spans)
	if available < 0 {
		available = 0
	}
	suffix := ""
	for shown >= 0 {
		hidden := len(reviewers) - shown
		suffix = ""
		if hidden > 0 {
			suffix = fmt.Sprintf(" +%d", hidden)
		}
		if shown*compactAvatarCols+displayWidth(suffix) <= available {
			break
		}
		shown--
	}
	if shown < 0 {
		shown = 0
		suffix = ""
	}

	row := textRow(spans...)
	for _, reviewer := range candidates[:shown] {
		col := spansWidth(row.Spans)
		row.Spans = append(row.Spans, Span{strings.Repeat(" ", compactAvatarCols), styleDim})
		avatar := RowAvatar{
			URL: reviewer.User.AvatarURL(), AccountID: reviewer.User.AccountID,
			Col: col, Cols: compactAvatarCols, Rows: 1,
		}
		if reviewer.Status == reviewerApproved {
			avatar.Badge = AvatarBadgeApproved
		}
		row.Avatars = append(row.Avatars, avatar)
	}
	if suffix != "" {
		row.Spans = append(row.Spans, Span{suffix, styleDim})
	}
	return row
}

// fillFilter keeps pulling pages while a filter is on: the filter only sees
// what has been fetched, and the cursor-near-the-end trigger in handle never
// fires when few rows match. Each arriving page rebuilds and re-checks, so
// this stops on its own once the screen is full or the list is exhausted.
func (v *listView) fillFilter(a *app) {
	if v.search.query() == "" || a.prsNext == "" {
		return
	}
	if v.shown*2 < v.vp.H+2 { // two rows per PR
		v.loadMore(a)
	}
}

func (v *listView) render(a *app, s *Screen) {
	x := s.WriteString(1, 0, fmt.Sprintf("Bitbucket PRs — %s/%s   ", a.ctx.Workspace, a.ctx.Repo), styleTitle, a.w)
	for _, st := range prStates {
		style := styleTabInactive
		if st == a.prsState {
			style = styleTabActive
		}
		x = s.WriteString(x, 0, st, style, a.w)
		x = s.WriteString(x, 0, "  ", styleNone, a.w)
	}
	q := v.search.query()
	count := fmt.Sprintf("%d件", len(a.prs))
	if q != "" {
		count = fmt.Sprintf("%d/%d件", v.shown, len(a.prs))
	}
	if a.prsNext != "" {
		count += "+" // more pages on the server, fetched as the cursor nears the end
	}
	x = s.WriteString(x, 0, count, styleDim, a.w)
	if q != "" {
		s.WriteString(x, 0, "  /"+q, styleMeta, a.w)
	}
	paintSeparator(s, 1, a.w)
	v.vp.Paint(s, a.contentRect())
}

// grabsKeys routes every key to handle while the filter prompt is open.
func (v *listView) grabsKeys() bool { return v.search.active }

// interceptEsc clears an applied filter instead of leaving the viewer.
func (v *listView) interceptEsc(a *app) bool {
	if v.search.query() == "" {
		return false
	}
	v.search.clear()
	v.rebuild(a)
	a.status = "絞り込みを解除しました"
	return true
}

func (v *listView) handle(a *app, k Key) {
	if v.search.active {
		if v.search.handle(k) {
			v.rebuild(a)
		}
		return
	}
	switch {
	case isKey(k, '/'):
		v.search.open()
	case isKey(k, 'j') || k.Kind == KeyDown:
		v.vp.MoveCursor(1)
	case isKey(k, 'k') || k.Kind == KeyUp:
		v.vp.MoveCursor(-1)
	case isKey(k, 'g'):
		v.vp.Cursor = -1
		v.vp.Top = 0
		if i := v.vp.nextSelectable(0, 1); i >= 0 {
			v.vp.Cursor = i
		}
	case isKey(k, 'G'):
		if i := v.vp.nextSelectable(len(v.vp.Rows)-1, -1); i >= 0 {
			v.vp.Cursor = i
			v.vp.EnsureVisible()
		}
	case k.Kind == KeyCtrl && k.R == 'd':
		v.vp.Scroll(v.vp.H / 2)
	case k.Kind == KeyCtrl && k.R == 'u':
		v.vp.Scroll(-v.vp.H / 2)
	case k.Kind == KeyEnter:
		if r := v.vp.Current(); r != nil && r.Kind == RowPR {
			a.push(newDetailView(a, a.prs[r.Item.(int)].ID))
		}
	case isKey(k, 's') || isKey(k, 'l') || k.Kind == KeyTab || k.Kind == KeyRight ||
		(k.Kind == KeyRune && k.R == '\t'):
		v.cycleState(a, 1)
	case isKey(k, 'h') || k.Kind == KeyShiftTab || k.Kind == KeyLeft:
		v.cycleState(a, -1)
	case isKey(k, 'r') || isKey(k, 'R'):
		v.load(a, true)
	case isKey(k, 'o'):
		if r := v.vp.Current(); r != nil && r.Kind == RowPR {
			openInBrowser(a, a.prs[r.Item.(int)].WebURL())
		}
	case isKey(k, 'y'):
		if r := v.vp.Current(); r != nil && r.Kind == RowPR {
			copyToClipboard(a, a.prs[r.Item.(int)].WebURL())
		}
	case isKey(k, 'b'):
		if r := v.vp.Current(); r != nil && r.Kind == RowPR {
			copyToClipboard(a, a.prs[r.Item.(int)].Source.Branch.Name)
		}
	case isKey(k, '?'):
		a.push(helpView{ctx: "list"})
	}
	// Cursor within ~5 PRs (2 rows each) of the end: prefetch the next page.
	if a.prsNext != "" && v.vp.Cursor >= len(v.vp.Rows)-10 {
		v.loadMore(a)
	}
}

// cycleState moves the state filter forward/backward and reloads the list.
func (v *listView) cycleState(a *app, dir int) {
	for i, st := range prStates {
		if st == a.prsState {
			a.prsState = prStates[(i+dir+len(prStates))%len(prStates)]
			break
		}
	}
	a.prs = nil
	v.load(a, false)
}

func (v *listView) footer(a *app) string {
	if v.search.active {
		return v.search.prompt()
	}
	if q := v.search.query(); q != "" {
		// q clears the filter here too (the app routes q and Esc alike), so
		// the hint must not promise that q quits.
		return "j/k:移動  Enter:開く  /:" + q + "  q/Esc:絞込解除  r:再読込  o:ブラウザ"
	}
	return "j/k:移動  Enter:開く  Tab/s:state切替  /:絞り込み  y:URL  b:ブランチ  r:再読込  q:終了"
}

// isKey reports whether k is the plain rune r.
func isKey(k Key, r rune) bool { return k.Kind == KeyRune && k.R == r }

// paintSeparator draws a full-width dim horizontal line at row y.
func paintSeparator(s *Screen, y, w int) {
	for x := 0; x < w; x++ {
		s.Set(x, y, '─', styleDim)
	}
}
