# hint-copy

herdr の画面に表示されている URL・ファイルパス・IPアドレス・git SHA・メールアドレス・UUID を、キーボードだけでクリップボードにコピーする herdr プラグイン。tmux-thumbs / vimium のヒントラベル方式。

```
┌─ overlay ──────────────────────────────┐
│ Deployed to attps://example.com/app    │   ← 候補の先頭1〜2文字が
│ Config at stc/nginx/nginx.conf         │      ラベルに置き換わる
│ Server d92.168.1.10 responded          │
│                                        │
│ type a label to copy · esc to cancel   │
└────────────────────────────────────────┘
```

キーバインドを押す → 画面が候補ラベル付きで再表示される → ラベルのキーを押す → その文字列がクリップボードに入り、トーストで通知される。

## インストール

```sh
# ローカル開発(このリポジトリをそのまま登録)
make plugin-link

# または GitHub から
herdr plugin install <owner>/herdr_plugins/hint-copy
```

ビルドには Go が必要です(`[[build]]` が `go build` を実行)。

`~/.config/herdr/config.toml` にキーバインドを追加:

```toml
[[keys.command]]
key = "prefix+y"
type = "plugin_action"
command = "kayakatu.hint-copy.copy"
description = "hint-copy: pick and copy"
```

## 操作

| キー | 動作 |
|---|---|
| ラベル(a, s, d, ...) | その候補をコピーして閉じる。候補が25個以上のときは2文字ラベル |
| Esc / Ctrl-C | キャンセル |
| その他のキー | 入力中のラベルをリセット |

同じ文字列が画面に複数回出ている場合は同じラベルを共有します。

## 設定

`~/.config/herdr/plugins/config/kayakatu.hint-copy/config.toml`(無ければデフォルトで動作):

```toml
alphabet = "asdfghjklqwertyuiopzxcvb"  # ラベルに使うキー(ホームロー優先)
reverse = false        # true: 画面下(プロンプト近く)の候補に早いラベルを割当
osc52 = false          # OSC 52 も /dev/tty に出力(SSH先のクリップボード用)
max_candidates = 100   # ユニーク候補の上限
hint_fg = "black"      # ラベル色(色名 or 0-255)
hint_bg = "yellow"
match_fg = "green"

[patterns]             # 組み込みパターンの on/off(デフォルトは ipv6 のみ off)
url = true
path = true
ipv4 = true
ipv6 = false
sha = true
email = true
uuid = true

[[custom_patterns]]    # 独自パターンを追加できる
name = "jira"
regex = '[A-Z]{2,10}-\d+'
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

### 実装メモ

- action(キーバインド側)はフォーカスペインを解決して `pane.read`(visible)で候補の有無をプリチェックし、`plugin.pane.open` でオーバーレイを開く。overlay 配置では `target_pane_id` を渡してはいけない(herdr がアクティブペインに自動アンカーする)。対象ペインIDは env `HINTCOPY_TARGET_PANE_ID` で UI に渡す
- UI(オーバーレイ側)は自分の `HERDR_PANE_ID` ではなく `HINTCOPY_TARGET_PANE_ID` を読み直す
- UIプロセスの終了で herdr がオーバーレイを自動クローズする
