package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// Markdown support, two-tier: mdStyledLines gives descriptions and comments
// lightweight in-TUI styling, and the `m` key hands the raw markdown to the
// user's own renderer (config `markdown_viewer`, glow by default) in a herdr
// popup — the same pattern as the external diff tool.

const envMDFile = "BBPR_MD_FILE"

// mdStyledLines renders markdown source as styled, wrapped display lines —
// the in-tab renderer for descriptions and comment bodies. Block elements
// (headings, lists, quotes, fences, rules, tables) and inline ones (code,
// bold, links) are interpreted; anything ambiguous passes through verbatim.
// The `m` popup covers full-fidelity rendering.
func mdStyledLines(text string, width int, base StyleID) [][]Span {
	if width < 10 {
		width = 10
	}
	var out [][]Span
	emit := func(spans []Span) { out = append(out, wrapSpans(spans, width)...) }
	// emitHanging wraps content at a marker-indented width, prefixing the
	// marker on the first line and matching spaces after.
	emitHanging := func(marker string, markerStyle StyleID, content []Span) {
		w := width - displayWidth(marker)
		if w < 10 {
			w = 10
		}
		for i, ws := range wrapSpans(content, w) {
			lead := Span{marker, markerStyle}
			if i > 0 {
				lead = Span{strings.Repeat(" ", displayWidth(marker)), styleNone}
			}
			out = append(out, append([]Span{lead}, ws...))
		}
	}

	inFence := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]
		switch {
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			bar := "── "
			if lang := strings.Trim(trimmed, "`~ "); lang != "" && !inFence {
				bar += lang + " "
			}
			inFence = !inFence
			if pad := width - displayWidth(bar); pad > 0 {
				bar += strings.Repeat("─", pad)
			}
			emit([]Span{{bar, styleDim}})
		case inFence:
			emit([]Span{{line, styleMDCode}})
		case strings.HasPrefix(trimmed, "#"):
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			markers := [...]string{"◆ ", "◆ ", "◇ ", "◇ ", "・", "・"}
			emit([]Span{{markers[min(level, 6)-1] + strings.TrimSpace(trimmed[level:]), styleMDHead}})
		case strings.HasPrefix(trimmed, ">"):
			body := strings.TrimPrefix(strings.TrimPrefix(trimmed, ">"), " ")
			emitHanging(indent+"▎ ", styleDim, mdInlineSpans(body, styleDim))
		case isHRule(trimmed):
			emit([]Span{{strings.Repeat("─", width), styleDim}})
		case strings.HasPrefix(trimmed, "|"):
			emit([]Span{{indent + mdTableLine(trimmed), tableStyle(trimmed, base)}})
		default:
			if marker, rest, ok := splitBullet(line); ok {
				m := indent + "• "
				if len(indent) >= 2 {
					m = indent + "◦ "
				}
				if isNumberedMarker(marker) {
					m = marker // keep "12. "
				}
				switch {
				case strings.HasPrefix(rest, "[ ] "):
					m, rest = m+"☐ ", rest[4:]
				case strings.HasPrefix(rest, "[x] ") || strings.HasPrefix(rest, "[X] "):
					m, rest = m+"☑ ", rest[4:]
				}
				emitHanging(m, styleHunk, mdInlineSpans(rest, base))
				continue
			}
			emit(mdInlineSpans(line, base))
		}
	}
	return out
}

// mdInlineSpans parses one line's inline markdown: `code`, **bold**,
// *italic*, [text](url) (URL concealed), and bare URLs. Unclosed markers
// pass through untouched; backslash escapes before punctuation are dropped.
func mdInlineSpans(line string, base StyleID) []Span {
	var spans []Span
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			spans = append(spans, Span{plain.String(), base})
			plain.Reset()
		}
	}
	i := 0
	for i < len(line) {
		rest := line[i:]
		switch {
		case rest[0] == '\\' && len(rest) > 1 && rest[1] < 0x80 && strings.ContainsRune("\\`*_{}[]()#+-.!|>", rune(rest[1])):
			plain.WriteByte(rest[1])
			i += 2
			continue
		case rest[0] == '`':
			if end := strings.Index(rest[1:], "`"); end >= 0 {
				flush()
				spans = append(spans, Span{rest[1 : end+1], styleMDCode})
				i += end + 2
				continue
			}
		case strings.HasPrefix(rest, "**"):
			if end := strings.Index(rest[2:], "**"); end > 0 {
				flush()
				spans = append(spans, Span{rest[2 : end+2], styleTitle})
				i += end + 4
				continue
			}
		case rest[0] == '*' && len(rest) > 2 && rest[1] != ' ' && rest[1] != '*':
			if end := strings.IndexByte(rest[1:], '*'); end > 0 && rest[end] != ' ' {
				flush()
				spans = append(spans, Span{rest[1 : end+1], styleItalic})
				i += end + 2
				continue
			}
		case rest[0] == '[':
			if ct := strings.Index(rest, "]("); ct > 0 {
				if cu := strings.IndexByte(rest[ct:], ')'); cu > 0 {
					flush()
					spans = append(spans, Span{rest[1:ct], styleMDLink})
					i += ct + cu + 1
					continue
				}
			}
		case strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://"):
			end := strings.IndexAny(rest, " \t)>」）")
			if end < 0 {
				end = len(rest)
			}
			flush()
			spans = append(spans, Span{rest[:end], styleMDLink})
			i += end
			continue
		}
		r, size := utf8.DecodeRuneInString(rest)
		plain.WriteRune(r)
		i += size
	}
	flush()
	if len(spans) == 0 {
		spans = []Span{{"", base}}
	}
	return spans
}

