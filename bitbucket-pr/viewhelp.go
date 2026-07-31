package main

// helpView is the `?` cheat sheet; any key returns. ctx selects which
// screen's keys to show ("list" | "detail" | "diff") — a combined table
// left users guessing where each key works.
type helpView struct {
	ctx string
}

type helpLine struct {
	key, desc string
}

var helpByCtx = map[string]struct {
	title string
	lines []helpLine
}{
	"list": {"PR一覧", []helpLine{
		{"j / k / ↑ / ↓", "移動"},
		{"Enter", "PR詳細を開く"},
		{"s / Tab / l / h  (→/←)", "state フィルタ切替 (OPEN/MERGED/…)"},
		{"y", "PR URL をコピー"},
		{"b", "source branch 名をコピー"},
	}},
	"detail": {"PR詳細", []helpLine{
		{"Tab / Shift-Tab / 1-3", "タブ切替 (Overview/Files/Comments)"},
		{"l / h  (→/←)", "タブ切替 (次 / 前)"},
		{"j / k / ↑ / ↓", "移動 (Comments: 左リスト移動、右にプレビュー)"},
		{"Enter", "Files: diffツールで開く / Comments: コードへジャンプ"},
		{"Ctrl-d / Ctrl-u", "Comments: 右プレビューをスクロール"},
		{"v", "内蔵diffビューアで開く (Filesタブ)"},
		{"C", "コメント投稿 (Comments のコメント上ではそこへ返信 — 返信行なら入れ子返信)"},
		{"x", "Comments: 選択コメントを削除 (y で確定)"},
		{"s", "Comments: スレッドを resolve / 再オープン (インラインのみ)"},
		{"D", "PR全体のdiffをdiffツールで開く"},
		{"y", "PR URL をコピー"},
		{"b", "source branch 名をコピー"},
	}},
	"diff": {"diffビューア", []helpLine{
		{"j / k / ↑ / ↓", "移動"},
		{"]h / [h", "次 / 前の hunk"},
		{"]f / [f  (→/←)", "次 / 前のファイル"},
		{"Enter / za", "hunk 折畳トグル"},
		{"zA", "全 hunk 折畳トグル"},
		{"w", "長い行の折返し切替"},
		{"c", "インラインコメント表示切替"},
		{"v / V", "行選択の開始・解除（j/k で範囲を伸ばす、Esc でも解除）"},
		{"C", "コメント投稿（カーソル行 / v 選択範囲 / スレッドへの返信）"},
		{"Ctrl-f / Ctrl-b", "1ページスクロール"},
		{"D", "PR全体のdiffをdiffツールで開く"},
	}},
}

var helpCommon = []helpLine{
	{"g / G", "先頭 / 末尾"},
	{"Ctrl-d / Ctrl-u", "半ページスクロール"},
	{"r", "再読込"},
	{"o", "ブラウザで開く"},
	{"?", "このヘルプ"},
	{"q / Esc", "戻る / 終了"},
}

func (h helpView) render(a *app, s *Screen) {
	sec := helpByCtx[h.ctx]
	s.WriteString(1, 0, "bitbucket-pr — キーバインド ("+sec.title+")", styleTitle, a.w)
	paintSeparator(s, 1, a.w)
	y := 2
	write := func(l helpLine) {
		s.WriteString(2, y, padRight(l.key, 24), styleMeta, a.w)
		s.WriteString(27, y, l.desc, styleNone, a.w)
		y++
	}
	for _, l := range sec.lines {
		write(l)
	}
	y++
	s.WriteString(2, y, "共通", styleDim, a.w)
	y++
	for _, l := range helpCommon {
		write(l)
	}
}

func (helpView) handle(a *app, k Key) { a.pop() }

func (helpView) footer(a *app) string { return "任意のキーで戻る" }
