package main

import (
	"fmt"
	"strings"
	"time"
)

// hunkRef identifies the hunk a diff row belongs to (viewport Item).
type hunkRef struct{ hunk int }

// escInterceptor lets a view consume q/Esc instead of the app popping it —
// the diff view uses it to cancel an active line selection.
type escInterceptor interface{ interceptEsc(a *app) bool }

func (v *diffView) interceptEsc(*app) bool {
	if v.selAnchor < 0 {
		return false
	}
	v.selAnchor = -1
	return true
}

// diffView shows one file's diff hunk by hunk, with inline comment threads
// boxed under their anchor lines and orphaned (outdated/unmatched) threads
// collected at the end.
type diffView struct {
	prID    int
	fileIdx int // index into detail.diffstat
	vp      Viewport
	folded  map[int]bool
	showCmt bool
	wrap    bool // long diff lines: wrapped vs clipped (w key)
	pending rune // first key of a two-key sequence (]h, [h, ]f, [f, za, zA)

	// selAnchor is the row where v/V started a line selection (-1 = none);
	// the selection runs from there to the cursor. Row indices go stale on
	// rebuild, so any rebuild clears it.
	selAnchor int

	// threadFor/threadSpan mirror detailView's: reply-target lookup and the
	// focused-thread highlight range per comment root ID.
	threadFor  map[int]CommentThread
	threadSpan map[int][2]int

	// pendingJump is a comment root ID to scroll to once its row exists
	// (set when the Comments tab jumps into the diff); 0 = none.
	pendingJump int
}

func newDiffView(a *app, prID, fileIdx int) *diffView {
	v := &diffView{
		prID:      prID,
		fileIdx:   fileIdx,
		folded:    map[int]bool{},
		showCmt:   a.cfg.ShowComments,
		selAnchor: -1,
	}
	if a.cfg.ContextFold {
		v.foldAll(a, true)
	}
	v.rebuild(a)
	return v
}

// fileDiff resolves the FileDiff for the current diffstat entry (nil when
// the API diff omitted it — binary or truncated).
func (v *diffView) fileDiff(a *app) (*prDetail, *DiffStatEntry, *FileDiff) {
	d := a.detailFor(v.prID)
	if v.fileIdx < 0 || v.fileIdx >= len(d.diffstat) {
		return d, nil, nil
	}
	st := &d.diffstat[v.fileIdx]
	for i := range d.files {
		f := &d.files[i]
		if f.Path() == st.Path() || (st.OldPath() != "" && f.Path() == st.OldPath()) {
			return d, st, f
		}
	}
	return d, st, nil
}

func (v *diffView) foldAll(a *app, fold bool) {
	_, _, f := v.fileDiff(a)
	if f == nil {
		return
	}
	for i := range f.Hunks {
		v.folded[i] = fold
	}
}

// threadsFor collects the file's inline comment threads and precomputes
// which (hunk, line) each one anchors to; unanchored ones are orphans.
func (v *diffView) threadsFor(a *app, f *FileDiff, st *DiffStatEntry) (map[[2]int][]CommentThread, []CommentThread) {
	d := a.detailFor(v.prID)
	byFile := inlineThreadsByFile(d.comments)
	var threads []CommentThread
	seen := map[int]bool{}
	paths := []string{}
	if st != nil {
		paths = append(paths, st.Path())
		if st.OldPath() != "" && st.OldPath() != st.Path() {
			paths = append(paths, st.OldPath())
		}
	}
	for _, p := range paths {
		for _, t := range byFile[p] {
			if !seen[t.Root.ID] {
				seen[t.Root.ID] = true
				threads = append(threads, t)
			}
		}
	}

	return anchorThreads(f, threads)
}

// anchorThreads maps each thread onto the (hunk, line) it anchors to;
// threads with no matching line (outdated, truncated diff) come back as
// orphans.
func anchorThreads(f *FileDiff, threads []CommentThread) (map[[2]int][]CommentThread, []CommentThread) {
	anchored := map[[2]int][]CommentThread{}
	var orphans []CommentThread
	for _, t := range threads {
		found := false
		if f != nil {
			for hi := range f.Hunks {
				for li, l := range f.Hunks[hi].Lines {
					if t.matchesLine(l) {
						anchored[[2]int{hi, li}] = append(anchored[[2]int{hi, li}], t)
						found = true
						break
					}
				}
				if found {
					break
				}
			}
		}
		if !found {
			orphans = append(orphans, t)
		}
	}
	return anchored, orphans
}

