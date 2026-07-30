package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Markdown support, two-tier: mdStyledLines gives descriptions and comments
// lightweight in-TUI styling, and the `m` key hands the raw markdown to the
// user's own renderer (config `markdown_viewer`, glow by default) in a herdr
// popup — the same pattern as the external diff tool.

const envMDFile = "BBPR_MD_FILE"

// mdStyledLines renders markdown source as styled, wrapped display lines:
// headings, fenced code, quotes, and list bullets get colors; body text
// keeps base. Deliberately line-based — enough to scan a PR description
// without a real renderer.
func mdStyledLines(text string, width int, base StyleID) [][]Span {
	if width < 10 {
		width = 10
	}
	var out [][]Span
	inFence := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		style := base
		switch {
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			inFence = !inFence
			style = styleMeta
		case inFence:
			style = styleMeta
		case strings.HasPrefix(trimmed, "#"):
			style = styleTitle
		case strings.HasPrefix(trimmed, ">"):
			style = styleDim
		default:
			if marker, rest, ok := splitBullet(line); ok {
				wrapped := wrapText(rest, width-displayWidth(marker))
				if len(wrapped) == 0 {
					wrapped = []string{""}
				}
				out = append(out, []Span{{marker, styleHunk}, {wrapped[0], base}})
				pad := strings.Repeat(" ", displayWidth(marker))
				for _, w := range wrapped[1:] {
					out = append(out, []Span{{pad + w, base}})
				}
				continue
			}
		}
		for _, w := range wrapText(line, width) {
			out = append(out, []Span{{w, style}})
		}
	}
	return out
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
