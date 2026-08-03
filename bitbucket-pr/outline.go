package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	outlineSchemaVersion = 3
	outlineHighFanIn     = 3
	outlineLargeFileLine = 800

	// Smell thresholds. god-helper must be a stronger signal than the "!"
	// risky mark (fan-in >= 3), and 2 caller directories happen naturally
	// with one adjacent package; 3 is the smallest evidence of cross-cutting
	// use. Bloat requires both absolute and relative growth so that routine
	// additions to already-long functions stay quiet.
	outlineGodHelperFanIn = 5
	outlineGodHelperDirs  = 3
	outlineBloatMinGrowth = 30
	outlineBigAddedLines  = 80
)

// outlineChange is the review weight of one changed symbol. The ASCII marks
// deliberately match rinkaku's quiet visual vocabulary.
type outlineChange string

const (
	outlineAdded            outlineChange = "added"
	outlineSignatureChanged outlineChange = "signature_changed"
	outlineRemoved          outlineChange = "removed"
	outlineBodyOnly         outlineChange = "body_only"
)

func (c outlineChange) mark() string {
	switch c {
	case outlineAdded:
		return "+"
	case outlineSignatureChanged:
		return "~"
	case outlineRemoved:
		return "x"
	default:
		return " "
	}
}

// outlineRef is one caller/callee in a symbol's one-hop neighborhood.
// Changed is useful in the detail pane: references already in the PR stand
// out from unchanged repository context.
type outlineRef struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Line      int    `json:"line"`
	Changed   bool   `json:"changed,omitempty"`
	Ambiguous bool   `json:"ambiguous,omitempty"`
}

// outlineSymbol is the stable, cacheable representation used by the UI.
type outlineSymbol struct {
	ID           string        `json:"id"`
	Path         string        `json:"path"`
	OldPath      string        `json:"old_path,omitempty"`
	Language     string        `json:"language"`
	Name         string        `json:"name"`
	Kind         string        `json:"kind"`
	Signature    string        `json:"signature,omitempty"`
	OldSignature string        `json:"old_signature,omitempty"`
	Change       outlineChange `json:"change"`
	StartLine    int           `json:"start_line,omitempty"`
	EndLine      int           `json:"end_line,omitempty"`
	OldStartLine int           `json:"old_start_line,omitempty"`
	OldEndLine   int           `json:"old_end_line,omitempty"`
	Test         bool          `json:"test,omitempty"`
	FanIn        int           `json:"fan_in,omitempty"`
	Callers      []outlineRef  `json:"callers,omitempty"`
	Callees      []outlineRef  `json:"callees,omitempty"`
}

func (s outlineSymbol) contractChanged() bool {
	return s.Change == outlineAdded || s.Change == outlineRemoved || s.Change == outlineSignatureChanged
}

func (s outlineSymbol) risky() bool {
	return s.contractChanged() && s.FanIn >= outlineHighFanIn
}

// outlineCallableSymbolKind reports whether a kind has a body whose line
// count is meaningful for size smells. Type-ish kinds (struct, class, enum,
// config tables) legitimately grow long and would only produce noise.
func outlineCallableSymbolKind(kind string) bool {
	switch kind {
	case "fn", "method", "ctor":
		return true
	}
	return false
}

// godHelperStats counts unambiguous callers and the distinct directories
// they live in. Ambiguous references are excluded so that generic names
// (Get, run, ...) resolved by best-effort matching cannot inflate the count.
func (s outlineSymbol) godHelperStats() (callers, dirs int) {
	if s.Test {
		return 0, 0
	}
	seen := map[string]bool{}
	for _, c := range s.Callers {
		if c.Ambiguous {
			continue
		}
		callers++
		seen[filepath.ToSlash(filepath.Dir(c.Path))] = true
	}
	return callers, len(seen)
}

// godHelper flags a symbol that many unrelated parts of the codebase call:
// high fan-in alone is fine for a stable utility, but combined with callers
// scattered across directories it marks a helper that crosses
// responsibility boundaries.
func (s outlineSymbol) godHelper() bool {
	callers, dirs := s.godHelperStats()
	return callers >= outlineGodHelperFanIn && dirs >= outlineGodHelperDirs
}

// bloatGrowth reports how many lines a paired callable grew while keeping
// (or merely tweaking) its signature. Requires both +outlineBloatMinGrowth
// lines and a 1.5x size increase.
func (s outlineSymbol) bloatGrowth() (growth int, ok bool) {
	if s.Test || !outlineCallableSymbolKind(s.Kind) {
		return 0, false
	}
	if s.Change != outlineBodyOnly && s.Change != outlineSignatureChanged {
		return 0, false
	}
	if s.OldEndLine == 0 || s.OldStartLine == 0 || s.EndLine == 0 {
		return 0, false
	}
	newLines := s.EndLine - s.StartLine + 1
	oldLines := s.OldEndLine - s.OldStartLine + 1
	growth = newLines - oldLines
	if growth < outlineBloatMinGrowth {
		return 0, false
	}
	if newLines*2 < oldLines*3 {
		return 0, false
	}
	return growth, true
}

