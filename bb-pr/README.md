# bb-pr — Bitbucket PR Viewer

Bitbucket Cloud のプルリクエストを herdr のペイン内で閲覧・コメントするビューアです。
PR 一覧 → 詳細（説明・レビュアー・変更ファイル・コメント）→ hunk 単位の diff（インラインコメント付き）
の閲覧に加え、`C` キーでコード行へのインラインコメント・返信・PRコメントを投稿できます
（本文はいつものエディタが popup で開きます）。approve / merge は非対応です（`o` でブラウザへ）。

- plugin ID: `boooowy.bb-pr`
- version: `0.6.0`
- platforms: macOS / Linux

Files タブでファイルを Enter すると、PR 全体の diff が**外部 diff ツール**
（デフォルト: [hunk](https://github.com/modem-dev/hunk)、config で任意ツールに変更可）の
**ポップアップ**（95%×95%、`difftool_placement` で tab/overlay/split にも変更可）で開き、
**選択したファイルが先頭に表示**されます。q でツールを閉じればすぐビューアに戻れます。
インラインコメントを diff 行に埋め込み表示する**内蔵ビューア**（`v` キー）も併用でき、
Comments タブのインラインコメントで Enter するとコード文脈へジャンプします。

## 画面

PR 一覧:

```text
 Bitbucket PRs — myworkspace/my-app   OPEN  MERGED  DECLINED  12件
──────────────────────────────────────────────────────────────────────
 #482   feat: 経路検索のキャッシュ層を追加      💬4   kayashima  2時間前
        feature/route-cache → develop
 #479   fix: null チェック漏れ                        tanaka     1日前
        fix/null-check → develop
──────────────────────────────────────────────────────────────────────
 j/k:移動  Enter:開く  s:state切替  r:再読込  o:ブラウザ  q:終了
```

diff ビュー（hunk 単位・インラインコメント埋め込み）:

```text
 src/cache/route_cache.go        +120 -8  [3/8 files]  #482
──────────────────────────────────────────────────────────────────────
─── @@ -14,6 +14,20 @@ func NewRouteCache ──────── [hunk 1/5] 💬1 ───
   14   14   func NewRouteCache(r *redis.Client) *RouteCache {
   15        -	return &RouteCache{r: r}
        15   +	return &RouteCache{r: r, ttl: defaultTTL}
 ┌─💬 tanaka (2時間前) ─────────────────────────────────────────────
 │ ttl は config から取れるようにしませんか?
 │  └ kayashima (1時間前)
 │    次の PR でやります
 └────────────────────────────────────────────────────────────────────
─── @@ -40,3 +54,8 @@ ──────────────── [hunk 2/5] [+11 lines] ───
──────────────────────────────────────────────────────────────────────
 j/k:移動  ]h/[h:hunk  ]f/[f:ファイル  za/zA:折畳  c:💬  o:ブラウザ  q:戻る
```

PR 更新後に位置がずれたコメント（outdated）は捨てずに、各ファイル末尾の
「Orphaned comments」セクションにまとめて表示します。

## 必要環境

- Herdr 0.7.0 以上
- macOS または Linux
- Go 1.26.4 以上（インストール時の plugin build に使用）
- Bitbucket Cloud の API トークン（後述）
- （任意）[hunk](https://github.com/modem-dev/hunk) — デフォルトの外部 diff ビューア。
  `brew install hunk` または `npm i -g hunkdiff`。`diff_tool` 設定で別ツールに変更可能で、
  無くても内蔵ビューアだけで動作します

## インストール

```sh
herdr plugin install boooowy/herdr_plugins/bb-pr
```

ローカル開発では:

```sh
cd bb-pr && make plugin-link
```

## 認証設定

次の環境変数を読みます（他ツールと共用している場合は追加設定不要）:

```sh
export ATLASSIAN_USER_ID="<Atlassianアカウントのメールアドレス>"
export ATLASSIAN_API_TOKEN="<APIトークン>"
```

API トークンは <https://id.atlassian.com/manage-profile/security/api-tokens> で作成します
（App Password は 2026年6月に廃止済みのため使えません）。
**コメント投稿（`C`）を使う場合は、トークンに pullrequest への write スコープが必要です**
（閲覧だけなら read で十分）。

環境変数の代わりに `~/.config/herdr/plugins/config/boooowy.bb-pr/config.toml` に
`email` / `api_token` を書くこともできます（そちらが優先）。

## 使い方

起動方法は3つ:

1. **コマンドパレット** — `Bitbucket PR: open viewer for current repo`。
   フォーカス中ペインの作業ディレクトリの `git remote origin` からリポジトリを自動判別し、
   PR 一覧を新しいタブで開きます。
2. **PR URL を Ctrl+クリック** — ターミナルに流れた
   `https://bitbucket.org/<ws>/<repo>/pull-requests/<id>` を Ctrl+クリックすると、
   その PR の詳細を直接開きます。
3. **キーバインド** — `~/.config/herdr/config.toml` に例えば:

```toml
[[keys.command]]
key = "cmd+alt+9"                # 配列も可: ["prefix+p", "cmd+alt+9"]
type = "plugin_action"           # shell/pane/popup では動きません
command = "boooowy.bb-pr.open"
description = "Bitbucket PR: open viewer for current repo"
```

設定は herdr の再起動、または `herdr server reload-config` で反映されます。

## キーバインド（ビューア内。`?` でその画面で使えるキーを表示）

| キー | 動作 |
|---|---|
| `j` / `k` / `↑` / `↓` | 移動 |
| `Ctrl-d` / `Ctrl-u`, `Ctrl-f` / `Ctrl-b` | スクロール |
| `g` / `G` | 先頭 / 末尾 |
| `Enter` | 開く（一覧→詳細→**diff ツール**、Comments のインラインコメント→**コードへジャンプ**）/ 内蔵ビューアでは hunk 折畳トグル |
| `v` | 内蔵 diff ビューアで開く（Files タブ。インラインコメント埋め込み表示） |
| `Tab` / `Shift-Tab` / `1`-`3` / `l` / `h`（`→` / `←`） | 詳細タブ切替（Overview / Files / Comments） |
| `e` | 説明文の全文表示（Overview） |
| `s` / `Tab` / `l` / `h`（`→` / `←`） | state フィルタ切替（OPEN / MERGED / DECLINED / SUPERSEDED）（一覧） |
| `]h` / `[h` | 次 / 前の hunk（内蔵ビューア） |
| `]f` / `[f`（`→` / `←`） | 次 / 前のファイル（内蔵ビューア） |
| `za` / `zA` | hunk 折畳 / 全折畳（内蔵ビューア） |
| `w` | 長い行の折返し切替（内蔵ビューア） |
| `c` | インラインコメント表示切替（内蔵ビューア） |
| `C` | **コメント投稿** — diff 行:インラインコメント / スレッド上:返信 / それ以外:PRコメント。エディタが popup で開き、保存して閉じると投稿（空なら中止）。反映は `r` |
| `m` | 説明文 / 選択スレッドを **Markdown ビューア**（既定 glow）の popup で表示 |
| `D` | PR 全体の diff を diff ツールで開く |
| `r` | 再読込（キャッシュ破棄） |
| `o` | ブラウザで開く |
| `y` | PR URL をクリップボードにコピー（一覧 / 詳細） |
| `b` | source branch 名をクリップボードにコピー（一覧 / 詳細） |
| `q` / `Esc` | 戻る / 終了 |

## 設定（config.toml、全項目省略可）

```toml
# 認証（未設定なら環境変数 ATLASSIAN_USER_ID / ATLASSIAN_API_TOKEN）
email = ""
api_token = ""

default_workspace = ""   # git remote で判別できないときのフォールバック
default_repo = ""
default_state = "OPEN"   # 一覧の初期フィルタ
placement = "tab"        # tab | split | zoomed | overlay — ビューア自体の配置
list_tab_title = "PRs {repo}"  # ビューアの herdr タブ名。{repo} {workspace} が使える
show_comments = true     # diff 内インラインコメントの初期表示
context_fold = false     # true で内蔵ビューアの hunk を折り畳んだ状態で開く
http_timeout_sec = 20

# 外部 diff ツール（{patch} が patch ファイルパスに置換される。
# {patch} が無い場合は patch を stdin に流す）
diff_tool = ["hunk", "patch", "{patch}"]
files_enter = "difftool"      # difftool | builtin — Files タブ Enter の動作

# diff ツールの表示先。popup は herdr 0.7.5 以上（それ未満は "tab" を設定）。
#   popup:   枠付きポップアップ。ツール終了（q）で自動的に閉じてビューアへ戻る
#   overlay: タブ全面表示。終了時にフォーカスと zoom 状態を復元
#   tab:     専用タブ。開いたまま保持でき、同じ PR のタブは再利用される
difftool_placement = "popup"
difftool_width = "95%"        # popup のみ有効（セル数 or "95%"）
difftool_height = "95%"
diff_tab_title = "PR #{id}"   # difftool_placement = "tab" のタブ名。{id} {title} {repo}

# m キーの Markdown ビューア（{file} = mdファイル。無ければ stdin に流す）
markdown_viewer = ["glow", "-p", "{file}"]
# C キーのコメント編集エディタ（未設定なら $EDITOR、それも無ければ nvim）
comment_editor = []           # 例: ["nvim", "+startinsert", "{file}"]

# 色（色名 / 0-255 / #rrggbb）
add_fg = "green"
del_fg = "red"
hunk_fg = "cyan"
comment_fg = "white"
comment_border_fg = "#8be9fd"
outdated_fg = "yellow"
```

## 制限事項

- approve・merge は行えません（`o` でブラウザへ）。コメント投稿後の画面反映は `r` で再読込してください。
- PR 一覧は50件ずつのオンデマンド取得です。カーソルが末尾付近に来ると次の50件を自動で読み込みます
  （ヘッダの「N件+」の `+` が未取得分の印）。
- 起動時は前回取得した一覧を即表示し、裏で最新に更新します
  （キャッシュは plugin の state ディレクトリ配下 `cache/`）。
- Bitbucket API の diff は大きな PR で切り詰められます（1ファイル2000行/100KB、全体8000行、200ファイル）。
  検出時は Files タブにバナーを表示します。全文はブラウザで確認してください。
- 説明文・コメントは raw Markdown をワードラップ表示します（リッチレンダリングなし）。

## 開発

```sh
make build   # bin/bb-pr
make test    # go test -race ./...
make plugin-link

# デバッグ
herdr plugin action invoke open --plugin boooowy.bb-pr
herdr plugin log list --plugin boooowy.bb-pr --limit 10
touch /tmp/bbpr-debug.log   # 詳細ログ有効化

# API 応答の目視（ペイン外で実行）
./bin/bb-pr dump prs
./bin/bb-pr dump pr 482
./bin/bb-pr dump diff 482
```

`screen.go` / `keys.go` / `debug.go` / `herdr.go` は hint-copy（commit `2029b96` 時点）の
スナップショットです。hint-copy 側のリファクタ完了後に共有モジュール化する予定のため、
これらのファイルへの機能追加は最小限にしてください。
