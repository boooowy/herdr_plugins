package main

import (
	"strings"
	"testing"
)

func TestAssignLineLabelsSkipsBlankLines(t *testing.T) {
	lines := []string{"first", "", "  \r", "second"}
	ll := AssignLineLabels(lines, 0, len(lines), defaultConfig())
	if ll.LabelFor(0) != "a" || ll.LabelFor(3) != "s" {
		t.Errorf("labels: 0=%q 3=%q", ll.LabelFor(0), ll.LabelFor(3))
	}
	if ll.LabelFor(1) != "" || ll.LabelFor(2) != "" {
		t.Error("blank lines must stay unlabeled")
	}
	if line, ok := ll.Exact("s"); !ok || line != 3 {
		t.Errorf("Exact(s) = %d, %v", line, ok)
	}
}

func TestAssignLineLabelsWindowAndReverse(t *testing.T) {
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, "line")
	}
	// Only the window [5, 10) is labeled.
	ll := AssignLineLabels(lines, 5, 5, defaultConfig())
	if ll.LabelFor(4) != "" || ll.LabelFor(5) != "a" {
		t.Errorf("window wrong: 4=%q 5=%q", ll.LabelFor(4), ll.LabelFor(5))
	}
	cfg := defaultConfig()
	cfg.Reverse = true
	ll = AssignLineLabels(lines, 5, 5, cfg)
	if ll.LabelFor(9) != "a" {
		t.Errorf("reverse should label bottom line 'a', got %q", ll.LabelFor(9))
	}
}

func TestAssignLineLabelsTwoCharWhenMany(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "x")
	}
	ll := AssignLineLabels(lines, 0, 30, defaultConfig())
	if ll.Width() != 2 {
		t.Errorf("Width = %d, want 2", ll.Width())
	}
	if ll.HasPrefix("!") {
		t.Error("bogus prefix accepted")
	}
	if !ll.HasPrefix("a") {
		t.Error("prefix 'a' missing")
	}
	if _, ok := ll.Exact("a"); ok {
		t.Error("single char must not be exact in two-char mode")
	}
}

func TestSingleLineTextTrimsBothEnds(t *testing.T) {
	lines := []string{"  go build -o bin/x .  "}
	if got := SingleLineText(lines, 0); got != "go build -o bin/x ." {
		t.Errorf("got %q", got)
	}
}

func TestRangeText(t *testing.T) {
	lines := []string{
		"func main() {   ",
		"\tfmt.Println(1)",
		"",
		"\tfmt.Println(2)\r",
		"}",
	}
	want := "func main() {\n\tfmt.Println(1)\n\n\tfmt.Println(2)\n}"
	if got := RangeText(lines, 0, 4); got != want {
		t.Errorf("got %q", got)
	}
	// Reversed arguments normalize; leading indent survives.
	if got := RangeText(lines, 3, 1); got != "\tfmt.Println(1)\n\n\tfmt.Println(2)" {
		t.Errorf("reversed got %q", got)
	}
	if strings.HasSuffix(RangeText(lines, 0, 4), "\n") {
		t.Error("no trailing newline")
	}
}