func (v *diffView) rebuild(a *app) {
	d, st, f := v.fileDiff(a)
	var rows []Row
	now := time.Now()
	v.selAnchor = -1 // row indices go stale
	v.threadFor = map[int]CommentThread{}
	v.threadSpan = map[int][2]int{}
	addThread := func(t CommentThread) {
		v.threadFor[t.Root.ID] = t
		start := len(rows)
		rows = append(rows, threadRows(t, a.w, now, t.lineLabel())...)
		v.threadSpan[t.Root.ID] = [2]int{start, len(rows) - 1}
	}

	if st == nil {
		rows = append(rows, textRow(Span{" (ファイルがありません)", styleDim}))
		v.vp.Reset(rows)
		return
	}
	if f == nil {
		msg := " (このファイルの diff は API から取得できません — 切り詰めの可能性)"
		if d.diffText == "" {
			msg = " (diff 読み込み中…)"
		}
		rows = append(rows, textRow(Span{msg, styleDim}))
		v.vp.Reset(rows)
		return
	}
	if f.IsBinary {
		rows = append(rows, textRow(Span{" (バイナリファイル)", styleDim}))
	}
	if len(f.Hunks) == 0 && !f.IsBinary {
		rows = append(rows, textRow(Span{" (テキスト変更なし — rename / mode change のみ)", styleDim}))
	}

	anchored, orphans := v.threadsFor(a, f, st)

	numW := 4
	for hi := range f.Hunks {
		h := &f.Hunks[hi]
		// Hunk header: ─── @@ -a,b +c,d @@ section ─── [hunk i/n] ───
		head := fmt.Sprintf("─── @@ -%d,%d +%d,%d @@ %s ", h.OldStart, h.OldCount, h.NewStart, h.NewCount,
			truncateWidth(h.Section, a.w/2))
		tail := fmt.Sprintf(" [hunk %d/%d]", hi+1, len(f.Hunks))
		if v.folded[hi] {
			tail += fmt.Sprintf(" [+%d lines]", len(h.Lines))
		}
		if n := hunkCommentCount(anchored, hi); n > 0 {
			tail += fmt.Sprintf(" 💬%d", n)
		}
		pad := a.w - displayWidth(head) - displayWidth(tail) - 1
		if pad < 0 {
			pad = 0
		}
		rows = append(rows, row(RowHunk, hunkRef{hi}, true,
			Span{head + strings.Repeat("─", pad), styleHunk},
			Span{tail, styleHunk},
		))
		if v.folded[hi] {
			continue
		}
		for li, l := range h.Lines {
			rows = append(rows, diffLineRows(l, numW, a.w, v.wrap)...)
			if v.showCmt {
				for _, t := range anchored[[2]int{hi, li}] {
					addThread(t)
				}
			}
		}
	}

	if v.showCmt && len(orphans) > 0 {
		rows = append(rows, textRow(), textRow(
			Span{"── Orphaned comments (outdated / 現在の diff に対応なし) ", styleOutdated},
			Span{strings.Repeat("─", max(0, a.w-52)), styleDim}))
		for _, t := range orphans {
			addThread(t)
		}
	}

	v.vp.Reset(rows)
}

