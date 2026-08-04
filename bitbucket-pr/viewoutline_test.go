package main

import (
	"errors"
	"strings"
	"testing"
)

func outlineFixture() *outlineResult {
	callee := outlineSymbol{
		ID: "callee", Path: "acallee/service.go", Language: "Go", Name: "NewService", Kind: "function",
		Signature: "func NewService(name string) *Service", OldSignature: "func NewService() *Service",
		Change: outlineSignatureChanged, StartLine: 10, EndLine: 14, OldStartLine: 10, FanIn: 4,
		Callers: []outlineRef{{ID: "caller", Path: "zcaller/main.go", Name: "main", Kind: "function", Line: 7, Changed: true}},
	}
	caller := outlineSymbol{
		ID: "caller", Path: "zcaller/main.go", Language: "Go", Name: "main", Kind: "function",
		Signature: "func main()", Change: outlineBodyOnly, StartLine: 5, EndLine: 12,
		Callees: []outlineRef{{ID: "callee", Path: "acallee/service.go", Name: "NewService", Kind: "function", Line: 10, Changed: true}},
	}
	testSymbol := outlineSymbol{
		ID: "caller-test", Path: "zcaller/main.go", Language: "Go", Name: "TestMain", Kind: "function",
		Signature: "func TestMain(t *testing.T)", Change: outlineAdded, StartLine: 20, EndLine: 24, Test: true,
	}
	return &outlineResult{
		Schema: outlineSchemaVersion, SourceHash: "source", DestinationHash: "destination",
		Files: []outlineFile{
			{Path: "acallee/service.go", Language: "Go", Lines: 900, Symbols: []outlineSymbol{callee}},
			{Path: "zcaller/main.go", Language: "Go", Lines: 40, Symbols: []outlineSymbol{caller}, TestSymbols: []outlineSymbol{testSymbol}},
		},
		Edges: []outlineEdge{{From: "caller", To: "callee"}},
	}
}

func outlineFixtureApp(width int) (*app, *detailView, *prDetail) {
	a := &app{w: width, h: 30, detail: map[int]*prDetail{}}
	d := a.detailFor(7)
	d.pr = &PullRequest{ID: 7, Title: "outline fixture", State: "OPEN"}
	d.pr.Source.Commit.Hash = "source"
	d.pr.Destination.Commit.Hash = "destination"
	d.diffText = "diff --git a/acallee/service.go b/acallee/service.go\n"
	d.diffstat = []DiffStatEntry{{Status: "modified", New: &struct {
		Path string `json:"path"`
	}{Path: "acallee/service.go"}}}
	d.files = []FileDiff{{
		NewPath: "acallee/service.go",
		Hunks: []Hunk{{OldStart: 10, OldCount: 1, NewStart: 10, NewCount: 2, Lines: []DiffLine{
			{Kind: LineDel, OldNo: 10, Text: "func NewService() *Service"},
			{Kind: LineAdd, NewNo: 10, Text: "func NewService(name string) *Service"},
		}}},
	}}
	d.comments = []Comment{}
	d.outline = outlineFixture()
	v := &detailView{prID: 7, tab: tabOutline}
	return a, v, d
}

func outlineRowTexts(rows []Row) string {
	var b strings.Builder
	for _, row := range rows {
		b.WriteString(spansText(row))
		b.WriteByte('\n')
	}
	return b.String()
}

func outlineDirOrder(rows []Row) []string {
	var paths []string
	for _, row := range rows {
		if ref, ok := row.Item.(outlineRowRef); ok && ref.Kind == "dir" {
			paths = append(paths, ref.Path)
		}
	}
	return paths
}

