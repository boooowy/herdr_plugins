package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"

	"golang.org/x/term"
)

// runUI is the overlay pane's entrypoint. It re-reads the target pane itself
// (never its own HERDR_PANE_ID — that is the overlay), renders the hints, and
// waits for a label or a cancel. Exiting closes the overlay; herdr tears the
// pane down when its process ends.
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
	if len(cands) == 0 {
		client.notify("Hint Copy", "No candidates on screen", "none")
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

	out := bufio.NewWriter(os.Stdout)
	fmt.Fprint(out, "\x1b[?1049h\x1b[?25l") // alt screen, hide cursor
	defer func() {
		fmt.Fprint(out, "\x1b[?25h\x1b[?1049l")
		out.Flush()
	}()

	in := bufio.NewReader(os.Stdin)
	prefix := ""
	for {
		Render(out, text, cands, lm, prefix, w, h, cfg)
		out.Flush()

		b, err := in.ReadByte()
		if err != nil {
			return
		}
		switch b {
		case 0x1b, 0x03: // Esc / Ctrl-C — any escape sequence cancels too
			return
		default:
			r := unicode.ToLower(rune(b))
			if !strings.ContainsRune(cfg.Alphabet, r) {
				prefix = ""
				continue
			}
			prefix += string(r)
			if picked, ok := lm.Exact(prefix); ok {
				copyAndConfirm(client, picked, cfg)
				return
			}
			if !lm.HasPrefix(prefix) {
				prefix = ""
			}
		}
	}
}
