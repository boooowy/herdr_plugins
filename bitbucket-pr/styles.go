package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Visual styles for every element the viewer paints. Flush maps them to SGR
// sequences via sgrTable.
const (
	styleNone        StyleID = iota
	styleTitle               // screen title / PR title (bold)
	styleDim                 // separators, hints, muted meta
	styleAdd                 // diff added line
	styleDel                 // diff deleted line
	styleHunk                // @@ hunk headers
	styleMeta                // file headers, branch names
	styleCursor              // cursor row (inverse)
	styleTabActive           // active detail tab
	styleTabInactive         // inactive detail tab
	styleComment             // comment body text
	styleOutdated            // outdated/orphaned comment badge
	styleApproved            // approved reviewers / +stats
	styleChangesReq          // changes-requested reviewers / -stats
	styleError               // error banners
	styleAuthor              // comment/PR author names
	styleMDHead              // markdown headings
	styleMDCode              // markdown inline code + fenced blocks (code bg)
	styleMDLink              // markdown link text / bare URLs
	styleItalic              // markdown emphasis
	styleCodePad             // code-block padding (bg only, forms the rectangle)
	styleSynKeyword          // syntax: keywords
	styleSynString           // syntax: string literals
	styleSynComment          // syntax: comments
	styleSynNumber           // syntax: numeric literals
	styleSynFunc             // syntax: function/class names
)

// styleOnCommentBg is the StyleID offset for "same style, on the comment
// background": sgrTable derives a bg-tinted variant of every base style so
// comment areas read as blocks without border characters. Code styles keep
// their own (later, thus winning) background.
const styleOnCommentBg StyleID = 200

// styleOnFocusBg is the offset Screen.FocusRow adds to highlight the focused
// comment thread / the visual line selection: base styles (0-199) land at
// 400-599, comment-bg styles (200-399) at 600-799 — both render on focus_bg
// (which replaces the comment bg; a style's own bg still wins).
const styleOnFocusBg StyleID = 400

func onCmtBg(s StyleID) StyleID   { return s + styleOnCommentBg }
func onFocusBg(s StyleID) StyleID { return s + styleOnFocusBg }

// focusRow shifts every cell of a painted row onto the focus background —
// the focused-thread / line-selection highlight. (Lives here rather than on
// Screen: screen.go is a hint-copy snapshot.)
func focusRow(s *Screen, y, x, maxX int) {
	if y < 0 || y >= s.H {
		return
	}
	if maxX > s.W {
		maxX = s.W
	}
	for ; x < maxX; x++ {
		if c := s.at(x, y); c.Style < styleOnFocusBg {
			c.Style += styleOnFocusBg
		}
	}
}

// focusViewportRows applies focusRow to the viewport rows [start, end) that
// are currently visible inside rect.
func focusViewportRows(s *Screen, vp *Viewport, rect Rect, start, end int) {
	for i := start; i < end; i++ {
		if i < vp.Top || i >= vp.Top+rect.H {
			continue
		}
		focusRow(s, rect.Y+i-vp.Top, rect.X, rect.X+rect.W)
	}
}

// sgrTable builds the SGR sequence for each style from the user config,
// plus a comment-background variant of every style (see styleOnCommentBg).
func sgrTable(cfg Config) map[StyleID]string {
	codeBg := "\x1b[" + colorCode(cfg.MDCodeBg, true) + "m"
	m := map[StyleID]string{
		styleTitle:       "\x1b[1m",
		styleDim:         "\x1b[2m",
		styleAdd:         "\x1b[" + colorCode(cfg.AddFg, false) + "m",
		styleDel:         "\x1b[" + colorCode(cfg.DelFg, false) + "m",
		styleHunk:        "\x1b[" + colorCode(cfg.HunkFg, false) + "m",
		styleMeta:        "\x1b[36m",
		styleCursor:      "\x1b[7m",
		styleTabActive:   "\x1b[1;4m",
		styleTabInactive: "\x1b[2m",
		styleComment:     "\x1b[" + colorCode(cfg.CommentFg, false) + "m",
		styleOutdated:    "\x1b[1;" + colorCode(cfg.OutdatedFg, false) + "m",
		styleApproved:    "\x1b[" + colorCode(cfg.AddFg, false) + "m",
		styleChangesReq:  "\x1b[" + colorCode(cfg.DelFg, false) + "m",
		styleError:       "\x1b[1;31m",
		styleAuthor:      "\x1b[1;34m",
		styleMDHead:      "\x1b[1;" + colorCode(cfg.MDHeadingFg, false) + "m",
		styleMDCode:      codeBg + "\x1b[" + colorCode(cfg.MDCodeFg, false) + "m",
		styleMDLink:      "\x1b[4;36m",
		styleItalic:      "\x1b[3m",
		styleCodePad:     codeBg,
		styleSynKeyword:  codeBg + "\x1b[35m",
		styleSynString:   codeBg + "\x1b[32m",
		styleSynComment:  codeBg + "\x1b[3;90m",
		styleSynNumber:   codeBg + "\x1b[38;5;215m",
		styleSynFunc:     codeBg + "\x1b[34m",
	}
	// Comment-area and focus variants: the area bg comes first so a style's
	// own bg (code blocks) still wins where present. Focus replaces the
	// comment bg (600s) and also tints plain rows (400s, diff-line selection).
	cmtBg := "\x1b[" + colorCode(cfg.CommentBg, true) + "m"
	focusBg := "\x1b[" + colorCode(cfg.FocusBg, true) + "m"
	m[onCmtBg(styleNone)] = cmtBg
	m[onFocusBg(styleNone)] = focusBg
	m[onFocusBg(onCmtBg(styleNone))] = focusBg
	for id, seq := range m {
		if id < styleOnCommentBg {
			m[onCmtBg(id)] = cmtBg + seq
			m[onFocusBg(id)] = focusBg + seq
			m[onFocusBg(onCmtBg(id))] = focusBg + seq
		}
	}
	return m
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
