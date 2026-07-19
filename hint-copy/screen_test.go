package main

import (
	"strings"
	"testing"
)

func flushPlain(s *Screen) string {
	var sb strings.Builder
	s.Flush(&sb, sgrTable(defaultConfig()))
	return sgrRe.ReplaceAllString(sb.String(), "")
}

func TestScreenWriteStringClips(t *testing.T) {
	s := NewScreen(5, 1)
	end := s.WriteString(0, 0, "abcdefgh", styleNone, 5)
	if end != 5 {
		t.Errorf("end = %d", end)
	}
	if got := flushPlain(s); got != "abcde" {
		t.Errorf("got %q", got)
	}
}

func TestScreenWideRunes(t *testing.T) {
	s := NewScreen(6, 1)
	s.WriteString(0, 0, "あxい", styleNone, 6)
	if got := flushPlain(s); got != "あxい" {
		t.Errorf("got %q", got)
	}
	// A wide rune that would split at the edge is dropped, not halved.
	s2 := NewScreen(2, 1)
	s2.WriteString(0, 0, "xあ", styleNone, 2)
	if got := flushPlain(s2); got != "x" {
		t.Errorf("edge clip got %q", got)
	}
}

func TestScreenFlushTrimsTrailingBlanks(t *testing.T) {
	s := NewScreen(10, 2)
	s.WriteString(0, 0, "hi", styleNone, 10)
	out := flushPlain(s)
	rows := strings.Split(out, "\r\n")
	if rows[0] != "hi" {
		t.Errorf("row0 = %q (trailing blanks not trimmed)", rows[0])
	}
	// Styled trailing spaces are content (e.g. inverse anchor) and survive.
	s.Set(3, 1, ' ', styleInverse)
	rows = strings.Split(flushPlain(s), "\r\n")
	if len([]rune(rows[1])) != 4 {
		t.Errorf("styled blank trimmed: %q", rows[1])
	}
}

func TestScreenCloneIndependent(t *testing.T) {
	s := NewScreen(4, 1)
	s.WriteString(0, 0, "abcd", styleNone, 4)
	c := s.Clone()
	c.Set(0, 0, 'z', styleNone)
	if flushPlain(s) != "abcd" || flushPlain(c) != "zbcd" {
		t.Errorf("clone not independent: %q %q", flushPlain(s), flushPlain(c))
	}
}
