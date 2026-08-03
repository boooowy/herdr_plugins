package main

import "testing"

func smellCallers(dirs []string, ambiguous int) []outlineRef {
	refs := make([]outlineRef, 0, len(dirs))
	for i, dir := range dirs {
		refs = append(refs, outlineRef{
			ID:        string(rune('a' + i)),
			Path:      dir + "/caller.go",
			Name:      "caller",
			Kind:      "fn",
			Line:      i + 1,
			Ambiguous: i < ambiguous,
		})
	}
	return refs
}

func TestOutlineGodHelper(t *testing.T) {
	tests := []struct {
		name      string
		dirs      []string
		ambiguous int
		test      bool
		want      bool
	}{
		{"5 callers across 3 dirs", []string{"api", "api", "batch", "batch", "cli"}, 0, false, true},
		{"5 callers across 2 dirs", []string{"api", "api", "api", "batch", "batch"}, 0, false, false},
		{"4 callers across 4 dirs", []string{"api", "batch", "cli", "web"}, 0, false, false},
		{"ambiguous callers do not count", []string{"api", "batch", "cli", "web", "job", "etl", "ops"}, 3, false, false},
		{"test symbols never flag", []string{"api", "api", "batch", "batch", "cli"}, 0, true, false},
		{"no callers", nil, 0, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := outlineSymbol{Kind: "fn", Change: outlineBodyOnly, Test: tt.test,
				Callers: smellCallers(tt.dirs, tt.ambiguous)}
			s.FanIn = len(s.Callers)
			if got := s.godHelper(); got != tt.want {
				callers, dirs := s.godHelperStats()
				t.Errorf("godHelper() = %v (callers:%d dirs:%d), want %v", got, callers, dirs, tt.want)
			}
		})
	}
}

func TestOutlineBloatGrowth(t *testing.T) {
	symbol := func(kind string, change outlineChange, oldLines, newLines int, test bool) outlineSymbol {
		s := outlineSymbol{Kind: kind, Change: change, Test: test}
		if newLines > 0 {
			s.StartLine, s.EndLine = 1, newLines
		}
		if oldLines > 0 {
			s.OldStartLine, s.OldEndLine = 1, oldLines
		}
		return s
	}
	tests := []struct {
		name       string
		symbol     outlineSymbol
		wantGrowth int
		wantOK     bool
	}{
		{"tripled body", symbol("fn", outlineBodyOnly, 20, 60, false), 40, true},
		{"large but shallow growth", symbol("fn", outlineBodyOnly, 200, 240, false), 0, false},
		{"steep but small growth", symbol("fn", outlineBodyOnly, 10, 30, false), 0, false},
		{"exactly 1.5x at minimum growth", symbol("fn", outlineBodyOnly, 60, 90, false), 30, true},
		{"signature change also qualifies", symbol("method", outlineSignatureChanged, 40, 90, false), 50, true},
		{"added is big's territory", symbol("fn", outlineAdded, 0, 120, false), 0, false},
		{"unpaired old side", symbol("fn", outlineBodyOnly, 0, 120, false), 0, false},
		{"struct kinds excluded", symbol("struct", outlineBodyOnly, 20, 60, false), 0, false},
		{"test symbols excluded", symbol("fn", outlineBodyOnly, 20, 60, true), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			growth, ok := tt.symbol.bloatGrowth()
			if growth != tt.wantGrowth || ok != tt.wantOK {
				t.Errorf("bloatGrowth() = (%d, %v), want (%d, %v)", growth, ok, tt.wantGrowth, tt.wantOK)
			}
		})
	}
}

func TestOutlineBigAddedLines(t *testing.T) {
	symbol := func(kind string, change outlineChange, lines int, test bool) outlineSymbol {
		return outlineSymbol{Kind: kind, Change: change, Test: test, StartLine: 1, EndLine: lines}
	}
	tests := []struct {
		name      string
		symbol    outlineSymbol
		wantLines int
		wantOK    bool
	}{
		{"at threshold", symbol("fn", outlineAdded, 80, false), 80, true},
		{"below threshold", symbol("fn", outlineAdded, 79, false), 79, false},
		{"struct kinds excluded", symbol("struct", outlineAdded, 200, false), 0, false},
		{"only added qualifies", symbol("fn", outlineBodyOnly, 120, false), 0, false},
		{"test symbols excluded", symbol("fn", outlineAdded, 120, true), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines, ok := tt.symbol.bigAddedLines()
			if lines != tt.wantLines || ok != tt.wantOK {
				t.Errorf("bigAddedLines() = (%d, %v), want (%d, %v)", lines, ok, tt.wantLines, tt.wantOK)
			}
		})
	}
}

func TestOutlineSmellMaskAndQueryText(t *testing.T) {
	s := outlineSymbol{
		Kind: "fn", Change: outlineBodyOnly,
		StartLine: 1, EndLine: 60, OldStartLine: 1, OldEndLine: 20,
		Callers: smellCallers([]string{"api", "api", "batch", "batch", "cli"}, 0),
	}
	if got := s.smells(); got != smellGod|smellBloat {
		t.Errorf("smells() = %b, want god|bloat", got)
	}
	if got := s.smellQueryText(); got != "god-helper 呼出集中 bloat 肥大" {
		t.Errorf("smellQueryText() = %q", got)
	}
	file := outlineFile{Symbols: []outlineSymbol{s, {Kind: "fn", Change: outlineAdded, StartLine: 1, EndLine: 90}}}
	if got := file.smells(); got != smellGod|smellBloat|smellBig {
		t.Errorf("file smells() = %b, want god|bloat|big", got)
	}
	if got := smellChipLabels(file.smells()); len(got) != 3 || got[0] != "god" || got[1] != "bloat" || got[2] != "big" {
		t.Errorf("smellChipLabels() = %v", got)
	}
	if got := (outlineSymbol{Kind: "fn", Change: outlineBodyOnly}).smellQueryText(); got != "" {
		t.Errorf("clean symbol smellQueryText() = %q, want empty", got)
	}
	if got := smellChipLabels(0); got != nil {
		t.Errorf("smellChipLabels(0) = %v, want nil", got)
	}
}
