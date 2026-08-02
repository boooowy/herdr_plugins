package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type outlineChangedRaw struct {
	Symbol outlineSymbol
	Raw    rawOutlineSymbol
}

type outlineGraphSymbol struct {
	Raw     rawOutlineSymbol
	ID      string
	Changed bool
}

type outlineParseResult struct {
	Symbols []rawOutlineSymbol
	Skipped string
	Err     error
}

// parseOutlineSources bounds Tree-sitter work even when git grep returns a
// large candidate set. Each worker owns its parser through parseOutlineFile;
// collection remains keyed by path so later graph construction is stable.
func parseOutlineSources(paths []string, sources map[string][]byte) map[string]outlineParseResult {
	results := make(map[string]outlineParseResult, len(paths))
	if len(paths) == 0 {
		return results
	}
	type parsed struct {
		Path string
		outlineParseResult
	}
	jobs := make(chan string)
	done := make(chan parsed)
	workers := min(4, len(paths))
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for path := range jobs {
				symbols, skipped, err := parseOutlineFile(path, sources[path])
				done <- parsed{Path: path, outlineParseResult: outlineParseResult{Symbols: symbols, Skipped: skipped, Err: err}}
			}
		}()
	}
	go func() {
		for _, path := range paths {
			jobs <- path
		}
		close(jobs)
		wg.Wait()
		close(done)
	}()
	for item := range done {
		results[item.Path] = item.outlineParseResult
	}
	return results
}

func analyzeOutline(repoDir, sourceHash, destinationHash string, diffs []FileDiff) (*outlineResult, error) {
	if err := gitCommitAvailable(repoDir, sourceHash); err != nil {
		return nil, err
	}
	if err := gitCommitAvailable(repoDir, destinationHash); err != nil {
		return nil, err
	}

	result := &outlineResult{
		Schema:          outlineSchemaVersion,
		SourceHash:      sourceHash,
		DestinationHash: destinationHash,
	}
	newPaths, oldPaths := make([]string, 0, len(diffs)), make([]string, 0, len(diffs))
	for i := range diffs {
		f := &diffs[i]
		if f.IsBinary {
			continue
		}
		if f.NewPath != "" {
			if outlineMaySupportPath(f.NewPath) {
				newPaths = append(newPaths, f.NewPath)
			}
		}
		if f.OldPath != "" {
			if outlineMaySupportPath(f.OldPath) {
				oldPaths = append(oldPaths, f.OldPath)
			}
		}
	}
	newPaths = uniqueStrings(newPaths)
	oldPaths = uniqueStrings(oldPaths)
	newChanged, err := gitReadFiles(repoDir, sourceHash, newPaths)
	if err != nil {
		return nil, err
	}
	oldChanged, err := gitReadFiles(repoDir, destinationHash, oldPaths)
	if err != nil {
		return nil, err
	}

	changedNewRaw := map[string][]rawOutlineSymbol{}
	changedOldRaw := map[string][]rawOutlineSymbol{}
	for path, parsed := range parseOutlineSources(newPaths, newChanged) {
		if parsed.Err != nil {
			return nil, fmt.Errorf("parse %s at %.12s: %w", path, sourceHash, parsed.Err)
		}
		changedNewRaw[path] = parsed.Symbols
	}
	for path, parsed := range parseOutlineSources(oldPaths, oldChanged) {
		if parsed.Err != nil {
			return nil, fmt.Errorf("parse %s at %.12s: %w", path, destinationHash, parsed.Err)
		}
		changedOldRaw[path] = parsed.Symbols
	}

	changed := map[string]outlineChangedRaw{}
	for i := range diffs {
		file, mappings := classifyOutlineFile(&diffs[i], newChanged, oldChanged, changedNewRaw, changedOldRaw)
		result.Files = append(result.Files, file)
		for _, mapping := range mappings {
			changed[mapping.Symbol.ID] = mapping
		}
	}

	// Search for both changed definitions and calls originating in them. The
	// latter discovers unchanged one-hop callees without parsing every file.
	nameSet := map[string]bool{}
	for _, mapping := range changed {
		if usefulOutlineName(mapping.Symbol.Name) {
			nameSet[mapping.Symbol.Name] = true
		}
		for _, call := range mapping.Raw.Calls {
			if usefulOutlineName(call) {
				nameSet[call] = true
			}
		}
	}
	names := make([]string, 0, len(nameSet))
	for name := range nameSet {
		names = append(names, name)
	}
	sort.Strings(names)

	candidatePaths, err := gitCandidatePaths(repoDir, sourceHash, names)
	if err != nil {
		return nil, err
	}
	for path := range newChanged {
		candidatePaths = append(candidatePaths, path)
	}
	candidatePaths = uniqueStrings(candidatePaths)
	candidateSources, err := gitReadFiles(repoDir, sourceHash, candidatePaths)
	if err != nil {
		return nil, err
	}

	changedByRaw := map[string]outlineSymbol{}
	for _, mapping := range changed {
		if mapping.Symbol.Change == outlineRemoved {
			continue
		}
		changedByRaw[rawOutlineKey(mapping.Raw)] = mapping.Symbol
	}
	var graphSymbols []outlineGraphSymbol
	parsedCandidates := parseOutlineSources(candidatePaths, candidateSources)
	for _, path := range candidatePaths {
		parsed := parsedCandidates[path]
		if parsed.Err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", path, parsed.Err))
			continue
		}
		if parsed.Skipped != "" {
			continue
		}
		for _, raw := range parsed.Symbols {
			entry := outlineGraphSymbol{Raw: raw, ID: outlineSymbolID(raw.Path, raw.Kind, raw.Name, raw.StartLine)}
			if changedSymbol, ok := changedByRaw[rawOutlineKey(raw)]; ok {
				entry.ID = changedSymbol.ID
				entry.Changed = true
			}
			graphSymbols = append(graphSymbols, entry)
		}
	}
	// Removed definitions are absent from the source snapshot, but source-side
	// callers that still mention them are exactly the risky context reviewers
	// need. Add their destination-snapshot definition as a graph target.
	for _, mapping := range changed {
		if mapping.Symbol.Change == outlineRemoved {
			graphSymbols = append(graphSymbols, outlineGraphSymbol{Raw: mapping.Raw, ID: mapping.Symbol.ID, Changed: true})
		}
	}
	applyOutlineGraph(result, graphSymbols)

	sort.SliceStable(result.Files, func(i, j int) bool { return result.Files[i].Path < result.Files[j].Path })
	return result, nil
}

