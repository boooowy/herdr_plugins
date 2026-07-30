package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Visual styles for every element the viewer paints. Flush maps them to SGR
// sequences via sgrTable.
const (
	styleNone          StyleID = iota
	styleTitle                 // screen title / PR title (bold)
	styleDim                   // separators, hints, muted meta
	styleAdd                   // diff added line
	styleDel                   // diff deleted line
	styleHunk                  // @@ hunk headers
	styleMeta                  // file headers, branch names
	styleCursor                // cursor row (inverse)
	styleTabActive             // active detail tab
	styleTabInactive           // inactive detail tab
	styleComment               // comment body text
	styleCommentBorder         // comment thread box borders
	styleOutdated              // outdated/orphaned comment badge
	styleApproved              // approved reviewers / +stats
	styleChangesReq            // changes-requested reviewers / -stats
	styleError                 // error banners
	styleAuthor                // comment/PR author names
)

// sgrTable builds the SGR sequence for each style from the user config.
func sgrTable(cfg Config) map[StyleID]string {
	return map[StyleID]string{
		styleTitle:         "\x1b[1m",
		styleDim:           "\x1b[2m",
		styleAdd:           "\x1b[" + colorCode(cfg.AddFg, false) + "m",
		styleDel:           "\x1b[" + colorCode(cfg.DelFg, false) + "m",
		styleHunk:          "\x1b[" + colorCode(cfg.HunkFg, false) + "m",
		styleMeta:          "\x1b[36m",
		styleCursor:        "\x1b[7m",
		styleTabActive:     "\x1b[1;4m",
		styleTabInactive:   "\x1b[2m",
		styleComment:       "\x1b[" + colorCode(cfg.CommentFg, false) + "m",
		styleCommentBorder: "\x1b[" + colorCode(cfg.CommentBorderFg, false) + "m",
		styleOutdated:      "\x1b[1;" + colorCode(cfg.OutdatedFg, false) + "m",
		styleApproved:      "\x1b[" + colorCode(cfg.AddFg, false) + "m",
		styleChangesReq:    "\x1b[" + colorCode(cfg.DelFg, false) + "m",
		styleError:         "\x1b[1;31m",
		styleAuthor:        "\x1b[1;34m",
	}
}

// colorCode maps a color name, a 0-255 number, or a #rrggbb hex value to an
// SGR parameter. (Snapshot of hint-copy/render.go colorCode as of 2029b96.)
func colorCode(name string, bg bool) string {
	if len(name) == 7 && name[0] == '#' {
		if v, err := strconv.ParseUint(name[1:], 16, 32); err == nil {
			base := "38"
			if bg {
				base = "48"
			}
			return fmt.Sprintf("%s;2;%d;%d;%d", base, (v>>16)&0xff, (v>>8)&0xff, v&0xff)
		}
	}
	named := map[string]int{
		"black": 30, "red": 31, "green": 32, "yellow": 33,
		"blue": 34, "magenta": 35, "cyan": 36, "white": 37,
		"bright-black": 90, "gray": 90, "bright-red": 91, "bright-green": 92,
		"bright-yellow": 93, "bright-blue": 94, "bright-magenta": 95,
		"bright-cyan": 96, "bright-white": 97,
	}
	if code, ok := named[strings.ToLower(name)]; ok {
		if bg {
			code += 10
		}
		return strconv.Itoa(code)
	}
	if n, err := strconv.Atoi(name); err == nil && n >= 0 && n <= 255 {
		if bg {
			return "48;5;" + strconv.Itoa(n)
		}
		return "38;5;" + strconv.Itoa(n)
	}
	// Unknown name: default foreground / background.
	if bg {
		return "49"
	}
	return "39"
}