// wrapSpans wraps a styled line at width cells, preserving span styles
// across the breaks.
func wrapSpans(spans []Span, width int) [][]Span {
	if width < 1 {
		width = 1
	}
	var out [][]Span
	var cur []Span
	curW := 0
	for _, sp := range spans {
		var b strings.Builder
		for _, r := range sp.Text {
			rw := runewidth.RuneWidth(r)
			if curW+rw > width {
				if b.Len() > 0 {
					cur = append(cur, Span{b.String(), sp.Style})
					b.Reset()
				}
				out = append(out, cur)
				cur, curW = nil, 0
			}
			b.WriteRune(r)
			curW += rw
		}
		if b.Len() > 0 {
			cur = append(cur, Span{b.String(), sp.Style})
		}
	}
	return append(out, cur)
}

// isHRule reports a thematic break: a line of only -, * or _ (3+ of one kind).
func isHRule(t string) bool {
	t = strings.ReplaceAll(t, " ", "")
	if len(t) < 3 {
		return false
	}
	c := t[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for i := 1; i < len(t); i++ {
		if t[i] != c {
			return false
		}
	}
	return true
}

// mdTableLine restyles a pipe-table row: │ borders, ─ in delimiter rows.
func mdTableLine(t string) string {
	if isTableDelimiter(t) {
		t = strings.Map(func(r rune) rune {
			switch r {
			case '|':
				return '┼'
			case '-', ':':
				return '─'
			}
			return r
		}, t)
		return t
	}
	return strings.ReplaceAll(t, "|", "│")
}

func isTableDelimiter(t string) bool {
	seen := false
	for _, r := range t {
		switch r {
		case '|', ' ':
		case '-', ':':
			seen = true
		default:
			return false
		}
	}
	return seen
}

func tableStyle(t string, base StyleID) StyleID {
	if isTableDelimiter(t) {
		return styleDim
	}
	return base
}

// isNumberedMarker reports whether a splitBullet marker is "12. "-style.
func isNumberedMarker(m string) bool {
	m = strings.TrimLeft(m, " \t")
	return len(m) > 0 && m[0] >= '0' && m[0] <= '9'
}

// splitBullet splits "  - item" (or "12. item") into marker and rest;
// ok=false when the line is not a list item.
func splitBullet(line string) (marker, rest string, ok bool) {
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	t := line[indent:]
	for _, m := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(t, m) {
			return line[:indent+2], t[2:], true
		}
	}
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(t) && t[i] == '.' && t[i+1] == ' ' {
		return line[:indent+i+2], t[i+2:], true
	}
	return "", "", false
}

// openMarkdownView writes md to the state dir and opens the configured
// markdown viewer in a popup.
func openMarkdownView(a *app, prID int, md string) {
	path := filepath.Join(stateDir(), fmt.Sprintf("md-%d.md", prID))
	if err := os.WriteFile(path, []byte(md), 0o600); err != nil {
		a.status = "markdown ファイルを書けません: " + err.Error()
		return
	}
	_, err := a.herdr.pluginPaneOpen(pluginID, "mdview", "popup", true,
		map[string]string{envMDFile: path}, a.cfg.DifftoolWidth, a.cfg.DifftoolHeight)
	if err != nil {
		a.status = "markdown ビューアを開けません: " + err.Error()
	}
}

// prMarkdown renders the PR description as a standalone markdown document.
func prMarkdown(pr *PullRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# #%d %s\n\n", pr.ID, pr.Title)
	fmt.Fprintf(&b, "`%s` → `%s` — %s (%s)\n\n---\n\n",
		pr.Source.Branch.Name, pr.Destination.Branch.Name, pr.Author.Name(), pr.State)
	b.WriteString(pr.Description())
	b.WriteString("\n")
	return b.String()
}

// threadMarkdown renders one comment thread (root + replies) as markdown.
func threadMarkdown(t CommentThread) string {
	var b strings.Builder
	head := fmt.Sprintf("### %s (%s)", t.Root.User.Name(), t.Root.CreatedOn.Local().Format("2006-01-02 15:04"))
	if t.Root.Inline != nil {
		head += fmt.Sprintf(" — `%s` %s", t.Root.Inline.Path, t.lineLabel())
	}
	b.WriteString(head + "\n\n" + t.Root.Content.Raw + "\n")
	for _, r := range t.Replies {
		fmt.Fprintf(&b, "\n---\n\n### ↳ %s (%s)\n\n%s\n",
			r.User.Name(), r.CreatedOn.Local().Format("2006-01-02 15:04"), r.Content.Raw)
	}
	return b.String()
}

// runMDViewUI is the mdview pane's entrypoint: run the configured markdown
// viewer on the file, inheriting the popup PTY.
func runMDViewUI() {
	path := os.Getenv(envMDFile)
	if path == "" {
		errExit("missing " + envMDFile + "; launch me via the viewer")
	}
	if _, err := os.Stat(path); err != nil {
		errExit("markdown file missing:", err)
	}
	cfg := loadConfig()
	argv, hasFile := expandArgv(cfg.MarkdownViewer, "{file}", path)
	if len(argv) == 0 {
		errExit("markdown_viewer is empty")
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		showToolInstallHelp(cfg, argv[0], []string{
			"デフォルトの glow を使う場合:",
			"  brew install glow",
			"",
			"別のビューアを使う場合は config.toml で指定できます（{file} = markdownファイル）:",
			`  markdown_viewer = ["glow", "-p", "{file}"]`,
			`  markdown_viewer = ["nvim", "-R", "{file}"]`,
		})
		return
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if hasFile {
		cmd.Stdin = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			errExit("open markdown:", err)
		}
		defer f.Close()
		cmd.Stdin = f
	}
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		errExit("run markdown viewer:", err)
	}
}
