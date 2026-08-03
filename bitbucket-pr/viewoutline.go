package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const outlineSplitWidth = 100

type outlineViewState struct {
	preview Viewport

	collapsedDirs  map[string]bool
	collapsedFiles map[string]bool
	expandedTests  map[string]bool
	alphabetical   bool
	pendingG       bool

	jumps   []string
	jumpPos int
}

type outlineRowRef struct {
	Kind string // dir | file | tests | symbol
	Path string
	ID   string
}

type outlineDirNode struct {
	Name  string
	Path  string
	Dirs  map[string]*outlineDirNode
	Files []int
}

type outlineDirStats struct {
	Files, Changed, API, FanIn int
	Smells                     smellMask
	Risk, Large                bool
}

func outlineSplit(w int) bool { return w >= outlineSplitWidth }

func outlineMasterWidth(w int) int {
	lw := w * 44 / 100
	if lw < 36 {
		lw = 36
	}
	if lw > 64 {
		lw = 64
	}
	return lw
}

func (v *detailView) ensureOutline(a *app, d *prDetail) {
	if d.outlineStarted || d.outline != nil || d.outlineLoading || d.pr == nil || d.diffText == "" {
		return
	}
	d.outlineStarted = true
	if a.ctx.RepoDir == "" {
		d.outlineErr = "ローカルcheckoutが関連付けられていません。対象リポジトリ内のペインからPRを開いてください。"
		return
	}
	sourceHash := d.pr.Source.Commit.Hash
	destinationHash := d.pr.Destination.Commit.Hash
	if cached := loadOutlineCache(a.ctx.Workspace, a.ctx.Repo, sourceHash, destinationHash); cached != nil {
		d.outline = cached
		return
	}
	d.outlineLoading = true
	gen, prID := v.outlineGen, v.prID
	diffs := append([]FileDiff(nil), d.files...)
	truncWarning := diffTruncated(d)
	repoDir, workspace, repo := a.ctx.RepoDir, a.ctx.Workspace, a.ctx.Repo
	a.fetch(fmt.Sprintf("outline %d", prID), func() (func(*app), error) {
		result, analysisErr := analyzeOutline(repoDir, sourceHash, destinationHash, diffs)
		if analysisErr == nil {
			if truncWarning != "" {
				result.Warnings = append(result.Warnings, truncWarning)
			}
			saveOutlineCache(workspace, repo, result)
		}
		return func(a *app) {
			current := a.detailFor(prID)
			if gen != v.outlineGen || current.pr == nil || current.pr.Source.Commit.Hash != sourceHash {
				return
			}
			current.outlineLoading = false
			if analysisErr != nil {
				current.outlineErr = outlineUserError(repoDir, sourceHash, destinationHash, analysisErr)
			} else {
				current.outline = result
				current.outlineErr = ""
			}
			v.rebuild(a)
		}, nil
	})
}

func outlineUserError(repoDir, sourceHash, destinationHash string, err error) string {
	msg := err.Error()
	if strings.Contains(msg, "がローカルにありません") {
		return fmt.Sprintf("PRのcommitがローカルにありません。%s でPRブランチをfetchしてから r で再読込してください。 (%s → %s)",
			repoDir, shortHash(destinationHash), shortHash(sourceHash))
	}
	return "Outlineを解析できません: " + msg
}

func shortHash(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}