// bigAddedLines reports the size of a brand-new callable that already
// exceeds outlineBigAddedLines. New files classify every symbol as added,
// so oversized functions in generated-from-scratch code are caught too.
func (s outlineSymbol) bigAddedLines() (lines int, ok bool) {
	if s.Test || s.Change != outlineAdded || !outlineCallableSymbolKind(s.Kind) {
		return 0, false
	}
	if s.EndLine == 0 {
		return 0, false
	}
	lines = s.EndLine - s.StartLine + 1
	return lines, lines >= outlineBigAddedLines
}

// smellMask identifies which smells a symbol (or its file/directory
// subtree) carries; bloat and big are mutually exclusive by Change, so a
// single symbol carries at most two bits.
type smellMask uint8

const (
	smellGod   smellMask = 1 << iota // 呼出集中
	smellBloat                       // 肥大
	smellBig                         // 巨大
)

func (s outlineSymbol) smells() smellMask {
	var m smellMask
	if s.godHelper() {
		m |= smellGod
	}
	if _, ok := s.bloatGrowth(); ok {
		m |= smellBloat
	}
	if _, ok := s.bigAddedLines(); ok {
		m |= smellBig
	}
	return m
}

// smellChipLabels returns the aggregate chip labels (no counts) in a fixed
// order matching the per-symbol chip order.
func smellChipLabels(m smellMask) []string {
	var labels []string
	if m&smellGod != 0 {
		labels = append(labels, "呼出集中")
	}
	if m&smellBloat != 0 {
		labels = append(labels, "肥大")
	}
	if m&smellBig != 0 {
		labels = append(labels, "巨大")
	}
	return labels
}

// smellQueryText feeds the "/" filter. Both the Japanese chip labels and
// English aliases are listed so /肥大 and /bloat find the same symbols.
func (s outlineSymbol) smellQueryText() string {
	var parts []string
	if s.godHelper() {
		parts = append(parts, "呼出集中 god-helper")
	}
	if _, ok := s.bloatGrowth(); ok {
		parts = append(parts, "肥大 bloat")
	}
	if _, ok := s.bigAddedLines(); ok {
		parts = append(parts, "巨大 big")
	}
	return strings.Join(parts, " ")
}

// outlineFile keeps production and test symbols separate so tests can remain
// collapsed without disappearing from the change map.
type outlineFile struct {
	Path        string          `json:"path"`
	OldPath     string          `json:"old_path,omitempty"`
	Language    string          `json:"language,omitempty"`
	Lines       int             `json:"lines,omitempty"`
	Symbols     []outlineSymbol `json:"symbols,omitempty"`
	TestSymbols []outlineSymbol `json:"test_symbols,omitempty"`
	Skipped     string          `json:"skipped,omitempty"`
}

func (f outlineFile) changedCount() int { return len(f.Symbols) + len(f.TestSymbols) }

func (f outlineFile) apiCount() int {
	n := 0
	for _, symbol := range f.Symbols {
		if symbol.contractChanged() {
			n++
		}
	}
	return n
}

func (f outlineFile) smells() smellMask {
	var m smellMask
	for _, symbol := range f.Symbols {
		m |= symbol.smells()
	}
	return m
}

func (f outlineFile) fanIn() int {
	n := 0
	for _, symbol := range f.Symbols {
		n += symbol.FanIn
	}
	return n
}

func (f outlineFile) risky() bool {
	for _, symbol := range f.Symbols {
		if symbol.risky() {
			return true
		}
	}
	return false
}

// outlineEdge only stores changed-to-changed relationships. It is sufficient
// for topological directory ordering; unchanged one-hop context remains on
// the symbols themselves as Callers/Callees.
type outlineEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type outlineResult struct {
	Schema          int           `json:"schema"`
	SourceHash      string        `json:"source_hash"`
	DestinationHash string        `json:"destination_hash"`
	Files           []outlineFile `json:"files"`
	Edges           []outlineEdge `json:"edges,omitempty"`
	Warnings        []string      `json:"warnings,omitempty"`
}

func (r *outlineResult) symbolCount() int {
	if r == nil {
		return 0
	}
	n := 0
	for _, file := range r.Files {
		n += file.changedCount()
	}
	return n
}

func (r *outlineResult) testSymbolCount() int {
	if r == nil {
		return 0
	}
	n := 0
	for _, file := range r.Files {
		n += len(file.TestSymbols)
	}
	return n
}

func (r *outlineResult) symbolByID(id string) *outlineSymbol {
	if r == nil {
		return nil
	}
	for fi := range r.Files {
		for si := range r.Files[fi].Symbols {
			if r.Files[fi].Symbols[si].ID == id {
				return &r.Files[fi].Symbols[si]
			}
		}
		for si := range r.Files[fi].TestSymbols {
			if r.Files[fi].TestSymbols[si].ID == id {
				return &r.Files[fi].TestSymbols[si]
			}
		}
	}
	return nil
}

func outlineSymbolID(path, kind, name string, line int) string {
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%d", filepath.ToSlash(path), kind, name, line)
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

func uniqueOutlineRefs(refs []outlineRef) []outlineRef {
	seen := map[string]bool{}
	out := make([]outlineRef, 0, len(refs))
	for _, ref := range refs {
		key := ref.ID
		if key == "" {
			key = fmt.Sprintf("%s:%d:%s", ref.Path, ref.Line, ref.Name)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ref)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Changed != out[j].Changed {
			return out[i].Changed
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func normalizeSignature(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