// diffLineRows renders one diff body line: old/new line numbers, the marker,
// and the content in the line-kind color. The row's Item carries the
// DiffLine so `c` can anchor an inline comment to it. With wrap, overlong
// content continues on gutter-indented, non-selectable rows.
func diffLineRows(l DiffLine, numW, width int, wrap bool) []Row {
	oldNo, newNo := "", ""
	if l.OldNo > 0 {
		oldNo = fmt.Sprintf("%*d", numW, l.OldNo)
	} else {
		oldNo = strings.Repeat(" ", numW)
	}
	if l.NewNo > 0 {
		newNo = fmt.Sprintf("%*d", numW, l.NewNo)
	} else {
		newNo = strings.Repeat(" ", numW)
	}
	marker, style := " ", styleNone
	switch l.Kind {
	case LineAdd:
		marker, style = "+", styleAdd
	case LineDel:
		marker, style = "-", styleDel
	case LineNoNewline:
		marker, style = `\`, styleDim
	}
	text := strings.ReplaceAll(l.Text, "\t", "    ")
	gutterW := numW*2 + 4 // "OLD NEW " + "M "
	first := func(content string) Row {
		return row(RowDiffLine, l, true,
			Span{oldNo + " " + newNo + " ", styleDim},
			Span{marker + " ", style},
			Span{content, style},
		)
	}
	if !wrap || displayWidth(text) <= width-gutterW {
		return []Row{first(text)}
	}
	chunks := wrapText(text, max(width-gutterW, 10))
	rows := []Row{first(chunks[0])}
	pad := strings.Repeat(" ", gutterW)
	for _, c := range chunks[1:] {
		rows = append(rows, Row{Kind: RowDiffLine, Spans: []Span{{pad, styleNone}, {c, style}}})
	}
	return rows
}

func hunkCommentCount(anchored map[[2]int][]CommentThread, hi int) int {
	n := 0
	for key, ts := range anchored {
		if key[0] == hi {
			n += len(ts)
		}
	}
	return n
}

// tryPendingJump moves the cursor to the pendingJump comment row. It needs
// a real viewport height (Paint sets vp.H), so render passes the frame's
// height explicitly before painting.
func (v *diffView) tryPendingJump(h int) {
	if v.pendingJump == 0 {
		return
	}
	for i, r := range v.vp.Rows {
		if r.Kind == RowComment && r.Selectable {
			if id, ok := r.Item.(int); ok && id == v.pendingJump {
				v.vp.Cursor = i
				if h > 0 {
					v.vp.H = h
					// Park the comment near the top with a few rows of code
					// context above it (like the Comments-tab preview does),
					// instead of EnsureVisible's bottom-edge minimum scroll.
					v.vp.Top = max(i-3, 0)
					v.vp.clampTop()
					v.pendingJump = 0
				}
				return
			}
		}
	}
}

func (v *diffView) render(a *app, s *Screen) {
	v.tryPendingJump(a.h - 3)
	_, st, f := v.fileDiff(a)
	d := a.detailFor(v.prID)
	title := " (no file)"
	stats := ""
	if st != nil {
		title = " " + st.Path()
		if f != nil {
			title = " " + f.DisplayPath()
		}
		stats = fmt.Sprintf("+%d -%d", st.LinesAdded, st.LinesRemoved)
	}
	s.WriteString(1, 0, truncateWidth(title, a.w-24), styleTitle, a.w)
	right := fmt.Sprintf("%s  [%d/%d files]  #%d", stats, v.fileIdx+1, len(d.diffstat), v.prID)
	s.WriteString(a.w-1-displayWidth(right), 0, right, styleDim, a.w)
	paintSeparator(s, 1, a.w)
	rect := Rect{X: 0, Y: 2, W: a.w, H: a.h - 3}
	v.vp.Paint(s, rect)
	if lo, hi, ok := v.selection(); ok {
		// Active line selection (v/V): tint the selected rows.
		focusViewportRows(s, &v.vp, rect, lo, hi+1)
	} else if r := v.vp.Current(); r != nil && r.Kind == RowComment {
		// Cursor on a thread: light it up like the Comments tab does.
		if id, ok := r.Item.(int); ok {
			if span, ok := v.threadSpan[id]; ok {
				focusViewportRows(s, &v.vp, rect, span[0], span[1])
			}
		}
	}
}

// selection returns the active v/V row range [lo, hi] (inclusive).
func (v *diffView) selection() (lo, hi int, ok bool) {
	if v.selAnchor < 0 || v.vp.Cursor < 0 {
		return 0, 0, false
	}
	lo, hi = v.selAnchor, v.vp.Cursor
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi, true
}

// selectedLines collects the diff lines inside the active selection.
func (v *diffView) selectedLines() []DiffLine {
	lo, hi, ok := v.selection()
	if !ok {
		return nil
	}
	var lines []DiffLine
	for i := lo; i <= hi && i < len(v.vp.Rows); i++ {
		r := v.vp.Rows[i]
		if r.Kind != RowDiffLine || !r.Selectable {
			continue // skip wrap continuations and stray rows
		}
		if l, ok := r.Item.(DiffLine); ok {
			lines = append(lines, l)
		}
	}
	return lines
}

// currentHunk returns the hunk index the cursor is in (-1 when none).
func (v *diffView) currentHunk() int {
	r := v.vp.Current()
	if r == nil {
		return -1
	}
	if ref, ok := r.Item.(hunkRef); ok {
		return ref.hunk
	}
	// Diff lines carry no hunkRef; walk back to the enclosing hunk header.
	for i := v.vp.Cursor; i >= 0; i-- {
		if ref, ok := v.vp.Rows[i].Item.(hunkRef); ok && v.vp.Rows[i].Kind == RowHunk {
			return ref.hunk
		}
	}
	return -1
}

func (v *diffView) handle(a *app, k Key) {
	// Two-key vim sequences: ]h/[h (hunk), ]f/[f (file), za/zA (fold).
	if v.pending != 0 {
		p := v.pending
		v.pending = 0
		if k.Kind == KeyRune {
			switch {
			case p == ']' && k.R == 'h':
				v.vp.JumpTo(func(r Row) bool { return r.Kind == RowHunk }, 1)
			case p == '[' && k.R == 'h':
				v.vp.JumpTo(func(r Row) bool { return r.Kind == RowHunk }, -1)
			case p == ']' && k.R == 'f':
				v.switchFile(a, 1)
			case p == '[' && k.R == 'f':
				v.switchFile(a, -1)
			case p == 'z' && k.R == 'a':
				v.toggleFold(a)
			case p == 'z' && k.R == 'A':
				v.toggleFoldAll(a)
			}
		}
		return
	}
	if k.Kind == KeyRune && (k.R == ']' || k.R == '[' || k.R == 'z') {
		v.pending = k.R
		return
	}

	switch {
	case isKey(k, 'j') || k.Kind == KeyDown:
		v.vp.MoveCursor(1)
	case isKey(k, 'k') || k.Kind == KeyUp:
		v.vp.MoveCursor(-1)
	case k.Kind == KeyCtrl && k.R == 'd':
		v.vp.Scroll(v.vp.H / 2)
	case k.Kind == KeyCtrl && k.R == 'u':
		v.vp.Scroll(-v.vp.H / 2)
	case k.Kind == KeyCtrl && k.R == 'f':
		v.vp.Scroll(v.vp.H)
	case k.Kind == KeyCtrl && k.R == 'b':
		v.vp.Scroll(-v.vp.H)
	case isKey(k, 'g'):
		v.vp.Top = 0
		if i := v.vp.nextSelectable(0, 1); i >= 0 {
			v.vp.Cursor = i
		}
	case isKey(k, 'G'):
		if i := v.vp.nextSelectable(len(v.vp.Rows)-1, -1); i >= 0 {
			v.vp.Cursor = i
			v.vp.EnsureVisible()
		}
	case k.Kind == KeyRight:
		v.switchFile(a, 1)
	case k.Kind == KeyLeft:
		v.switchFile(a, -1)
	case k.Kind == KeyEnter:
		v.toggleFold(a)
	case isKey(k, 'C'):
		v.showCmt = !v.showCmt
		v.rebuild(a)
	case isKey(k, 'w'):
		v.wrap = !v.wrap
		v.rebuild(a)
	case isKey(k, 'v') || isKey(k, 'V'):
		// Start / cancel a line selection (extend with j/k, then c).
		switch {
		case v.selAnchor >= 0:
			v.selAnchor = -1
		default:
			if r := v.vp.Current(); r != nil && r.Kind == RowDiffLine {
				v.selAnchor = v.vp.Cursor
			} else {
				a.status = "diff 行の上で v を押してください"
			}
		}
	case isKey(k, 'c'):
		v.composeComment(a)
	case isKey(k, 'o'):
		d := a.detailFor(v.prID)
		if d.pr != nil {
			openInBrowser(a, d.pr.WebURL())
		}
	case isKey(k, 'D'):
		d := a.detailFor(v.prID)
		focus := ""
		if v.fileIdx >= 0 && v.fileIdx < len(d.diffstat) {
			focus = d.diffstat[v.fileIdx].Path()
		}
		openInDiffTool(a, v.prID, focus)
	case isKey(k, '?'):
		a.push(helpView{ctx: "diff"})
	}
}

// composeComment opens the comment editor for the current context: the v/V
// line selection (range comment), an inline comment on the cursor's diff
// line, or a reply on a comment thread.
func (v *diffView) composeComment(a *app) {
	if lines := v.selectedLines(); lines != nil {
		_, st, _ := v.fileDiff(a)
		if st == nil {
			return
		}
		in := rangeAnchor(st.Path(), lines)
		if in == nil {
			a.status = "この範囲にはコメントできません"
			return
		}
		v.selAnchor = -1
		openCommentEditor(a, v.prID, in, 0, st.Path()+" "+anchorLabel(in)+" へのコメント")
		return
	}

	r := v.vp.Current()
	if r == nil {
		return
	}
	switch r.Kind {
	case RowComment:
		if id, ok := r.Item.(int); ok {
			target := ""
			if t, ok := v.threadFor[id]; ok {
				target = replyTarget(t)
			}
			openCommentEditor(a, v.prID, nil, id, target)
		}
	case RowDiffLine:
		l, ok := r.Item.(DiffLine)
		if !ok {
			return
		}
		_, st, _ := v.fileDiff(a)
		if st == nil {
			return
		}
		in := rangeAnchor(st.Path(), []DiffLine{l})
		if in == nil {
			a.status = "この行にはコメントできません"
			return
		}
		openCommentEditor(a, v.prID, in, 0, st.Path()+" "+anchorLabel(in)+" へのコメント")
	default:
		a.status = "diff 行かコメントの上で C を押してください"
	}
}

// rangeAnchor maps selected diff lines onto a Bitbucket inline anchor. New
// side wins: to = the last new-side line, start_to = the first (when they
// differ); a deletions-only selection anchors on the old side. nil when no
// line is commentable.
func rangeAnchor(path string, lines []DiffLine) *InlineAnchor {
	var newNos, oldNos []int
	for _, l := range lines {
		if l.Kind != LineDel && l.NewNo > 0 {
			newNos = append(newNos, l.NewNo)
		} else if l.OldNo > 0 {
			oldNos = append(oldNos, l.OldNo)
		}
	}
	in := &InlineAnchor{Path: path}
	switch {
	case len(newNos) > 0:
		end := newNos[len(newNos)-1]
		in.To = &end
		if start := newNos[0]; start != end {
			in.StartTo = &start
		}
	case len(oldNos) > 0:
		end := oldNos[len(oldNos)-1]
		in.From = &end
		if start := oldNos[0]; start != end {
			in.StartFrom = &start
		}
	default:
		return nil
	}
	return in
}

// toggleFold flips the fold state of the hunk under the cursor.
func (v *diffView) toggleFold(a *app) {
	if hi := v.currentHunk(); hi >= 0 {
		v.folded[hi] = !v.folded[hi]
		v.rebuild(a)
		v.snapToHunk(hi)
	}
}

// toggleFoldAll folds everything, or unfolds when everything is already
// folded.
func (v *diffView) toggleFoldAll(a *app) {
	hi := v.currentHunk()
	allFolded := true
	_, _, f := v.fileDiff(a)
	if f != nil {
		for i := range f.Hunks {
			if !v.folded[i] {
				allFolded = false
				break
			}
		}
	}
	v.foldAll(a, !allFolded)
	v.rebuild(a)
	if hi >= 0 {
		v.snapToHunk(hi)
	}
}

// switchFile moves to the next/previous file in the diffstat, resetting
// fold state.
func (v *diffView) switchFile(a *app, dir int) {
	d := a.detailFor(v.prID)
	next := v.fileIdx + dir
	if next < 0 || next >= len(d.diffstat) {
		return
	}
	v.fileIdx = next
	v.folded = map[int]bool{}
	if a.cfg.ContextFold {
		v.foldAll(a, true)
	}
	v.vp = Viewport{}
	v.rebuild(a)
}

// snapToHunk puts the cursor on hunk hi's header row.
func (v *diffView) snapToHunk(hi int) {
	for i, r := range v.vp.Rows {
		if ref, ok := r.Item.(hunkRef); ok && r.Kind == RowHunk && ref.hunk == hi {
			v.vp.Cursor = i
			v.vp.EnsureVisible()
			return
		}
	}
}

func (v *diffView) footer(a *app) string {
	if lines := v.selectedLines(); lines != nil {
		label := "選択中"
		_, st, _ := v.fileDiff(a)
		if st != nil {
			if in := rangeAnchor(st.Path(), lines); in != nil {
				label = "選択中 " + anchorLabel(in)
			}
		}
		return label + "  j/k:範囲変更  c:コメント  v/Esc:解除"
	}
	cKey := "c:コメント"
	if r := v.vp.Current(); r != nil && r.Kind == RowComment {
		if id, ok := r.Item.(int); ok {
			if t, ok := v.threadFor[id]; ok {
				cKey = "c:" + replyTarget(t)
			}
		}
	}
	return "j/k:移動  ]h/[h:hunk  ]f/[f:ファイル  za:折畳  w:折返し  v:選択  C:💬  " + cKey + "  D:diffツール  q:戻る"
}
