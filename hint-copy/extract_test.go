package main

import (
	"strings"
	"testing"
)

func texts(cands []Candidate) []string {
	var out []string
	for _, c := range cands {
		out = append(out, c.Text)
	}
	return out
}

func extractTexts(t *testing.T, screen string) []string {
	t.Helper()
	return texts(Extract(screen, defaultConfig()))
}

func TestExtractURL(t *testing.T) {
	cases := map[string]string{
		"Deployed to https://example.com/app now":            "https://example.com/app",
		"see (https://example.com/x).":                       "https://example.com/x",
		"wiki https://en.wikipedia.org/wiki/Go_(language) !": "https://en.wikipedia.org/wiki/Go_(language)",
		"file file:///tmp/x.log here":                        "file:///tmp/x.log",
		"trailing https://example.com,":                      "https://example.com",
	}
	for in, want := range cases {
		got := extractTexts(t, in)
		if len(got) == 0 || got[0] != want {
			t.Errorf("Extract(%q) = %v, want first %q", in, got, want)
		}
	}
}

func TestExtractPath(t *testing.T) {
	got := extractTexts(t, "config at /etc/nginx/nginx.conf and ~/work/app/main.go plus src/lib/util.ts")
	want := []string{"/etc/nginx/nginx.conf", "~/work/app/main.go", "src/lib/util.ts"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractIPv4(t *testing.T) {
	got := extractTexts(t, "server 192.168.1.10 and 10.0.0.1:8080 responded")
	want := []string{"192.168.1.10", "10.0.0.1:8080"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v want %v", got, want)
	}
	if got := extractTexts(t, "not an ip 999.999.999.999 ok"); len(got) != 0 {
		t.Errorf("bogus ip matched: %v", got)
	}
}

func TestExtractSHA(t *testing.T) {
	got := extractTexts(t, "commit 3f2a91c fixed it, full 0cce37b56fb94e2ba53fe14ee0ae5d1e1f22a611")
	want := []string{"3f2a91c", "0cce37b56fb94e2ba53fe14ee0ae5d1e1f22a611"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v want %v", got, want)
	}
	// Pure decimal runs must not count as SHAs (they are number matches).
	cands := Extract("pid 1234567 running", defaultConfig())
	if len(cands) != 1 || cands[0].Pattern != "number" {
		t.Errorf("decimal should match as number, got %+v", cands)
	}
}

func TestExtractEmailAndUUID(t *testing.T) {
	got := extractTexts(t, "mail user@example.com id 123e4567-e89b-12d3-a456-426614174000 done")
	want := []string{"user@example.com", "123e4567-e89b-12d3-a456-426614174000"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v want %v", got, want)
	}
}

// A URL that contains a path must yield only the URL: overlap resolution keeps
// the longest match at a position.
func TestOverlapURLBeatsPath(t *testing.T) {
	got := extractTexts(t, "open https://example.com/a/b/c.html now")
	if len(got) != 1 || got[0] != "https://example.com/a/b/c.html" {
		t.Errorf("got %v", got)
	}
}

func TestRuneOffsetsWithCJK(t *testing.T) {
	line := "設定は /etc/foo.conf です"
	cands := Extract(line, defaultConfig())
	if len(cands) != 1 {
		t.Fatalf("got %v", cands)
	}
	c := cands[0]
	runes := []rune(line)
	if string(runes[c.StartRune:c.EndRune]) != "/etc/foo.conf" {
		t.Errorf("offsets wrong: %q", string(runes[c.StartRune:c.EndRune]))
	}
}

func TestCapUnique(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		b.WriteString("/tmp/file")
		b.WriteString(strings.Repeat("x", i+1))
		b.WriteString("\n")
	}
	cfg := defaultConfig()
	cfg.MaxCandidates = 10
	cands := Extract(b.String(), cfg)
	uniq := map[string]bool{}
	for _, c := range cands {
		uniq[c.Text] = true
	}
	if len(uniq) != 10 {
		t.Errorf("unique = %d, want 10", len(uniq))
	}
}

// The same string twice is kept as two candidates (both get labeled) sharing
// one unique slot.
func TestDuplicateOccurrencesKept(t *testing.T) {
	cands := Extract("x /tmp/a.log y\nz /tmp/a.log w", defaultConfig())
	if len(cands) != 2 {
		t.Fatalf("got %d candidates: %v", len(cands), texts(cands))
	}
}

func TestPatternToggleAndCustom(t *testing.T) {
	cfg := defaultConfig()
	cfg.Patterns = map[string]bool{"sha": false}
	cfg.CustomPatterns = []CustomPattern{{Name: "jira", Regex: `[A-Z]{2,10}-\d+`}}
	got := texts(Extract("fix ABC-123 at 3f2a91c", cfg))
	if strings.Join(got, "|") != "ABC-123" {
		t.Errorf("got %v", got)
	}
}

func TestExtractMarkdownURL(t *testing.T) {
	cands := Extract("see [the docs](https://example.com/docs) here", defaultConfig())
	if len(cands) != 1 {
		t.Fatalf("got %v", texts(cands))
	}
	c := cands[0]
	if c.Pattern != "markdown_url" || c.Text != "https://example.com/docs" {
		t.Errorf("got %+v", c)
	}
	line := []rune("see [the docs](https://example.com/docs) here")
	if string(line[c.StartRune:c.EndRune]) != c.Text {
		t.Errorf("group span misaligned: %q", string(line[c.StartRune:c.EndRune]))
	}
}

func TestExtractDiffPath(t *testing.T) {
	screen := "--- a/src/main.go\n+++ b/src/main.go\ndiff --git a/pkg/x.go b/pkg/x.go"
	cands := Extract(screen, defaultConfig())
	for _, c := range cands {
		if c.Pattern != "diff_path" {
			t.Errorf("leftover %s candidate: %q", c.Pattern, c.Text)
		}
	}
	got := texts(cands)
	want := []string{"src/main.go", "src/main.go", "pkg/x.go"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractDockerDigest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("0abc", 16)
	cands := Extract("pulled "+digest+" done", defaultConfig())
	if len(cands) != 1 || cands[0].Pattern != "docker" || cands[0].Text != digest {
		t.Errorf("got %v", cands)
	}
}

func TestExtractAddressAndColor(t *testing.T) {
	got := extractTexts(t, "at 0xDEADbeef color #a1b2c3 and #fff but not #zzzz")
	want := []string{"0xDEADbeef", "#a1b2c3", "#fff"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractLinenum(t *testing.T) {
	cands := Extract("error at src/lib/util.ts:45:12 and main.go:123 but time 12:34", defaultConfig())
	got := texts(cands)
	want := []string{"src/lib/util.ts:45:12", "main.go:123"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v want %v", got, want)
	}
	for _, c := range cands {
		if c.Pattern != "linenum" {
			t.Errorf("%q matched as %s, want linenum", c.Text, c.Pattern)
		}
	}
}

func TestExtractKubernetes(t *testing.T) {
	cands := Extract("restarted deployment.apps/zookeeper ok", defaultConfig())
	if len(cands) != 1 || cands[0].Pattern != "kubernetes" || cands[0].Text != "deployment.apps/zookeeper" {
		t.Errorf("got %+v", cands)
	}
}

func TestExtractQuoted(t *testing.T) {
	// Content is copied without the quotes; escapes kept as-is.
	cands := Extract(`msg "hello \"world\"" and '/tmp/x.log' end`, defaultConfig())
	got := texts(cands)
	want := []string{`hello \"world\"`, "/tmp/x.log"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v want %v", got, want)
	}
	for _, c := range cands {
		if c.Pattern != "quoted" {
			t.Errorf("%q matched as %s, want quoted", c.Text, c.Pattern)
		}
	}
	// Apostrophes never open a single-quoted string.
	if got := extractTexts(t, "don't panic it's fine"); len(got) != 0 {
		t.Errorf("apostrophe matched: %v", got)
	}
}

func TestExtractDatetime(t *testing.T) {
	got := extractTexts(t, "on 2026-07-19 and 2026-07-19T12:34:56Z done")
	want := []string{"2026-07-19", "2026-07-19T12:34:56Z"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestExtractSemverAndNumber(t *testing.T) {
	cands := Extract("release v1.2.3 and 1.2.3-rc.1 port 8080 pid 999", defaultConfig())
	byPattern := map[string][]string{}
	for _, c := range cands {
		byPattern[c.Pattern] = append(byPattern[c.Pattern], c.Text)
	}
	if strings.Join(byPattern["semver"], "|") != "v1.2.3|1.2.3-rc.1" {
		t.Errorf("semver: %v", byPattern["semver"])
	}
	if strings.Join(byPattern["number"], "|") != "8080" {
		t.Errorf("number: %v (999 is only 3 digits)", byPattern["number"])
	}
	// A real IP stays ipv4; a bogus dotted quad matches nothing.
	cands = Extract("host 1.2.3.4 junk 999.999.999.999", defaultConfig())
	if len(cands) != 1 || cands[0].Pattern != "ipv4" || cands[0].Text != "1.2.3.4" {
		t.Errorf("got %+v", cands)
	}
}

func TestCustomPatternGroup(t *testing.T) {
	cfg := defaultConfig()
	cfg.CustomPatterns = []CustomPattern{{Name: "ticket", Regex: `ticket=(\w+)`, Group: 1}}
	cands := Extract("see ticket=ABC123x", cfg)
	if len(cands) != 1 || cands[0].Text != "ABC123x" {
		t.Errorf("got %+v", cands)
	}
	// Out-of-range group is rejected, not fatal.
	cfg.CustomPatterns = []CustomPattern{{Name: "bad", Regex: `x`, Group: 2}}
	if _, bad := buildPatterns(cfg); len(bad) != 1 || bad[0] != "bad" {
		t.Errorf("bad = %v", bad)
	}
}

func TestInvalidCustomPatternReported(t *testing.T) {
	cfg := defaultConfig()
	cfg.CustomPatterns = []CustomPattern{{Name: "broken", Regex: `[`}}
	_, bad := buildPatterns(cfg)
	if len(bad) != 1 || bad[0] != "broken" {
		t.Errorf("bad = %v", bad)
	}
}
