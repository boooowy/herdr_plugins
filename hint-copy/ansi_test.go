package main

import (
	"strings"
	"testing"
)

func TestParseANSIStripsAndStyles(t *testing.T) {
	tbl := newStyleTable()
	cells, plain := parseANSI("plain \x1b[31mred\x1b[0m end\nline2", tbl)
	if len(plain) != 2 || plain[0] != "plain red end" || plain[1] != "line2" {
		t.Fatalf("plain = %q", plain)
	}
	// "red" cells carry a dynamic style, the rest styleNone.
	line := cells[0]
	if line[6].Style < dynStyleBase || line[7].Style != line[6].Style {
		t.Errorf("red not styled: %+v", line[6:9])
	}
	if line[0].Style != styleNone || line[10].Style != styleNone {
		t.Errorf("plain parts styled: %+v %+v", line[0], line[10])
	}
}

func TestParseANSIStateAcrossLinesAndExtendedColor(t *testing.T) {
	tbl := newStyleTable()
	cells, plain := parseANSI("\x1b[1;38;5;208ma\nb\x1b[0mc", tbl)
	if plain[0] != "a" || plain[1] != "bc" {
		t.Fatalf("plain = %q", plain)
	}
	if cells[0][0].Style != cells[1][0].Style {
		t.Error("style must carry across the newline")
	}
	if cells[1][1].Style != styleNone {
		t.Error("reset not applied")
	}
	seq := tbl.sequence(cells[0][0].Style)
	if seq != "\x1b[1;38;5;208m" {
		t.Errorf("seq = %q", seq)
	}
}

func TestParseANSISkipsOSCAndCSI(t *testing.T) {
	tbl := newStyleTable()
	_, plain := parseANSI("a\x1b]0;title\ab\x1b[2Kc\x1b[10;20Hd", tbl)
	if plain[0] != "abcd" {
		t.Errorf("plain = %q", plain[0])
	}
}

func TestParseANSIWideRunes(t *testing.T) {
	tbl := newStyleTable()
	cells, plain := parseANSI("\x1b[32m日本\x1b[0mx", tbl)
	if plain[0] != "日本x" {
		t.Fatalf("plain = %q", plain[0])
	}
	line := cells[0]
	// 日(2 cells) 本(2 cells) x → 5 cells, continuations at 1 and 3.
	if len(line) != 5 || line[1].R != 0 || line[3].R != 0 {
		t.Errorf("cells = %+v", line)
	}
	s := NewScreen(10, 1)
	s.styles = tbl
	s.PutCells(0, 0, line, 10)
	if got := flushPlain(s); got != "日本x" {
		t.Errorf("roundtrip = %q", got)
	}
}

func TestParseANSIHexColorParam(t *testing.T) {
	if got := colorCode("#bd93f9", false); got != "38;2;189;147;249" {
		t.Errorf("hex fg = %q", got)
	}
	if got := colorCode("#444477", true); got != "48;2;68;68;119" {
		t.Errorf("hex bg = %q", got)
	}
}

func TestFlushEmitsDynamicStyles(t *testing.T) {
	tbl := newStyleTable()
	cells, _ := parseANSI("\x1b[35mxy\x1b[0mz", tbl)
	s := NewScreen(5, 1)
	s.styles = tbl
	s.PutCells(0, 0, cells[0], 5)
	var sb strings.Builder
	s.Flush(&sb, sgrTable(defaultConfig()))
	if !strings.Contains(sb.String(), "\x1b[35m") {
		t.Errorf("dynamic SGR missing: %q", sb.String())
	}
}
