package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
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
)

// uiView is everything derived from the terminal size and the captured
// panes. It is rebuilt wholesale on SIGWINCH — herdr resizes the overlay pty
// shortly after launch, so the first build can happen at a transient size.
type uiView struct {
	comp         *composite
	base         *Screen
	text         string
	targetStyled [][]Cell
	lines        []string
	cands        []Candidate
	lm           *LabelMap
	content      Rect
	ll           *LineLabels
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

// waitForSize polls the terminal size until it settles into the expected
// area-minus-border relation with the snapshot (herdr resizes the fresh
// overlay pty once or twice right after spawn). It gives up after 600ms and
// returns whatever is current — the SIGWINCH handler catches late resizes.
func waitForSize(lay *paneLayoutResult) (int, int) {
	deadline := time.Now().Add(600 * time.Millisecond)
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
		if time.Now().After(deadline) {
			debugf("waitForSize: gave up at term=%dx%d area=%dx%d", w, h, lay.Area.Width, lay.Area.Height)
			return w, h
		}
		time.Sleep(15 * time.Millisecond)
	}
}

// buildUIView captures the panes and derives all rendering state for the
// given terminal size.
func buildUIView(client *herdrClient, target string, lay *paneLayoutResult, cfg Config, tbl *styleTable, w, h int) (*uiView, error) {
	v := &uiView{}
	v.comp = buildComposite(client, lay, target, w, h, cfg, tbl)
	if v.comp != nil {
		v.text = joinPlain(v.comp.plain[target])
		v.targetStyled = v.comp.styled[target]
	} else {
		ansiText, _, err := client.paneReadFull(target, "visible", 0, true)
		if err != nil {
			return nil, err
		}
		var plainLines []string
		v.targetStyled, plainLines = parseANSI(ansiText, tbl)
		v.text = joinPlain(plainLines)
	}
	v.lines = strings.Split(v.text, "\n")
	v.cands = Extract(v.text, cfg)
	v.lm = AssignLabels(v.cands, cfg)

	if v.comp != nil {
		v.content = v.comp.target
		if v.content.Y+v.content.H > h-1 {
			v.content.H = h - 1 - v.content.Y
		}
	} else {
		_, view := viewWindow(len(v.lines), h)
		v.content = Rect{X: 0, Y: 0, W: w, H: view}
	}
	lineOffset := 0
	if len(v.lines) > v.content.H {
		lineOffset = len(v.lines) - v.content.H
	}
	v.ll = AssignLineLabels(v.lines, lineOffset, v.content.H, cfg)

	if v.comp != nil {
		v.base = v.comp.base
	} else {
		v.base = NewScreen(w, h)
		v.base.styles = tbl
		paintPaneCells(v.base, v.content, v.targetStyled)
	}
	return v, nil
}

// runUI is the overlay pane's entrypoint. It re-reads the target pane itself
// (never its own HERDR_PANE_ID — that is the overlay), renders the hints, and
// waits for input. When the tab is split it composites the whole tab from
// the action's pre-overlay layout snapshot so no pane appears to move. Space
// toggles token/line mode; '[' (or the copy-mode action) enters the
// vim-style copy mode with scrollback. Exiting closes the overlay.
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

	w, h := waitForSize(lay)
	debugf("startup: term=%dx%d", w, h)

	tbl := newStyleTable()
	v, err := buildUIView(client, target, lay, cfg, tbl, w, h)
	if err != nil {
		errExit("read pane:", err)
	}

	if !startCopy && len(v.cands) == 0 && strings.TrimSpace(v.text) == "" {
		client.notify("Hint Copy", "Nothing to copy on screen", "none")
		return
	}

	// A screen with no token candidates but visible text starts straight in
	// line mode — that is the only thing left to copy.
	mode := modeToken
	if len(v.cands) == 0 {
		mode = modeLine
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
			plainBuf, styled, trunc := v.text, v.targetStyled, false
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
			return
		}
	}

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

	sgr := sgrTable(cfg)

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

	for {
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
			PaintTokenOverlay(frame, v.content, v.text, v.cands, v.lm, prefix)
			PaintFooter(frame, tokenFooter(prefix))
		default:
			frame = v.base.Clone()
			clearRect(frame, v.content)
			PaintLineModeStyled(frame, v.content, v.targetStyled, v.ll, prefix, anchor, cursor)
			PaintFooter(frame, lineFooter(prefix, anchor, cursor))
		}
		frame.Flush(out, sgr)
		out.Flush()

		var k Key
		var ok bool
		select {
		case <-winch:
			// The overlay pty settled to a new size: rebuild everything at
			// the fresh geometry and repaint.
			w, h = waitForSize(lay)
			debugf("winch: term=%dx%d", w, h)
			if nv, verr := buildUIView(client, target, lay, cfg, tbl, w, h); verr == nil {
				v = nv
			}
			if cs != nil {
				cs.W, cs.H = v.content.W, v.content.H
				cs.scrollToCursor()
			}
			prefix, anchor, cursor = "", -1, -1
			continue
		case k, ok = <-keyCh:
			if !ok {
				return
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
				return
			case ActYank:
				copyAndConfirm(client, yanked, cfg)
				return
			}
			continue
		}

		switch {
		case k.Kind == KeyEsc && mode == modeLine && anchor >= 0:
			// Staged Esc: clear the selection first, quit on the next Esc.
			prefix, anchor, cursor = "", -1, -1
		case k.Kind == KeyEsc || (k.Kind == KeyCtrl && k.R == 'c'):
			return
		case k.Kind == KeyRune && k.R == '?':
			help = true
			prefix = ""
		case k.Kind == KeyRune && k.R == '[': // reserved: enter copy mode
			enterCopy()
		case k.Kind == KeyRune && k.R == ' ': // toggles token/line mode
			if mode == modeToken {
				mode = modeLine
			} else {
				mode = modeToken
			}
			prefix, anchor, cursor = "", -1, -1
		case mode == modeLine && anchor >= 0 && (k.Kind == KeyDown || (k.Kind == KeyRune && k.R == 'j')):
			cursor = v.ll.NextLine(cursor) // vim-style extend down
			prefix = ""
		case mode == modeLine && anchor >= 0 && (k.Kind == KeyUp || (k.Kind == KeyRune && k.R == 'k')):
			cursor = v.ll.PrevLine(cursor) // vim-style extend up
			prefix = ""
		case k.Kind == KeyEnter:
			if mode == modeLine && anchor >= 0 {
				if anchor == cursor {
					copyAndConfirm(client, SingleLineText(v.lines, anchor), cfg)
				} else {
					copyAndConfirm(client, RangeText(v.lines, anchor, cursor), cfg)
				}
				return
			}
		case k.Kind == KeyRune:
			r := unicode.ToLower(k.R)
			if !strings.ContainsRune(cfg.Alphabet, r) {
				prefix = ""
				continue
			}
			prefix += string(r)
			if mode == modeToken {
				if picked, ok := v.lm.Exact(prefix); ok {
					copyAndConfirm(client, picked, cfg)
					return
				}
				if !v.lm.HasPrefix(prefix) {
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
