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
	outlineSchemaVersion = 1
	outlineHighFanIn     = 3
	outlineLargeFileLine = 800
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
