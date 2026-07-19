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

func renderLinesPlain(t *testing.T, screen string, width, height int, cfg Config, prefix string, anchor int) (plain, raw string) {
	t.Helper()
	lines := strings.Split(screen, "\n")
	offset, view := viewWindow(len(lines), height)
	ll := AssignLineLabels(lines, offset, view, cfg)
	var sb strings.Builder
	RenderLines(&sb, screen, ll, prefix, anchor, width, height, cfg)
	return sgrRe.ReplaceAllString(sb.String(), ""), sb.String()
}

// Every non-empty line gets label + space in a uniform margin, so two lines
// with the same indent stay column-aligned; empty lines get no label.
func TestRenderLinesMargin(t *testing.T) {
	out, _ := renderLinesPlain(t, "make build\n\n  go test", 80, 24, defaultConfig(), "", -1)
	rows := strings.Split(out, "\r\n")
	if rows[0] != "a make build" {
		t.Errorf("row0 = %q", rows[0])
	}
	if rows[1] != "" {
		t.Errorf("empty row = %q", rows[1])
	}
	if rows[2] != "s   go test" {
		t.Errorf("row2 = %q (margin misaligned)", rows[2])
	}
}

func TestRenderLinesAnchorInverse(t *testing.T) {
	_, raw := renderLinesPlain(t, "one\ntwo", 80, 24, defaultConfig(), "", 1)
	if !strings.Contains(raw, "\x1b[7m") {
		t.Error("anchor line missing inverse video")
	}
}

func TestRenderLinesFooterStates(t *testing.T) {
	out, _ := renderLinesPlain(t, "one\ntwo", 80, 24, defaultConfig(), "", -1)
	if !strings.Contains(out, "line mode") {
		t.Errorf("idle footer: %q", out)
	}
	out, _ = renderLinesPlain(t, "one\ntwo", 80, 24, defaultConfig(), "", 0)
	if !strings.Contains(out, "copy range") {
		t.Errorf("anchor footer: %q", out)
	}
}

func TestRenderLinesClipsAtWidth(t *testing.T) {
	out, _ := renderLinesPlain(t, strings.Repeat("x", 200), 10, 24, defaultConfig(), "", -1)
	row := strings.Split(out, "\r\n")[0]
	if len([]rune(row)) > 10 {
		t.Errorf("row too long: %d", len([]rune(row)))
	}
}

func TestRenderHelp(t *testing.T) {
	cfg := defaultConfig()
	cfg.Patterns = map[string]bool{"number": false}
	cfg.CustomPatterns = []CustomPattern{{Name: "jira", Regex: `X-\d+`}}
	var sb strings.Builder
	RenderHelp(&sb, 100, 40, cfg)
	out := sgrRe.ReplaceAllString(sb.String(), "")
	for _, want := range []string{"line mode", "copy the range", "url", "quoted", "jira", "press any key"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q", want)
		}
	}
	if !strings.Contains(out, "number        off") {
		t.Errorf("disabled pattern not shown as off:\n%s", out)
	}
	if !strings.Contains(out, "ipv6          off") {
		t.Error("ipv6 default-off not shown")
	}
}

func TestRenderTokenFooterMentionsLineMode(t *testing.T) {
	out := renderPlain(t, "see /tmp/x.log ok", 80, 24, defaultConfig(), "")
	if !strings.Contains(out, "space: line mode") {
		t.Errorf("footer: %q", out)
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
