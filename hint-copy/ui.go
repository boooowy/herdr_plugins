package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"

	"golang.org/x/term"
)

type uiMode int

const (
	modeToken uiMode = iota
	modeLine
	modeCopy
	modeWord
)

// uiData is everything derived from the captured pane content alone — it is
// independent of the overlay's terminal size, so resizes and mode switches
// never re-read or re-parse the panes.
type uiData struct {
	parsed       *tabParsed
	text         string
	targetStyled [][]Cell
	lines        []string
	cands        []Candidate
	lm           *LabelMap
	wordCands    []Candidate // built lazily on the first word-mode entry
	wordLm       *LabelMap
}

// ensureWords extracts the word-mode candidates on first use.
func (d *uiData) ensureWords(cfg Config) {
	if d.wordLm != nil {
		return
	}
	d.wordCands = ExtractWords(d.text, cfg)
	d.wordLm = AssignLabels(d.wordCands, cfg)
}

// uiView is the geometry-dependent rendering state, rebuilt from the cached
// uiData whenever the terminal size changes.
type uiView struct {
	comp    *composite
	content Rect
	// paintRect is the rect the pane's base content was actually painted at
	// (composite: comp.target, un-clamped). Token mode overlays labels onto
	// that pre-painted content, so it must map lines with the SAME offset the
	// base used — content is clamped for the footer, which would otherwise
	// shift the labels off their content. Line/copy mode repaint their own
	// content into `content` and stay self-consistent.
	paintRect Rect
	base      *Screen
	ll        *LineLabels
}

// layoutFromEnv decodes the pre-overlay layout snapshot the action captured.
func layoutFromEnv() *paneLayoutResult {
	s := os.Getenv(envLayout)
	if s == "" {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	var lay paneLayoutResult
	if json.Unmarshal(data, &lay) != nil {
		return nil
	}
	return &lay
}

// waitForSize waits until the terminal size settles into the expected
// area-minus-border relation with the snapshot (herdr resizes the fresh
// overlay pty once or twice right after spawn). It is SIGWINCH-driven: winch
// must already be registered by the caller, so a resize landing between the
// size probe and the wait cannot be lost. It gives up after 600ms and
// returns whatever is current — the main loop's SIGWINCH handling catches
// anything later.
func waitForSize(lay *paneLayoutResult, winch <-chan os.Signal) (int, int) {
	deadline := time.NewTimer(600 * time.Millisecond)
	defer deadline.Stop()
	for {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil || w <= 0 || h <= 0 {
			w, h = 80, 24
		}
		if lay == nil {
			return w, h
		}
		dw, dh := lay.Area.Width-w, lay.Area.Height-h
		if dw >= 1 && dw <= 4 && dh >= 1 && dh <= 4 {
			return w, h
		}
		select {
		case <-winch:
		case <-deadline.C:
			debugf("waitForSize: gave up at term=%dx%d area=%dx%d", w, h, lay.Area.Width, lay.Area.Height)
			return w, h
		}
	}
}

// buildUIView derives the rendering state for one terminal size from the
// cached capture — no herdr round trips, so SIGWINCH rebuilds are instant.
func buildUIView(d *uiData, lay *paneLayoutResult, target string, cfg Config, tbl *styleTable, w, h int) *uiView {
	v := &uiView{}
	v.comp = buildComposite(lay, d.parsed, target, w, h, cfg, tbl)
	if v.comp != nil {
		v.content = v.comp.target
		v.paintRect = v.comp.target // the rect buildComposite painted the base at
		if v.content.Y+v.content.H > h-1 {
			v.content.H = h - 1 - v.content.Y
		}
		v.base = v.comp.base
	} else {
		_, view := viewWindow(len(d.lines), h)
		v.content = Rect{X: 0, Y: 0, W: w, H: view}
		v.paintRect = v.content
		v.base = NewScreen(w, h)
		v.base.styles = tbl
		paintPaneCells(v.base, v.content, d.targetStyled)
	}
	lineOffset := 0
	if len(d.lines) > v.content.H {
		lineOffset = len(d.lines) - v.content.H
	}
	v.ll = AssignLineLabels(d.lines, lineOffset, v.content.H, cfg)
	return v
}

// runUI is the overlay pane's entrypoint. It enters the alternate screen as
// early as possible (the overlay pane is blank until we write), kicks off
// the pane captures in parallel with the pty-size settling, and only then
// hands over to the interactive loop. All cleanup runs on return — uiLoop
// reports errors instead of exiting, so raw mode and the alt screen are
// always unwound.
func runUI() {
	client, err := newHerdrClient()
	if err != nil {
		errExit(err)
	}
	target := os.Getenv(envTargetPane)
	if target == "" {
		errExit("missing " + envTargetPane + "; launch me via the action")
	}
	startCopy := os.Getenv("HINTCOPY_MODE") == "copy"
	lay := layoutFromEnv()

	cfg := loadConfig()
	if cfg.loadErr != "" {
		client.notify("Hint Copy", cfg.loadErr, "none")
	}
	if _, bad := buildPatterns(cfg); len(bad) > 0 {
		client.notify("Hint Copy", "invalid custom pattern(s): "+strings.Join(bad, ", "), "none")
	}

	t0 := time.Now()

	// The captures are pure herdr round trips whose results do not depend on
	// the overlay's size — run them while the pty settles below.
	capCh := make(chan *tabCapture, 1)
	go func() { capCh <- captureTab(client, lay, target) }()

	// Register SIGWINCH before the first size probe so a resize landing in
	// between cannot be lost.
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		errExit("raw mode:", err)
	}
	out := bufio.NewWriter(os.Stdout)
	fmt.Fprint(out, "\x1b[?1049h\x1b[?25l") // alt screen, hide cursor
	out.Flush()

	loopErr := uiLoop(client, target, lay, cfg, startCopy, out, winch, capCh, t0)

	fmt.Fprint(out, "\x1b[?25h\x1b[?1049l")
	out.Flush()
	term.Restore(int(os.Stdin.Fd()), oldState)
	if loopErr != nil {
		errExit(loopErr)
	}
}

