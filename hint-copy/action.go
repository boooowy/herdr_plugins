package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"time"
)

// envLayout carries the tab layout captured BEFORE the overlay opens. Once
// the overlay exists, pane.layout reports it as a split of the launch pane
// (shrinking the real pane's rect), so the pre-overlay snapshot is the only
// correct source for compositing.
const envLayout = "HINTCOPY_LAYOUT"

// envTargetPane carries the launch pane's id into the overlay process. The
// overlay's own HERDR_PANE_ID is the overlay pane itself, so the original
// pane must travel explicitly.
const envTargetPane = "HINTCOPY_TARGET_PANE_ID"

const pluginID = "boooowy.hint-copy"

// runAction is the keybinding entrypoint. It runs server-side (no terminal):
// resolve the focused pane, snapshot the tab layout, and ask herdr to open
// the hints overlay anchored over that pane. Everything else — including the
// empty-screen check — happens in the overlay process, so the overlay opens
// with as few socket round trips as possible. mode "copy" makes the overlay
// start directly in copy mode.
func runAction(mode string) {
	t0 := time.Now()
	client, err := newHerdrClient()
	if err != nil {
		errExit(err)
	}
	paneID := resolveTargetPane(client)
	if paneID == "" {
		errExit("could not resolve the focused pane")
	}

	env := map[string]string{envTargetPane: paneID}
	if mode == "copy" {
		env["HINTCOPY_MODE"] = "copy"
	}
	// The snapshot travels even for a single-pane tab: the UI needs the tab
	// area to recognize the overlay pty's final size, and the composite path
	// keeps the pane border in place so nothing appears to move.
	if lay, lerr := client.paneLayout(paneID); lerr == nil && len(lay.Panes) >= 1 {
		if data, merr := json.Marshal(lay); merr == nil {
			env[envLayout] = base64.StdEncoding.EncodeToString(data)
		}
	}
	// Overlay panes always anchor to the active pane; herdr rejects an
	// explicit target_pane_id here. The pane to read still travels via env.
	if err := client.pluginPaneOpen(pluginID, "hints", "overlay", "", true, env); err != nil {
		errExit("open overlay:", err)
	}
	debugf("action: overlay opened after %s", time.Since(t0))
}

// resolveTargetPane picks the pane whose screen we hint: HERDR_PANE_ID when
// herdr injected it, otherwise whichever pane is focused right now.
func resolveTargetPane(client *herdrClient) string {
	if id := os.Getenv("HERDR_PANE_ID"); id != "" {
		return id
	}
	id, err := client.focusedPaneID()
	if err != nil {
		return ""
	}
	return id
}
