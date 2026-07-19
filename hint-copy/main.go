// hint-copy is a herdr plugin that copies strings visible on screen (URLs,
// paths, IP addresses, git SHAs, ...) with tmux-thumbs style hint labels,
// keyboard only. It has two entrypoints wired up in herdr-plugin.toml:
//
//	hint-copy action  — invoked by a keybinding; opens the overlay pane
//	hint-copy ui      — runs inside the overlay pane; renders hints, copies
package main

import (
	"fmt"
	"os"
)

const version = "0.3.0"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "action":
		runAction()
	case "ui":
		runUI()
	case "version":
		fmt.Println("hint-copy " + version)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: hint-copy <action|ui|version>")
	os.Exit(2)
}

// errExit logs a fatal error to stderr (captured by herdr plugin logs) and
// exits non-zero.
func errExit(args ...any) {
	fmt.Fprintln(os.Stderr, args...)
	os.Exit(1)
}
