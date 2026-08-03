package main

import (
	"fmt"
	"strings"
	"time"
)

const (
	pickerSlugWidth    = 28
	pickerUpdatedWidth = 10
	// pickerFillCap stops the filter's automatic page chase on huge
	// workspaces; past this the user narrows the query instead.
	pickerFillCap = 500
)

// repoPickerView is the workspace repository picker: search the list, pick a
// repo, land in its PR list. It sits at the bottom of the view stack in
// picker mode, so q/Esc from the PR list comes back here to switch repos.
type repoPickerView struct {
	vp     Viewport
	search searchBox
	shown  int

	// pendingClone is the repository awaiting the y/n clone confirmation
	// (a copy — reloads may swap a.repos underneath); nil = none.
	pendingClone *Repository

	// Server-side name search: kicks in while a filter is on but the full
	// list has not been paged in, so matches beyond the fill cap still
	// appear. remoteResults holds the extra hits for remoteQuery; rows for
	// them carry negative Item values (see repoAt).
	remoteQuery    string
	remoteResults  []Repository
	remoteInFlight bool
}

func newRepoPickerView(a *app) *repoPickerView {
	v := &repoPickerView{}
	v.scanRoots(a)
	v.load(a, false)
	return v
}

// repoAt resolves a row Item to its repository: non-negative values index
// a.repos, negative ones the remote search results (-1 → remoteResults[0]).
func (v *repoPickerView) repoAt(a *app, item int) *Repository {
	if item >= 0 {
		if item < len(a.repos) {
			return &a.repos[item]
		}
		return nil
	}
	if i := -item - 1; i < len(v.remoteResults) {
		return &v.remoteResults[i]
	}
	return nil
}

// scanRoots merges the repo_roots checkout scan into localDirs off the main
// loop (the roots may sit on a slow disk; the picker paints immediately from
// the persisted index).
func (v *repoPickerView) scanRoots(a *app) {
	roots := a.cfg.RepoRoots
	if len(roots) == 0 {
		return
	}
	a.fetch("scan repo_roots", func() (func(*app), error) {
		found := scanRepoRoots(roots)
		return func(a *app) {
			if len(found) == 0 {
				return
			}
			for key, dir := range found {
				if _, ok := a.localDirs[key]; !ok {
					a.localDirs[key] = dir
				}
			}
			v.rebuild(a) // refresh the checkout marks
		}, nil
	})
}

// load fetches the first page of the workspace repository list (painting the
// disk cache first); later pages arrive on demand via loadMore.
func (v *repoPickerView) load(a *app, force bool) {
	ws := a.ctx.Workspace
	if !force && a.repos != nil {
		v.rebuild(a)
		return
	}
	a.repos, a.reposNext, a.reposErr = nil, "", ""
	a.reposMoreLoading = false
	a.reposGen++
	gen := a.reposGen
	if !force {
		if cached := loadRepoCache(ws); len(cached) > 0 {
			a.repos = cached
		}
	}
	first := a.client.listReposFirstURL(ws, "")
	a.fetch("repos", func() (func(*app), error) {
		repos, next, err := a.client.listReposPage(first)
		return func(a *app) {
			if a.reposGen != gen {
				return
			}
			if err != nil {
				a.reposErr = err.Error()
				a.status = a.reposErr
				v.rebuild(a)
				return
			}
			if repos == nil {
				repos = []Repository{}
			}
			a.reposErr = ""
			a.repos = repos
			a.reposNext = next
			saveRepoCache(ws, repos)
			v.rebuild(a)
		}, nil
	})
	v.rebuild(a)
}

