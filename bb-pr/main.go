// bb-pr is a herdr plugin: a read-only Bitbucket Cloud pull-request viewer.
// It shows the PR list for the current repo, PR details, a hunk-by-hunk diff,
// and review comments — all inside a herdr pane. Entrypoints wired up in
// herdr-plugin.toml:
//
//	bb-pr action — invoked by the palette/keybinding or a ctrl-clicked PR URL;
//	               resolves the repo context and opens the viewer pane
//	bb-pr ui     — runs inside the viewer pane; renders the TUI
package main

import (
	"fmt"
	"os"
)

const version = "0.9.0"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "action":
		runAction()
	case "ui":
		runUI()
	case "difftool-ui":
		runDiffToolUI()
	case "comment-ui":
		runCommentUI()
	case "dump": // dev helper: print raw API responses to stdout
		runDump(os.Args[2:])
	case "version":
		fmt.Println("bb-pr " + version)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: bb-pr <action|ui|difftool-ui|comment-ui|dump|version>")
	os.Exit(2)
}

// errExit logs a fatal error to stderr (captured by herdr plugin logs) and
// exits non-zero.
func errExit(args ...any) {
	fmt.Fprintln(os.Stderr, args...)
	os.Exit(1)
}
