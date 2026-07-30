package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// relTime renders a compact relative timestamp ("3分前", "2時間前", "5日前").
func relTime(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "たった今"
	case d < time.Hour:
		return fmt.Sprintf("%d分前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d時間前", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d日前", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// truncateWidth cuts s so its display width fits w cells, appending "…"
// when something was cut.
func truncateWidth(s string, w int) string {
	if runewidth.StringWidth(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	return runewidth.Truncate(s, w-1, "") + "…"
}

// wrapText soft-wraps s to the given display width, splitting long runs at
// cell boundaries (CJK aware). Newlines in the input are preserved.
func wrapText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if line == "" {
			out = append(out, "")
			continue
		}
		cur, curW := strings.Builder{}, 0
		for _, r := range line {
			rw := runewidth.RuneWidth(r)
			if curW+rw > width {
				out = append(out, cur.String())
				cur.Reset()
				curW = 0
			}
			cur.WriteRune(r)
			curW += rw
		}
		if cur.Len() > 0 {
			out = append(out, cur.String())
		}
	}
	return out
}

// displayWidth is the string's width in terminal cells (CJK aware).
func displayWidth(s string) int { return runewidth.StringWidth(s) }

// padRight pads s with spaces to exactly w display cells (truncating when
// longer).
func padRight(s string, w int) string {
	sw := runewidth.StringWidth(s)
	if sw > w {
		return truncateWidth(s, w)
	}
	return s + strings.Repeat(" ", w-sw)
}