func (v *detailView) outlineRows(a *app, d *prDetail) []Row {
	if d.pr == nil || d.diffText == "" {
		return []Row{textRow(Span{" PR情報とdiffを読み込み中…", styleDim})}
	}
	if d.outlineLoading {
		return []Row{
			textRow(Span{" 変更の輪郭を解析中…", styleMeta}),
			textRow(Span{" Tree-sitterでシグネチャと1-hop参照を調べています", styleDim}),
		}
	}
	if d.outlineErr != "" {
		rows := []Row{textRow(Span{" Outlineは利用できません", styleError}), textRow()}
		for _, line := range wrapText(d.outlineErr, max(a.w-4, 20)) {
			rows = append(rows, textRow(Span{"  " + line, styleNone}))
		}
		rows = append(rows, textRow(), textRow(Span{" 他のタブは通常どおり利用できます", styleDim}))
		return rows
	}
	result := d.outline
	if result == nil {
		return []Row{textRow(Span{" Outlineを準備しています…", styleDim})}
	}
	if len(result.Files) == 0 {
		return []Row{
			textRow(Span{" 変更シンボルは見つかりませんでした", styleDim}),
			textRow(Span{" 変更ファイルはFilesタブで確認できます", styleDim}),
		}
	}
	if v.outline.collapsedDirs == nil {
		v.outline.collapsedDirs = map[string]bool{}
		v.outline.collapsedFiles = map[string]bool{}
		v.outline.expandedTests = map[string]bool{}
	}

	root := buildOutlineTree(result)
	ranks, cycles := outlineDirectoryRanks(result)
	q := v.search[tabOutline].query()
	width := a.w
	if outlineSplit(a.w) {
		width = outlineMasterWidth(a.w)
	}
	summary := fmt.Sprintf(" %d changed symbols", result.symbolCount())
	if tests := result.testSymbolCount(); tests > 0 {
		summary += fmt.Sprintf(" (tests:%d)", tests)
	}
	summary += fmt.Sprintf(" in %d/%d files", changedOutlineFileCount(result), len(result.Files))
	rows := []Row{textRow(Span{summary, styleTitle}, Span{"  · attention map, not verification", styleDim})}
	for _, warning := range result.Warnings {
		rows = append(rows, textRow(Span{" ⚠ " + truncateWidth(warning, max(width-4, 12)), styleOutdated}))
	}
	beforeTree := len(rows)
	v.appendOutlineDirRows(&rows, root, result, ranks, cycles, 0, q, width)
	if len(rows) == beforeTree && q != "" {
		rows = append(rows, textRow(Span{" (一致するシンボルはありません)", styleDim}))
	}
	return rows
}

func buildOutlineTree(result *outlineResult) *outlineDirNode {
	root := &outlineDirNode{Dirs: map[string]*outlineDirNode{}}
	for i, file := range result.Files {
		parts := strings.Split(filepath.ToSlash(file.Path), "/")
		node := root
		path := ""
		for _, name := range parts[:max(len(parts)-1, 0)] {
			if path == "" {
				path = name
			} else {
				path += "/" + name
			}
			child := node.Dirs[name]
			if child == nil {
				child = &outlineDirNode{Name: name, Path: path, Dirs: map[string]*outlineDirNode{}}
				node.Dirs[name] = child
			}
			node = child
		}
		node.Files = append(node.Files, i)
	}
	return root
}

func (v *detailView) appendOutlineDirRows(rows *[]Row, node *outlineDirNode, result *outlineResult,
	ranks map[string]int, cycles map[string]bool, depth int, q string, width int) bool {
	start := len(*rows)
	if node.Path != "" {
		if q != "" && !outlineDirMatches(node, result, q) {
			return false
		}
		stats := outlineStatsForDir(node, result)
		marker := "v"
		if v.outline.collapsedDirs[node.Path] && q == "" {
			marker = ">"
		}
		prefix := strings.Repeat("  ", depth)
		spans := []Span{{prefix, styleNone}}
		if stats.Risk {
			spans = append(spans, Span{"! ", styleError})
		} else {
			spans = append(spans, Span{"  ", styleNone})
		}
		spans = append(spans, Span{marker + " " + node.Name, styleTitle})
		if cycles[node.Path] {
			spans = append(spans, Span{" (cycle)", styleOutdated})
		}
		spans = appendOutlineBadges(spans, stats.Changed, stats.API, stats.FanIn, stats.Smells, 0, width)
		*rows = append(*rows, row(RowOutlineDir, outlineRowRef{Kind: "dir", Path: node.Path}, true, spans...))
		if v.outline.collapsedDirs[node.Path] && q == "" {
			return true
		}
		depth++
	}

	dirs := make([]*outlineDirNode, 0, len(node.Dirs))
	for _, child := range node.Dirs {
		dirs = append(dirs, child)
	}
	sort.SliceStable(dirs, func(i, j int) bool {
		if !v.outline.alphabetical {
			ri, rj := outlineRankForTree(dirs[i], ranks), outlineRankForTree(dirs[j], ranks)
			if ri != rj {
				return ri < rj
			}
		}
		return dirs[i].Name < dirs[j].Name
	})
	for _, child := range dirs {
		v.appendOutlineDirRows(rows, child, result, ranks, cycles, depth, q, width)
	}

	files := append([]int(nil), node.Files...)
	sort.SliceStable(files, func(i, j int) bool { return result.Files[files[i]].Path < result.Files[files[j]].Path })
	for _, fileIndex := range files {
		v.appendOutlineFileRows(rows, result, fileIndex, depth, q, width)
	}
	return len(*rows) > start
}

