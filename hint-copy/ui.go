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
)

// runUI is the overlay pane's entrypoint. It re-reads the target pane itself
// (never its own HERDR_PANE_ID — that is the overlay), renders the hints, and
// waits for a label or a cancel. Space toggles between token mode (URLs,
// paths, ...) and line mode (whole lines, with anchor+label range selection).
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
	text, err := client.paneRead(target, "visible")
	if err != nil {
		errExit("read pane:", err)
	}

	cfg := loadConfig()
	if cfg.loadErr != "" {
		client.notify("Hint Copy", cfg.loadErr, "none")
	}
	if _, bad := buildPatterns(cfg); len(bad) > 0 {
		client.notify("Hint Copy", "invalid custom pattern(s): "+strings.Join(bad, ", "), "none")
	}
	cands := Extract(text, cfg)
	lines := strings.Split(text, "\n")
	if len(cands) == 0 && strings.TrimSpace(text) == "" {
		client.notify("Hint Copy", "Nothing to copy on screen", "none")
		return
	}
	lm := AssignLabels(cands, cfg)

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		errExit("raw mode:", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		w, h = 80, 24
	}
	offset, view := viewWindow(len(lines), h)
	ll := AssignLineLabels(lines, offset, view, cfg)

	out := bufio.NewWriter(os.Stdout)
	fmt.Fprint(out, "\x1b[?1049h\x1b[?25l") // alt screen, hide cursor
	defer func() {
		fmt.Fprint(out, "\x1b[?25h\x1b[?1049l")
		out.Flush()
	}()

	// A screen with no token candidates but visible text starts straight in
	// line mode — that is the only thing left to copy.
	mode := modeToken
	if len(cands) == 0 {
		mode = modeLine
	}
	prefix := ""
	anchor := -1
	help := false

	in := bufio.NewReader(os.Stdin)
	for {
		switch {
		case help:
			RenderHelp(out, w, h, cfg)
		case mode == modeToken:
			Render(out, text, cands, lm, prefix, w, h, cfg)
		default:
			RenderLines(out, text, ll, prefix, anchor, w, h, cfg)
		}
		out.Flush()

		b, err := in.ReadByte()
		if err != nil {
			return
		}
		if help { // any key returns from help to where you were
			help = false
			continue
		}
		switch {
		case b == 0x1b || b == 0x03: // Esc / Ctrl-C — any escape sequence cancels too
			return
		case b == '?':
			help = true
			prefix = ""
		case b == ' ': // checked before the alphabet so it always toggles
			if mode == modeToken {
				mode = modeLine
			} else {
				mode = modeToken
			}
			prefix, anchor = "", -1
		case b == '\r' || b == '\n':
			if mode == modeLine && anchor >= 0 {
				copyAndConfirm(client, SingleLineText(lines, anchor), cfg)
				return
			}
		default:
			r := unicode.ToLower(rune(b))
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
		}
	}
}
