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

const version = "0.8.0"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "action":
		mode := ""
		if len(os.Args) > 2 {
			mode = os.Args[2]
		}
		runAction(mode)
	case "ui":
		runUI()
	case "frame": // dev helper: print one composite frame
		runFrame(os.Args[2:])
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
