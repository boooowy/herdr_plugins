package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ---- placeLabel: a label must never spill onto unrelated content ----

func TestPlaceLabelOverLeadingCells(t *testing.T) {
	s := NewScreen(20, 1)
	s.WriteString(0, 0, "xyzw/file", styleNone, 20)
	placeLabel(s, Rect{X: 0, Y: 0, W: 20, H: 1}, 0, 0, 9, "a", styleHint)
	if got := s.at(0, 0).R; got != 'a' {
		t.Errorf("label not stamped over the leading cell: %q", got)
	}
	if got := s.at(1, 0).R; got != 'y' {
		t.Errorf("cell after the label must keep its content: %q", got)
	}
}

func TestPlaceLabelOffLeftWhenSpanTooNarrow(t *testing.T) {
	s := NewScreen(20, 1)
	s.Set(3, 0, 'x', styleNone) // 1-cell span at x=3, blanks to its left
	s.Set(4, 0, 'y', styleNone) // neighbor that must survive
	placeLabel(s, Rect{X: 0, Y: 0, W: 20, H: 1}, 0, 3, 4, "as", styleHint)
	if s.at(1, 0).R != 'a' || s.at(2, 0).R != 's' {
		t.Errorf("label not placed off-left: %q%q", s.at(1, 0).R, s.at(2, 0).R)
	}
	if s.at(3, 0).R != 'x' || s.at(4, 0).R != 'y' {
		t.Errorf("span or neighbor overwritten: %q%q", s.at(3, 0).R, s.at(4, 0).R)
	}
}

func TestPlaceLabelOffRightWhenLeftBlocked(t *testing.T) {
	s := NewScreen(20, 1)
	s.WriteString(0, 0, "ab x", styleNone, 20) // span 'x' at 3; left neighbors busy
	placeLabel(s, Rect{X: 0, Y: 0, W: 20, H: 1}, 0, 3, 4, "as", styleHint)
	if s.at(4, 0).R != 'a' || s.at(5, 0).R != 's' {
		t.Errorf("label not placed off-right: %q%q", s.at(4, 0).R, s.at(5, 0).R)
	}
	if s.at(3, 0).R != 'x' {
		t.Error("span overwritten")
	}
}

func TestPlaceLabelSkippedWhenNoRoom(t *testing.T) {
	s := NewScreen(5, 1)
	s.WriteString(0, 0, "abxcd", styleNone, 5) // 1-cell span at 2, no blanks anywhere
	placeLabel(s, Rect{X: 0, Y: 0, W: 5, H: 1}, 0, 2, 3, "as", styleHint)
	for i, want := range []rune("abxcd") {
		if got := s.at(i, 0).R; got != want {
			t.Errorf("cell %d = %q, want %q (label must be dropped, not spill)", i, got, want)
		}
	}
}

func TestPlaceLabelRespectsRectEdges(t *testing.T) {
	s := NewScreen(10, 1)
	// Span at the rect's left edge, too narrow: off-left would leave the rect.
	s.Set(0, 0, 'x', styleNone)
	s.Set(1, 0, 'y', styleNone)
	placeLabel(s, Rect{X: 0, Y: 0, W: 2, H: 1}, 0, 0, 1, "as", styleHint)
	if s.at(0, 0).R != 'x' || s.at(1, 0).R != 'y' {
		t.Error("label leaked past the rect edges")
	}
}

// The original bug: a 2-char label on a 1-rune quoted match overwrote the
// closing quote. With placement rules it is dropped instead (both neighbors
// are quotes), leaving the content intact.
func TestOverlayLabelNeverSpillsOntoNeighbor(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 30; i++ { // >24 unique candidates → all labels are 2 chars
		fmt.Fprintf(&b, "/tmp/f%02d\n", i)
	}
	b.WriteString("run 'a' now")
	text := b.String()
	cfg := defaultConfig()
	cands := Extract(text, cfg)
	lm := AssignLabels(cands, cfg)
	var quoted Candidate
	for _, c := range cands {
		if c.Text == "a" {
			quoted = c
		}
	}
	if quoted.Text == "" {
		t.Fatal("expected the quoted 'a' candidate")
	}
	if len(lm.LabelFor("a")) < 2 {
		t.Fatalf("test needs a multi-char label, got %q", lm.LabelFor("a"))
	}
	s := NewScreen(40, 40)
	PaintTokens(s, Rect{X: 0, Y: 0, W: 40, H: 39}, text, cands, lm, "")
	y := quoted.Line
	if got := string([]rune{s.at(4, y).R, s.at(5, y).R, s.at(6, y).R, s.at(8, y).R}); got != "'a'n" {
		t.Errorf("cells around the quoted match = %q, want unchanged \"'a'n\"", got)
	}
}

// ---- prefix narrowing: ruled-out spans lose their highlight ----

func TestOverlayPrefixDimsRuledOutSpans(t *testing.T) {
	text := "/tmp/a /tmp/b"
	cfg := defaultConfig()
	cands := Extract(text, cfg)
	// Force 2-char labels so a 1-key prefix narrows without copying.
	lm := &LabelMap{
		byLabel: map[string]string{"as": "/tmp/a", "sd": "/tmp/b"},
		byText:  map[string]string{"/tmp/a": "as", "/tmp/b": "sd"},
	}
	s := NewScreen(40, 5)
	PaintTokens(s, Rect{X: 0, Y: 0, W: 40, H: 4}, text, cands, lm, "a")
	// "/tmp/a" (matching "as") keeps its span highlight past the label cells.
	if got := s.at(3, 0).Style; got != styleMatch {
		t.Errorf("matching span style = %d, want styleMatch", got)
	}
	// "/tmp/b" (label "sd") is ruled out: span keeps its original style.
	if got := s.at(10, 0).Style; got == styleMatch {
		t.Error("ruled-out span must not stay highlighted")
	}
	// Its label dims instead of disappearing.
	if got := s.at(7, 0).Style; got != styleDim {
		t.Errorf("ruled-out label style = %d, want styleDim", got)
	}
}