func smellOutlineFixture() *outlineResult {
	godBloat := outlineSymbol{
		ID: "godbloat", Path: "util/helpers.go", Language: "Go", Name: "DoAll", Kind: "fn",
		Signature: "func DoAll(v int) error", OldSignature: "func DoAll(v int) error",
		Change: outlineBodyOnly, StartLine: 1, EndLine: 60, OldStartLine: 1, OldEndLine: 20,
		FanIn: 5, Callers: []outlineRef{
			{ID: "c1", Path: "api/a.go", Name: "a", Kind: "fn", Line: 1},
			{ID: "c2", Path: "api/b.go", Name: "b", Kind: "fn", Line: 1},
			{ID: "c3", Path: "batch/c.go", Name: "c", Kind: "fn", Line: 1},
			{ID: "c4", Path: "batch/d.go", Name: "d", Kind: "fn", Line: 1},
			{ID: "c5", Path: "cli/e.go", Name: "e", Kind: "fn", Line: 1},
		},
	}
	big := outlineSymbol{
		ID: "big", Path: "util/helpers.go", Language: "Go", Name: "BuildEverything", Kind: "fn",
		Signature: "func BuildEverything() error", Change: outlineAdded, StartLine: 70, EndLine: 159,
	}
	bigStruct := outlineSymbol{
		ID: "bigstruct", Path: "util/helpers.go", Language: "Go", Name: "Registry", Kind: "struct",
		Signature: "type Registry struct", Change: outlineAdded, StartLine: 160, EndLine: 400,
	}
	clean := outlineSymbol{
		ID: "clean", Path: "app/main.go", Language: "Go", Name: "main", Kind: "fn",
		Signature: "func main()", Change: outlineBodyOnly, StartLine: 5, EndLine: 12,
	}
	return &outlineResult{
		Schema: outlineSchemaVersion, SourceHash: "source", DestinationHash: "destination",
		Files: []outlineFile{
			{Path: "app/main.go", Language: "Go", Lines: 40, Symbols: []outlineSymbol{clean}},
			{Path: "util/helpers.go", Language: "Go", Lines: 400, Symbols: []outlineSymbol{godBloat, big, bigStruct}},
		},
	}
}

func TestOutlineRowsShowSmellChips(t *testing.T) {
	a, v, d := outlineFixtureApp(120)
	d.outline = smellOutlineFixture()
	text := outlineRowTexts(v.outlineRows(a, d))
	for _, want := range []string{" god ", " bloat:+40 ", " big:90 "} {
		if !strings.Contains(text, want) {
			t.Errorf("outline rows missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "smell:") {
		t.Errorf("count-style smell badge must be gone:\n%s", text)
	}
	sawHelperFileChips := false
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "helpers.go") && strings.Contains(line, " bloat ") && strings.Contains(line, " big ") {
			sawHelperFileChips = true
		}
		if strings.Contains(line, "Registry") && (strings.Contains(line, "big") || strings.Contains(line, "bloat")) {
			t.Errorf("struct symbol must not carry size chips: %q", line)
		}
		if strings.Contains(line, "main.go") && (strings.Contains(line, "bloat") || strings.Contains(line, "big") || strings.Contains(line, "god")) {
			t.Errorf("clean file must not carry smell chips: %q", line)
		}
	}
	if !sawHelperFileChips {
		t.Errorf("file row must aggregate smell kinds as chips:\n%s", text)
	}
}

func TestOutlinePreviewExplainsSmells(t *testing.T) {
	a, v, d := outlineFixtureApp(120)
	d.outline = smellOutlineFixture()
	text := outlineRowTexts(v.outlinePreviewRows(a, d, outlineRowRef{Kind: "symbol", ID: "godbloat"}, 80))
	for _, want := range []string{"god: 5箇所・3ディレクトリから呼ばれています", "bloat: シグネチャは同じまま本体が 20行 → 60行 (+40) に増えています"} {
		if !strings.Contains(text, want) {
			t.Errorf("preview missing %q:\n%s", want, text)
		}
	}
	text = outlineRowTexts(v.outlinePreviewRows(a, d, outlineRowRef{Kind: "symbol", ID: "big"}, 80))
	if !strings.Contains(text, "big: 新規追加の関数が90行あります") {
		t.Errorf("preview missing big explanation:\n%s", text)
	}
	text = outlineRowTexts(v.outlinePreviewRows(a, d, outlineRowRef{Kind: "symbol", ID: "clean"}, 80))
	if strings.Contains(text, "god:") || strings.Contains(text, "bloat:") {
		t.Errorf("clean symbol preview has smell rows:\n%s", text)
	}
}

