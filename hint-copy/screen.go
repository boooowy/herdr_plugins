package main

import (
	"io"
	"strings"

	"github.com/mattn/go-runewidth"
)

// StyleID names a visual style; Flush maps it to an SGR sequence via
// sgrTable (built-ins) or the screen's styleTable (dynamic, interned from
// ANSI captures). Painters set styles per cell instead of emitting escapes.
type StyleID uint16

const (
	styleNone StyleID = iota
	styleHint
	styleDim
	styleMatch
	styleInverse
	styleBorder
	styleSel
	styleCursor
	styleSearch
)

// sgrTable builds the SGR sequence for each style from the user config.
func sgrTable(cfg Config) map[StyleID]string {
	return map[StyleID]string{
		styleHint:    "\x1b[1;" + colorCode(cfg.HintFg, false) + ";" + colorCode(cfg.HintBg, true) + "m",
		styleDim:     "\x1b[2m",
		styleMatch:   "\x1b[" + colorCode(cfg.MatchFg, false) + "m",
		styleInverse: "\x1b[7m",
		styleBorder:  "\x1b[2m",
		styleSel:     "\x1b[" + colorCode(cfg.SelBg, true) + "m",
		styleCursor:  "\x1b[7m",
		styleSearch:  "\x1b[" + colorCode(cfg.SearchBg, true) + "m",
	}
}

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
// the grid into one terminal frame with the same framing the streaming
// renderers used (home cursor, per-row erase-to-eol, no scroll).
type Screen struct {
	W, H   int
	cells  []Cell
	styles *styleTable // resolves dynamic StyleIDs; nil for built-ins only
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

// SetStyle restyles a cell in place (cursor, selection, search highlights).
func (s *Screen) SetStyle(x, y int, st StyleID) {
	if x < 0 || y < 0 || x >= s.W || y >= s.H {
		return
	}
	s.at(x, y).Style = st
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

// Clone deep-copies the screen; composite mode clones the static base every
// frame and paints the dynamic parts on top.
func (s *Screen) Clone() *Screen {
	c := &Screen{W: s.W, H: s.H, cells: make([]Cell, len(s.cells)), styles: s.styles}
	copy(c.cells, s.cells)
	return c
}

// PutCells copies pre-styled cells (from an ANSI capture) into row y starting
// at x, clipped to maxX. A wide rune whose continuation would cross maxX is
// dropped.
func (s *Screen) PutCells(x, y int, cells []Cell, maxX int) {
	if y < 0 || y >= s.H {
		return
	}
	if maxX > s.W {
		maxX = s.W
	}
	for i := 0; i < len(cells) && x < maxX; i++ {
		c := cells[i]
		if c.R == 0 {
			continue // continuations re-emitted below
		}
		wdt := runewidth.RuneWidth(c.R)
		if x+wdt > maxX {
			return
		}
		*s.at(x, y) = c
		if wdt == 2 {
			*s.at(x+1, y) = Cell{R: 0, Style: c.Style}
		}
		x += wdt
	}
}

// Flush emits the frame: home cursor, then each row's content followed by
// erase-to-eol; every row but the last ends with \r\n. Trailing blank
// (space, unstyled) cells are trimmed per row — the erase-to-eol repaints
// them — which keeps output identical to the previous streaming renderers.
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
				} else if c.Style >= dynStyleBase && s.styles != nil {
					sb.WriteString(s.styles.sequence(c.Style))
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
