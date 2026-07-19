package main

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf8"
)

// copyAndConfirm puts text on the clipboard and reports the outcome as a
// herdr toast.
func copyAndConfirm(client *herdrClient, text string, cfg Config) {
	if err := copyToClipboard(text, cfg); err != nil {
		client.notify("Hint Copy failed", err.Error(), "none")
		return
	}
	client.notify("Copied", truncate(text, 60), "done")
}

// copyToClipboard writes text to the system clipboard: pbcopy on macOS, the
// first of wl-copy/xclip/xsel elsewhere. With cfg.OSC52 it additionally emits
// an OSC 52 sequence straight to /dev/tty (best effort — useful over SSH, and
// it becomes the only channel when no clipboard tool exists).
func copyToClipboard(text string, cfg Config) error {
	var err error
	switch runtime.GOOS {
	case "darwin":
		err = pipeTo(text, "pbcopy")
	default:
		err = linuxCopy(text)
	}
	if cfg.OSC52 {
		oscErr := osc52(text)
		if err != nil && oscErr == nil {
			return nil
		}
	}
	return err
}

// pipeTo feeds text to a command's stdin.
func pipeTo(text string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// linuxCopy tries the usual clipboard tools in order of preference.
func linuxCopy(text string) error {
	candidates := [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "-ib"},
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err == nil {
			return pipeTo(text, c[0], c[1:]...)
		}
	}
	return errors.New("no clipboard tool found (wl-copy, xclip or xsel); or set osc52 = true")
}

// osc52 asks the outer terminal to set its clipboard via the OSC 52 escape,
// written to /dev/tty rather than stdout (stdout is the herdr-rendered
// overlay, and pass-through is not guaranteed).
func osc52(text string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer tty.Close()
	_, err = tty.WriteString("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07")
	return err
}

// truncate shortens s to at most n runes for the toast body.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}
