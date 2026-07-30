package main

import (
	"strings"
	"testing"
)

func findLine(t *testing.T, lines [][]Span, text string) []Span {
	t.Helper()
	for _, spans := range lines {
		joined := ""
		for _, sp := range spans {
			joined += sp.Text
		}
		if strings.Contains(joined, text) {
			return spans
		}
	}
	t.Fatalf("line containing %q not found in %v", text, lines)
	return nil
}

func TestMDStyledLinesBlocks(t *testing.T) {
	md := "# 見出し\n\n本文です\n- 項目1\n- [ ] 未了\n- [x] 完了\n  - ネスト\n> 引用\n```go\ncode line\n```\nafter\n---\n| A | B |\n|---|---|"
	lines := mdStyledLines(md, 60, styleNone)

	if h := findLine(t, lines, "見出し"); h[0].Style != styleMDHead || !strings.HasPrefix(h[0].Text, "◆ ") {
		t.Errorf("heading: %+v", h)
	}
	if findLine(t, lines, "本文です")[0].Style != styleNone {
		t.Error("body must keep base style")
	}
	if b := findLine(t, lines, "項目1"); b[0].Text != "• " || b[0].Style != styleHunk {
		t.Errorf("bullet marker: %+v", b)
	}
	if cb := findLine(t, lines, "未了"); cb[0].Text != "• ☐ " {
		t.Errorf("unchecked box: %+v", cb)
	}
	if cb := findLine(t, lines, "完了"); cb[0].Text != "• ☑ " {
		t.Errorf("checked box: %+v", cb)
	}
	if n := findLine(t, lines, "ネスト"); n[0].Text != "  ◦ " {
		t.Errorf("nested bullet: %+v", n)
	}
	if q := findLine(t, lines, "引用"); !strings.Contains(q[0].Text, "▎") || q[0].Style != styleDim {
		t.Errorf("quote: %+v", q)
	}
	if findLine(t, lines, "code line")[0].Style != styleMDCode {
		t.Error("fenced code must use styleMDCode")
	}
	if g := findLine(t, lines, "── go "); g[0].Style != styleDim {
		t.Errorf("fence bar with lang: %+v", g)
	}
	if findLine(t, lines, "after")[0].Style != styleNone {
		t.Error("text after the closing fence must return to base")
	}
	if hr := findLine(t, lines, "────"); hr[0].Style != styleDim || displayWidth(hr[0].Text) != 60 {
		t.Errorf("hrule: %+v", hr)
	}
	if tb := findLine(t, lines, "│ A │ B │"); tb == nil {
		t.Error("table pipes must become │")
	}
	if td := findLine(t, lines, "┼"); td[0].Style != styleDim {
		t.Errorf("table delimiter: %+v", td)
	}
}

func TestMDInlineSpans(t *testing.T) {
	spans := mdInlineSpans("a `code` **強調** [リンク](https://x.example/) https://y.example/ \\(esc\\)", styleNone)
	joined := ""
	byStyle := map[StyleID]string{}
	for _, sp := range spans {
		joined += sp.Text
		byStyle[sp.Style] += sp.Text
	}
	if byStyle[styleMDCode] != "code" {
		t.Errorf("code span: %q", byStyle[styleMDCode])
	}
	if byStyle[styleTitle] != "強調" {
		t.Errorf("bold span: %q", byStyle[styleTitle])
	}
	if byStyle[styleMDLink] != "リンク"+"https://y.example/" {
		t.Errorf("link spans: %q", byStyle[styleMDLink])
	}
	if strings.Contains(joined, "https://x.example/") {
		t.Error("link URL must be concealed")
	}
	if !strings.Contains(joined, "(esc)") || strings.Contains(joined, `\(`) {
		t.Errorf("escapes must be unescaped: %q", joined)
	}

	// Unclosed markers pass through untouched.
	raw := "a `unclosed and **half"
	joined = ""
	for _, sp := range mdInlineSpans(raw, styleNone) {
		joined += sp.Text
	}
	if joined != raw {
		t.Errorf("unclosed passthrough: %q", joined)
	}

	// snake_case must never be italicized.
	joined = ""
	for _, sp := range mdInlineSpans("foo_bar_baz", styleNone) {
		if sp.Style != styleNone {
			t.Errorf("snake_case styled: %+v", sp)
		}
		joined += sp.Text
	}
	if joined != "foo_bar_baz" {
		t.Errorf("snake_case: %q", joined)
	}
}

func TestWrapSpans(t *testing.T) {
	lines := wrapSpans([]Span{{"abcd", styleNone}, {"efgh", styleTitle}}, 6)
	if len(lines) != 2 {
		t.Fatalf("lines = %v", lines)
	}
	// Break lands inside the second span: style must survive the split.
	if lines[0][1].Text != "ef" || lines[0][1].Style != styleTitle || lines[1][0].Text != "gh" || lines[1][0].Style != styleTitle {
		t.Errorf("split spans: %v", lines)
	}
	if got := wrapSpans([]Span{{"", styleNone}}, 10); len(got) != 1 {
		t.Errorf("empty line: %v", got)
	}
}

func TestMDStyledLinesWrapsAndKeepsBlanks(t *testing.T) {
	lines := mdStyledLines("aaaa bbbb cccc\n\nx", 10, styleNone)
	// 14-cell line at width 10 wraps to 2 lines, plus blank, plus "x".
	if len(lines) != 4 {
		t.Fatalf("got %d lines: %v", len(lines), lines)
	}
	if len(lines[2]) != 0 {
		t.Errorf("blank line must survive as an empty row: %v", lines[2])
	}
}

func TestSplitBullet(t *testing.T) {
	cases := []struct {
		in, marker, rest string
		ok               bool
	}{
		{"- item", "- ", "item", true},
		{"  * item", "  * ", "item", true},
		{"12. item", "12. ", "item", true},
		{"-not a bullet", "", "", false},
		{"plain", "", "", false},
	}
	for _, c := range cases {
		m, r, ok := splitBullet(c.in)
		if m != c.marker || r != c.rest || ok != c.ok {
			t.Errorf("splitBullet(%q) = %q, %q, %v", c.in, m, r, ok)
		}
	}
}

func TestDiffLineRowsWrap(t *testing.T) {
	l := DiffLine{Kind: LineAdd, NewNo: 7, Text: strings.Repeat("abcde ", 10)} // 60 cells
	// width 40, numW 4 → gutter 12, content 28 → 3 rows.
	rows := diffLineRows(l, 4, 40, true)
	if len(rows) != 3 {
		t.Fatalf("wrapped rows = %d, want 3: %v", len(rows), rows)
	}
	if !rows[0].Selectable || rows[1].Selectable {
		t.Error("only the first row must be selectable")
	}
	if _, ok := rows[0].Item.(DiffLine); !ok {
		t.Error("first row must carry the DiffLine item")
	}
	if got := diffLineRows(l, 4, 40, false); len(got) != 1 {
		t.Errorf("unwrapped must be a single row, got %d", len(got))
	}
}