// loadMore appends the next repository page when the cursor nears the end.
func (v *repoPickerView) loadMore(a *app) {
	if a.reposNext == "" || a.reposMoreLoading {
		return
	}
	a.reposMoreLoading = true
	gen := a.reposGen
	next := a.reposNext
	a.fetch("repos more", func() (func(*app), error) {
		repos, nn, err := a.client.listReposPage(next)
		if err != nil {
			return func(a *app) {
				if a.reposGen == gen {
					a.reposMoreLoading = false
				}
				a.status = err.Error()
			}, nil
		}
		return func(a *app) {
			if a.reposGen != gen {
				return
			}
			a.reposMoreLoading = false
			a.repos = append(a.repos, repos...)
			a.reposNext = nn
			v.rebuild(a)
		}, nil
	})
}

// rebuild regenerates the rows from the fetched list, honouring the `/`
// filter. Row.Item is the index into a.repos (negative: remoteResults).
func (v *repoPickerView) rebuild(a *app) {
	now := time.Now()
	q := v.search.query()
	rows := make([]Row, 0, len(a.repos)*2)
	v.shown = 0
	for i := range a.repos {
		r := &a.repos[i]
		if !matchQuery(q, r.Slug, r.Name, r.Description) {
			continue
		}
		v.shown++
		rows = append(rows, v.repoRow(a, r, i, now), v.metaRow(a, r))
	}
	if q != "" {
		// Server-side hits beyond the locally paged-in list.
		local := make(map[string]bool, len(a.repos))
		for i := range a.repos {
			local[a.repos[i].FullName] = true
		}
		for i := range v.remoteResults {
			r := &v.remoteResults[i]
			if local[r.FullName] || !matchQuery(q, r.Slug, r.Name, r.Description) {
				continue
			}
			v.shown++
			rows = append(rows, v.repoRow(a, r, -i-1, now), v.metaRow(a, r))
		}
	}
	if v.shown == 0 {
		switch {
		case a.loading > 0:
			rows = append(rows, textRow(Span{" 読み込み中…", styleDim}))
		case a.reposErr != "":
			rows = append(rows, textRow(Span{" 取得に失敗しました（r で再読込）: " + a.reposErr, styleDim}))
		case q != "":
			rows = append(rows, textRow(Span{" (一致するリポジトリはありません)", styleDim}))
		default:
			rows = append(rows, textRow(Span{" (リポジトリがありません)", styleDim}))
		}
	}
	v.vp.Reset(rows)
	v.fillFilter(a)
	v.maybeRemoteSearch(a)
}

// repoRow is the first line of one repository entry.
func (v *repoPickerView) repoRow(a *app, r *Repository, item int, now time.Time) Row {
	descW := pickerDescWidth(a)
	desc := r.Description
	if desc == "" {
		desc = "(説明なし)"
	}
	desc = strings.ReplaceAll(desc, "\n", " ")
	repoRow := row(RowRepo, item, true,
		Span{" ", styleNone},
		Span{padRight(truncateWidth(r.Slug, pickerSlugWidth), pickerSlugWidth), styleNone},
		Span{"  ", styleNone},
		Span{padRight(truncateWidth(desc, descW), descW), styleDim},
		Span{"  " + padRight(truncateWidth(relTime(r.UpdatedOn, now), pickerUpdatedWidth), pickerUpdatedWidth), styleDim},
	)
	repoRow.CursorRows = 2
	return repoRow
}

// metaRow is the second, muted line under each repo: its local checkout.
func (v *repoPickerView) metaRow(a *app, r *Repository) Row {
	key := repoDirKey(a.ctx.Workspace, r.Slug)
	switch {
	case a.cloneInFlight[r.FullName]:
		return textRow(
			Span{listItemDivider, styleDim},
			Span{"◌ clone中… → " + shortenHome(cloneDestFor(a.cfg, r.Slug)), styleMeta},
		)
	case a.localDirs[key] != "":
		return textRow(
			Span{listItemDivider, styleDim},
			Span{"● ", styleApproved},
			Span{shortenHome(a.localDirs[key]), styleDim},
		)
	default:
		return textRow(
			Span{listItemDivider, styleDim},
			Span{"○ ローカルなし（C でclone）", styleDim},
		)
	}
}

