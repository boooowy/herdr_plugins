package main

import (
	"strings"
	"unicode/utf8"
)

// searchBox is the shared footer-line filter input used by the PR list and
// by the detail view's Files / Comments tabs. The query outlives input mode:
// Enter only closes the prompt, Esc clears the filter.
//
// Input is buffered as raw bytes because the key reader decodes one byte at
// a time (Key.R is that byte) — multi-byte input (Japanese, emoji) only
// becomes a rune once its whole sequence has arrived.
type searchBox struct {
	active bool
	buf    []byte
}

// query is the filter text; "" means no filtering.
func (s *searchBox) query() string { return string(s.buf) }

// open starts input mode, keeping the current query so `/` refines it.
func (s *searchBox) open() { s.active = true }

// clear drops the filter and leaves input mode.
func (s *searchBox) clear() {
	s.active = false
	s.buf = nil
}

// handle consumes one key while in input mode and reports whether the query
// changed (the caller then rebuilds its rows).
func (s *searchBox) handle(k Key) bool {
	switch {
	case k.Kind == KeyEnter:
		s.active = false // confirm: the filter stays applied
	case k.Kind == KeyEsc:
		s.clear()
		return true
	case k.Kind == KeyBackspace:
		if len(s.buf) == 0 {
			s.active = false // nothing left to delete: leave input mode
			return false
		}
		_, size := utf8.DecodeLastRune(s.buf)
		s.buf = s.buf[:len(s.buf)-size]
		return true
	case k.Kind == KeyRune:
		s.buf = append(s.buf, byte(k.R))
		return true
	}
	return false
}

// prompt is the footer line shown while typing (the terminal cursor is
// hidden app-wide, so the caret is drawn as a block).
func (s *searchBox) prompt() string {
	return "/絞り込み: " + s.query() + "▌   Enter:確定  Esc:取消"
}

// matchQuery reports whether every whitespace-separated term of q appears
// (case-insensitively) in at least one of fields. An empty query matches
// everything.
func matchQuery(q string, fields ...string) bool {
	terms := strings.Fields(strings.ToLower(q))
	if len(terms) == 0 {
		return true
	}
	lowered := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			lowered = append(lowered, strings.ToLower(f))
		}
	}
	for _, t := range terms {
		hit := false
		for _, f := range lowered {
			if strings.Contains(f, t) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}
