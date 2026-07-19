package main

import "strings"

// LineLabels binds visible non-empty lines to hint labels for line mode.
// Unlike token labels there is no dedup: every line is its own target.
type LineLabels struct {
	byLabel  map[string]int // label → absolute line index
	byLine   map[int]string // absolute line index → label
	labelLen int
}

// AssignLineLabels labels the non-empty lines of the visible window
// [offset, offset+view), top to bottom. cfg.Reverse hands the earliest
// (easiest) labels to the bottom lines instead.
func AssignLineLabels(lines []string, offset, view int, cfg Config) *LineLabels {
	var idx []int
	for i := offset; i < len(lines) && i < offset+view; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			idx = append(idx, i)
		}
	}
	if cfg.Reverse {
		for i, j := 0, len(idx)-1; i < j; i, j = i+1, j-1 {
			idx[i], idx[j] = idx[j], idx[i]
		}
	}
	labels := GenerateLabels(len(idx), []rune(cfg.Alphabet))
	ll := &LineLabels{
		byLabel: make(map[string]int, len(idx)),
		byLine:  make(map[int]string, len(idx)),
	}
	for i, li := range idx {
		if i >= len(labels) {
			break
		}
		ll.byLabel[labels[i]] = li
		ll.byLine[li] = labels[i]
		if len(labels[i]) > ll.labelLen {
			ll.labelLen = len(labels[i])
		}
	}
	return ll
}

// Exact returns the line index for a fully typed label.
func (ll *LineLabels) Exact(label string) (int, bool) {
	line, ok := ll.byLabel[label]
	return line, ok
}

// HasPrefix reports whether any label starts with the typed prefix.
func (ll *LineLabels) HasPrefix(prefix string) bool {
	for label := range ll.byLabel {
		if strings.HasPrefix(label, prefix) {
			return true
		}
	}
	return false
}

// LabelFor returns the label of a line ("" for empty/unlabeled lines).
func (ll *LineLabels) LabelFor(line int) string {
	return ll.byLine[line]
}

// Width is the uniform label length (0 when nothing is labeled).
func (ll *LineLabels) Width() int {
	return ll.labelLen
}

// SingleLineText is what a one-line copy yields: the line with surrounding
// whitespace stripped — when you grab a single command or value, indent is
// noise (kitty's line hint behaves the same).
func SingleLineText(lines []string, i int) string {
	return strings.TrimSpace(lines[i])
}

// RangeText joins the inclusive line range for a multi-line copy. Leading
// indent is preserved and interior empty lines are kept — ranges are code or
// log blocks where both carry meaning; only trailing whitespace per line is
// dropped. Argument order does not matter.
func RangeText(lines []string, a, b int) string {
	if a > b {
		a, b = b, a
	}
	out := make([]string, 0, b-a+1)
	for i := a; i <= b && i < len(lines); i++ {
		out = append(out, strings.TrimRight(lines[i], " \t\r"))
	}
	return strings.Join(out, "\n")
}
