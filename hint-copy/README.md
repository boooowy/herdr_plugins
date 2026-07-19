# hint-copy

herdr の画面に表示されている文字列を、キーボードだけでクリップボードにコピーする herdr プラグイン。tmux-thumbs / vimium のヒントラベル方式。行単位・範囲のコピー(行モード)、vim風のカーソル選択+スクロールバック(copyモード)にも対応。

分割レイアウトでは**タブ全体を色付きで合成再現**します(各ペインを ANSI キャプチャして色・ハイライトごと再描画、枠線はフォーカス紫+テーマ色、枠内タイトルも表示)。起動してもペインの位置は動いて見えず、対象ペインにラベルが重なるだけです。枠線の色は `border_fg` / `border_focus_fg` で調整できます(既定は dracula 相当)。

```
┌─ overlay ──────────────────────────────┐
│ Deployed to attps://example.com/app    │   ← 候補の先頭1〜2文字が
│ Config at stc/nginx/nginx.conf         │      ラベルに置き換わる
│ Server d92.168.1.10 responded          │
│                                        │
│ type a label to copy · space: line mode│
└────────────────────────────────────────┘
```

キーバインドを押す → 画面が候補ラベル付きで再表示される → ラベルのキーを押す → その文字列がクリップボードに入り、トーストで通知される。

## インストール

```sh
# ローカル開発(このリポジトリをそのまま登録)
make plugin-link

# または GitHub から
herdr plugin install boooowy/herdr_plugins/hint-copy
```

ビルドには Go が必要です(`[[build]]` が `go build` を実行)。

`~/.config/herdr/config.toml` にキーバインドを追加:

```toml
[[keys.command]]
key = "prefix+y"
type = "plugin_action"
command = "kayakatu.hint-copy.copy"
description = "hint-copy: pick and copy"

[[keys.command]]
key = "cmd+alt+8"
type = "plugin_action"
command = "kayakatu.hint-copy.copy-mode"
description = "hint-copy: copy mode (scrollback)"
```

## 操作

