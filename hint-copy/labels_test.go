package main

import (
	"strings"
	"testing"
)

func TestGenerateLabelsSingleChar(t *testing.T) {
	alpha := []rune("asdf")
	labels := GenerateLabels(4, alpha)
	if strings.Join(labels, "") != "asdf" {
		t.Errorf("got %v", labels)
	}
}

func TestGenerateLabelsTwoCharUniformAndPrefixFree(t *testing.T) {
	alpha := []rune("asdf")
	labels := GenerateLabels(5, alpha)
	if len(labels) != 5 {
		t.Fatalf("got %d labels", len(labels))
	}
	seen := map[string]bool{}
	for _, l := range labels {
		if len(l) != 2 {
			t.Errorf("label %q is not uniform 2-char", l)
		}
		if seen[l] {
			t.Errorf("duplicate label %q", l)
		}
		seen[l] = true
	}
}

func TestGenerateLabelsDefaultAlphabetCapacity(t *testing.T) {
	alpha := []rune(defaultConfig().Alphabet)
	labels := GenerateLabels(100, alpha)
	if len(labels) != 100 {
		t.Fatalf("got %d", len(labels))
	}
	seen := map[string]bool{}
	for _, l := range labels {
		if seen[l] {
			t.Errorf("dup %q", l)
		}
		seen[l] = true
	}
}

func TestAssignLabelsSharedForIdenticalText(t *testing.T) {
	cands := []Candidate{
		{Line: 0, Text: "/tmp/a"},
		{Line: 1, Text: "/tmp/b"},
		{Line: 2, Text: "/tmp/a"},
	}
	lm := AssignLabels(cands, defaultConfig())
	if lm.LabelFor("/tmp/a") == "" || lm.LabelFor("/tmp/b") == "" {
		t.Fatal("missing labels")
	}
	if lm.LabelFor("/tmp/a") == lm.LabelFor("/tmp/b") {
		t.Error("distinct texts share a label")
	}
	if got, _ := lm.Exact(lm.LabelFor("/tmp/a")); got != "/tmp/a" {
		t.Errorf("Exact roundtrip got %q", got)
	}
}

func TestAssignLabelsReverse(t *testing.T) {
	cands := []Candidate{
		{Line: 0, Text: "top"},
		{Line: 5, Text: "bottom"},
	}
	cfg := defaultConfig()
	cfg.Reverse = true
	lm := AssignLabels(cands, cfg)
	if lm.LabelFor("bottom") != "a" {
		t.Errorf("bottom label = %q, want %q", lm.LabelFor("bottom"), "a")
	}
}

func TestHasPrefix(t *testing.T) {
	cands := make([]Candidate, 0, 30)
	for i := 0; i < 30; i++ {
		cands = append(cands, Candidate{Text: strings.Repeat("x", i+1)})
	}
	lm := AssignLabels(cands, defaultConfig()) // 30 > 24 → two-char labels
	if !lm.HasPrefix("a") {
		t.Error("expected prefix 'a'")
	}
	if lm.HasPrefix("!") {
		t.Error("unexpected prefix '!'")
	}
	if _, ok := lm.Exact("a"); ok {
		t.Error("single char must not be exact in two-char mode")
	}
}
