package main

import (
	"regexp"
	"strconv"
	"strings"
)

// LineKind classifies one diff body line.
type LineKind int

const (
	LineContext LineKind = iota
	LineAdd
	LineDel
	LineNoNewline // "\ No newline at end of file"
)

// DiffLine is one body line of a hunk. OldNo/NewNo are 1-based line numbers
// on each side; 0 means the line does not exist on that side.
type DiffLine struct {
	Kind         LineKind
	OldNo, NewNo int
	Text         string // without the leading +/-/space marker
}

// Hunk is one @@ block.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Section            string // trailing context after the second @@
	Lines              []DiffLine
}

// FileDiff is one file's part of a unified diff.
type FileDiff struct {
	OldPath, NewPath string // "" for /dev/null sides
	IsBinary         bool
	IsRename         bool
	IsNew            bool
	IsDeleted        bool
	Hunks            []Hunk
}

// Path returns the file's display path (new side, falling back to old).
func (f *FileDiff) Path() string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

// DisplayPath renders the path with rename notation.
func (f *FileDiff) DisplayPath() string {
	if f.IsRename && f.OldPath != "" && f.NewPath != "" && f.OldPath != f.NewPath {
		return f.OldPath + " → " + f.NewPath
	}
	return f.Path()
}

// Added / Deleted count the change lines across hunks.
func (f *FileDiff) Added() (n int) {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == LineAdd {
				n++
			}
		}
	}
	return
}

func (f *FileDiff) Deleted() (n int) {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == LineDel {
				n++
			}
		}
	}
	return
}

var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$`)

// ParseUnifiedDiff parses a git-style unified diff (what the Bitbucket PR
// diff endpoint returns) into per-file hunks with old/new line numbers.
// It tolerates: quoted paths, /dev/null sides, "Binary files ... differ",
// rename-/mode-only entries with no hunks, "\ No newline at end of file",
// and empty context lines emitted as "" instead of " ".
func ParseUnifiedDiff(text string) []FileDiff {
	var files []FileDiff
	var cur *FileDiff
	var curHunk *Hunk
	oldNo, newNo := 0, 0

	flushHunk := func() {
		if cur != nil && curHunk != nil {
			cur.Hunks = append(cur.Hunks, *curHunk)
		}
		curHunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			files = append(files, *cur)
		}
		cur = nil
	}

	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			old, new_ := parseGitDiffPaths(line[len("diff --git "):])
			cur = &FileDiff{OldPath: old, NewPath: new_}

		case cur == nil && strings.HasPrefix(line, "--- "):
			// Plain unified diff without a "diff --git" header.
			cur = &FileDiff{OldPath: stripDiffPath(line[4:])}

		case cur != nil && curHunk == nil && strings.HasPrefix(line, "--- "):
			cur.OldPath = stripDiffPath(line[4:])
			if cur.OldPath == "" {
				cur.IsNew = true
			}

		case cur != nil && curHunk == nil && strings.HasPrefix(line, "+++ "):
			cur.NewPath = stripDiffPath(line[4:])
			if cur.NewPath == "" {
				cur.IsDeleted = true
			}

		case cur != nil && curHunk == nil && strings.HasPrefix(line, "rename from "):
			cur.IsRename = true
			cur.OldPath = line[len("rename from "):]

		case cur != nil && curHunk == nil && strings.HasPrefix(line, "rename to "):
			cur.IsRename = true
			cur.NewPath = line[len("rename to "):]

		case cur != nil && curHunk == nil && strings.HasPrefix(line, "new file mode"):
			cur.IsNew = true
			cur.OldPath = ""

		case cur != nil && curHunk == nil && strings.HasPrefix(line, "deleted file mode"):
			cur.IsDeleted = true
			cur.NewPath = ""

		case cur != nil && (strings.HasPrefix(line, "Binary files ") || line == "GIT binary patch"):
			cur.IsBinary = true

		case cur != nil && strings.HasPrefix(line, "@@"):
			m := hunkHeaderRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			flushHunk()
			h := Hunk{
				OldStart: atoiDefault(m[1], 0),
				OldCount: atoiDefault(m[2], 1),
				NewStart: atoiDefault(m[3], 0),
				NewCount: atoiDefault(m[4], 1),
				Section:  m[5],
			}
			curHunk = &h
			oldNo, newNo = h.OldStart, h.NewStart

		case curHunk != nil:
			var dl DiffLine
			switch {
			case strings.HasPrefix(line, "+"):
				dl = DiffLine{Kind: LineAdd, NewNo: newNo, Text: line[1:]}
				newNo++
			case strings.HasPrefix(line, "-"):
				dl = DiffLine{Kind: LineDel, OldNo: oldNo, Text: line[1:]}
				oldNo++
			case strings.HasPrefix(line, `\`):
				dl = DiffLine{Kind: LineNoNewline, Text: strings.TrimPrefix(line, `\ `)}
			case strings.HasPrefix(line, " ") || line == "":
				// Some producers emit truly empty context lines.
				if line == "" && i == len(lines)-1 {
					continue // trailing newline artifact of strings.Split
				}
				dl = DiffLine{Kind: LineContext, OldNo: oldNo, NewNo: newNo, Text: strings.TrimPrefix(line, " ")}
				oldNo++
				newNo++
			default:
				// Anything else ends the hunk (defensive; usually a new
				// "diff --git" which the first case catches next round).
				flushHunk()
				continue
			}
			curHunk.Lines = append(curHunk.Lines, dl)
		}
	}
	flushFile()
	return files
}

// parseGitDiffPaths splits the `a/... b/...` tail of a "diff --git" line,
// handling quoted paths ("a/dir name/x") and unquoted paths with spaces
// (resolved at the last " b/" separator).
func parseGitDiffPaths(s string) (oldPath, newPath string) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `"`) {
		if unq, rest, ok := unquotePrefix(s); ok {
			return stripABPrefix(unq), stripDiffPath(strings.TrimSpace(rest))
		}
	}
	if idx := strings.LastIndex(s, ` b/`); idx >= 0 {
		return stripABPrefix(s[:idx]), stripABPrefix(s[idx+1:])
	}
	parts := strings.SplitN(s, " ", 2)
	if len(parts) == 2 {
		return stripABPrefix(parts[0]), stripDiffPath(parts[1])
	}
	return stripABPrefix(s), ""
}

// unquotePrefix parses a leading C-style quoted string and returns it with
// the remainder.
func unquotePrefix(s string) (unquoted, rest string, ok bool) {
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			if unq, err := strconv.Unquote(s[:i+1]); err == nil {
				return unq, s[i+1:], true
			}
			return "", "", false
		}
	}
	return "", "", false
}

// stripDiffPath normalizes a ---/+++ path: unquotes, drops the a/ or b/
// prefix, and maps /dev/null to "".
func stripDiffPath(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `"`) {
		if unq, _, ok := unquotePrefix(s); ok {
			s = unq
		}
	}
	if s == "/dev/null" {
		return ""
	}
	return stripABPrefix(s)
}

// stripABPrefix drops git's a/ or b/ path prefix.
func stripABPrefix(s string) string {
	if strings.HasPrefix(s, "a/") || strings.HasPrefix(s, "b/") {
		return s[2:]
	}
	if s == "/dev/null" {
		return ""
	}
	return s
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
