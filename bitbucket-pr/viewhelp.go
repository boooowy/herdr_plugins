package main

// helpOverlay is the `?` cheat sheet drawn above the current view. Keeping it
// out of the navigation stack preserves the screen beneath it and lets q/Esc
// close the help without navigating backward.
type helpOverlay struct {
	ctx      string
	top      int
	pageRows int
	maxTop   int
}

type helpLine struct {
	key, desc string
}

var helpByCtx = map[string]struct {
	title string
	lines []helpLine
}{
	"picker": {"リポジトリ選択", []helpLine{
		{"j / k / ↑ / ↓", "移動"},
		{"Tab / s / l / h / 1-3  (→/←)", "ビュー切替 (Repos / My PRs / Review)"},
		{"Enter", "Repos: PR一覧を開く / My PRs・Review: PR詳細を開く"},
		{"/", "絞り込み (Repos: 名前/説明、PR: 番号/リポジトリ/タイトル/著者/ブランチ)"},
		{"C", "ローカルにclone (y で確定。既存checkoutがあれば関連付けのみ)"},
		{"y", "Repos: clone URL / PR: PR URL をコピー"},
	}},
	"list": {"PR一覧", []helpLine{
		{"j / k / ↑ / ↓", "移動"},
		{"Enter", "PR詳細を開く"},
		{"s / Tab / l / h  (→/←)", "state フィルタ切替 (OPEN/MERGED/…)"},
		{"/", "絞り込み (番号/タイトル/著者/ブランチ、Esc で解除)"},
		{"y", "PR URL をコピー"},
		{"b", "source branch 名をコピー"},
	}},
	"detail": {"PR詳細", []helpLine{
		{"Tab / Shift-Tab / 1-4", "タブ切替 (Overview/Outline/Files/Comments)"},
		{"l / h  (→/←)", "タブ切替 (次 / 前)"},
		{"j / k / ↑ / ↓", "移動 (Outline/Comments: 左リスト移動、右にプレビュー)"},
		{"Enter", "Outline: 展開/diffへ / Files: diffツール / Comments: コードへ"},
		{"hunk: + / c", "diff行にdraft noteを追加 (終了後にコメント投稿を確認)"},
		{"gg / gd / gr", "Outline: 先頭 / 変更callee / 変更callerへ移動"},
		{"Ctrl-o / Ctrl-i", "Outline: シンボル移動履歴を戻る / 進む"},
		{"O", "Outline: 依存順 / アルファベット順を切替"},
		{"Ctrl-d / Ctrl-u", "Outline/Comments: 右プレビューをスクロール"},
		{"v", "内蔵diffビューアで開く (Filesタブ)"},
		{"/", "絞り込み (Outline: シンボル/パス、Files: パス、Comments: 本文/著者/パス)"},
		{"c", "コメント投稿 (Comments のコメント上ではそこへ返信 — 返信行なら入れ子返信)"},
		{"x", "Comments: 選択コメントを削除 (y で確定)"},
		{"s", "Comments: スレッドを resolve / 再オープン (インラインのみ)"},
		{"a / A / X", "PRを承認 / 自分の承認取消 / Decline (yで確定、OPENのみ)"},
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
		{"C", "インラインコメント表示切替"},
		{"v / V", "行選択の開始・解除（j/k で範囲を伸ばす、Esc でも解除）"},
		{"c", "コメント投稿（カーソル行 / v 選択範囲 / スレッドへの返信）"},
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

func (a *app) openHelp(ctx string) {
	a.help = &helpOverlay{ctx: ctx}
}

func (h *helpOverlay) handle(a *app, k Key) {
	switch {
	case isKey(k, 'q') || isKey(k, '?') || k.Kind == KeyEsc:
		a.help = nil
	case isKey(k, 'j') || k.Kind == KeyDown:
		h.top++
	case isKey(k, 'k') || k.Kind == KeyUp:
		h.top--
	case k.Kind == KeyCtrl && k.R == 'd':
		h.top += max(1, h.pageRows/2)
	case k.Kind == KeyCtrl && k.R == 'u':
		h.top -= max(1, h.pageRows/2)
	}
	if h.top < 0 {
		h.top = 0
	}
	if h.top > h.maxTop {
		h.top = h.maxTop
	}
}

func paintHelpOverlay(a *app, s *Screen) {
	h := a.help
	sec := helpByCtx[h.ctx]
	const preferredWidth = 84
	contentWidth := modalContentWidth(s.W, preferredWidth)
	lines := helpModalLines(sec.lines, contentWidth)
	lines = append(lines, modalLine{NoWrap: true})
	lines = append(lines, modalLine{Spans: []Span{{Text: "共通", Style: styleDim}}, NoWrap: true})
	lines = append(lines, helpModalLines(helpCommon, contentWidth)...)
	result := paintModal(s, modalSpec{
		Title:          "キーバインド — " + sec.title,
		Lines:          lines,
		Footer:         modalLine{Spans: []Span{{Text: "j/k・Ctrl-d/u スクロール   q/Esc/? 閉じる", Style: styleDim}}, Center: true},
		PreferredWidth: preferredWidth,
		Top:            h.top,
	})
	h.top = result.Top
	h.pageRows = result.PageRows
	h.maxTop = max(0, result.ContentRows-result.PageRows)
}

func helpModalLines(entries []helpLine, width int) []modalLine {
	var out []modalLine
	if width >= 52 {
		const keyWidth = 24
		descWidth := max(1, width-keyWidth)
		for _, entry := range entries {
			parts := wrapText(entry.desc, descWidth)
			for i, part := range parts {
				key := ""
				if i == 0 {
					key = entry.key
				}
				out = append(out, modalLine{Spans: []Span{
					{Text: padRight(key, keyWidth), Style: styleMeta},
					{Text: part, Style: styleNone},
				}, NoWrap: true})
			}
		}
		return out
	}
	for _, entry := range entries {
		out = append(out, modalLine{Spans: []Span{{Text: entry.key, Style: styleMeta}}, NoWrap: true})
		for _, part := range wrapText(entry.desc, max(1, width-2)) {
			out = append(out, modalLine{Spans: []Span{{Text: "  " + part, Style: styleNone}}, NoWrap: true})
		}
	}
	return out
}
