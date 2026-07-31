package main

import (
	"os/exec"
	"runtime"
	"strings"
)

// openInBrowser launches the system browser for url and reports the outcome
// in the footer.
func openInBrowser(a *app, url string) {
	if url == "" {
		return
	}
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	if err := exec.Command(cmd, url).Start(); err != nil {
		a.status = "ブラウザを起動できません: " + err.Error()
		return
	}
	debugf("browser: %s", url)
}

// copyToClipboard puts text on the system clipboard (pbcopy on macOS,
// wl-copy/xclip/xsel on Linux) and reports the outcome in the footer.
func copyToClipboard(a *app, text string) {
	var candidates [][]string
	if runtime.GOOS == "darwin" {
		candidates = [][]string{{"pbcopy"}}
	} else {
		candidates = [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "-ib"}}
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			a.status = "コピーしました: " + truncateWidth(text, 60)
			return
		}
	}
	a.status = "クリップボードにコピーできません (pbcopy/xclip が見つかりません)"
}