func (v *detailView) appendOutlineFileRows(rows *[]Row, result *outlineResult, fileIndex, depth int, q string, width int) {
	file := &result.Files[fileIndex]
	pathMatches := matchQuery(q, file.Path, file.OldPath, file.Language, file.Skipped)
	matchingProduction := filterOutlineSymbols(file.Symbols, q, pathMatches)
	matchingTests := filterOutlineSymbols(file.TestSymbols, q, pathMatches)
	if q != "" && !pathMatches && len(matchingProduction) == 0 && len(matchingTests) == 0 {
		return
	}
	marker := "v"
	if file.Skipped != "" || v.outline.collapsedFiles[file.Path] && q == "" {
		marker = ">"
	}
	indent := strings.Repeat("  ", depth)
	spans := []Span{{indent, styleNone}}
	if file.risky() {
		spans = append(spans, Span{"! ", styleError})
	} else {
		spans = append(spans, Span{"  ", styleNone})
	}
	spans = append(spans, Span{marker + " " + filepath.Base(file.Path), styleMeta})
	if file.Skipped != "" {
		spans = append(spans, Span{" (skipped: " + file.Skipped + ")", styleDim})
	} else {
		spans = appendOutlineBadges(spans, file.changedCount(), file.apiCount(), file.fanIn(), file.smells(), file.Lines, width)
	}
	*rows = append(*rows, row(RowOutlineFile, outlineRowRef{Kind: "file", Path: file.Path}, true, spans...))
	if file.Skipped != "" || v.outline.collapsedFiles[file.Path] && q == "" {
		return
	}
	for _, symbol := range matchingProduction {
		*rows = append(*rows, outlineSymbolRow(symbol, depth+1))
	}
	if len(matchingTests) > 0 {
		expanded := v.outline.expandedTests[file.Path] || q != ""
		marker := ">"
		if expanded {
			marker = "v"
		}
		*rows = append(*rows, row(RowOutlineFile, outlineRowRef{Kind: "tests", Path: file.Path}, true,
			Span{strings.Repeat("  ", depth+1) + "  " + marker + fmt.Sprintf(" %d tests", len(matchingTests)), styleDim}))
		if expanded {
			for _, symbol := range matchingTests {
				*rows = append(*rows, outlineSymbolRow(symbol, depth+2))
			}
		}
	}
}

func outlineSymbolRow(symbol outlineSymbol, depth int) Row {
	spans := []Span{{strings.Repeat("  ", depth), styleNone}}
	if symbol.risky() {
		spans = append(spans, Span{"! ", styleError})
	} else {
		spans = append(spans, Span{"  ", styleNone})
	}
	markStyle := styleDim
	switch symbol.Change {
	case outlineAdded:
		markStyle = styleApproved
	case outlineSignatureChanged:
		markStyle = styleOutdated
	case outlineRemoved:
		markStyle = styleChangesReq
	}
	spans = append(spans, Span{symbol.Change.mark() + " ", markStyle}, Span{padRight(symbol.Kind, 9), styleDim})
	nameStyle := styleNone
	if symbol.Change == outlineBodyOnly || symbol.Test {
		nameStyle = styleDim
	}
	spans = append(spans, Span{symbol.Name, nameStyle})
	if symbol.contractChanged() && !symbol.Test {
		spans = append(spans, Span{"  api", styleOutdated})
	}
	if symbol.FanIn > 0 {
		spans = append(spans, Span{fmt.Sprintf("  fan-in:%d", symbol.FanIn), styleMeta})
	}
	if symbol.godHelper() {
		spans = append(spans, Span{" ", styleNone}, Span{" 呼出集中 ", styleSmellBadge})
	}
	if growth, ok := symbol.bloatGrowth(); ok {
		spans = append(spans, Span{" ", styleNone}, Span{fmt.Sprintf(" 肥大+%d ", growth), styleSmellBadge})
	}
	if lines, ok := symbol.bigAddedLines(); ok {
		spans = append(spans, Span{" ", styleNone}, Span{fmt.Sprintf(" 巨大%d行 ", lines), styleSmellBadge})
	}
	return row(RowOutlineSymbol, outlineRowRef{Kind: "symbol", Path: symbol.Path, ID: symbol.ID}, true, spans...)
}

