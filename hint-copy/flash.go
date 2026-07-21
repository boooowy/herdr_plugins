package main

import (
	"bufio"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// flashAndDwell paints one final frame with the copied region highlighted in
// styleFlash and holds it briefly, so a copy is confirmed on-screen regardless
// of how herdr's toast delivery is configured. ms <= 0 skips the flash.
func flashAndDwell(out *bufio.Writer, sgr map[StyleID]string, ms int, frame *Screen) {
	if ms <= 0 || frame == nil {
		return
	}
	frame.Flush(out, sgr)
	out.Flush()
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

// flashFrameToken builds the token-mode flash frame: the real pane content
// with every visible occurrence of the copied string lit in styleFlash. The
// span walk mirrors PaintTokenOverlay so wide/CJK runes light the right cells.
func flashFrameToken(v *uiView, picked string) *Screen {
	f := v.base.Clone()
	r := v.paintRect // match the rect the base content was painted at
	lines := strings.Split(v.text, "\n")
	offset := 0
	if len(lines) > r.H {
		offset = len(lines) - r.H
	}
	maxX := r.X + r.W
	for _, c := range v.cands {
		if c.Text != picked {
			continue
		}
		if c.Line < offset || c.Line-offset >= r.H || c.Line >= len(lines) {
			continue
		}
		y := r.Y + c.Line - offset
		runes := []rune(lines[c.Line])
		x := r.X
		for i := 0; i < c.StartRune && i < len(runes); i++ {
			x += runewidth.RuneWidth(runes[i])
		}
		for i := c.StartRune; i < c.EndRune && i < len(runes); i++ {
			wdt := runewidth.RuneWidth(runes[i])
			if x+wdt > maxX {
				break
			}
			f.SetStyle(x, y, styleFlash)
			if wdt == 2 {
				f.SetStyle(x+1, y, styleFlash)
			}
			x += wdt
		}
	}
	return f
}

// flashFrameLine builds the line-mode flash frame: the styled content drawn as
// usual, then the copied line range repainted in styleFlash across the content
// width (the same [margin, maxX) span the selection highlight covers).
func flashFrameLine(v *uiView, anchor, cursor int) *Screen {
	f := v.base.Clone()
	clearRect(f, v.content)
	PaintLineModeStyled(f, v.content, v.targetStyled, v.ll, "", anchor, cursor)
	lo, hi := selRange(anchor, cursor)
	offset := 0
	if len(v.targetStyled) > v.content.H {
		offset = len(v.targetStyled) - v.content.H
	}
	margin := 0
	if v.ll.Width() > 0 {
		margin = v.ll.Width() + 1
	}
	maxX := v.content.X + v.content.W
	for li := 0; li < v.content.H && offset+li < len(v.targetStyled); li++ {
		abs := offset + li
		if abs < lo || abs > hi {
			continue
		}
		y := v.content.Y + li
		for ix := v.content.X + margin; ix < maxX; ix++ {
			f.SetStyle(ix, y, styleFlash)
		}
	}
	return f
}

// flashFrameCopy builds the copy-mode flash frame: the copy-mode viewport
// drawn as usual, then the yanked selection repainted in styleFlash.
func flashFrameCopy(v *uiView, cs *CopyState, styled [][]Cell) *Screen {
	f := v.base.Clone()
	clearRect(f, v.content)
	PaintCopyMode(f, v.content, cs, styled)
	if cs.Sel == nil {
		return f
	}
	a, b := cs.Sel.Anchor, cs.Cur
	if a.Line > b.Line || (a.Line == b.Line && a.Col > b.Col) {
		a, b = b, a
	}
	for li := a.Line; li <= b.Line; li++ {
		c1, c2 := 0, len(cs.Buf[li])-1
		if !cs.Sel.Linewise {
			if li == a.Line {
				c1 = a.Col
			}
			if li == b.Line {
				c2 = b.Col
			}
		}
		styleSpan(f, v.content, cs, li, c1, c2, styleFlash)
	}
	return f
}