// fillFilter keeps pulling pages while a filter is on (see listView), capped
// so a huge workspace cannot be paged through by a single narrow query.
func (v *repoPickerView) fillFilter(a *app) {
	if v.search.query() == "" || a.reposNext == "" || len(a.repos) >= pickerFillCap {
		return
	}
	if v.shown*2 < v.vp.H+2 {
		v.loadMore(a)
	}
}

// maybeRemoteSearch asks the server for name matches while the local list is
// incomplete (more pages, or the fill cap stopped the chase). One request in
// flight at a time; a query typed past it is chased once it lands.
func (v *repoPickerView) maybeRemoteSearch(a *app) {
	q := v.search.query()
	if q == "" || a.reposNext == "" || v.remoteInFlight {
		return
	}
	// The q=name~ operand is a single string: search the first term
	// server-side, the full query still filters locally in rebuild.
	term := strings.Fields(q)[0]
	if term == v.remoteQuery {
		return
	}
	v.remoteInFlight = true
	gen := a.reposGen
	first := a.client.listReposFirstURL(a.ctx.Workspace, term)
	a.fetch("repos search "+term, func() (func(*app), error) {
		repos, _, err := a.client.listReposPage(first) // one page of matches is plenty for a picker
		return func(a *app) {
			v.remoteInFlight = false
			if a.reposGen != gen {
				return
			}
			if err != nil {
				a.status = err.Error()
				return
			}
			v.remoteQuery = term
			v.remoteResults = repos
			v.rebuild(a) // re-checks maybeRemoteSearch for a query typed meanwhile
		}, nil
	})
}

func pickerDescWidth(a *app) int {
	w := a.w - 1 - pickerSlugWidth - 2 - 2 - pickerUpdatedWidth - 1
	if w < 1 {
		w = 1
	}
	return w
}

func (v *repoPickerView) render(a *app, s *Screen) {
	x := s.WriteString(1, 0, fmt.Sprintf("Bitbucket リポジトリ — %s   ", a.ctx.Workspace), styleTitle, a.w)
	q := v.search.query()
	count := fmt.Sprintf("%d件", len(a.repos))
	if q != "" {
		count = fmt.Sprintf("%d/%d件", v.shown, len(a.repos))
	}
	if a.reposNext != "" {
		count += "+"
	}
	x = s.WriteString(x, 0, count, styleDim, a.w)
	if q != "" {
		s.WriteString(x, 0, "  /"+q, styleMeta, a.w)
	}
	paintSeparator(s, 1, a.w)
	v.vp.Paint(s, a.contentRect())
}

func (v *repoPickerView) grabsKeys() bool { return v.search.active }

// interceptEsc clears an applied filter (or cancels a pending clone) instead
// of closing the viewer.
func (v *repoPickerView) interceptEsc(a *app) bool {
	if v.pendingClone != nil {
		v.pendingClone = nil
		a.status = "cloneを取り消しました"
		return true
	}
	if v.search.query() == "" {
		return false
	}
	v.search.clear()
	v.rebuild(a)
	a.status = "絞り込みを解除しました"
	return true
}

// modal shows the clone confirmation.
func (v *repoPickerView) modal(a *app) *modalSpec {
	r := v.pendingClone
	if r == nil {
		return nil
	}
	return &modalSpec{
		Title: "clone",
		Lines: []modalLine{
			{Spans: []Span{{Text: r.FullName + " を", Style: styleTitle}}, Center: true},
			{Spans: []Span{{Text: shortenHome(cloneDestFor(a.cfg, r.Slug)) + " にcloneしますか？", Style: styleTitle}}, Center: true},
			{},
		},
		Footer: modalLine{Spans: []Span{
			{Text: "y", Style: styleApproved},
			{Text: " 実行     その他のキー 取消", Style: styleDim},
		}, Center: true},
		PreferredWidth: 64,
	}
}

