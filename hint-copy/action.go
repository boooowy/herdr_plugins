package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
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

const pluginID = "kayakatu.hint-copy"

// runAction is the keybinding entrypoint. It runs server-side (no terminal):
// resolve the focused pane, make sure there is something to copy, then ask
// herdr to open the hints overlay anchored over that pane. mode "copy" makes
// the overlay start directly in copy mode (and skips the emptiness pre-check
// — the scrollback can hold content even when the visible screen is blank).
func runAction(mode string) {
	client, err := newHerdrClient()
	if err != nil {
		errExit(err)
	}
	paneID := resolveTargetPane(client)
	if paneID == "" {
		errExit("could not resolve the focused pane")
	}

	if mode != "copy" {
		// Pre-check: line mode can copy any non-blank line, so only a truly
		// empty screen skips the overlay in favor of a toast.
		text, err := client.paneRead(paneID, "visible")
		if err != nil {
			errExit("read pane:", err)
		}
		cfg := loadConfig()
		if len(Extract(text, cfg)) == 0 && strings.TrimSpace(text) == "" {
			client.notify("Hint Copy", "Nothing to copy on screen", "none")
			return
		}
	}

	env := map[string]string{envTargetPane: paneID}
	if mode == "copy" {
		env["HINTCOPY_MODE"] = "copy"
	}
	if lay, lerr := client.paneLayout(paneID); lerr == nil && len(lay.Panes) >= 2 {
		if data, merr := json.Marshal(lay); merr == nil {
			env[envLayout] = base64.StdEncoding.EncodeToString(data)
		}
	}
	// Overlay panes always anchor to the active pane; herdr rejects an
	// explicit target_pane_id here. The pane to read still travels via env.
	if err := client.pluginPaneOpen(pluginID, "hints", "overlay", "", true, env); err != nil {
		errExit("open overlay:", err)
	}
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