func classifyOutlineFile(diff *FileDiff, newSource, oldSource map[string][]byte,
	newRaw, oldRaw map[string][]rawOutlineSymbol) (outlineFile, []outlineChangedRaw) {
	path := diff.Path()
	file := outlineFile{Path: path, OldPath: diff.OldPath}
	if diff.IsBinary {
		file.Skipped = "binary"
		return file, nil
	}
	sourcePath := diff.NewPath
	oldPath := diff.OldPath
	languageSource := newSource[sourcePath]
	if sourcePath == "" {
		languageSource = oldSource[oldPath]
	}
	lang, supported := outlineLanguageForFile(path, languageSource)
	if !supported {
		file.Skipped = "unsupported language"
		return file, nil
	}
	file.Language = lang.Name
	if sourcePath != "" {
		file.Lines = sourceLineCount(newSource[sourcePath])
	}
	if (sourcePath != "" && generatedSource(newSource[sourcePath])) ||
		(sourcePath == "" && generatedSource(oldSource[oldPath])) {
		file.Skipped = "generated"
		return file, nil
	}
	newSymbols, oldSymbols := newRaw[sourcePath], oldRaw[oldPath]
	newLines, oldLines := changedLineSets(diff)
	newTouched := directlyTouchedSymbols(newSymbols, newLines)
	oldTouched := directlyTouchedSymbols(oldSymbols, oldLines)

	oldGroups := groupRawSymbols(oldSymbols)
	newGroups := groupRawSymbols(newSymbols)
	oldToNew, newToOld := map[int]int{}, map[int]int{}
	for key, oldIdxs := range oldGroups {
		newIdxs := newGroups[key]
		for i := 0; i < min(len(oldIdxs), len(newIdxs)); i++ {
			oldToNew[oldIdxs[i]] = newIdxs[i]
			newToOld[newIdxs[i]] = oldIdxs[i]
		}
	}

	var mappings []outlineChangedRaw
	for ni := range newSymbols {
		oi, paired := newToOld[ni]
		if !newTouched[ni] && !(paired && oldTouched[oi]) {
			continue
		}
		raw := newSymbols[ni]
		change := outlineAdded
		oldSignature, oldStart := "", 0
		if paired {
			old := oldSymbols[oi]
			oldSignature, oldStart = old.Signature, old.StartLine
			if raw.Signature != old.Signature {
				change = outlineSignatureChanged
			} else {
				change = outlineBodyOnly
			}
		}
		symbol := makeOutlineSymbol(raw, oldPath, change)
		symbol.OldSignature = oldSignature
		symbol.OldStartLine = oldStart
		appendOutlineSymbol(&file, symbol)
		mappings = append(mappings, outlineChangedRaw{Symbol: symbol, Raw: raw})
	}
	for oi := range oldSymbols {
		if !oldTouched[oi] {
			continue
		}
		if ni, paired := oldToNew[oi]; paired && (oldTouched[oi] || newTouched[ni]) {
			continue
		}
		raw := oldSymbols[oi]
		symbol := makeOutlineSymbol(raw, oldPath, outlineRemoved)
		symbol.Path = path
		symbol.OldPath = oldPath
		symbol.OldSignature = raw.Signature
		symbol.Signature = ""
		symbol.OldStartLine = raw.StartLine
		symbol.StartLine, symbol.EndLine = 0, 0
		appendOutlineSymbol(&file, symbol)
		mappings = append(mappings, outlineChangedRaw{Symbol: symbol, Raw: raw})
	}
	if file.changedCount() == 0 {
		file.Skipped = "no changed symbols"
	}
	return file, mappings
}

