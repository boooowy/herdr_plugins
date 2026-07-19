package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"

	"golang.org/x/term"
)

type uiMode int

const (
	modeToken uiMode = iota
	modeLine
	modeCopy
)

// runUI is the overlay pane's entrypoint. It re-reads the target pane itself
// (never its own HERDR_PANE_ID — that is the overlay), renders the hints, and
// waits for input. When the tab is split it composites the whole tab so no
// pane appears to move. Space toggles token/line mode; '[' (or launching via
// the copy-mode action) enters the vim-style copy mode with scrollback.
// Exiting closes the overlay; herdr tears the pane down when its process ends.
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

	cfg := loadConfig()
	if cfg.loadErr != "" {
		client.notify("Hint Copy", cfg.loadErr, "none")
	}
	if _, bad := buildPatterns(cfg); len(bad) > 0 {
		client.notify("Hint Copy", "invalid custom pattern(s): "+strings.Join(bad, ", "), "none")
	}

	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		w, h = 80, 24
	}

	tbl := newStyleTable()
	comp := buildComposite(client, target, w, h, cfg, tbl)
	var text string
	var targetStyled [][]Cell
	if comp != nil {
		text = joinPlain(comp.plain[target])
		targetStyled = comp.styled[target]
	} else {
		ansiText, _, rerr := client.paneReadFull(target, "visible", 0, true)
		if rerr != nil {
			errExit("read pane:", rerr)
		}
		var plainLines []string
		targetStyled, plainLines = parseANSI(ansiText, tbl)
		text = joinPlain(plainLines)
	}

	cands := Extract(text, cfg)
	lines := strings.Split(text, "\n")
	if !startCopy && len(cands) == 0 && strings.TrimSpace(text) == "" {
		client.notify("Hint Copy", "Nothing to copy on screen", "none")
		return
	}
	lm := AssignLabels(cands, cfg)

	// The region the interaction lives in: the target pane's content rect in
	// composite mode (clipped above the footer row), the whole view
	// otherwise.
	var content Rect
	if comp != nil {
		content = comp.target
		if content.Y+content.H > h-1 {
			content.H = h - 1 - content.Y
		}
	} else {
		_, view := viewWindow(len(lines), h)
		content = Rect{X: 0, Y: 0, W: w, H: view}
	}
	lineOffset := 0
	if len(lines) > content.H {
		lineOffset = len(lines) - content.H
	}
	ll := AssignLineLabels(lines, lineOffset, content.H, cfg)

	// A screen with no token candidates but visible text starts straight in
	// line mode — that is the only thing left to copy.
	mode := modeToken
	if len(cands) == 0 {
		mode = modeLine
	}
	prefix := ""
	anchor := -1
	help := false
	var cs *CopyState
	var copyStyled [][]Cell

	// enterCopy lazily loads the scrollback buffer (colors kept); the visible
	// capture is the fallback when the recent read fails or is empty.
	enterCopy := func() {
		if cs == nil {
			plainBuf, styled, trunc := text, targetStyled, false
			if ansiText, tr, rerr := client.paneReadFull(target, "recent", cfg.ScrollbackLines, true); rerr == nil {
				sc, pl := parseANSI(ansiText, tbl)
				if p := joinPlain(pl); strings.TrimSpace(p) != "" {
					plainBuf, styled, trunc = p, sc, tr
				}
			}
			cs = NewCopyState(plainBuf, content.W, content.H, trunc)
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
	// The static base under every frame: the whole-tab composite, or the
	// target's own colored content when compositing is unavailable.
	base := (*Screen)(nil)
	if comp != nil {
		base = comp.base
	} else {
		base = NewScreen(w, h)
		base.styles = tbl
		paintPaneCells(base, content, targetStyled)
	}

	keys := newKeyReader(os.Stdin)
	for {
		var frame *Screen
		switch {
		case help:
			frame = NewScreen(w, h)
			PaintHelp(frame, Rect{X: 0, Y: 0, W: w, H: h - 1}, cfg)
			PaintFooter(frame, " press any key to return")
		case mode == modeCopy:
			frame = base.Clone()
			clearRect(frame, content)
			PaintCopyMode(frame, content, cs, copyStyled)
			PaintFooter(frame, copyFooter(cs))
		case mode == modeToken:
			frame = base.Clone()
			PaintTokenOverlay(frame, content, text, cands, lm, prefix)
			PaintFooter(frame, tokenFooter(prefix))
		default:
			frame = base.Clone()
			clearRect(frame, content)
			PaintLineModeStyled(frame, content, targetStyled, ll, prefix, anchor)
			PaintFooter(frame, lineFooter(prefix, anchor))
		}
		frame.Flush(out, sgr)
		out.Flush()

		k, err := keys.Read()
		if err != nil {
			return
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
			prefix, anchor = "", -1
		case k.Kind == KeyEnter:
			if mode == modeLine && anchor >= 0 {
				copyAndConfirm(client, SingleLineText(lines, anchor), cfg)
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
				if picked, ok := lm.Exact(prefix); ok {
					copyAndConfirm(client, picked, cfg)
					return
				}
				if !lm.HasPrefix(prefix) {
					prefix = ""
				}
			} else {
				if line, ok := ll.Exact(prefix); ok {
					prefix = ""
					switch {
					case anchor < 0:
						anchor = line // first label: set the anchor, keep going
					case line == anchor:
						copyAndConfirm(client, SingleLineText(lines, anchor), cfg)
						return
					default:
						copyAndConfirm(client, RangeText(lines, anchor, line), cfg)
						return
					}
				} else if !ll.HasPrefix(prefix) {
					prefix = ""
				}
			}
		default: // arrows etc. reset the pending label
			prefix = ""
		}
	}
}
