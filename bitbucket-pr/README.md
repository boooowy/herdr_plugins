# bitbucket-pr — Bitbucket PR Viewer

Bitbucket Cloud のプルリクエストを herdr のペイン内で閲覧・コメントするビューアです。
PR 一覧 → 詳細（説明・レビュアー・変更ファイル・コメント）→ hunk 単位の diff（インラインコメント付き）
の閲覧に加え、`C` キーでコード行へのインラインコメント・返信・PRコメントを投稿できます
（本文はいつものエディタが popup で開きます）。approve / merge は非対応です（`o` でブラウザへ）。

- plugin ID: `boooowy.bitbucket-pr`
- version: `0.14.0`
- platforms: macOS / Linux

Files タブでファイルを Enter すると、PR 全体の diff が**外部 diff ツール**
（デフォルト: [hunk](https://github.com/modem-dev/hunk)、config で任意ツールに変更可）の
**ポップアップ**（95%×95%、`difftool_placement` で tab/overlay/split にも変更可）で開き、
**選択したファイルが先頭に表示**されます。q でツールを閉じればすぐビューアに戻れます。
インラインコメントを diff 行に埋め込み表示する**内蔵ビューア**（`v` キー）も併用でき、
Comments タブのインラインコメントで Enter するとコード文脈へジャンプします。

hunk とは双方向に連携します（hunk 0.13 以上）:

- **既存コメントの表示** — PR のインラインコメント（返信含む）が hunk の diff 内に
  注釈として表示されます。hunk の Next/Previous Comment ショートカットで注釈間を移動できます。
- **Draft note の投稿** — hunk 内で `+`（または `c`）で入力した draft note は、
  hunk を閉じたときに一覧確認画面が出て、`y` で Bitbucket のインラインコメントとして
  一括投稿されます（`n` で破棄）。投稿後はビューアの Comments に自動反映されます。
  注意: hunk の note は単一行のみです（hunk の仕様。`c` は選択中 hunk の最初の変更行に、
  マウスの `+` はその行に付きます）。**複数行コメントは内蔵ビューアの `v` 選択**で投稿できます。

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
 💬 tanaka (2時間前)            ← コメントエリアは背景色で表現
    ttl は config から取れるようにしませんか?
    ↳ kayashima (1時間前)
      次の PR でやります
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
  `brew install hunk` または `npm i -g hunkdiff`。**config.toml を作らなくても
  Files タブの Enter はデフォルトで hunk を起動します**（`diff_tool` 設定で別ツールに変更可）。
  未インストールの場合はインストール手順の案内画面が出るだけで、
  内蔵ビューア（`v`）だけでも一通り使えます。コメント連携（注釈表示 / draft note 投稿）には
  hunk 0.13 以上が必要です

## インストール

```sh
herdr plugin install boooowy/herdr_plugins/bitbucket-pr
```

ローカル開発では:

```sh
cd bitbucket-pr && make plugin-link
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

環境変数の代わりに `~/.config/herdr/plugins/config/boooowy.bitbucket-pr/config.toml` に
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
command = "boooowy.bitbucket-pr.open"
description = "Bitbucket PR: open viewer for current repo"
```

設定は herdr の再起動、または `herdr server reload-config` で反映されます。

## キーバインド（ビューア内。`?` でその画面で使えるキーを表示）

| キー                                                   | 動作                                                                                                                                                                                                                                                                                                 |
| ------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `j` / `k` / `↑` / `↓`                                  | 移動                                                                                                                                                                                                                                                                                                 |
| `Ctrl-d` / `Ctrl-u`, `Ctrl-f` / `Ctrl-b`               | スクロール（Comments タブでは `Ctrl-d`/`Ctrl-u` が右プレビューのスクロール）                                                                                                                                                                                                                         |
| `g` / `G`                                              | 先頭 / 末尾                                                                                                                                                                                                                                                                                          |
| `Enter`                                                | 開く（一覧→詳細→**diff ツール**、Comments のスレッド／ファイル→**コードへジャンプ**）/ 内蔵ビューアでは hunk 折畳トグル                                                                                                                                                                              |
| `v`                                                    | Files タブ: 内蔵 diff ビューアで開く / 内蔵ビューア内: **行選択の開始・解除**（`j`/`k` で範囲を伸ばし `C` で複数行コメント。`Esc` でも解除）                                                                                                                                                         |
| `Tab` / `Shift-Tab` / `1`-`3` / `l` / `h`（`→` / `←`） | 詳細タブ切替（Overview / Files / Comments）                                                                                                                                                                                                                                                          |
| `s` / `Tab` / `l` / `h`（`→` / `←`）                   | state フィルタ切替（OPEN / MERGED / DECLINED / SUPERSEDED）（一覧）                                                                                                                                                                                                                                  |
| `]h` / `[h`                                            | 次 / 前の hunk（内蔵ビューア）                                                                                                                                                                                                                                                                       |
| `]f` / `[f`（`→` / `←`）                               | 次 / 前のファイル（内蔵ビューア）                                                                                                                                                                                                                                                                    |
| `za` / `zA`                                            | hunk 折畳 / 全折畳（内蔵ビューア）                                                                                                                                                                                                                                                                   |
| `w`                                                    | 長い行の折返し切替（内蔵ビューア）                                                                                                                                                                                                                                                                   |
| `c`                                                    | インラインコメント表示切替（内蔵ビューア）                                                                                                                                                                                                                                                           |
| `C`                                                    | **コメント投稿** — diff 行:インラインコメント / `v` 選択中:**複数行コメント** / コメント上:返信（Comments タブの返信行では**入れ子返信**） / それ以外:PRコメント。投稿先はフッタに「C:返信→著者 L15」等で常に表示。エディタが popup で開き、保存して閉じると投稿（空なら中止）。**画面には自動反映** |
| `x`                                                    | Comments タブ: 選択コメントを**削除**（フッタで `y` 確認。返信付きコメントは Bitbucket 仕様でソフト削除）                                                                                                                                                                                            |
| `s`                                                    | Comments タブ: スレッドを **resolve / 再オープン**（インラインスレッドのみ。返信行では**親スレッドに作用** — フッタに「s:親をresolve」と表示。解決済みは一覧に ✓・スレッドに [resolved] 表示）                                                                                                       |
| `D`                                                    | PR 全体の diff を diff ツールで開く                                                                                                                                                                                                                                                                  |
| `r`                                                    | 再読込（キャッシュ破棄）                                                                                                                                                                                                                                                                             |
| `o`                                                    | ブラウザで開く                                                                                                                                                                                                                                                                                       |
| `y`                                                    | PR URL をクリップボードにコピー（一覧 / 詳細）                                                                                                                                                                                                                                                       |
| `b`                                                    | source branch 名をクリップボードにコピー（一覧 / 詳細）                                                                                                                                                                                                                                              |
| `q` / `Esc`                                            | 戻る / 終了                                                                                                                                                                                                                                                                                          |

## 設定（config.toml、全項目省略可）

設定ファイルは `~/.config/herdr/plugins/config/boooowy.bitbucket-pr/config.toml`。
**ファイル自体が無くても、以下に示す値がそのままデフォルトとして使われて動作します**
（外部 diff ツールは hunk、表示は popup 95%×95% など）。

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
# {patch} が無い場合は patch を stdin に流す。{ctx} は既存コメントを hunk の
# 注釈として表示するための agent-context JSON のパス — hunk 以外のツールでは外す。
# --agent-notes は注釈を初期表示にするフラグ）
diff_tool = ["hunk", "patch", "{patch}", "--agent-context", "{ctx}", "--agent-notes"]
files_enter = "difftool"      # difftool | builtin — Files タブ Enter の動作

# diff ツールの表示先。popup は herdr 0.7.5 以上（それ未満は "tab" を設定）。
#   popup:   枠付きポップアップ。ツール終了（q）で自動的に閉じてビューアへ戻る
#   overlay: タブ全面表示。終了時にフォーカスと zoom 状態を復元
#   tab:     専用タブ。開いたまま保持でき、同じ PR のタブは再利用される
difftool_placement = "popup"
difftool_width = "95%"        # popup のみ有効（セル数 or "95%"）
difftool_height = "95%"
diff_tab_title = "PR #{id}"   # difftool_placement = "tab" のタブ名。{id} {title} {repo}

# C キーのコメント編集エディタ（未設定なら $EDITOR、それも無ければ nvim）
comment_editor = []           # 例: ["nvim", "+startinsert", "{file}"]

# タブ内 Markdown レンダリングの色
md_heading_fg = "#c792ea"     # 見出し
md_code_fg = "#e5c07b"        # コード（インライン / 未知言語のブロック）
md_code_bg = "#161821"        # コードブロック / インラインコードの背景
comment_bg = "#20222e"        # コメントエリアの背景
focus_bg = "#2e3350"          # フォーカス中スレッド / v 選択行の背景

# 色（色名 / 0-255 / #rrggbb）
add_fg = "green"
del_fg = "red"
hunk_fg = "cyan"
comment_fg = "white"
outdated_fg = "yellow"
```

## 制限事項

- approve・merge は行えません（`o` でブラウザへ）。
- コメントの **like（いいね）は非対応**です。Bitbucket Cloud の公式 REST API に like の
  エンドポイントが存在せず、Web UI が使う内部 API は API トークン認証を受け付けないため
  実装できません（公式 API が提供されたら対応予定）。
- resolve できるのはインライン（diff 上の）スレッドのみです（API の制約）。
- PR 一覧は50件ずつのオンデマンド取得です。カーソルが末尾付近に来ると次の50件を自動で読み込みます
  （ヘッダの「N件+」の `+` が未取得分の印）。
- 起動時は前回取得した一覧を即表示し、裏で最新に更新します
  （キャッシュは plugin の state ディレクトリ配下 `cache/`）。
- Bitbucket API の diff は大きな PR で切り詰められます（1ファイル2000行/100KB、全体8000行、200ファイル）。
  検出時は Files タブにバナーを表示します。全文はブラウザで確認してください。
- 説明文・コメントはタブ内で Markdown レンダリングされます（見出し・箇条書き・チェックボックス・
  `code`・**太字**・リンク（URLは隠す）・引用・罫線・テーブル罫線、コードブロックは背景色付きで
  シンタックスハイライト）。画像やテーブルの桁揃えは再現されません（`o` でブラウザへ）。
- コメントは罫線ではなく背景色でエリアを表現し、返信は ↳ とインデントで入れ子を示します。
  Comments タブは（横幅90桁以上で）**マスター・ディテール表示**になります: 左にファイル見出し＋
  スレッド一覧（L番号・著者・本文抜粋）、右に選択中ファイルのプレビュー。プレビューは
  **コメントが付いた hunk 全体を diff 形式で表示**し、該当行の直下にコメントを埋め込みます
  （diff から位置が特定できないコメントは末尾に L番号ラベル付きで表示）。選択中のスレッドは
  プレビュー側の背景色が明るくなり（`focus_bg`）、`C` の投稿先はフッタに常に表示されます。
  90桁未満の狭いペインでは従来のフラット一覧に自動フォールバックします。
- hunk の draft note は hunk のプロセス終了と同時に hunk 側から消えるため、bitbucket-pr は hunk の
  起動中に 0.5 秒間隔でローカルの hunk セッションから note を取得しています。note を書き終えた
  直後（0.5秒以内）に q で閉じると取りこぼす理論上の窓がありますが、実用上は問題ありません。

## 開発

```sh
make build   # bin/bitbucket-pr
make test    # go test -race ./...
make plugin-link

# デバッグ
herdr plugin action invoke open --plugin boooowy.bitbucket-pr
herdr plugin log list --plugin boooowy.bitbucket-pr --limit 10
touch /tmp/bitbucket-pr-debug.log   # 詳細ログ有効化

# API 応答の目視（ペイン外で実行）
./bin/bitbucket-pr dump prs
./bin/bitbucket-pr dump pr 482
./bin/bitbucket-pr dump diff 482
```