func appendOutlineBadges(spans []Span, changed, api, fanIn int, smells smellMask, lines, width int) []Span {
	spans = append(spans, Span{fmt.Sprintf("  chg:%d", changed), styleMeta})
	if api > 0 {
		spans = append(spans, Span{fmt.Sprintf("  api:%d", api), styleOutdated})
	}
	for _, label := range smellChipLabels(smells) {
		spans = append(spans, Span{" ", styleNone}, Span{" " + label + " ", styleSmellBadge})
	}
	if fanIn > 0 {
		spans = append(spans, Span{fmt.Sprintf("  fan-in:%d", fanIn), styleMeta})
	}
	if lines > 0 {
		style := styleDim
		if lines >= outlineLargeFileLine {
			style = styleOutdated
		}
		spans = append(spans, Span{fmt.Sprintf("  lines:%d", lines), style})
	}
	return spans
}

func filterOutlineSymbols(symbols []outlineSymbol, q string, pathMatches bool) []outlineSymbol {
	if q == "" || pathMatches {
		return symbols
	}
	out := make([]outlineSymbol, 0, len(symbols))
	for _, symbol := range symbols {
		if matchQuery(q, symbol.Name, symbol.Kind, symbol.Signature, symbol.OldSignature, string(symbol.Change), symbol.smellQueryText()) {
			out = append(out, symbol)
		}
	}
	return out
}

func outlineDirMatches(node *outlineDirNode, result *outlineResult, q string) bool {
	if matchQuery(q, node.Path, node.Name) {
		return true
	}
	for _, fileIndex := range node.Files {
		file := result.Files[fileIndex]
		if matchQuery(q, file.Path, file.OldPath, file.Language, file.Skipped) ||
			len(filterOutlineSymbols(file.Symbols, q, false)) > 0 || len(filterOutlineSymbols(file.TestSymbols, q, false)) > 0 {
			return true
		}
	}
	for _, child := range node.Dirs {
		if outlineDirMatches(child, result, q) {
			return true
		}
	}
	return false
}

func outlineStatsForDir(node *outlineDirNode, result *outlineResult) outlineDirStats {
	var stats outlineDirStats
	for _, fileIndex := range node.Files {
		file := result.Files[fileIndex]
		stats.Files++
		stats.Changed += file.changedCount()
		stats.API += file.apiCount()
		stats.FanIn += file.fanIn()
		stats.Smells |= file.smells()
		stats.Risk = stats.Risk || file.risky()
		stats.Large = stats.Large || file.Lines >= outlineLargeFileLine
	}
	for _, child := range node.Dirs {
		childStats := outlineStatsForDir(child, result)
		stats.Files += childStats.Files
		stats.Changed += childStats.Changed
		stats.API += childStats.API
		stats.FanIn += childStats.FanIn
		stats.Smells |= childStats.Smells
		stats.Risk = stats.Risk || childStats.Risk
		stats.Large = stats.Large || childStats.Large
	}
	return stats
}

func changedOutlineFileCount(result *outlineResult) int {
	n := 0
	for _, file := range result.Files {
		if file.changedCount() > 0 {
			n++
		}
	}
	return n
}

func outlineDirectoryRanks(result *outlineResult) (map[string]int, map[string]bool) {
	symbolDir := map[string]string{}
	for _, file := range result.Files {
		dir := filepath.ToSlash(filepath.Dir(file.Path))
		if dir == "." {
			dir = ""
		}
		for _, symbols := range [][]outlineSymbol{file.Symbols, file.TestSymbols} {
			for _, symbol := range symbols {
				symbolDir[symbol.ID] = dir
			}
		}
	}
	adj, indegree, dirs := map[string]map[string]bool{}, map[string]int{}, map[string]bool{}
	for _, dir := range symbolDir {
		dirs[dir] = true
	}
	for _, edge := range result.Edges {
		from, fromOK := symbolDir[edge.From]
		to, toOK := symbolDir[edge.To]
		if !fromOK || !toOK || from == to {
			continue
		}
		if adj[from] == nil {
			adj[from] = map[string]bool{}
		}
		if !adj[from][to] {
			adj[from][to] = true
			indegree[to]++
		}
	}
	var queue []string
	for dir := range dirs {
		if indegree[dir] == 0 {
			queue = append(queue, dir)
		}
	}
	sort.Strings(queue)
	ranks := map[string]int{}
	rank := 0
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		ranks[dir] = rank
		rank++
		var next []string
		for to := range adj[dir] {
			indegree[to]--
			if indegree[to] == 0 {
				next = append(next, to)
			}
		}
		sort.Strings(next)
		queue = append(queue, next...)
	}
	cycles := map[string]bool{}
	var remaining []string
	for dir := range dirs {
		if _, ok := ranks[dir]; !ok {
			remaining = append(remaining, dir)
			cycles[dir] = true
		}
	}
	sort.Strings(remaining)
	for _, dir := range remaining {
		ranks[dir] = rank
		rank++
	}
	return ranks, cycles
}