| キー | 動作 |
| --- | --- |
| ラベル(a, s, d, ...) | その候補をコピーして閉じる。候補が25個以上のときは2文字ラベル |
| Space | トークンモード ⇔ 行モードの切替 |
| [ | copyモードに入る(予約キー) |
| ? | ヘルプ表示(トークン/行モードのみ。任意のキーで戻る) |
| Esc / Ctrl-C | キャンセル |
| その他のキー | 入力中のラベルをリセット |

同じ文字列が画面に複数回出ている場合は同じラベルを共有します。

### 行モード(複数行コピー)

オーバーレイ内で Space を押すと各行の左にラベルが付きます:

| 操作 | 動作 |
| --- | --- |
| ラベル1つ目 | その行を**アンカー**として選択(反転表示) |
| Enter / 同じラベル | アンカーの1行をコピー(前後の空白は除去) |
| 別のラベル | アンカーからその行までの**範囲**をコピー(インデント・途中の空行は保持) |

URL等の候補が1つも無い画面では、最初から行モードで開きます。

### copyモード(vim風選択+スクロールバック)

オーバーレイ内で `[`、または `kayakatu.hint-copy.copy-mode` アクション(キーバインド例: `cmd+alt+8`)で直接起動。herdr 本体の copy モードと同じキー感覚で、スクロールバック(既定5000行、`scrollback_lines` で変更可)を遡ってコピーできます。

| キー | 動作 |
| --- | --- |
| h j k l / 矢印 | カーソル移動 |
| w / b / e | 単語移動(行を跨ぐ) |
| { / } | 段落(空行)単位の移動 |
| 0 / ^ / $ | 行頭 / 最初の非空白 / 行末 |
| g / G | バッファ先頭 / 末尾 |
| Ctrl+f / Ctrl+b | 1ページ移動 |
| Ctrl+u / Ctrl+d | 半ページ移動 |
| v / Space | 文字単位の選択開始・解除 |
| V | 行単位の選択開始・解除 |
| y / Enter | 選択範囲をコピーして閉じる(選択が必要) |
| / と ? | 前方 / 後方検索(リテラル・smart-case、インクリメンタル) |
| n / N | 次 / 前のマッチへ(ラップアラウンド) |
| q / Esc | 終了(Esc は 検索入力 → 選択 → 検索ハイライト を先に解除する段階式) |

copyモード中の `?` は後方検索です(ヘルプはトークン/行モードの `?` から)。

## 抽出パターン

| パターン | 例 | コピーされるもの |
| --- | --- | --- |
| markdown_url | `[docs](https://x.com/d)` | URL部分のみ |
| url | `https://example.com/app` | 全体 |
| email | `user@example.com` | 全体 |
| diff_path | `+++ b/src/main.go` | `src/main.go`(a/ b/ 除去) |
| uuid | `123e4567-e89b-…` | 全体 |
| docker | `sha256:0abc…(64桁)` | 全体 |
| ipv4 | `192.168.1.10:8080` | 全体(ポート含む) |
| color | `#a1b2c3` `#fff` | 全体 |
| ipv6(既定off) | `fe80::1` | 全体 |
| sha | `3f2a91c` | 全体 |
| address | `0xDEADbeef` | 全体 |
| linenum | `main.go:123` `x.ts:45:12` | 全体 |
| kubernetes | `deployment.apps/zookeeper` | 全体 |
| quoted | `"hello"` `'/tmp/x.log'` | 中身のみ(クォート除去) |
| path | `/etc/nginx/nginx.conf` `~/x` | 全体 |
| datetime | `2026-07-19T12:34:56Z` | 全体 |
| semver | `v1.2.3` `1.2.3-rc.1` | 全体 |
| number | `8080`(4桁以上) | 全体 |

重なった場合は「より長いマッチ → 優先度の高いパターン」の順で勝ちます(URL内のパスはURLが勝つ、`1.2.3.4` は ipv4 が勝つ、など)。

## 設定

`~/.config/herdr/plugins/config/kayakatu.hint-copy/config.toml`(無ければデフォルトで動作):

```toml
alphabet = "asdfghjklqwertyuiopzxcvb"  # ラベルに使うキー(ホームロー優先)
reverse = false        # true: 画面下(プロンプト近く)の候補に早いラベルを割当
osc52 = false          # OSC 52 も /dev/tty に出力(SSH先のクリップボード用)
max_candidates = 100   # ユニーク候補の上限
scrollback_lines = 5000  # copyモードで遡れる行数
sel_bg = "blue"        # copyモードの選択範囲の背景色
search_bg = "magenta"  # copyモードの検索マッチの背景色
border_fg = "#444477"        # 合成表示の枠線色(色名 / 0-255 / #rrggbb)
border_focus_fg = "#bd93f9"  # フォーカスペインの枠線色
hint_fg = "black"      # ラベル色(色名 or 0-255)
hint_bg = "yellow"
match_fg = "green"

[patterns]             # 組み込みパターンの on/off(デフォルトは ipv6 のみ off)
number = false         # off にしたいものだけ書けばよい
datetime = false

[[custom_patterns]]    # 独自パターンを追加できる
name = "jira"
regex = '[A-Z]{2,10}-\d+'

[[custom_patterns]]
name = "ticket-id"     # group でキャプチャグループだけをコピー対象にできる
regex = 'ticket=(\w+)'
group = 1
```

クリップボードは macOS では `pbcopy`、Linux では `wl-copy` → `xclip` → `xsel` の順で使います。

## 開発

```sh
make test         # go test -race ./...
make build        # ./bin/hint-copy にビルド
make plugin-link  # ビルドして herdr に登録
```

デバッグ:

```sh
herdr plugin action invoke copy --plugin kayakatu.hint-copy   # キーなしで起動
herdr plugin log list --plugin kayakatu.hint-copy             # stdout/stderr を確認
```
