package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	pickerSlugWidth    = 28
	pickerUpdatedWidth = 10
	pickerAuthorWidth  = 14
	pickerPRRepoWidth  = 22
	// pickerFillCap stops the filter's automatic page chase on huge
	// workspaces; past this the user narrows the query instead.
	pickerFillCap = 500
)

// pickerMode selects what the picker lists: the workspace's repositories,
// the user's own open PRs, or open PRs waiting for the user's review.
type pickerMode int

const (
	pickerModeRepos pickerMode = iota
	pickerModeMine
	pickerModeReviewing
)

var pickerModeLabels = []string{"Repos", "My PRs", "Review"}

func pickerModeFromView(view string) pickerMode {
	switch view {
	case "mine":
		return pickerModeMine
	case "reviewing":
		return pickerModeReviewing
	default:
		return pickerModeRepos
	}
}

// repoPickerView is the workspace repository picker: search the list, pick a
// repo, land in its PR list. It sits at the bottom of the view stack in
// picker mode, so q/Esc from the PR list comes back here to switch repos.
// Tab switches to the cross-repo PR dashboards (My PRs / Review), where
// Enter goes straight to the PR detail.
type repoPickerView struct {
	vp     Viewport
	search searchBox
	shown  int
	mode   pickerMode

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
	v := &repoPickerView{mode: pickerModeFromView(a.ctx.PickerView)}
	v.scanRoots(a)
	v.ensureModeData(a)
	return v
}

// ensureModeData loads the current mode's list unless it already has fresh
// data this session.
func (v *repoPickerView) ensureModeData(a *app) {
	switch v.mode {
	case pickerModeMine:
		if !a.myPRs.loaded {
			v.loadCrossPRs(a, &a.myPRs, "mine", false)
			return
		}
	case pickerModeReviewing:
		if !a.reviewPRs.loaded {
			v.loadCrossPRs(a, &a.reviewPRs, "reviewing", false)
			return
		}
	default:
		if a.repos == nil {
			v.load(a, false)
			return
		}
	}
	v.rebuild(a)
}

// cycleMode steps through Repos → My PRs → Review.
func (v *repoPickerView) cycleMode(a *app, dir int) {
	n := len(pickerModeLabels)
	v.mode = pickerMode((int(v.mode) + dir + n) % n)
	v.ensureModeData(a)
}

// crossList resolves the current mode's dashboard state (nil in Repos mode).
func (v *repoPickerView) crossList(a *app) *crossPRList {
	switch v.mode {
	case pickerModeMine:
		return &a.myPRs
	case pickerModeReviewing:
		return &a.reviewPRs
	default:
		return nil
	}
}

// reviewScanCap bounds the Review scan's repository fan-out so a
// misconfigured window cannot eat the API rate limit.
const reviewScanCap = 300

// reviewScanSlugs merges the Review view's scan sources in priority order,
// deduplicated and capped: contributor repos (recently pushed), repos of the
// user's own PRs, local checkouts, repos seen in the previous Review result
// (sticky), and configured pins. Reports whether the cap truncated the set.
func reviewScanSlugs(contributor []string, myPRs []PullRequest, localDirs map[string]string, ws string, prev []PullRequest, extras []string) ([]string, bool) {
	seen := map[string]bool{}
	var out []string
	add := func(slug string) {
		if slug != "" && !seen[slug] {
			seen[slug] = true
			out = append(out, slug)
		}
	}
	for _, s := range extras { // pins first: they must survive the cap
		add(s)
	}
	for _, s := range contributor {
		add(s)
	}
	for i := range myPRs {
		add(myPRs[i].RepoSlug())
	}
	for key := range localDirs {
		if s, ok := strings.CutPrefix(key, ws+"/"); ok {
			add(s)
		}
	}
	for i := range prev {
		add(prev[i].RepoSlug())
	}
	if len(out) > reviewScanCap {
		return out[:reviewScanCap], true
	}
	return out, false
}