func outlineRankForTree(node *outlineDirNode, ranks map[string]int) int {
	best := int(^uint(0) >> 1)
	for dir, rank := range ranks {
		if dir == node.Path || strings.HasPrefix(dir, node.Path+"/") {
			best = min(best, rank)
		}
	}
	return best
}

func (v *detailView) syncOutlinePreview(a *app, d *prDetail) {
	if !outlineSplit(a.w) || d.outline == nil {
		return
	}
	row := v.vp[tabOutline].Current()
	if row == nil {
		v.outline.preview.Reset(nil)
		return
	}
	ref, ok := row.Item.(outlineRowRef)
	if !ok {
		v.outline.preview.Reset([]Row{textRow(Span{" j/kで変更シンボルを選択してください", styleDim})})
		return
	}
	width := a.w - outlineMasterWidth(a.w) - 1
	v.outline.preview.Reset(v.outlinePreviewRows(a, d, ref, width))
}

func (v *detailView) outlinePreviewRows(a *app, d *prDetail, ref outlineRowRef, width int) []Row {
	result := d.outline
	switch ref.Kind {
	case "symbol":
		symbol := result.symbolByID(ref.ID)
		if symbol == nil {
			return nil
		}
		rows := []Row{textRow(
			Span{" " + symbol.Kind + " ", styleDim},
			Span{symbol.Name, styleTitle},
			Span{"  " + string(symbol.Change), outlineChangeStyle(symbol.Change)},
		)}
		if symbol.risky() {
			rows = append(rows, textRow(Span{fmt.Sprintf(" ! contract change + fan-in:%d", symbol.FanIn), styleError}))
		}
		if symbol.godHelper() {
			callers, dirs := symbol.godHelperStats()
			rows = append(rows, textRow(Span{fmt.Sprintf(" ! 呼出集中: %d箇所・%dディレクトリから呼ばれています。責務の分割を検討する価値があります", callers, dirs), styleOutdated}))
		}
		if growth, ok := symbol.bloatGrowth(); ok {
			oldLines := symbol.OldEndLine - symbol.OldStartLine + 1
			rows = append(rows, textRow(Span{fmt.Sprintf(" ! 肥大: シグネチャは同じまま本体が %d行 → %d行 (+%d) に増えています", oldLines, oldLines+growth, growth), styleOutdated}))
		}
		if lines, ok := symbol.bigAddedLines(); ok {
			rows = append(rows, textRow(Span{fmt.Sprintf(" ! 巨大: 新規追加の%sが%d行あります", outlineKindJa(symbol.Kind), lines), styleOutdated}))
		}
		rows = append(rows, textRow())
		if symbol.OldSignature != "" && symbol.OldSignature != symbol.Signature {
			rows = append(rows, textRow(Span{" Signature", styleTitle}))
			rows = appendWrappedCode(rows, " - ", symbol.OldSignature, styleDel, width)
			if symbol.Signature != "" {
				rows = appendWrappedCode(rows, " + ", symbol.Signature, styleAdd, width)
			}
		} else if symbol.Signature != "" {
			rows = append(rows, textRow(Span{" Signature", styleTitle}))
			rows = appendWrappedCode(rows, "   ", symbol.Signature, styleMDCode, width)
		}
		rows = appendOutlineRefs(rows, "Used by", symbol.Callers, width)
		rows = appendOutlineRefs(rows, "Calls", symbol.Callees, width)
		rows = append(rows, textRow(), textRow(Span{" Related diff", styleTitle}))
		rows = append(rows, outlineRelatedDiffRows(d, *symbol, width)...)
		return rows
	case "file", "tests":
		file := outlineFileByPath(result, ref.Path)
		if file == nil {
			return nil
		}
		rows := []Row{textRow(Span{" " + file.Path, styleTitle}), textRow(
			Span{fmt.Sprintf(" %s  chg:%d", file.Language, file.changedCount()), styleMeta},
			Span{fmt.Sprintf("  api:%d  fan-in:%d  lines:%d", file.apiCount(), file.fanIn(), file.Lines), styleDim},
		), textRow()}
		if file.Skipped != "" {
			rows = append(rows, textRow(Span{" skipped: " + file.Skipped, styleDim}))
			return rows
		}
		for _, symbols := range [][]outlineSymbol{file.Symbols, file.TestSymbols} {
			for _, symbol := range symbols {
				rows = append(rows, textRow(Span{" " + symbol.Change.mark() + " ", outlineChangeStyle(symbol.Change)}, Span{symbol.Kind + " " + symbol.Name, styleNone}))
			}
		}
		return rows
	case "dir":
		return []Row{
			textRow(Span{" " + ref.Path, styleTitle}),
			textRow(Span{" 呼び出し元に近いディレクトリほど上に並びます", styleDim}),
			textRow(Span{" Oでアルファベット順へ切り替えられます", styleDim}),
		}
	}
	return nil
}