// uiLoop renders the hints and waits for input. It re-reads the target pane
// itself (never its own HERDR_PANE_ID — that is the overlay). When the tab
// is split it composites the whole tab from the action's pre-overlay layout
// snapshot so no pane appears to move. Space toggles token/line mode; tab
// toggles word mode; '[' (or the copy-mode action) enters the vim-style copy
// mode with scrollback. Returning closes the overlay.
func uiLoop(client *herdrClient, target string, lay *paneLayoutResult, cfg Config, startCopy bool, out *bufio.Writer, winch chan os.Signal, capCh <-chan *tabCapture, t0 time.Time) error {
	w, h := waitForSize(lay, winch)
	debugf("size settled: term=%dx%d after %s", w, h, time.Since(t0))
	tc := <-capCh
	debugf("capture done after %s", time.Since(t0))

	tbl := newStyleTable()
	sgr := sgrTable(cfg)

	d := &uiData{parsed: parseCapture(tc, tbl)}
	d.targetStyled = d.parsed.styled[target]
	d.text = joinPlain(d.parsed.plain[target])
	d.lines = strings.Split(d.text, "\n")
	if d.targetStyled == nil {
		return errors.New("read pane: no capture for " + target)
	}

	v := buildUIView(d, lay, target, cfg, tbl, w, h)

	// First frame: the composite base alone, before extraction — the sooner
	// the real pane pixels are back on screen, the shorter the blank-overlay
	// moment reads.
	if !startCopy {
		first := v.base.Clone()
		PaintFooter(first, " …")
		first.Flush(out, sgr)
		out.Flush()
		debugf("base frame flushed after %s", time.Since(t0))
	}

	d.cands = Extract(d.text, cfg)
	d.lm = AssignLabels(d.cands, cfg)
	debugf("extract done after %s (%d candidates)", time.Since(t0), len(d.cands))

	if !startCopy && len(d.cands) == 0 && strings.TrimSpace(d.text) == "" {
		client.notify("Hint Copy", "Nothing to copy on screen", "none")
		return nil
	}

	// Startup mode: word mode by default (default_mode = "token" restores
	// the pattern-first behavior). Whichever comes first falls through to
	// the next mode with candidates; a screen with visible text but nothing
	// to label starts in line mode — the only thing left to copy.
	mode := modeLine
	if cfg.DefaultMode == "word" {
		d.ensureWords(cfg)
		if len(d.wordCands) > 0 {
			mode = modeWord
		} else if len(d.cands) > 0 {
			mode = modeToken
		}
	} else if len(d.cands) > 0 {
		mode = modeToken
	}
	prefix := ""
	anchor := -1
	cursor := -1
	help := false
	var cs *CopyState
	var copyStyled [][]Cell

	// enterCopy lazily loads the scrollback buffer (colors kept); the visible
	// capture is the fallback when the recent read fails or is empty.
	enterCopy := func() {
		if cs == nil {
			plainBuf, styled, trunc := d.text, d.targetStyled, false
			if ansiText, tr, rerr := client.paneReadFull(target, "recent", cfg.ScrollbackLines, true); rerr == nil {
				sc, pl := parseANSI(ansiText, tbl)
				if p := joinPlain(pl); strings.TrimSpace(p) != "" {
					plainBuf, styled, trunc = p, sc, tr
				}
			}
			cs = NewCopyState(plainBuf, v.content.W, v.content.H, trunc)
			copyStyled = styled
		}
		mode = modeCopy
		prefix, anchor = "", -1
	}
	if startCopy {
		enterCopy()
		if strings.TrimSpace(strings.Join(cs.Lines, "")) == "" {
			client.notify("Hint Copy", "Nothing to copy on screen", "none")
			return nil
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

	firstFrame := true
	for {
		// The active label map/candidates: word mode swaps in its own set but
		// reuses all of token mode's painting and key handling.
		cands, lm := d.cands, d.lm
		if mode == modeWord {
			cands, lm = d.wordCands, d.wordLm
		}

		var frame *Screen
		switch {
		case help:
			frame = NewScreen(w, h)
			PaintHelp(frame, Rect{X: 0, Y: 0, W: w, H: h - 1}, cfg)
			PaintFooter(frame, " press any key to return")
		case mode == modeCopy:
			frame = v.base.Clone()
			clearRect(frame, v.content)
			PaintCopyMode(frame, v.content, cs, copyStyled)
			PaintFooter(frame, copyFooter(cs))
		case mode == modeToken:
			frame = v.base.Clone()
			PaintTokenOverlay(frame, v.paintRect, d.text, cands, lm, prefix)
			PaintFooter(frame, tokenFooter(prefix, lm, false))
		case mode == modeWord:
			// Word mode inserts labels before the words instead of covering
			// them, so it repaints the target pane like line mode does.
			frame = v.base.Clone()
			clearRect(frame, v.content)
			PaintWordInsert(frame, v.content, d.targetStyled, cands, lm, prefix)
			PaintFooter(frame, tokenFooter(prefix, lm, true))
		default:
			frame = v.base.Clone()
			clearRect(frame, v.content)
			PaintLineModeStyled(frame, v.content, d.targetStyled, v.ll, prefix, anchor, cursor)
			PaintFooter(frame, lineFooter(prefix, anchor, cursor))
		}
		frame.Flush(out, sgr)
		out.Flush()
		if firstFrame {
			debugf("labeled frame flushed after %s", time.Since(t0))
			firstFrame = false
		}

		var k Key
		var ok bool
		select {
		case <-winch:
			// The overlay pty settled to a new size: re-derive the geometry
			// from the cached capture (no re-read) and repaint.
			w, h = waitForSize(lay, winch)
			debugf("winch: term=%dx%d", w, h)
			v = buildUIView(d, lay, target, cfg, tbl, w, h)
			if cs != nil {
				cs.W, cs.H = v.content.W, v.content.H
				cs.scrollToCursor()
			}
			prefix, anchor, cursor = "", -1, -1
			continue
		case k, ok = <-keyCh:
			if !ok {
				return nil
			}
		}

		if help { // any key returns from help to where you were
			help = false
			continue
		}

		if mode == modeCopy {
			act, yanked := cs.Handle(k)
			switch act {
			case ActExit:
				return nil
			case ActYank:
				copyAndConfirm(client, yanked, cfg)
				if cfg.FlashMs > 0 {
					flashAndDwell(out, sgr, cfg.FlashMs, flashFrameCopy(v, cs, copyStyled))
				}
				return nil
			}
			continue
		}

		switch {
		case k.Kind == KeyEsc && mode == modeLine && anchor >= 0:
			// Staged Esc: clear the selection first, quit on the next Esc.
			prefix, anchor, cursor = "", -1, -1
		case k.Kind == KeyEsc || (k.Kind == KeyCtrl && k.R == 'c'):
			return nil
		case k.Kind == KeyRune && k.R == '?':
			help = true
			prefix = ""
		case k.Kind == KeyRune && k.R == '[': // reserved: enter copy mode
			enterCopy()
		case k.Kind == KeyTab: // toggles word mode (labels every word)
			if mode == modeWord {
				mode = modeToken
			} else {
				d.ensureWords(cfg)
				mode = modeWord
			}
			prefix, anchor, cursor = "", -1, -1
		case k.Kind == KeyRune && k.R == ' ': // toggles token/line mode
			if mode == modeLine {
				mode = modeToken
			} else {
				mode = modeLine
			}
			prefix, anchor, cursor = "", -1, -1
		case mode == modeLine && anchor >= 0 && (k.Kind == KeyDown || (k.Kind == KeyRune && k.R == 'j')):
			cursor = v.ll.NextLine(cursor) // vim-style extend down
			prefix = ""
		case mode == modeLine && anchor >= 0 && (k.Kind == KeyUp || (k.Kind == KeyRune && k.R == 'k')):
			cursor = v.ll.PrevLine(cursor) // vim-style extend up
			prefix = ""
		case k.Kind == KeyEnter || (mode == modeLine && anchor >= 0 && k.Kind == KeyRune && unicode.ToLower(k.R) == 'y'):
			if mode == modeLine && anchor >= 0 {
				if anchor == cursor {
					copyAndConfirm(client, SingleLineText(d.lines, anchor), cfg)
				} else {
					copyAndConfirm(client, RangeText(d.lines, anchor, cursor), cfg)
				}
				if cfg.FlashMs > 0 {
					flashAndDwell(out, sgr, cfg.FlashMs, flashFrameLine(v, d.targetStyled, anchor, cursor))
				}
				return nil
			}
		case k.Kind == KeyRune:
			r := unicode.ToLower(k.R)
			if !strings.ContainsRune(cfg.Alphabet, r) {
				prefix = ""
				continue
			}
			prefix += string(r)
			if mode == modeToken || mode == modeWord {
				if picked, ok := lm.Exact(prefix); ok {
					copyAndConfirm(client, picked, cfg)
					if cfg.FlashMs > 0 {
						flashAndDwell(out, sgr, cfg.FlashMs, flashFrameToken(v, d.text, cands, picked))
					}
					return nil
				}
				if !lm.HasPrefix(prefix) {
					prefix = ""
				}
			} else {
				if line, ok := v.ll.Exact(prefix); ok {
					prefix = ""
					if anchor < 0 {
						anchor, cursor = line, line // first label: set the anchor
					} else {
						cursor = line // jump the moving end; enter confirms
					}
				} else if !v.ll.HasPrefix(prefix) {
					prefix = ""
				}
			}
		default: // arrows etc. reset the pending label
			prefix = ""
		}
	}
}
