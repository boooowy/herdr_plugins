package main

// modalLine is one logical row inside an in-app modal. Long rows are wrapped
// at terminal-cell boundaries while preserving their styles.
type modalLine struct {
	Spans  []Span
	Center bool
	// NoWrap marks a row that a caller has already laid out for the modal's
	// content width. Help uses it to keep its key and description columns from
	// being wrapped a second time near a width boundary.
	NoWrap bool
}

type modalSpec struct {
	Title          string
	Lines          []modalLine
	Footer         modalLine
	PreferredWidth int
	Top            int
}

type modalPaintResult struct {
	Rect        Rect
	Top         int
	PageRows    int
	ContentRows int
}

type modalProvider interface {
	modal(*app) *modalSpec
}

// paintOverlay is deliberately the last painter in a frame. This gives the
// modal ownership of its rectangle without requiring a Herdr popup or pane.
func paintOverlay(a *app, s *Screen) {
	if a.help != nil {
		paintHelpOverlay(a, s)
		return
	}
	if provider, ok := a.top().(modalProvider); ok {
		if spec := provider.modal(a); spec != nil {
			paintModal(s, *spec)
		}
	}
}

func modalWidth(screenWidth, preferred int) int {
	if screenWidth <= 0 {
		return 0
	}
	maxWidth := screenWidth - 4
	if maxWidth < 4 {
		maxWidth = screenWidth
	}
	if preferred <= 0 || preferred > maxWidth {
		preferred = maxWidth
	}
	if preferred < 4 {
		preferred = min(screenWidth, 4)
	}
	return preferred
}

func modalContentWidth(screenWidth, preferred int) int {
	return max(1, modalWidth(screenWidth, preferred)-4)
}

func paintModal(s *Screen, spec modalSpec) modalPaintResult {
	if s.W < 4 || s.H < 3 {
		return modalPaintResult{}
	}
	w := modalWidth(s.W, spec.PreferredWidth)
	contentWidth := max(1, w-4)
	lines := wrapModalLines(spec.Lines, contentWidth)

	footerRows := 0
	if len(spec.Footer.Spans) > 0 {
		footerRows = 1
	}
	desiredHeight := 2 + len(lines) + footerRows
	maxHeight := s.H - 4
	if maxHeight < 3 {
		maxHeight = s.H
	}
	h := min(desiredHeight, maxHeight)
	if h < 3 {
		h = 3
	}
	x, y := (s.W-w)/2, (s.H-h)/2
	r := Rect{X: x, Y: y, W: w, H: h}
	pageRows := max(0, h-2-footerRows)
	maxTop := max(0, len(lines)-pageRows)
	top := spec.Top
	if top < 0 {
		top = 0
	}
	if top > maxTop {
		top = maxTop
	}

	s.HideAvatars(r)
	fillRect(s, r, onFocusBg(styleNone))
	for i := 0; i < pageRows && top+i < len(lines); i++ {
		paintModalLine(s, r.X+2, r.Y+1+i, contentWidth, lines[top+i])
	}
	if footerRows > 0 {
		paintModalLine(s, r.X+2, r.Y+r.H-2, contentWidth, spec.Footer)
	}
	// Paint the frame last. Even if a future content painter gets its clipping
	// wrong, it cannot replace either vertical edge of the modal.
	paintModalBorder(s, r, spec.Title)
	return modalPaintResult{Rect: r, Top: top, PageRows: pageRows, ContentRows: len(lines)}
}

func wrapModalLines(lines []modalLine, width int) []modalLine {
	var out []modalLine
	for _, line := range lines {
		if line.NoWrap {
			out = append(out, line)
			continue
		}
		wrapped := wrapSpans(line.Spans, width)
		for _, spans := range wrapped {
			out = append(out, modalLine{Spans: spans, Center: line.Center})
		}
	}
	return out
}

func fillRect(s *Screen, r Rect, style StyleID) {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			s.Set(x, y, ' ', style)
		}
	}
}

func paintModalBorder(s *Screen, r Rect, title string) {
	style := onFocusBg(styleDim)
	for x := r.X + 1; x < r.X+r.W-1; x++ {
		s.Set(x, r.Y, '─', style)
		s.Set(x, r.Y+r.H-1, '─', style)
	}
	for y := r.Y + 1; y < r.Y+r.H-1; y++ {
		s.Set(r.X, y, '│', style)
		s.Set(r.X+r.W-1, y, '│', style)
	}
	s.Set(r.X, r.Y, '┌', style)
	s.Set(r.X+r.W-1, r.Y, '┐', style)
	s.Set(r.X, r.Y+r.H-1, '└', style)
	s.Set(r.X+r.W-1, r.Y+r.H-1, '┘', style)
	if title != "" && r.W > 6 {
		s.WriteString(r.X+2, r.Y, " "+truncateWidth(title, r.W-6)+" ", onFocusBg(styleTitle), r.X+r.W-2)
	}
}

func paintModalLine(s *Screen, x, y, width int, line modalLine) {
	maxX := x + width
	if line.Center {
		lineWidth := 0
		for _, span := range line.Spans {
			lineWidth += displayWidth(span.Text)
		}
		if lineWidth < width {
			x += (width - lineWidth) / 2
		}
	}
	for _, span := range line.Spans {
		x = s.WriteString(x, y, span.Text, modalStyle(span.Style), maxX)
	}
}

func modalStyle(style StyleID) StyleID {
	if style >= styleOnFocusBg {
		return style
	}
	return onFocusBg(style)
}
