package main

// GenerateLabels returns n distinct hint labels drawn from alphabet. All
// labels have the same length (1 char while n fits in the alphabet, otherwise
// 2 chars for everyone), which makes the set trivially prefix-free — a typed
// key sequence can never be both a complete label and the prefix of another.
func GenerateLabels(n int, alphabet []rune) []string {
	if n <= 0 {
		return nil
	}
	if n <= len(alphabet) {
		labels := make([]string, n)
		for i := 0; i < n; i++ {
			labels[i] = string(alphabet[i])
		}
		return labels
	}
	labels := make([]string, 0, n)
	for _, a := range alphabet {
		for _, b := range alphabet {
			labels = append(labels, string(a)+string(b))
			if len(labels) == n {
				return labels
			}
		}
	}
	// More candidates than two-char combinations: callers cap candidates well
	// below alphabet² (24² = 576), so truncation here is theoretical.
	return labels
}

// LabelMap binds candidate strings to hint labels. Identical strings share a
// label (so a URL printed five times is one keypress, not five labels).
type LabelMap struct {
	byLabel map[string]string // label → text to copy
	byText  map[string]string // text → its label
}

// AssignLabels gives every unique candidate string a label, in first-seen
// screen order. With cfg.Reverse the easiest labels (earliest in the alphabet)
// go to the strings nearest the bottom of the screen — usually the most recent
// output next to the prompt.
func AssignLabels(cands []Candidate, cfg Config) *LabelMap {
	var order []string
	seen := make(map[string]bool)
	for _, c := range cands {
		if !seen[c.Text] {
			seen[c.Text] = true
			order = append(order, c.Text)
		}
	}
	if cfg.Reverse {
		for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
			order[i], order[j] = order[j], order[i]
		}
	}
	labels := GenerateLabels(len(order), []rune(cfg.Alphabet))
	lm := &LabelMap{
		byLabel: make(map[string]string, len(order)),
		byText:  make(map[string]string, len(order)),
	}
	for i, text := range order {
		if i >= len(labels) {
			break
		}
		lm.byLabel[labels[i]] = text
		lm.byText[text] = labels[i]
	}
	return lm
}

// Exact returns the text for a fully typed label.
func (lm *LabelMap) Exact(label string) (string, bool) {
	text, ok := lm.byLabel[label]
	return text, ok
}

// HasPrefix reports whether any label starts with the typed prefix.
func (lm *LabelMap) HasPrefix(prefix string) bool {
	for label := range lm.byLabel {
		if len(label) >= len(prefix) && label[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// LabelFor returns the label assigned to a candidate's text ("" when the
// candidate fell past the cap).
func (lm *LabelMap) LabelFor(text string) string {
	return lm.byText[text]
}
