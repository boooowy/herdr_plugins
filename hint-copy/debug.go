package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// debugf appends a timestamped line to /tmp/hintcopy-debug.log, but only
// when that file already exists (`touch` it to enable, remove to disable) —
// the overlay runs server-side with no visible stderr, so this is the only
// way to observe startup behavior in a real herdr session.
func debugf(format string, args ...any) {
	if _, err := os.Stat("/tmp/hintcopy-debug.log"); err != nil {
		return
	}
	f, err := os.OpenFile("/tmp/hintcopy-debug.log", os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s [%d] %s\n", time.Now().Format("15:04:05.000"), os.Getpid(), fmt.Sprintf(format, args...))
}

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
	lay, lerr := client.paneLayout(target)
	if lerr != nil {
		errExit("layout:", lerr)
	}
	comp := buildComposite(client, lay, target, w, h, cfg, tbl)
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
