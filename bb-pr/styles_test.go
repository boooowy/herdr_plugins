package main

import (
	"strings"
	"testing"
)

func TestSgrTableFocusVariants(t *testing.T) {
	m := sgrTable(defaultConfig())
	focusBg := "\x1b[" + colorCode(defaultConfig().FocusBg, true) + "m"

	// 400 series: base styles on the focus bg (diff-line selection).
	if got := m[onFocusBg(styleAdd)]; !strings.HasPrefix(got, focusBg) || got == focusBg {
		t.Errorf("focus add = %q", got)
	}
	if m[onFocusBg(styleNone)] != focusBg {
		t.Errorf("focus none = %q", m[onFocusBg(styleNone)])
	}
	// 600 series: comment-bg styles refocused — same rendering as 400s
	// (focus bg replaces the comment bg entirely).
	if m[onFocusBg(onCmtBg(styleAuthor))] != m[onFocusBg(styleAuthor)] {
		t.Error("refocused comment style must equal the focused base style")
	}
	// Code blocks keep their own bg: code seq appended after the focus bg.
	if got := m[onFocusBg(onCmtBg(styleMDCode))]; !strings.HasPrefix(got, focusBg) ||
		!strings.Contains(got, colorCode(defaultConfig().MDCodeBg, true)) {
		t.Errorf("refocused code style = %q", got)
	}
}

func TestFocusRow(t *testing.T) {
	s := NewScreen(10, 2)
	s.WriteString(0, 0, "ab", styleAdd, 10)
	s.WriteString(2, 0, "cd", onCmtBg(styleComment), 10)
	focusRow(s, 0, 0, 10)
	if got := s.at(0, 0).Style; got != onFocusBg(styleAdd) {
		t.Errorf("base cell style = %d", got)
	}
	if got := s.at(2, 0).Style; got != onFocusBg(onCmtBg(styleComment)) {
		t.Errorf("comment cell style = %d", got)
	}
	if got := s.at(5, 0).Style; got != onFocusBg(styleNone) {
		t.Errorf("empty cell style = %d", got)
	}
	// Idempotent: already-focused cells stay put.
	focusRow(s, 0, 0, 10)
	if got := s.at(0, 0).Style; got != onFocusBg(styleAdd) {
		t.Errorf("double focus style = %d", got)
	}
	// Other rows untouched.
	if got := s.at(0, 1).Style; got != styleNone {
		t.Errorf("row 1 style = %d", got)
	}
}
