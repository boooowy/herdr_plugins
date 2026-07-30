package main

import (
	"fmt"
	"time"
)

// prStates is the `s` key's filter cycle.
var prStates = []string{"OPEN", "MERGED", "DECLINED"}

// listView is the PR list: one selectable row per PR plus a muted meta row
// with the branch flow underneath.
type listView struct {
	vp Viewport
}

func newListView(a *app) *listView {
	v := &listView{}
	a.prsState = a.cfg.DefaultState
	v.load(a, false)
	return v
}

// load fetches the PR list for the current state filter (unless cached).
func (v *listView) load(a *app, force bool) {
	state := a.prsState
	if !force && a.prs != nil {
		v.rebuild(a)
		return
	}
	a.prs = nil
	v.rebuild(a)
	a.fetch("prs "+state, func() (func(*app), error) {
		prs, err := a.client.listPRs(a.ctx.Workspace, a.ctx.Repo, state)
		if err != nil {
			return nil, err
		}
		return func(a *app) {
			if a.prsState != state {
				return // the filter moved on while we were fetching
			}
			a.prs = prs
			v.rebuild(a)
		}, nil
	})
}

// rebuild regenerates the viewport rows from the cached list.
func (v *listView) rebuild(a *app) {
	now := time.Now()
	rows := make([]Row, 0, len(a.prs)*2)
	for i := range a.prs {
		pr := &a.prs[i]
		right := fmt.Sprintf(" %s  %s", truncateWidth(pr.Author.Name(), 16), relTime(pr.UpdatedOn, now))
		badge := ""
		if pr.CommentCount > 0 {
			badge = fmt.Sprintf(" 💬%d", pr.CommentCount)
		}
		titleW := a.w - 2 - 7 - displayWidth(badge) - displayWidth(right)
		rows = append(rows, row(RowPR, i, true,
			Span{fmt.Sprintf(" #%-5d ", pr.ID), styleMeta},
			Span{padRight(pr.Title, titleW), styleNone},
			Span{badge, styleDim},
			Span{right, styleDim},
		))
		rows = append(rows, textRow(
			Span{"        ", styleNone},
			Span{pr.Source.Branch.Name + " → " + pr.Destination.Branch.Name, styleDim},
		))
	}
	if len(a.prs) == 0 && a.loading == 0 {
		rows = append(rows, textRow(Span{" (" + a.prsState + " のPRはありません)", styleDim}))
	}
	v.vp.Reset(rows)
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
	s.WriteString(x, 0, fmt.Sprintf("%d件", len(a.prs)), styleDim, a.w)
	paintSeparator(s, 1, a.w)
	v.vp.Paint(s, a.contentRect())
}

func (v *listView) handle(a *app, k Key) {
	switch {
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
	case isKey(k, 's'):
		for i, st := range prStates {
			if st == a.prsState {
				a.prsState = prStates[(i+1)%len(prStates)]
				break
			}
		}
		a.prs = nil
		v.load(a, false)
	case isKey(k, 'r') || isKey(k, 'R'):
		v.load(a, true)
	case isKey(k, 'o'):
		if r := v.vp.Current(); r != nil && r.Kind == RowPR {
			openInBrowser(a, a.prs[r.Item.(int)].WebURL())
		}
	case isKey(k, '?'):
		a.push(helpView{})
	}
}

func (v *listView) footer(a *app) string {
	return "j/k:移動  Enter:開く  s:state切替  r:再読込  o:ブラウザ  q:終了"
}

// isKey reports whether k is the plain rune r.
func isKey(k Key, r rune) bool { return k.Kind == KeyRune && k.R == r }

// paintSeparator draws a full-width dim horizontal line at row y.
func paintSeparator(s *Screen, y, w int) {
	for x := 0; x < w; x++ {
		s.Set(x, y, '─', styleDim)
	}
}
