package main

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Candidate is one copyable string found on screen. Offsets are rune indices
// within the line, EndRune exclusive.
type Candidate struct {
	Line      int
	StartRune int
	EndRune   int
	Text      string
	Pattern   string
}

// pattern is one extraction rule. Higher priority wins when matches overlap.
// filter post-processes a raw regex match (Go RE2 has no lookaround): it may
// trim trailing characters or return "" to reject the match entirely.
type pattern struct {
	name     string
	priority int
	re       *regexp.Regexp
	filter   func(string) string
}

var builtinPatterns = []pattern{
	{
		name:     "url",
		priority: 100,
		// Parens/brackets stay in the charset; trimTrailingPunct drops them
		// only when unbalanced, so wiki URLs like .../Go_(language) survive.
		re:       regexp.MustCompile("(?:https?|ftp|file)://[^\\s<>\"'`]+"),
		filter:   trimTrailingPunct,
	},
	{
		name:     "email",
		priority: 90,
		re:       regexp.MustCompile(`\b[\w.+-]+@[\w-]+(?:\.[\w-]+)+\b`),
	},
	{
		name:     "uuid",
		priority: 80,
		re:       regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`),
	},
	{
		name:     "ipv4",
		priority: 70,
		re:       regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(?::\d{1,5})?\b`),
	},
	{
		name:     "ipv6",
		priority: 60,
		re:       regexp.MustCompile(`\b(?:[0-9a-fA-F]{1,4}:){2,7}[0-9a-fA-F]{1,4}\b`),
		filter: func(s string) string {
			// Require at least 3 groups so times like 12:34:56 need letters
			// and MAC-style pairs don't slip through as easily.
			if strings.Count(s, ":") < 2 {
				return ""
			}
			return s
		},
	},
	{
		name:     "sha",
		priority: 50,
		re:       regexp.MustCompile(`\b[0-9a-f]{7,40}\b`),
		filter: func(s string) string {
			// A hex run with no letters is almost certainly a plain number.
			if !strings.ContainsAny(s, "abcdef") {
				return ""
			}
			return s
		},
	},
	{
		name:     "path",
		priority: 30,
		// Absolute, ~/ and ./-style paths, plus bare relative paths that
		// contain at least one slash.
		re:     regexp.MustCompile(`(?:~|\.{1,2})?/[\w.+@%~-]+(?:/[\w.+@%~-]+)*/?|\b[\w.+@%~-]+(?:/[\w.+@%~-]+)+/?`),
		filter: trimTrailingPunct,
	},
}

// trimTrailingPunct drops punctuation that is almost always prose, not part of
// the value: sentence punctuation always, and a trailing ")" or "]" only when
// it is unbalanced within the match (so "https://en.wikipedia.org/wiki/Go_(x)"
// keeps its parenthesis but "(see https://example.com)" loses it).
func trimTrailingPunct(s string) string {
	for len(s) > 0 {
		last := s[len(s)-1]
		switch {
		case strings.ContainsRune(".,;:!?\"'", rune(last)):
			s = s[:len(s)-1]
		case last == ')' && strings.Count(s, ")") > strings.Count(s, "("):
			s = s[:len(s)-1]
		case last == ']' && strings.Count(s, "]") > strings.Count(s, "["):
			s = s[:len(s)-1]
		default:
			return s
		}
	}
	return s
}

// buildPatterns returns the enabled built-in patterns plus compiled custom
// patterns from the config. Invalid custom regexes are collected into badCustom
// so the caller can surface them once without failing.
func buildPatterns(cfg Config) (pats []pattern, badCustom []string) {
	for _, p := range builtinPatterns {
		if cfg.patternEnabled(p.name) {
			pats = append(pats, p)
		}
	}
	for _, cp := range cfg.CustomPatterns {
		re, err := regexp.Compile(cp.Regex)
		if err != nil {
			badCustom = append(badCustom, cp.Name)
			continue
		}
		// Custom patterns sit above path, below the specific built-ins.
		pats = append(pats, pattern{name: cp.Name, priority: 40, re: re})
	}
	return pats, badCustom
}

// Extract finds all candidates in the captured screen text, resolves overlaps
// (longest match at a position wins, then higher pattern priority), and caps
// the result at cfg.MaxCandidates unique strings in screen order.
func Extract(screen string, cfg Config) []Candidate {
	pats, _ := buildPatterns(cfg)
	lines := strings.Split(screen, "\n")
	var out []Candidate
	for li, line := range lines {
		var ms []Candidate
		for _, p := range pats {
			for _, loc := range p.re.FindAllStringIndex(line, -1) {
				text := line[loc[0]:loc[1]]
				if p.filter != nil {
					text = p.filter(text)
				}
				if text == "" {
					continue
				}
				start := utf8.RuneCountInString(line[:loc[0]])
				ms = append(ms, Candidate{
					Line:      li,
					StartRune: start,
					EndRune:   start + utf8.RuneCountInString(text),
					Text:      text,
					Pattern:   p.name,
				})
			}
		}
		out = append(out, resolveOverlaps(ms, pats)...)
	}
	return capUnique(out, cfg.MaxCandidates)
}

// resolveOverlaps keeps a non-overlapping subset of the line's matches: sorted
// by start ascending, longer first at the same start, then pattern priority.
// A left-to-right sweep drops anything that overlaps an already-kept match, so
// a URL swallows the path-like substring inside it.
func resolveOverlaps(ms []Candidate, pats []pattern) []Candidate {
	prio := make(map[string]int, len(pats))
	for _, p := range pats {
		prio[p.name] = p.priority
	}
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].StartRune != ms[j].StartRune {
			return ms[i].StartRune < ms[j].StartRune
		}
		li, lj := ms[i].EndRune-ms[i].StartRune, ms[j].EndRune-ms[j].StartRune
		if li != lj {
			return li > lj
		}
		return prio[ms[i].Pattern] > prio[ms[j].Pattern]
	})
	var kept []Candidate
	lastEnd := -1
	for _, m := range ms {
		if m.StartRune < lastEnd {
			continue
		}
		// Identical span already kept (same start as previous, sweep order
		// guarantees the better one came first).
		if len(kept) > 0 && kept[len(kept)-1].StartRune == m.StartRune {
			continue
		}
		kept = append(kept, m)
		lastEnd = m.EndRune
	}
	return kept
}

// capUnique limits candidates to the first max unique strings in order;
// further occurrences of an already-admitted string are kept (they share its
// label), strings beyond the cap are dropped.
func capUnique(cands []Candidate, max int) []Candidate {
	if max <= 0 {
		max = 100
	}
	seen := make(map[string]bool)
	var out []Candidate
	for _, c := range cands {
		if !seen[c.Text] {
			if len(seen) >= max {
				continue
			}
			seen[c.Text] = true
		}
		out = append(out, c)
	}
	return out
}