// outlineKindJa renders a callable kind for Japanese preview sentences.
// Only kinds that pass outlineCallableSymbolKind can reach it.
func outlineKindJa(kind string) string {
	switch kind {
	case "method":
		return "メソッド"
	case "ctor":
		return "コンストラクタ"
	default:
		return "関数"
	}
}

func outlineChangeStyle(change outlineChange) StyleID {
	switch change {
	case outlineAdded:
		return styleApproved
	case outlineSignatureChanged:
		return styleOutdated
	case outlineRemoved:
		return styleChangesReq
	default:
		return styleDim
	}
}

func appendWrappedCode(rows []Row, prefix, text string, style StyleID, width int) []Row {
	chunks := wrapText(text, max(width-displayWidth(prefix)-2, 12))
	for i, chunk := range chunks {
		p := prefix
		if i > 0 {
			p = strings.Repeat(" ", displayWidth(prefix))
		}
		rows = append(rows, textRow(Span{p, styleDim}, Span{chunk, style}))
	}
	return rows
}

func appendOutlineRefs(rows []Row, title string, refs []outlineRef, width int) []Row {
	if len(refs) == 0 {
		return rows
	}
	rows = append(rows, textRow(), textRow(Span{" " + title, styleTitle}))
	for _, ref := range refs {
		mark := "  "
		style := styleDim
		if ref.Changed {
			mark, style = "* ", styleMeta
		}
		if ref.Ambiguous {
			mark = "? "
			style = styleOutdated
		}
		label := fmt.Sprintf("%s%s %s  %s:%d", mark, ref.Kind, ref.Name, ref.Path, ref.Line)
		rows = append(rows, textRow(Span{" " + truncateWidth(label, max(width-2, 10)), style}))
	}
	return rows
}

func outlineRelatedDiffRows(d *prDetail, symbol outlineSymbol, width int) []Row {
	file := fileDiffFor(d.files, symbol.Path)
	if file == nil && symbol.OldPath != "" {
		file = fileDiffFor(d.files, symbol.OldPath)
	}
	if file == nil {
		return []Row{textRow(Span{" diffに対応するファイルがありません", styleDim})}
	}
	oldSide := symbol.Change == outlineRemoved
	start, end := symbol.StartLine, symbol.EndLine
	if oldSide {
		start, end = symbol.OldStartLine, symbol.OldStartLine
	}
	var rows []Row
	for hi := range file.Hunks {
		hunk := &file.Hunks[hi]
		if !outlineHunkTouches(hunk, start, end, oldSide) {
			continue
		}
		head := fmt.Sprintf(" @@ -%d,%d +%d,%d @@ %s", hunk.OldStart, hunk.OldCount, hunk.NewStart, hunk.NewCount, hunk.Section)
		rows = append(rows, textRow(Span{truncateWidth(head, max(width-1, 12)), styleHunk}))
		for _, line := range hunk.Lines {
			rows = append(rows, unselectableRows(diffLineRows(line, 4, width, false))...)
		}
	}
	if len(rows) == 0 {
		rows = append(rows, textRow(Span{" 対応するhunkが見つかりません", styleDim}))
	}
	return rows
}

func outlineHunkTouches(hunk *Hunk, start, end int, old bool) bool {
	for _, line := range hunk.Lines {
		n := line.NewNo
		if old {
			n = line.OldNo
		}
		if n >= start && n <= end {
			return true
		}
	}
	return false
}

func outlineFileByPath(result *outlineResult, path string) *outlineFile {
	for i := range result.Files {
		if result.Files[i].Path == path || result.Files[i].OldPath == path {
			return &result.Files[i]
		}
	}
	return nil
}

