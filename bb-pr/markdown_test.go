package main

import (
	"strings"
	"testing"
)

func TestMDStyledLines(t *testing.T) {
	md := "# 見出し\n\n本文です\n- 項目1\n> 引用\n```\ncode line\n```\nafter"
	lines := mdStyledLines(md, 40, styleNone)

	find := func(text string) []Span {
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

	if find("見出し")[0].Style != styleTitle {
		t.Error("heading must use styleTitle")
	}
	if find("本文です")[0].Style != styleNone {
		t.Error("body must keep base style")
	}
	if b := find("項目1"); b[0].Style != styleHunk || b[0].Text != "- " {
		t.Errorf("bullet marker: %+v", b)
	}
	if find("引用")[0].Style != styleDim {
		t.Error("quote must be dim")
	}
	if find("code line")[0].Style != styleMeta {
		t.Error("fenced code must use styleMeta")
	}
	if find("after")[0].Style != styleNone {
		t.Error("text after the closing fence must return to base")
	}
}

func TestMDStyledLinesWrapsAndKeepsBlanks(t *testing.T) {
	lines := mdStyledLines("aaaa bbbb cccc\n\nx", 10, styleNone)
	// 14-cell line at width 10 wraps to 2 lines, plus blank, plus "x".
	if len(lines) != 4 {
		t.Fatalf("got %d lines: %v", len(lines), lines)
	}
	if lines[2][0].Text != "" {
		t.Errorf("blank line must survive: %v", lines[2])
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