func makeOutlineSymbol(raw rawOutlineSymbol, oldPath string, change outlineChange) outlineSymbol {
	return outlineSymbol{
		ID:        outlineSymbolID(raw.Path, raw.Kind, raw.Name, raw.StartLine),
		Path:      raw.Path,
		OldPath:   oldPath,
		Language:  raw.Language,
		Name:      raw.Name,
		Kind:      raw.Kind,
		Signature: raw.Signature,
		Change:    change,
		StartLine: raw.StartLine,
		EndLine:   raw.EndLine,
		Test:      raw.Test,
	}
}

func appendOutlineSymbol(file *outlineFile, symbol outlineSymbol) {
	if symbol.Test {
		file.TestSymbols = append(file.TestSymbols, symbol)
	} else {
		file.Symbols = append(file.Symbols, symbol)
	}
}

func groupRawSymbols(symbols []rawOutlineSymbol) map[string][]int {
	groups := map[string][]int{}
	for i, symbol := range symbols {
		groups[symbol.pairKey()] = append(groups[symbol.pairKey()], i)
	}
	return groups
}

func changedLineSets(diff *FileDiff) (newLines, oldLines map[int]bool) {
	newLines, oldLines = map[int]bool{}, map[int]bool{}
	for _, hunk := range diff.Hunks {
		for _, line := range hunk.Lines {
			switch line.Kind {
			case LineAdd:
				newLines[line.NewNo] = true
			case LineDel:
				oldLines[line.OldNo] = true
			}
		}
	}
	return newLines, oldLines
}

func sourceLineCount(source []byte) int {
	if len(source) == 0 {
		return 0
	}
	return strings.Count(string(source), "\n") + 1
}

func rawOutlineKey(raw rawOutlineSymbol) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d", raw.Path, raw.Kind, raw.Name, raw.StartLine)
}

func applyOutlineGraph(result *outlineResult, symbols []outlineGraphSymbol) {
	byName := map[string][]outlineGraphSymbol{}
	changed := map[string]*outlineSymbol{}
	for _, graph := range symbols {
		byName[graph.Raw.Name] = append(byName[graph.Raw.Name], graph)
	}
	for fi := range result.Files {
		for si := range result.Files[fi].Symbols {
			changed[result.Files[fi].Symbols[si].ID] = &result.Files[fi].Symbols[si]
		}
		for si := range result.Files[fi].TestSymbols {
			changed[result.Files[fi].TestSymbols[si].ID] = &result.Files[fi].TestSymbols[si]
		}
	}
	edgeSeen := map[string]bool{}
	for _, caller := range symbols {
		if caller.Raw.Test {
			continue
		}
		for _, name := range caller.Raw.Calls {
			candidates := append([]outlineGraphSymbol(nil), byName[name]...)
			if len(candidates) == 0 {
				continue
			}
			sort.SliceStable(candidates, func(i, j int) bool {
				a, b := outlinePathScore(caller.Raw.Path, candidates[i].Raw.Path), outlinePathScore(caller.Raw.Path, candidates[j].Raw.Path)
				if a != b {
					return a > b
				}
				if candidates[i].Raw.Path != candidates[j].Raw.Path {
					return candidates[i].Raw.Path < candidates[j].Raw.Path
				}
				return candidates[i].Raw.StartLine < candidates[j].Raw.StartLine
			})
			ambiguous := len(candidates) > 1
			best := candidates[0]
			if best.ID == caller.ID {
				continue
			}
			callerRef := outlineRef{ID: caller.ID, Path: caller.Raw.Path, Name: caller.Raw.Name, Kind: caller.Raw.Kind, Line: caller.Raw.StartLine, Changed: caller.Changed, Ambiguous: ambiguous}
			calleeRef := outlineRef{ID: best.ID, Path: best.Raw.Path, Name: best.Raw.Name, Kind: best.Raw.Kind, Line: best.Raw.StartLine, Changed: best.Changed, Ambiguous: ambiguous}
			if target := changed[best.ID]; target != nil {
				target.Callers = append(target.Callers, callerRef)
			}
			if source := changed[caller.ID]; source != nil {
				source.Callees = append(source.Callees, calleeRef)
			}
			if caller.Changed && best.Changed {
				key := caller.ID + "\x00" + best.ID
				if !edgeSeen[key] {
					edgeSeen[key] = true
					result.Edges = append(result.Edges, outlineEdge{From: caller.ID, To: best.ID})
				}
			}
		}
	}
	for _, symbol := range changed {
		symbol.Callers = uniqueOutlineRefs(symbol.Callers)
		symbol.Callees = uniqueOutlineRefs(symbol.Callees)
		symbol.FanIn = len(symbol.Callers)
	}
}

func outlinePathScore(from, to string) int {
	if from == to {
		return 10_000
	}
	fromDir, toDir := filepath.Dir(from), filepath.Dir(to)
	score := 0
	if fromDir == toDir {
		score += 5_000
	}
	a, b := strings.Split(filepath.ToSlash(fromDir), "/"), strings.Split(filepath.ToSlash(toDir), "/")
	for i := 0; i < min(len(a), len(b)) && a[i] == b[i]; i++ {
		score += 100
	}
	return score
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = filepath.ToSlash(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