// handleOutlineKey owns Outline-specific sequences before detailView's common
// switch. `g` becomes a prefix only on this tab; the rest of the application
// keeps its existing one-key jump-to-top behavior.
func (v *detailView) handleOutlineKey(a *app, k Key) bool {
	if v.outline.pendingG {
		v.outline.pendingG = false
		switch {
		case isKey(k, 'g'):
			v.outlineJumpTop()
		case isKey(k, 'd'):
			v.openOutlineReferences(a, false)
		case isKey(k, 'r'):
			v.openOutlineReferences(a, true)
		default:
			a.status = "Outline: gg / gd / gr を使用してください"
		}
		return true
	}
	switch {
	case isKey(k, 'g'):
		v.outline.pendingG = true
		a.status = "Outline: gg=先頭  gd=callee  gr=caller"
		return true
	case isKey(k, 'O'):
		v.outline.alphabetical = !v.outline.alphabetical
		if v.outline.alphabetical {
			a.status = "Outlineをアルファベット順にしました"
		} else {
			a.status = "Outlineを依存順にしました"
		}
		v.rebuild(a)
		return true
	case k.Kind == KeyCtrl && k.R == 'o':
		v.navigateOutlineHistory(a, -1)
		return true
	case k.Kind == KeyCtrl && k.R == 'i':
		v.navigateOutlineHistory(a, 1)
		return true
	}
	return false
}

func (v *detailView) outlineJumpTop() {
	vp := &v.vp[tabOutline]
	if i := vp.nextSelectable(0, 1); i >= 0 {
		vp.Cursor = i
		vp.Top = 0
		vp.EnsureVisible()
	}
}

func (v *detailView) activateOutlineRow(a *app) {
	row := v.vp[tabOutline].Current()
	if row == nil {
		return
	}
	ref, ok := row.Item.(outlineRowRef)
	if !ok {
		return
	}
	switch ref.Kind {
	case "dir":
		v.outline.collapsedDirs[ref.Path] = !v.outline.collapsedDirs[ref.Path]
		v.rebuild(a)
	case "file":
		file := outlineFileByPath(a.detailFor(v.prID).outline, ref.Path)
		if file != nil && file.Skipped == "" {
			v.outline.collapsedFiles[ref.Path] = !v.outline.collapsedFiles[ref.Path]
			v.rebuild(a)
		}
	case "tests":
		v.outline.expandedTests[ref.Path] = !v.outline.expandedTests[ref.Path]
		v.rebuild(a)
	case "symbol":
		v.openOutlineSymbolInDiff(a, ref.ID)
	}
}

func (v *detailView) currentOutlineSymbol(a *app) *outlineSymbol {
	row := v.vp[tabOutline].Current()
	if row == nil {
		return nil
	}
	ref, ok := row.Item.(outlineRowRef)
	if !ok || ref.Kind != "symbol" {
		return nil
	}
	d := a.detailFor(v.prID)
	if d.outline == nil {
		return nil
	}
	return d.outline.symbolByID(ref.ID)
}

