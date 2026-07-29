package main

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Candidate is one copyable string found on screen. Offsets are rune indices
// within the line, EndRune exclusive. StartRune/EndRune/Text describe the
// copied span (a capture group for patterns like markdown links); MatchStart/
// MatchEnd cover the whole regex match and drive overlap resolution, so a
// `[text](url)` match swallows the bare-url candidate inside it. For plain
// patterns both spans are identical.
type Candidate struct {
	Line       int
	StartRune  int
	EndRune    int
	MatchStart int
	MatchEnd   int
	Text       string
	Pattern    string
}

// pattern is one extraction rule. Higher priority wins when matches overlap.
// group selects the submatch whose text is copied (0 = whole match). filter
// post-processes the group text (Go RE2 has no lookaround): it may trim
// trailing characters or return "" to reject the match entirely.
type pattern struct {
	name     string
	priority int
	group    int
	re       *regexp.Regexp
	filter   func(string) string
}

// dottedQuadRe spots 4+-part pure-numeric dotted tokens (IP-shaped, not
// semver); see the semver pattern's filter.
var dottedQuadRe = regexp.MustCompile(`^\d+(?:\.\d+){3,}$`)

var builtinPatterns = []pattern{
	{
		// Markdown link: only the target URL is copied, and the whole-match
		// span keeps the bare `url` pattern from double-labeling it.
		name:     "markdown_url",
		priority: 110,
		group:    1,
		re:       regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`),
	},
	{
		name:     "url",
		priority: 100,
		// Parens/brackets stay in the charset; trimTrailingPunct drops them
		// only when unbalanced, so wiki URLs like .../Go_(language) survive.
		re:     regexp.MustCompile("(?:https?|ftp|file)://[^\\s<>\"'`]+"),
		filter: trimTrailingPunct,
	},
	{
		name:     "email",
		priority: 90,
		re:       regexp.MustCompile(`\b[\w.+-]+@[\w-]+(?:\.[\w-]+)+\b`),
	},
	{
		// Unified-diff header paths, copied without the a/ b/ prefix. Two
		// entries share the "diff_path" name so one [patterns] toggle
		// controls both. ^ is safe: extraction runs per line.
		name:     "diff_path",
		priority: 95,
		group:    1,
		re:       regexp.MustCompile(`^(?:---|\+\+\+) [ab]/(\S+)`),
	},
	{
		name:     "diff_path",
		priority: 95,
		group:    1,
		re:       regexp.MustCompile(`^diff --git a/(\S+) b/\S+`),
	},
	{
		name:     "docker",
		priority: 85,
		re:       regexp.MustCompile(`\bsha256:[0-9a-f]{64}\b`),
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
		// Hex colors: longest alternation first (Go regexp is leftmost-first).
		// 6 hex digits are below sha's 7 minimum, so no overlap there.
		name:     "color",
		priority: 65,
		re:       regexp.MustCompile(`#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3})\b`),
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
		name:     "address",
		priority: 45,
		re:       regexp.MustCompile(`\b0x[0-9a-fA-F]+\b`),
	},
	{
		// path:line(:col) like main.go:123 — the dot-extension requirement
		// keeps clock times (12:34) out. Beats bare path by longer match.
		name:     "linenum",
		priority: 36,
		re:       regexp.MustCompile(`(?:~/|\./|/)?(?:[\w.+@%~-]+/)*[\w.+@%~-]+\.\w+:\d+(?::\d+)?\b`),
	},
	{
		// kubectl-style resource refs. Same span as path would match, so the
		// higher priority decides.
		name:     "kubernetes",
		priority: 34,
		re:       regexp.MustCompile(`\b(?:deployment|deploy|pod|po|service|svc|replicaset|rs|statefulset|sts|daemonset|ds|job|cronjob|namespace|ns|node|no|ingress|ing|configmap|cm|secret|pv|pvc)(?:\.[\w.-]+)?/[\w.-]+\b`),
	},
	{
		// Quoted strings: the content is copied without the quotes (escape
		// sequences are kept as-is). Two entries share the "quoted" name.
		name:     "quoted",
		priority: 32,
		group:    1,
		re:       regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`),
	},
	{
		// Single quotes only open after start-of-line or a delimiter, so
		// apostrophes (don't) never start a string — RE2 has no lookbehind.
		name:     "quoted",
		priority: 32,
		group:    1,
		re:       regexp.MustCompile(`(?:^|[\s(\[{=,:])'([^'\n]*)'`),
	},
	{
		name:     "path",
		priority: 30,
		// Absolute, ~/ and ./-style paths, plus bare relative paths that
		// contain at least one slash.
		re:     regexp.MustCompile(`(?:~|\.{1,2})?/[\w.+@%~-]+(?:/[\w.+@%~-]+)*/?|\b[\w.+@%~-]+(?:/[\w.+@%~-]+)+/?`),
		filter: trimTrailingPunct,
	},
	{
		// ISO date with optional time and zone. Swallows number's year match.
		name:     "datetime",
		priority: 25,
		re:       regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?)?\b`),
	},
	{
		// The regex also consumes 4+-part dotted quads so it never grabs the
		// first three components of an IP-like token; the filter then drops
		// those (real IPs are ipv4's job, bogus ones are noise).
		name:     "semver",
		priority: 20,
		re:       regexp.MustCompile(`\bv?\d+\.\d+\.\d+(?:\.\d+)*(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?\b`),
		filter: func(s string) string {
			if dottedQuadRe.MatchString(s) {
				return ""
			}
			return s
		},
	},
	{
		// Standalone integers (PIDs, ports). Lowest priority; complements
		// sha's pure-digit rejection.
		name:     "number",
		priority: 10,
		re:       regexp.MustCompile(`\b\d{4,}\b`),
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
		if err != nil || cp.Group < 0 || cp.Group > re.NumSubexp() {
			badCustom = append(badCustom, cp.Name)
			continue
		}
		// Custom patterns sit above path, below the specific built-ins.
		pats = append(pats, pattern{name: cp.Name, priority: 40, group: cp.Group, re: re})
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
			for _, loc := range p.re.FindAllStringSubmatchIndex(line, -1) {
				g := 2 * p.group
				if g+1 >= len(loc) || loc[g] < 0 {
					continue
				}
				text := line[loc[g]:loc[g+1]]
				if p.filter != nil {
					text = p.filter(text)
				}
				if text == "" {
					continue
				}
				start := utf8.RuneCountInString(line[:loc[g]])
				end := start + utf8.RuneCountInString(text)
				// The whole-match span drives overlap resolution; for plain
				// patterns it is the (post-filter) copied span itself.
				matchStart, matchEnd := start, end
				if p.group != 0 {
					matchStart = utf8.RuneCountInString(line[:loc[0]])
					matchEnd = utf8.RuneCountInString(line[:loc[1]])
				}
				ms = append(ms, Candidate{
					Line:       li,
					StartRune:  start,
					EndRune:    end,
					MatchStart: matchStart,
					MatchEnd:   matchEnd,
					Text:       text,
					Pattern:    p.name,
				})
			}
		}
		out = append(out, resolveOverlaps(ms, pats)...)
	}
	return capUnique(out, cfg.MaxCandidates)
}

// resolveOverlaps keeps a non-overlapping subset of the line's matches: sorted
// by whole-match start ascending, longer first at the same start, then pattern
// priority. A left-to-right sweep drops anything that overlaps an already-kept
// match, so a URL swallows the path-like substring inside it.
func resolveOverlaps(ms []Candidate, pats []pattern) []Candidate {
	prio := make(map[string]int, len(pats))
	for _, p := range pats {
		prio[p.name] = p.priority
	}
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].MatchStart != ms[j].MatchStart {
			return ms[i].MatchStart < ms[j].MatchStart
		}
		li, lj := ms[i].MatchEnd-ms[i].MatchStart, ms[j].MatchEnd-ms[j].MatchStart
		if li != lj {
			return li > lj
		}
		return prio[ms[i].Pattern] > prio[ms[j].Pattern]
	})
	var kept []Candidate
	lastEnd := -1
	for _, m := range ms {
		if m.MatchStart < lastEnd {
			continue
		}
		// Identical span already kept (same start as previous, sweep order
		// guarantees the better one came first).
		if len(kept) > 0 && kept[len(kept)-1].MatchStart == m.MatchStart {
			continue
		}
		kept = append(kept, m)
		lastEnd = m.MatchEnd
	}
	return kept
}

// ExtractWords finds every word-like run on screen for the word mode: runs
// of letters/digits plus cfg.WordChars, trimmed so they start and end
// alphanumeric, at least cfg.WordMinLen runes long. It deliberately ignores
// the token patterns — word mode is the explicit "label everything" escape
// hatch when no pattern matched what you want — and reuses Candidate so the
// token-mode painters and label maps work unchanged. Unlike Extract it caps
// at the label capacity (alphabet²) instead of max_candidates: labeling
// everything is the whole point, and an unlabeled word just looks broken.
func ExtractWords(screen string, cfg Config) []Candidate {
	minLen := cfg.WordMinLen
	if minLen <= 0 {
		minLen = 3
	}
	isWord := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(cfg.WordChars, r)
	}
	isEdge := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	var out []Candidate
	for li, line := range strings.Split(screen, "\n") {
		runes := []rune(line)
		for i := 0; i < len(runes); {
			if !isWord(runes[i]) {
				i++
				continue
			}
			j := i
			for j < len(runes) && isWord(runes[j]) {
				j++
			}
			s, e := i, j
			for s < e && !isEdge(runes[s]) {
				s++
			}
			for e > s && !isEdge(runes[e-1]) {
				e--
			}
			if e-s >= minLen {
				out = append(out, Candidate{
					Line:       li,
					StartRune:  s,
					EndRune:    e,
					MatchStart: s,
					MatchEnd:   e,
					Text:       string(runes[s:e]),
					Pattern:    "word",
				})
			}
			i = j
		}
	}
	n := len([]rune(cfg.Alphabet))
	return capUnique(out, n*n)
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
