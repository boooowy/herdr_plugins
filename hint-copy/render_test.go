package main

import (
	"regexp"
	"strings"
	"testing"
)

var sgrRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func renderPlain(t *testing.T, screen string, width, height int, cfg Config, prefix string) string {
	t.Helper()
	cands := Extract(screen, cfg)
	lm := AssignLabels(cands, cfg)
	var sb strings.Builder
	Render(&sb, screen, cands, lm, prefix, width, height, cfg)
	return sgrRe.ReplaceAllString(sb.String(), "")
}

// The label replaces the leading cells of the match: "a" + the rest of the
// path, at the exact position where the path started.
func TestRenderLabelReplacesLeadingCells(t *testing.T) {
	out := renderPlain(t, "see /tmp/x.log ok", 80, 24, defaultConfig(), "")
	line := strings.Split(out, "\r\n")[0]
	if !strings.Contains(line, "see atmp/x.log ok") {
		t.Errorf("line = %q", line)
	}
}

// CJK text before a candidate must not shift it: sequential rendering keeps
// the original rune sequence, label substitution aside.
func TestRenderCJKLineKeepsContent(t *testing.T) {
	out := renderPlain(t, "設定は /etc/foo.conf です", 80, 24, defaultConfig(), "")
	line := strings.Split(out, "\r\n")[0]
	if !strings.Contains(line, "設定は aetc/foo.conf です") {
		t.Errorf("line = %q", line)
	}
}

func TestRenderClipsAtWidth(t *testing.T) {
	out := renderPlain(t, strings.Repeat("x", 200), 10, 24, defaultConfig(), "")
	line := strings.Split(out, "\r\n")[0]
	if len([]rune(line)) > 10 {
		t.Errorf("line too long: %d", len([]rune(line)))
	}
}

// Taller capture than the overlay: the bottom lines win.
func TestRenderPrefersBottomLines(t *testing.T) {
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "line"+strings.Repeat("x", i%3))
	}
	lines = append(lines, "tail /var/log/last.log")
	out := renderPlain(t, strings.Join(lines, "\n"), 80, 10, defaultConfig(), "")
	if !strings.Contains(out, "avar/log/last.log") {
		t.Errorf("bottom line missing: %q", out)
	}
	if strings.Contains(out, "line\nline") && strings.Contains(out, lines[0]+"\r\n"+lines[1]) {
		t.Error("top lines should be clipped")
	}
}

func TestRenderFooterShowsPrefix(t *testing.T) {
	screen := ""
	for i := 0; i < 30; i++ {
		screen += "/tmp/f" + strings.Repeat("y", i+1) + "\n"
	}
	out := renderPlain(t, screen, 80, 40, defaultConfig(), "a")
	if !strings.Contains(out, "a_") {
		t.Error("typed prefix not shown in footer")
	}
}