func TestOutlineSearchFindsSmellTokens(t *testing.T) {
	for _, query := range []string{"bloat", "肥大"} {
		a, v, d := outlineFixtureApp(120)
		d.outline = smellOutlineFixture()
		v.search[tabOutline].buf = []byte(query)
		text := outlineRowTexts(v.outlineRows(a, d))
		if !strings.Contains(text, "DoAll") || strings.Contains(text, "BuildEverything") || strings.Contains(text, "main.go") {
			t.Fatalf("smell-filtered outline (%s) =\n%s", query, text)
		}
	}
}

func TestOutlineRowsShowAttentionBadgesAndCollapsedTests(t *testing.T) {
	a, v, d := outlineFixtureApp(120)
	rows := v.outlineRows(a, d)
	text := outlineRowTexts(rows)
	for _, want := range []string{"3 changed symbols (tests:1) in 2/2 files", "! v acallee", "api:1", "fan-in:4", "lines:900", "~ function NewService", "> 1 tests"} {
		if !strings.Contains(text, want) {
			t.Errorf("outline rows missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "TestMain") {
		t.Errorf("test symbols must start collapsed:\n%s", text)
	}
}

func TestOutlineRowsKeepSkipReasonsWhenNoSymbolsAreParsed(t *testing.T) {
	a, v, d := outlineFixtureApp(120)
	d.outline = &outlineResult{Files: []outlineFile{{Path: "README.md", Skipped: "unsupported language"}}}
	text := outlineRowTexts(v.outlineRows(a, d))
	if !strings.Contains(text, "0 changed symbols in 0/1 files") ||
		!strings.Contains(text, "README.md (skipped: unsupported language)") {
		t.Fatalf("all-skipped outline =\n%s", text)
	}
}

func TestOutlineRowsDependencyAndAlphabeticalOrder(t *testing.T) {
	a, v, d := outlineFixtureApp(120)
	if got := outlineDirOrder(v.outlineRows(a, d)); len(got) < 2 || got[0] != "zcaller" || got[1] != "acallee" {
		t.Fatalf("dependency order = %v", got)
	}
	v.outline.alphabetical = true
	if got := outlineDirOrder(v.outlineRows(a, d)); len(got) < 2 || got[0] != "acallee" || got[1] != "zcaller" {
		t.Fatalf("alphabetical order = %v", got)
	}
}

func TestOutlineSearchFindsSymbolAndPath(t *testing.T) {
	a, v, d := outlineFixtureApp(120)
	v.search[tabOutline].buf = []byte("NewService")
	text := outlineRowTexts(v.outlineRows(a, d))
	if !strings.Contains(text, "NewService") || strings.Contains(text, "main.go") {
		t.Fatalf("filtered outline =\n%s", text)
	}
}

func TestOutlineRenderWideSplitAndNarrowFallback(t *testing.T) {
	a, v, _ := outlineFixtureApp(120)
	v.rebuild(a)
	s := NewScreen(a.w, a.h)
	v.render(a, s)
	leftW := outlineMasterWidth(a.w)
	if c := s.at(leftW, 4); c.R != '│' {
		t.Fatalf("wide separator = %q", c.R)
	}
	painted := false
	for y := 4; y < a.h-1 && !painted; y++ {
		for x := leftW + 1; x < a.w; x++ {
			if c := s.at(x, y); c.R != ' ' && c.R != 0 {
				painted = true
				break
			}
		}
	}
	if !painted {
		t.Fatal("wide Outline preview is empty")
	}

	a.w = 80
	v.rebuild(a)
	s = NewScreen(a.w, a.h)
	v.render(a, s)
	if strings.Contains(screenRow(s, 4), "│") {
		t.Fatalf("narrow Outline unexpectedly uses split: %q", screenRow(s, 4))
	}
}

func TestOutlineWithoutCheckoutDoesNotStartAnalysis(t *testing.T) {
	a := &app{w: 100, h: 30, detail: map[int]*prDetail{}}
	d := a.detailFor(7)
	d.pr = &PullRequest{ID: 7}
	d.diffText = "diff"
	v := &detailView{prID: 7, tab: tabOutline}
	v.ensureOutline(a, d)
	if !d.outlineStarted || d.outlineLoading || !strings.Contains(d.outlineErr, "ローカルcheckout") {
		t.Fatalf("outline state = started:%v loading:%v err:%q", d.outlineStarted, d.outlineLoading, d.outlineErr)
	}
}

func TestOutlineEnterOpensDiffAtSymbolLine(t *testing.T) {
	a, v, d := outlineFixtureApp(120)
	a.stack = []view{v}
	v.rebuild(a)
	for i, row := range v.vp[tabOutline].Rows {
		if ref, ok := row.Item.(outlineRowRef); ok && ref.ID == "callee" {
			v.vp[tabOutline].Cursor = i
			break
		}
	}
	v.activateOutlineRow(a)
	if len(a.stack) != 2 {
		t.Fatalf("stack size = %d", len(a.stack))
	}
	diff, ok := a.top().(*diffView)
	if !ok || diff.pendingLine != 10 || diff.pendingOldLine {
		t.Fatalf("opened view = %#v", a.top())
	}
	diff.tryPendingJump(20)
	if diff.pendingLine != 0 {
		t.Fatalf("pending line was not resolved: %d", diff.pendingLine)
	}
	line, ok := diff.vp.Current().Item.(DiffLine)
	if !ok || line.NewNo != 10 {
		t.Fatalf("selected diff line = %#v (detail=%p)", diff.vp.Current(), d)
	}
}

func TestOutlineGDGRAndHistory(t *testing.T) {
	a, v, _ := outlineFixtureApp(120)
	v.rebuild(a)
	if !v.jumpToOutlineSymbol(a, "caller", false) {
		t.Fatal("caller row not found")
	}
	v.openOutlineReferences(a, false)
	if got := v.currentOutlineSymbol(a); got == nil || got.ID != "callee" {
		t.Fatalf("gd target = %#v", got)
	}
	v.navigateOutlineHistory(a, -1)
	if got := v.currentOutlineSymbol(a); got == nil || got.ID != "caller" {
		t.Fatalf("history target = %#v, status=%q", got, a.status)
	}
}

func TestOutlineUserErrorIdentifiesMissingSide(t *testing.T) {
	source := "68a5131400000000000000000000000000000000"
	destination := "b83aab7700000000000000000000000000000000"
	cases := []struct {
		name      string
		err       string
		want      string
		fetchable bool
	}{
		{"destination missing", "commit b83aab775731 がローカルにありません: fatal", "マージ先ブランチのcommit b83aab77", true},
		{"source missing", "commit 68a513140000 がローカルにありません: fatal", "PRブランチのcommit 68a51314", true},
		{"unknown hash", "commit 000000000000 がローカルにありません: fatal", "PRのcommit がローカルにありません", true},
		{"other error", "git ls-tree 68a513140000: exit status 128", "Outlineを解析できません", false},
	}
	for _, tc := range cases {
		msg, fetchable := outlineUserError("/repo", source, destination, errors.New(tc.err))
		if !strings.Contains(msg, tc.want) {
			t.Errorf("%s: message %q does not contain %q", tc.name, msg, tc.want)
		}
		if fetchable != tc.fetchable {
			t.Errorf("%s: fetchable = %v, want %v", tc.name, fetchable, tc.fetchable)
		}
	}
}

func TestOutlineFetchKeyOpensConfirmation(t *testing.T) {
	a, v, d := outlineFixtureApp(120)
	a.ctx.RepoDir = "/repo"

	// Without the missing-commit error F stays inert.
	if v.handleOutlineKey(a, Key{Kind: KeyRune, R: 'F'}) {
		t.Fatal("F was consumed without a fetchable error")
	}

	d.outlineFetchable = true
	if !v.handleOutlineKey(a, Key{Kind: KeyRune, R: 'F'}) {
		t.Fatal("F was not consumed in the fetchable error state")
	}
	if v.pendingAction.kind != detailActionFetch {
		t.Fatalf("pending action = %v, want detailActionFetch", v.pendingAction.kind)
	}
}