func (v *repoPickerView) handle(a *app, k Key) {
	if v.search.active {
		if v.search.handle(k) {
			v.rebuild(a)
		}
		return
	}
	if v.pendingClone != nil {
		r := *v.pendingClone
		v.pendingClone = nil
		if isKey(k, 'y') {
			v.runClone(a, r)
		} else {
			a.status = "cloneを取り消しました"
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
		if r := v.currentRepo(a); r != nil {
			v.openRepo(a, r)
		}
	case isKey(k, 'C'):
		if r := v.currentRepo(a); r != nil {
			v.beginClone(a, r)
		}
	case isKey(k, 'r') || isKey(k, 'R'):
		v.remoteQuery, v.remoteResults = "", nil
		v.load(a, true)
		v.scanRoots(a)
	case isKey(k, 'o'):
		if r := v.currentRepo(a); r != nil {
			openInBrowser(a, r.WebURL())
		}
	case isKey(k, 'y'):
		if r := v.currentRepo(a); r != nil {
			copyToClipboard(a, r.CloneURL(a.cfg.CloneProtocol))
		}
	case isKey(k, '?'):
		a.openHelp("picker")
	}
	if a.reposNext != "" && v.vp.Cursor >= len(v.vp.Rows)-10 {
		v.loadMore(a)
	}
}

// currentRepo resolves the repository under the cursor.
func (v *repoPickerView) currentRepo(a *app) *Repository {
	r := v.vp.Current()
	if r == nil || r.Kind != RowRepo {
		return nil
	}
	return v.repoAt(a, r.Item.(int))
}

// openRepo switches the app context to the selected repository and pushes
// its PR list. Reselecting the current repo keeps its fetched state.
func (v *repoPickerView) openRepo(a *app, r *Repository) {
	ws := a.ctx.Workspace
	key := repoDirKey(ws, r.Slug)

	// Trust the checkout index only after its origin still matches; a moved
	// or re-pointed directory silently drops out.
	dir := a.localDirs[key]
	if dir != "" {
		if ws2, repo2, err := repoFromDir(dir); err != nil || ws2 != ws || repo2 != r.Slug {
			forgetRepoDir(ws, r.Slug)
			delete(a.localDirs, key)
			dir = ""
			v.rebuild(a)
		}
	}

	if a.ctx.Repo != r.Slug || a.prs == nil {
		resetRepoState(a)
		a.ctx.Repo = r.Slug
	}
	a.ctx.RepoDir = dir
	a.renameViewerTab(renderListTabTitle(a.cfg, ws, r.Slug))
	a.push(newListView(a))
}

// beginClone validates and opens the y/n confirmation for cloning the
// selected repository.
func (v *repoPickerView) beginClone(a *app, r *Repository) {
	key := repoDirKey(a.ctx.Workspace, r.Slug)
	if a.localDirs[key] != "" {
		a.status = "すでにローカルにあります: " + shortenHome(a.localDirs[key])
		return
	}
	if msg := cloneBlocked(a, r); msg != "" {
		a.status = msg
		return
	}
	clone := *r // copy: a.repos may be swapped by a reload before y arrives
	v.pendingClone = &clone
}

// runClone kicks off the confirmed clone and keeps the row's clone state
// visible while it runs.
func (v *repoPickerView) runClone(a *app, r Repository) {
	slug := r.Slug
	startClone(a, r, func(a *app, dir string) {
		if dir != "" && a.ctx.Repo == slug {
			a.ctx.RepoDir = dir
		}
		v.rebuild(a)
	})
	v.rebuild(a)
}

func (v *repoPickerView) footer(a *app) string {
	if v.search.active {
		return v.search.prompt()
	}
	if q := v.search.query(); q != "" {
		return "j/k:移動  Enter:PR一覧  /:" + q + "  q/Esc:絞込解除  C:clone  r:再読込"
	}
	return "j/k:移動  Enter:PR一覧  /:検索  C:clone  o:ブラウザ  y:clone URL  r:再読込  q:終了"
}

