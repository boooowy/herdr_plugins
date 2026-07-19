package main

import (
	"fmt"
	"os"
	"strconv"
)

// runFrame is a development helper (`hint-copy frame <w> <h>`): it builds the
// composite for HINTCOPY_TARGET_PANE_ID and prints one token-mode frame to
// stdout. Useful for inspecting composite geometry without opening a real
// overlay.
func runFrame(args []string) {
	if len(args) < 2 {
		errExit("usage: hint-copy frame <width> <height>")
	}
	w, _ := strconv.Atoi(args[0])
	h, _ := strconv.Atoi(args[1])
	client, err := newHerdrClient()
	if err != nil {
		errExit(err)
	}
	target := os.Getenv(envTargetPane)
	if target == "" {
		errExit("missing " + envTargetPane)
	}
	cfg := loadConfig()
	tbl := newStyleTable()
	comp := buildComposite(client, target, w, h, cfg, tbl)
	if comp == nil {
		fmt.Println("composite: nil (would fall back to full-screen)")
		return
	}
	fmt.Printf("composite: target rect %+v\n", comp.target)
	text := joinPlain(comp.plain[target])
	cands := Extract(text, cfg)
	lm := AssignLabels(cands, cfg)
	frame := comp.base.Clone()
	content := comp.target
	if content.Y+content.H > h-1 {
		content.H = h - 1 - content.Y
	}
	PaintTokenOverlay(frame, content, text, cands, lm, "")
	PaintFooter(frame, tokenFooter(""))
	frame.Flush(os.Stdout, sgrTable(cfg))
	fmt.Println()
}