// loadCrossPRs fetches one cross-repo dashboard (SWR: the disk cache paints
// first). kind is "mine" or "reviewing". The reviewing fetch also loads the
// user's own PRs — they seed the scan set and warm the My PRs tab.
func (v *repoPickerView) loadCrossPRs(a *app, list *crossPRList, kind string, force bool) {
	ws := a.ctx.Workspace
	list.gen++
	gen := list.gen
	list.err, list.note = "", ""
	if !force && list.prs == nil {
		if cached := loadCrossPRCache(ws, kind); len(cached) > 0 {
			list.prs = cached
		}
	}
	// Capture everything the work closure needs up front; it must not touch
	// app state off the main goroutine.
	localDirs := make(map[string]string, len(a.localDirs))
	for k, d := range a.localDirs {
		localDirs[k] = d
	}
	prev := append([]PullRequest(nil), a.reviewPRs.prs...)
	extras := a.cfg.ReviewExtraRepos
	scanDays := a.cfg.ReviewScanDays
	client := a.client
	resultCh := a.resultCh
	a.fetch("cross prs "+kind, func() (func(*app), error) {
		uuid, err := client.currentUserUUID()
		var (
			prs    []PullRequest
			myPRs  []PullRequest
			failed []string
			capped bool
		)
		if err == nil {
			if kind == "mine" {
				prs, err = client.listMyPRs(ws, uuid)
			} else {
				var contributor []string
				myPRs, err = client.listMyPRs(ws, uuid)
				if err == nil {
					contributor, err = client.listContributorRepoSlugs(ws, scanDays)
				}
				if err == nil {
					var slugs []string
					slugs, capped = reviewScanSlugs(contributor, myPRs, localDirs, ws, prev, extras)
					// Stream scan progress to the footer; drop updates rather
					// than block when the main loop is busy.
					progress := func(done, total int) {
						update := func(a *app) {
							if list.gen == gen {
								a.status = fmt.Sprintf("Review走査中… %d/%d リポジトリ", done, total)
							}
						}
						select {
						case resultCh <- update:
						default:
						}
					}
					prs, failed, err = client.listReviewingPRs(ws, uuid, slugs, progress)
				}
			}
		}
		return func(a *app) {
			if kind == "reviewing" && myPRs != nil {
				// Free warm-up: the scan already fetched the user's own PRs.
				a.myPRs.prs = myPRs
				a.myPRs.loaded = true
				a.myPRs.err = ""
				saveCrossPRCache(ws, "mine", myPRs)
			}
			if list.gen != gen {
				return
			}
			if err != nil {
				list.err = err.Error()
				a.status = list.err
				v.rebuild(a)
				return
			}
			if prs == nil {
				prs = []PullRequest{}
			}
			list.prs = prs
			list.loaded = true
			a.status = ""
			var notes []string
			if len(failed) > 0 {
				notes = append(notes, fmt.Sprintf("%d件のリポジトリで取得に失敗", len(failed)))
			}
			if capped {
				notes = append(notes, fmt.Sprintf("走査対象が上限%d件を超えたため一部を省略", reviewScanCap))
			}
			list.note = strings.Join(notes, " / ")
			saveCrossPRCache(ws, kind, prs)
			v.rebuild(a)
		}, nil
	})
	v.rebuild(a)
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
// filter. Row.Item is the index into a.repos (negative: remoteResults) in
// Repos mode, or into the mode's crossPRList otherwise.
func (v *repoPickerView) rebuild(a *app) {
	if list := v.crossList(a); list != nil {
		v.rebuildCrossPRs(a, list)
		return
	}
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
		rows = append(rows, v.repoRow(a, r, i, now))
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
			rows = append(rows, v.repoRow(a, r, -i-1, now))
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

// rebuildCrossPRs renders one cross-repo PR dashboard: one line per PR with
// its repository slug, since the list spans the whole workspace.
func (v *repoPickerView) rebuildCrossPRs(a *app, list *crossPRList) {
	now := time.Now()
	q := v.search.query()
	rows := make([]Row, 0, len(list.prs)+2)
	v.shown = 0
	if list.note != "" {
		rows = append(rows, textRow(Span{" ⚠ " + list.note + "（r で再試行）", styleOutdated}))
	}
	titleW := pickerPRTitleWidth(a)
	for i := range list.prs {
		pr := &list.prs[i]
		if !matchQuery(q, "#"+strconv.Itoa(pr.ID), pr.RepoSlug(), pr.Title, pr.Author.Name(),
			pr.Source.Branch.Name, pr.Destination.Branch.Name) {
			continue
		}
		v.shown++
		rows = append(rows, row(RowPR, i, true,
			Span{fmt.Sprintf(" #%-6d ", pr.ID), styleMeta},
			Span{padRight(truncateWidth(pr.RepoSlug(), pickerPRRepoWidth), pickerPRRepoWidth), styleMeta},
			Span{"  ", styleNone},
			Span{padRight(truncateWidth(pr.Title, titleW), titleW), styleNone},
			Span{"  " + padRight(truncateWidth(pr.Author.Name(), pickerAuthorWidth), pickerAuthorWidth), styleDim},
			Span{"  " + padRight(truncateWidth(relTime(pr.UpdatedOn, now), pickerUpdatedWidth), pickerUpdatedWidth), styleDim},
		))
	}
	if v.shown == 0 {
		switch {
		case a.loading > 0 && !list.loaded:
			rows = append(rows, textRow(Span{" 読み込み中…", styleDim}))
			if v.mode == pickerModeReviewing {
				rows = append(rows, textRow(Span{fmt.Sprintf(" 書き込み権限のある直近%d日更新のリポジトリを問い合わせています（進捗はフッタに表示）", a.cfg.ReviewScanDays), styleDim}))
			}
		case list.err != "":
			rows = append(rows, textRow(Span{" 取得に失敗しました（r で再読込）: " + list.err, styleDim}))
		case q != "":
			rows = append(rows, textRow(Span{" (一致する PR はありません)", styleDim}))
		case v.mode == pickerModeMine:
			rows = append(rows, textRow(Span{" (自分が作成したOPENのPRはありません)", styleDim}))
		default:
			rows = append(rows, textRow(Span{" (レビュー待ちのPRはありません)", styleDim}))
		}
	}
	v.vp.Reset(rows)
}

func pickerPRTitleWidth(a *app) int {
	w := a.w - 1 - 8 - pickerPRRepoWidth - 2 - 2 - pickerAuthorWidth - 2 - pickerUpdatedWidth - 1
	if w < 1 {
		w = 1
	}
	return w
}

// repoRow is one repository line. The two leading mark cells show the local
// checkout state: ● checkout present, ◌ clone running, blank otherwise (the
// cursor row's checkout path appears at the right edge of the header).
func (v *repoPickerView) repoRow(a *app, r *Repository, item int, now time.Time) Row {
	mark, markStyle := "  ", styleDim
	switch {
	case a.cloneInFlight[r.FullName]:
		mark, markStyle = "◌ ", styleMeta
	case a.localDirs[repoDirKey(a.ctx.Workspace, r.Slug)] != "":
		mark, markStyle = "● ", styleApproved
	}
	descW := pickerDescWidth(a)
	desc := strings.ReplaceAll(r.Description, "\n", " ")
	return row(RowRepo, item, true,
		Span{" ", styleNone},
		Span{mark, markStyle},
		Span{padRight(truncateWidth(r.Slug, pickerSlugWidth), pickerSlugWidth), styleNone},
		Span{"  ", styleNone},
		Span{padRight(truncateWidth(desc, descW), descW), styleDim},
		Span{"  " + padRight(truncateWidth(relTime(r.UpdatedOn, now), pickerUpdatedWidth), pickerUpdatedWidth), styleDim},
	)
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
	w := a.w - 1 - 2 - pickerSlugWidth - 2 - 2 - pickerUpdatedWidth - 1
	if w < 1 {
		w = 1
	}
	return w
}

func (v *repoPickerView) render(a *app, s *Screen) {
	x := s.WriteString(1, 0, fmt.Sprintf("Bitbucket — %s   ", a.ctx.Workspace), styleTitle, a.w)
	for i, label := range pickerModeLabels {
		style := styleTabInactive
		if pickerMode(i) == v.mode {
			style = styleTabActive
		}
		x = s.WriteString(x, 0, label, style, a.w)
		x = s.WriteString(x, 0, "  ", styleNone, a.w)
	}
	q := v.search.query()
	total := len(a.repos)
	more := a.reposNext != ""
	if list := v.crossList(a); list != nil {
		total, more = len(list.prs), false
	}
	count := fmt.Sprintf("%d件", total)
	if q != "" {
		count = fmt.Sprintf("%d/%d件", v.shown, total)
	}
	if more {
		count += "+"
	}
	x = s.WriteString(x, 0, count, styleDim, a.w)
	if q != "" {
		x = s.WriteString(x, 0, "  /"+q, styleMeta, a.w)
	}
	if v.mode == pickerModeRepos {
		v.paintCursorPath(a, s, x)
	}
	paintSeparator(s, 1, a.w)
	v.vp.Paint(s, a.contentRect())
}

// paintCursorPath shows the cursor row's checkout path at the right edge of
// the header (the rows themselves only carry the ● mark).
func (v *repoPickerView) paintCursorPath(a *app, s *Screen, usedX int) {
	r := v.currentRepo(a)
	if r == nil {
		return
	}
	dir := a.localDirs[repoDirKey(a.ctx.Workspace, r.Slug)]
	if dir == "" {
		return
	}
	path := shortenHome(dir)
	avail := a.w - 1 - (usedX + 3) - 2 // gap after the counters, mark cells
	if avail < 8 {
		return
	}
	path = truncateWidthLeft(path, avail)
	x := a.w - 1 - displayWidth(path) - 2
	x = s.WriteString(x, 0, "● ", styleApproved, a.w)
	s.WriteString(x, 0, path, styleDim, a.w)
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
	case k.Kind == KeyTab || isKey(k, 's') || isKey(k, 'l') || k.Kind == KeyRight ||
		(k.Kind == KeyRune && k.R == '\t'):
		v.cycleMode(a, 1)
	case k.Kind == KeyShiftTab || isKey(k, 'h') || k.Kind == KeyLeft:
		v.cycleMode(a, -1)
	case isKey(k, '1') || isKey(k, '2') || isKey(k, '3'):
		v.mode = pickerMode(k.R - '1')
		v.ensureModeData(a)
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
		if pr := v.currentCrossPR(a); pr != nil {
			v.openCrossPR(a, *pr)
		} else if r := v.currentRepo(a); r != nil {
			v.openRepo(a, r)
		}
	case isKey(k, 'C'):
		if pr := v.currentCrossPR(a); pr != nil {
			v.beginCloneBySlug(a, pr.RepoSlug())
		} else if r := v.currentRepo(a); r != nil {
			v.beginClone(a, r)
		}
	case isKey(k, 'r') || isKey(k, 'R'):
		if list := v.crossList(a); list != nil {
			kind := "mine"
			if v.mode == pickerModeReviewing {
				kind = "reviewing"
			}
			v.loadCrossPRs(a, list, kind, true)
		} else {
			v.remoteQuery, v.remoteResults = "", nil
			v.load(a, true)
			v.scanRoots(a)
		}
	case isKey(k, 'o'):
		if pr := v.currentCrossPR(a); pr != nil {
			openInBrowser(a, pr.WebURL())
		} else if r := v.currentRepo(a); r != nil {
			openInBrowser(a, r.WebURL())
		}
	case isKey(k, 'y'):
		if pr := v.currentCrossPR(a); pr != nil {
			copyToClipboard(a, pr.WebURL())
		} else if r := v.currentRepo(a); r != nil {
			copyToClipboard(a, r.CloneURL(a.cfg.CloneProtocol))
		}
	case isKey(k, '?'):
		a.openHelp("picker")
	}
	if v.mode == pickerModeRepos && a.reposNext != "" && v.vp.Cursor >= len(v.vp.Rows)-10 {
		v.loadMore(a)
	}
}

// currentCrossPR resolves the PR under the cursor in My PRs / Review mode
// (nil in Repos mode).
func (v *repoPickerView) currentCrossPR(a *app) *PullRequest {
	list := v.crossList(a)
	if list == nil {
		return nil
	}
	r := v.vp.Current()
	if r == nil || r.Kind != RowPR {
		return nil
	}
	if i := r.Item.(int); i >= 0 && i < len(list.prs) {
		return &list.prs[i]
	}
	return nil
}

// openCrossPR switches the app context to the PR's repository and opens its
// detail directly (q/Esc comes back to this dashboard).
func (v *repoPickerView) openCrossPR(a *app, pr PullRequest) {
	slug := pr.RepoSlug()
	if slug == "" {
		a.status = "PRのリポジトリを特定できません"
		return
	}
	ws := a.ctx.Workspace
	key := repoDirKey(ws, slug)
	dir := a.localDirs[key]
	if dir != "" {
		if ws2, repo2, err := repoFromDir(dir); err != nil || ws2 != ws || repo2 != slug {
			forgetRepoDir(ws, slug)
			delete(a.localDirs, key)
			dir = ""
		}
	}
	if a.ctx.Repo != slug {
		resetRepoState(a)
		a.ctx.Repo = slug
	}
	a.ctx.RepoDir = dir
	a.renameViewerTab(renderListTabTitle(a.cfg, ws, slug))
	if pr.State == "" {
		pr.State = "OPEN"
	}
	a.push(newDetailView(a, pr.ID, &pr))
}

// beginCloneBySlug opens the clone confirmation for a repository known only
// by slug (a PR row): reuse the fetched repo list when possible, otherwise
// one API call fills in the clone links.
func (v *repoPickerView) beginCloneBySlug(a *app, slug string) {
	if slug == "" {
		return
	}
	for i := range a.repos {
		if a.repos[i].Slug == slug {
			v.beginClone(a, &a.repos[i])
			return
		}
	}
	ws := a.ctx.Workspace
	a.fetch("repo "+slug, func() (func(*app), error) {
		r, err := a.client.getRepo(ws, slug)
		return func(a *app) {
			if err != nil {
				a.status = "リポジトリ情報の取得に失敗: " + err.Error()
				return
			}
			v.beginClone(a, r)
		}, nil
	})
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
		return "j/k:移動  Enter:開く  /:" + q + "  q/Esc:絞込解除  C:clone  r:再読込"
	}
	if v.mode == pickerModeRepos {
		return "j/k:移動  Enter:PR一覧  Tab:ビュー切替  /:検索  C:clone  o:ブラウザ  y:clone URL  r:再読込  q:終了"
	}
	return "j/k:移動  Enter:PR詳細  Tab:ビュー切替  /:検索  C:clone  o:ブラウザ  y:URL  r:再読込  q:終了"
}