func (v *detailView) openOutlineSymbolInDiff(a *app, id string) {
	d := a.detailFor(v.prID)
	if d.outline == nil {
		return
	}
	symbol := d.outline.symbolByID(id)
	if symbol == nil {
		return
	}
	idx := -1
	for i := range d.diffstat {
		if d.diffstat[i].Path() == symbol.Path || d.diffstat[i].OldPath() == symbol.Path ||
			d.diffstat[i].Path() == symbol.OldPath || d.diffstat[i].OldPath() == symbol.OldPath {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.status = "diffにファイルが見つかりません: " + symbol.Path
		return
	}
	dv := newDiffView(a, v.prID, idx)
	if symbol.Change == outlineRemoved {
		dv.pendingLine = symbol.OldStartLine
		dv.pendingOldLine = true
	} else {
		dv.pendingLine = symbol.StartLine
	}
	dv.rebuild(a)
	a.push(dv)
}

func (v *detailView) openOutlineReferences(a *app, callers bool) {
	symbol := v.currentOutlineSymbol(a)
	if symbol == nil {
		a.status = "シンボル行で gd / gr を押してください"
		return
	}
	refs := symbol.Callees
	label := "Callees"
	if callers {
		refs, label = symbol.Callers, "Callers"
	}
	changed := make([]outlineRef, 0, len(refs))
	for _, ref := range refs {
		if ref.Changed && a.detailFor(v.prID).outline.symbolByID(ref.ID) != nil {
			changed = append(changed, ref)
		}
	}
	switch len(changed) {
	case 0:
		if len(refs) > 0 {
			first := refs[0]
			a.status = fmt.Sprintf("%sはPR外です: %s:%d", label, first.Path, first.Line)
		} else {
			a.status = label + "はありません"
		}
	case 1:
		v.jumpToOutlineSymbol(a, changed[0].ID, true)
	default:
		a.push(newOutlineRefView(v, label+" — "+symbol.Name, changed))
	}
}

func (v *detailView) jumpToOutlineSymbol(a *app, id string, record bool) bool {
	current := ""
	if symbol := v.currentOutlineSymbol(a); symbol != nil {
		current = symbol.ID
	}
	if v.search[tabOutline].query() != "" {
		v.search[tabOutline].clear()
	}
	d := a.detailFor(v.prID)
	if d.outline == nil {
		return false
	}
	target := d.outline.symbolByID(id)
	if target == nil {
		return false
	}
	for path := filepath.ToSlash(filepath.Dir(target.Path)); path != "." && path != ""; path = filepath.ToSlash(filepath.Dir(path)) {
		delete(v.outline.collapsedDirs, path)
	}
	delete(v.outline.collapsedFiles, target.Path)
	if target.Test {
		v.outline.expandedTests[target.Path] = true
	}
	v.rebuild(a)
	for i, row := range v.vp[tabOutline].Rows {
		if ref, ok := row.Item.(outlineRowRef); ok && ref.Kind == "symbol" && ref.ID == id {
			v.vp[tabOutline].Cursor = i
			v.vp[tabOutline].EnsureVisible()
			v.syncOutlinePreview(a, d)
			if record && current != "" && current != id {
				if v.outline.jumpPos < len(v.outline.jumps)-1 {
					v.outline.jumps = v.outline.jumps[:v.outline.jumpPos+1]
				}
				if len(v.outline.jumps) == 0 || v.outline.jumps[len(v.outline.jumps)-1] != current {
					v.outline.jumps = append(v.outline.jumps, current)
				}
				v.outline.jumps = append(v.outline.jumps, id)
				v.outline.jumpPos = len(v.outline.jumps) - 1
			}
			return true
		}
	}
	return false
}

func (v *detailView) navigateOutlineHistory(a *app, delta int) {
	if len(v.outline.jumps) == 0 {
		a.status = "Outlineの移動履歴はありません"
		return
	}
	next := v.outline.jumpPos + delta
	if next < 0 || next >= len(v.outline.jumps) {
		a.status = "Outlineの移動履歴の端です"
		return
	}
	v.outline.jumpPos = next
	v.jumpToOutlineSymbol(a, v.outline.jumps[next], false)
}

type outlineRefView struct {
	parent *detailView
	title  string
	refs   []outlineRef
	vp     Viewport
}

func newOutlineRefView(parent *detailView, title string, refs []outlineRef) *outlineRefView {
	v := &outlineRefView{parent: parent, title: title, refs: refs}
	rows := make([]Row, 0, len(refs))
	for i, ref := range refs {
		mark := ""
		if ref.Ambiguous {
			mark = "? "
		}
		rows = append(rows, row(RowOutlineSymbol, i, true,
			Span{fmt.Sprintf(" %s%s %s", mark, ref.Kind, ref.Name), styleTitle},
			Span{fmt.Sprintf("  %s:%d", ref.Path, ref.Line), styleDim}))
	}
	v.vp.Reset(rows)
	return v
}

func (v *outlineRefView) render(a *app, s *Screen) {
	s.WriteString(1, 0, "Outline — "+v.title, styleTitle, a.w)
	paintSeparator(s, 1, a.w)
	v.vp.Paint(s, Rect{X: 0, Y: 2, W: a.w, H: a.h - 3})
}

func (v *outlineRefView) handle(a *app, k Key) {
	switch {
	case isKey(k, 'j') || k.Kind == KeyDown:
		v.vp.MoveCursor(1)
	case isKey(k, 'k') || k.Kind == KeyUp:
		v.vp.MoveCursor(-1)
	case k.Kind == KeyEnter:
		row := v.vp.Current()
		if row == nil {
			return
		}
		idx, ok := row.Item.(int)
		if !ok || idx < 0 || idx >= len(v.refs) {
			return
		}
		a.pop()
		v.parent.jumpToOutlineSymbol(a, v.refs[idx].ID, true)
	}
}

func (v *outlineRefView) footer(a *app) string { return "j/k:選択  Enter:移動  q/Esc:戻る" }
