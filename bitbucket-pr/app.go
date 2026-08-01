package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"
)

// view is one screen on the navigation stack (PR list → PR detail → diff).
type view interface {
	render(a *app, s *Screen)
	handle(a *app, k Key)
	footer(a *app) string
}

// prDetail caches everything fetched for one PR; `r` drops and refetches.
type prDetail struct {
	pr       *PullRequest
	diffstat []DiffStatEntry
	comments []Comment
	diffText string
	files    []FileDiff
}

// app owns the terminal, the view stack, and all fetched data. Every
// mutation happens on the main loop goroutine: async fetches send apply
// closures over resultCh.
type app struct {
	ctx    uiContext
	cfg    Config
	client *bbClient
	herdr  *herdrClient
	sgr    map[StyleID]string

	w, h     int
	stack    []view
	status   string // transient footer message (last error)
	loading  int    // in-flight fetch count
	quit     bool
	resultCh chan func(*app)

	prs            []PullRequest // list cache for prsState (grows page by page)
	prsState       string
	prsNext        string // next PR-list page URL; "" = fully loaded
	prsErr         string // last list-fetch error; shown in the empty placeholder
	prsGen         int    // list generation; bumped on reload/state switch to drop stale pages
	prsMoreLoading bool   // a loadMore page is in flight
	detail         map[int]*prDetail

	difftoolPanes map[int]paneRef // PR id → its external diff tool tab

	// pendingDifftool queues an Enter/D pressed while the PR diff was
	// still downloading; the diff fetch completion fires it.
	pendingDifftool *difftoolRequest
}

// difftoolRequest is a deferred openInDiffTool call.
type difftoolRequest struct {
	prID  int
	focus string
}

// keyGrabber is a view that is accepting text input and must therefore see
// every key — including the q and Esc the main loop otherwise handles itself.
type keyGrabber interface{ grabsKeys() bool }

func grabsKeys(v view) bool {
	g, ok := v.(keyGrabber)
	return ok && g.grabsKeys()
}

func (a *app) push(v view) { a.stack = append(a.stack, v) }

func (a *app) pop() {
	if len(a.stack) > 0 {
		a.stack = a.stack[:len(a.stack)-1]
	}
	if len(a.stack) == 0 {
		a.quit = true
	}
}

func (a *app) top() view { return a.stack[len(a.stack)-1] }

// fetch runs work off the main loop and applies its result back on it. The
// work function returns an apply closure so all state mutation stays on the
// main goroutine.
func (a *app) fetch(label string, work func() (func(*app), error)) {
	a.loading++
	debugf("fetch: %s", label)
	go func() {
		apply, err := work()
		a.resultCh <- func(a *app) {
			a.loading--
			if err != nil {
				a.status = err.Error()
				debugf("fetch %s: %v", label, err)
				return
			}
			apply(a)
		}
	}()
}

// detailFor returns (creating if needed) the cache slot for a PR id.
func (a *app) detailFor(id int) *prDetail {
	d, ok := a.detail[id]
	if !ok {
		d = &prDetail{}
		a.detail[id] = d
	}
	return d
}

// contentRect is the viewport area between the per-view header (2 rows) and
// the footer (1 row).
func (a *app) contentRect() Rect {
	return Rect{X: 0, Y: 2, W: a.w, H: a.h - 3}
}

