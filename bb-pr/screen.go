// Snapshot of hint-copy/screen.go as of 2029b96 (adapted: dropped the
// dynamic styleTable/PutCells used for ANSI captures — bb-pr paints all
// content with its own styles). Do not import hint-copy — it is being
// refactored independently.
package main

import (
	"io"
	"strings"

	"github.com/mattn/go-runewidth"
)

// StyleID names a visual style; Flush maps it to an SGR sequence via the
// table built in styles.go. Painters set styles per cell instead of emitting
// escapes.
type StyleID uint16

// Cell is one terminal cell. R == 0 marks the continuation cell of a
// preceding wide (CJK) rune and is skipped on flush.
type Cell struct {
	R     rune
	Style StyleID
}

// Rect is a paint region in screen cells.
type Rect struct {
	X, Y, W, H int
}

// Screen is a cell-grid frame buffer. All painters write cells; Flush turns
// the grid into one terminal frame (home cursor, per-row erase-to-eol, no
// scroll).
type Screen struct {
	W, H  int
	cells []Cell
}

func NewScreen(w, h int) *Screen {
	s := &Screen{W: w, H: h, cells: make([]Cell, w*h)}
	for i := range s.cells {
		s.cells[i].R = ' '
	}
	return s
}

func (s *Screen) at(x, y int) *Cell { return &s.cells[y*s.W+x] }

// Set places one rune. Wide runes claim the following cell as continuation.
func (s *Screen) Set(x, y int, r rune, st StyleID) {
	if x < 0 || y < 0 || x >= s.W || y >= s.H {
		return
	}
	wdt := runewidth.RuneWidth(r)
	if wdt == 2 && x+1 >= s.W {
		return // wide rune would be cut in half at the edge
	}
	*s.at(x, y) = Cell{R: r, Style: st}
	if wdt == 2 {
		*s.at(x+1, y) = Cell{R: 0, Style: st}
	}
}

// SetStyle restyles a cell in place (cursor row highlight).
func (s *Screen) SetStyle(x, y int, st StyleID) {
	if x < 0 || y < 0 || x >= s.W || y >= s.H {
		return
	}
	s.at(x, y).Style = st
}

// StyleRow restyles every cell of row y between x and maxX (exclusive) that
// still has the plain style — used for the cursor-row highlight without
// clobbering colored diff markers.
func (s *Screen) StyleRow(y, x, maxX int, from, to StyleID) {
	if y < 0 || y >= s.H {
		return
	}
	if maxX > s.W {
		maxX = s.W
	}
	for ; x < maxX; x++ {
		if c := s.at(x, y); c.Style == from {
			c.Style = to
		}
	}
}

// WriteString writes text at (x, y) clipped to maxX (exclusive), skipping \r,
// and returns the column after the last written cell.
func (s *Screen) WriteString(x, y int, text string, st StyleID, maxX int) int {
	if maxX > s.W {
		maxX = s.W
	}
	for _, r := range text {
		if r == '\r' {
			continue
		}
		wdt := runewidth.RuneWidth(r)
		if x+wdt > maxX {
			break
		}
		s.Set(x, y, r, st)
		x += wdt
	}
	return x
}

// Flush emits the frame: home cursor, then each row's content followed by
// erase-to-eol; every row but the last ends with \r\n. Trailing blank
// (space, unstyled) cells are trimmed per row — the erase-to-eol repaints
// them.
func (s *Screen) Flush(w io.Writer, sgr map[StyleID]string) {
	var sb strings.Builder
	sb.WriteString("\x1b[H")
	for y := 0; y < s.H; y++ {
		last := -1
		for x := 0; x < s.W; x++ {
			c := s.at(x, y)
			if c.R != 0 && !(c.R == ' ' && c.Style == styleNone) {
				last = x
			}
		}
		cur := styleNone
		for x := 0; x <= last; x++ {
			c := s.at(x, y)
			if c.R == 0 {
				continue // wide-rune continuation
			}
			if c.Style != cur {
				sb.WriteString("\x1b[0m")
				if code, ok := sgr[c.Style]; ok && c.Style != styleNone {
					sb.WriteString(code)
				}
				cur = c.Style
			}
			sb.WriteRune(c.R)
		}
		if cur != styleNone {
			sb.WriteString("\x1b[0m")
		}
		sb.WriteString("\x1b[K")
		if y < s.H-1 {
			sb.WriteString("\r\n")
		}
	}
	io.WriteString(w, sb.String())
}
