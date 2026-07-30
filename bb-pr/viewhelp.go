package main

// helpView is the `?` cheat sheet; any key returns.
type helpView struct{}

var helpLines = []struct {
	key, desc string
}{
	{"j / k / ↑ / ↓", "移動"},
	{"Ctrl-d / Ctrl-u", "半ページスクロール"},
	{"Ctrl-f / Ctrl-b", "1ページスクロール (diff)"},
	{"g / G", "先頭 / 末尾"},
	{"Enter", "開く (一覧→詳細→diffツール) / hunk 折畳"},
	{"v", "内蔵diffビューア (Filesタブ・コメント表示付き)"},
	{"Tab / Shift-Tab / 1-3", "詳細タブ切替"},
	{"e", "説明文の全文表示 (Overview)"},
	{"s / Tab / Shift-Tab", "state フィルタ切替 (一覧)"},
	{"]h / [h", "次 / 前の hunk (diff)"},
	{"]f / [f  (→/←)", "次 / 前のファイル (diff)"},
	{"za / zA", "hunk 折畳 / 全折畳 (diff)"},
	{"c", "インラインコメント表示切替 (diff)"},
	{"D", "PR全体のdiffをdiffツールで開く (詳細/diff)"},
	{"r", "再読込"},
	{"o", "ブラウザで開く"},
	{"y", "PR URL をコピー (詳細)"},
	{"?", "このヘルプ"},
	{"q / Esc", "戻る / 終了"},
}

func (helpView) render(a *app, s *Screen) {
	s.WriteString(1, 0, "bb-pr — キーバインド", styleTitle, a.w)
	paintSeparator(s, 1, a.w)
	for i, l := range helpLines {
		y := 2 + i
		s.WriteString(2, y, padRight(l.key, 24), styleMeta, a.w)
		s.WriteString(27, y, l.desc, styleNone, a.w)
	}
}

func (helpView) handle(a *app, k Key) { a.pop() }

func (helpView) footer(a *app) string { return "任意のキーで戻る" }