// ---- footer: counts and the single-target preview ----

func TestTokenFooterCountsAndPreview(t *testing.T) {
	lm := &LabelMap{
		byLabel: map[string]string{"as": "https://x.example", "aa": "/tmp/y", "sd": "9999"},
		byText:  map[string]string{"https://x.example": "as", "/tmp/y": "aa", "9999": "sd"},
	}
	if f := tokenFooter("", lm, false); !strings.Contains(f, "3 targets") {
		t.Errorf("idle footer = %q", f)
	}
	if f := tokenFooter("a", lm, false); !strings.Contains(f, "2 targets") || !strings.Contains(f, "a_") {
		t.Errorf("narrowed footer = %q", f)
	}
	if f := tokenFooter("s", lm, false); !strings.Contains(f, "copy: 9999") {
		t.Errorf("single-target footer should preview the copy text: %q", f)
	}
	if f := tokenFooter("", lm, true); !strings.Contains(f, "word mode") || !strings.Contains(f, "tab: token mode") {
		t.Errorf("word footer = %q", f)
	}
}

func TestLabelMapMatchHelpers(t *testing.T) {
	lm := &LabelMap{byLabel: map[string]string{"as": "X", "aa": "Y"}}
	if lm.Count() != 2 || lm.MatchCount("a") != 2 || lm.MatchCount("as") != 1 {
		t.Errorf("counts: %d %d %d", lm.Count(), lm.MatchCount("a"), lm.MatchCount("as"))
	}
	if _, ok := lm.OnlyMatch("a"); ok {
		t.Error("OnlyMatch must fail with two matches")
	}
	if text, ok := lm.OnlyMatch("as"); !ok || text != "X" {
		t.Errorf("OnlyMatch = %q %v", text, ok)
	}
}

// ---- word mode extraction ----

func TestExtractWordsBasics(t *testing.T) {
	cfg := defaultConfig()
	out := ExtractWords("go run main.go -v x=1", cfg)
	texts := map[string]bool{}
	for _, c := range out {
		texts[c.Text] = true
	}
	for _, want := range []string{"run", "main.go"} {
		if !texts[want] {
			t.Errorf("missing word %q in %v", want, texts)
		}
	}
	if texts["go"] { // below word_min_len = 3
		t.Error("2-rune word must not be labeled")
	}
}

func TestExtractWordsTrimsEdgePunct(t *testing.T) {
	cfg := defaultConfig()
	out := ExtractWords("see file.txt. done", cfg)
	found := false
	for _, c := range out {
		if c.Text == "file.txt" {
			found = true
		}
		if c.Text == "file.txt." {
			t.Error("trailing punctuation not trimmed")
		}
	}
	if !found {
		t.Errorf("expected file.txt, got %v", out)
	}
}

func TestExtractWordsCJK(t *testing.T) {
	cfg := defaultConfig()
	out := ExtractWords("設定ファイル を確認", cfg)
	texts := map[string]bool{}
	for _, c := range out {
		texts[c.Text] = true
	}
	if !texts["設定ファイル"] {
		t.Errorf("CJK word run missing: %v", texts)
	}
}

// ---- single-pane composite: the border stays, nothing appears to move ----

func TestBuildCompositeSinglePane(t *testing.T) {
	var lay paneLayoutResult
	if err := json.Unmarshal([]byte(`{
		"zoomed": false,
		"area": {"x": 0, "y": 0, "width": 82, "height": 26},
		"focused_pane_id": "p1",
		"panes": [{"pane_id": "p1", "focused": true, "rect": {"x": 0, "y": 0, "width": 82, "height": 26}}]
	}`), &lay); err != nil {
		t.Fatal(err)
	}
	tbl := newStyleTable()
	cells, plain := parseANSI("hello", tbl)
	parsed := &tabParsed{
		titles: map[string]string{"p1": "zsh"},
		styled: map[string][][]Cell{"p1": cells},
		plain:  map[string][]string{"p1": plain},
	}
	// Overlay term is area minus the border ring: dw=dh=2, inside the 1-4 window.
	comp := buildComposite(&lay, parsed, "p1", 80, 24, defaultConfig(), tbl)
	if comp == nil {
		t.Fatal("single-pane tab must composite (not fall back)")
	}
	if comp.target.W < 2 || comp.target.H < 1 {
		t.Fatalf("bad target rect %+v", comp.target)
	}
	if got := comp.base.at(comp.target.X, comp.target.Y).R; got != 'h' {
		t.Errorf("pane content not painted at the target rect: %q", got)
	}
}

// ---- keys: Tab toggles word mode, so it must decode distinctly ----

func TestKeyReaderTab(t *testing.T) {
	k := pipeReader(t, []byte{0x09}, false)
	got, err := k.Read()
	if err != nil || got.Kind != KeyTab {
		t.Errorf("got %+v err %v, want KeyTab", got, err)
	}
}