// runUI is the viewer pane's entrypoint.
func runUI() {
	ctx, err := uiContextFromEnv()
	if err != nil {
		errExit(err)
	}
	cfg := loadConfig()
	herdr, err := newHerdrClient()
	if err != nil {
		errExit(err)
	}

	email, token, credsOK := cfg.credentials()

	// Kick the fetches off at a provisional terminal size BEFORE waiting for
	// the pane pty to settle: the ~1s network round trip overlaps the (up to
	// 600ms) size wait instead of queueing behind it.
	w, h := termSize()
	a := &app{
		ctx:           ctx,
		cfg:           cfg,
		herdr:         herdr,
		sgr:           sgrTable(cfg),
		w:             w,
		h:             h,
		resultCh:      make(chan func(*app), 8),
		detail:        map[int]*prDetail{},
		difftoolPanes: map[int]paneRef{},
	}
	if cfg.loadErr != "" {
		a.status = cfg.loadErr
	}
	if credsOK {
		a.client = newBBClient(email, token, time.Duration(cfg.HTTPTimeoutSec)*time.Second)
		a.push(newListView(a))
		if ctx.PRID > 0 {
			// Ctrl-clicked PR URL: go straight to the PR, list stays underneath.
			a.push(newDetailView(a, ctx.PRID))
		}
	}

	w, h = waitForSteadySize()
	a.w, a.h = w, h
	debugf("ui: start %s/%s pr=%d term=%dx%d", ctx.Workspace, ctx.Repo, ctx.PRID, w, h)

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		errExit("raw mode:", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	out := bufio.NewWriter(os.Stdout)
	fmt.Fprint(out, "\x1b[?1049h\x1b[?25l") // alt screen, hide cursor
	defer func() {
		fmt.Fprint(out, "\x1b[?25h\x1b[?1049l")
		out.Flush()
	}()

	if !credsOK {
		showAuthHelp(out, cfg, w, h)
		return
	}

	// Row widths were baked at the provisional size — regenerate at the
	// settled one (same as the SIGWINCH path).
	for _, v := range a.stack {
		if r, ok := v.(interface{ rebuild(*app) }); ok {
			r.rebuild(a)
		}
	}

	keyCh := make(chan Key)
	go func() {
		kr := newKeyReader(os.Stdin)
		for {
			k, kerr := kr.Read()
			if kerr != nil {
				close(keyCh)
				return
			}
			keyCh <- k
		}
	}()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)

	for !a.quit {
		frame := NewScreen(a.w, a.h)
		a.top().render(a, frame)
		paintFooter(a, frame)
		frame.Flush(out, a.sgr)
		out.Flush()

		select {
		case <-winch:
			a.w, a.h = termSize()
			debugf("ui: winch %dx%d", a.w, a.h)
			// Row widths bake in a.w, so regenerate every view's rows.
			for _, v := range a.stack {
				if r, ok := v.(interface{ rebuild(*app) }); ok {
					r.rebuild(a)
				}
			}
		case apply := <-a.resultCh:
			apply(a)
		case k, ok := <-keyCh:
			if !ok {
				return
			}
			a.status = ""
			switch {
			case k.Kind == KeyCtrl && k.R == 'c':
				return
			case grabsKeys(a.top()):
				// A view taking text input (the / filter) gets every key,
				// q and Esc included.
				a.top().handle(a, k)
			case k.Kind == KeyRune && k.R == 'q', k.Kind == KeyEsc:
				if v, ok := a.top().(escInterceptor); ok && v.interceptEsc(a) {
					break // the view consumed it (e.g. cancelled a selection)
				}
				a.pop()
			default:
				a.top().handle(a, k)
			}
		}
	}
}

// paintFooter draws the bottom hint/status line.
func paintFooter(a *app, s *Screen) {
	y := a.h - 1
	text := a.top().footer(a)
	if a.status != "" {
		s.WriteString(1, y, truncateWidth(a.status, a.w-2), styleError, a.w)
		return
	}
	x := s.WriteString(1, y, text, styleDim, a.w)
	if a.loading > 0 {
		s.WriteString(x+2, y, "読み込み中…", styleMeta, a.w)
	}
}

// showAuthHelp renders the full-screen setup instructions when no
// credentials are configured, then waits for a key.
func showAuthHelp(out *bufio.Writer, cfg Config, w, h int) {
	s := NewScreen(w, h)
	lines := []string{
		"Bitbucket の認証情報が見つかりません。",
		"",
		"次のいずれかを設定してください:",
		"",
		"  1. 環境変数",
		"       export ATLASSIAN_USER_ID=<Atlassianアカウントのメールアドレス>",
		"       export ATLASSIAN_API_TOKEN=<APIトークン>",
		"",
		"  2. " + os.Getenv("HERDR_PLUGIN_CONFIG_DIR") + "/config.toml",
		"       email = \"...\"",
		"       api_token = \"...\"",
		"",
		"APIトークンは https://id.atlassian.com/manage-profile/security/api-tokens",
		"で作成できます（App Password は2026年6月に廃止済み）。",
	}
	s.WriteString(1, 0, "bitbucket-pr — 認証設定が必要です", styleTitle, w)
	for i, l := range lines {
		s.WriteString(1, i+2, l, styleNone, w)
	}
	s.WriteString(1, h-1, " 任意のキーで終了", styleDim, w)
	s.Flush(out, sgrTable(cfg))
	out.Flush()
	kr := newKeyReader(os.Stdin)
	kr.Read()
}

// termSize reads the current pty size with a sane fallback.
func termSize() (int, int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

// waitForSteadySize polls until the pty size stops changing between two
// samples — herdr resizes a fresh pane pty once or twice right after spawn
// (see hint-copy's waitForSize), so the first paint usually lands at the
// final geometry. Late resizes are caught by SIGWINCH.
func waitForSteadySize() (int, int) {
	w, h := termSize()
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(30 * time.Millisecond)
		nw, nh := termSize()
		if nw == w && nh == h {
			return w, h
		}
		w, h = nw, nh
	}
	return w, h
}
