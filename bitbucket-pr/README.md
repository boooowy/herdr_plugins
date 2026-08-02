# bitbucket-pr — Bitbucket PR Viewer

Bitbucket Cloud のプルリクエストを herdr のペイン内で閲覧・コメントするビューアです。
PR 一覧 → 詳細（説明・変更シンボル・レビュアー・変更ファイル・コメント）→ hunk 単位の diff（インラインコメント付き）
の閲覧に加え、`c` キーでコード行へのインラインコメント・返信・PRコメントを投稿できます
（本文はいつものエディタが popup で開きます）。approve / merge は非対応です（`o` でブラウザへ）。

- plugin ID: `boooowy.bitbucket-pr`
- version: `0.20.0`
- platforms: macOS / Linux

Files タブでファイルを Enter すると、PR 全体の diff が**外部 diff ツール**
（デフォルト: [hunk](https://github.com/modem-dev/hunk)、config で任意ツールに変更可）の
**ポップアップ**（95%×95%、`difftool_placement` で tab/overlay/split にも変更可）で開き、
**選択したファイルが先頭に表示**されます。q でツールを閉じればすぐビューアに戻れます。
インラインコメントを diff 行に埋め込み表示する**内蔵ビューア**（`v` キー）も併用でき、
Comments タブのインラインコメントで Enter するとコード文脈へジャンプします。
Kitty graphics 対応環境では、PR 投稿者・レビューアと表示中のコメント投稿者・返信者の
アバターもユーザー名の横に表示します。承認済みレビューアには画像右下のチェックで状態を示します。

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
 #482   feat: 経路検索のキャッシュ層を追加       💬4   A: [画像] kayashima       2時間前
        feature/route-cache → develop               R: [画像✓][画像 ] +2
 #479   fix: null チェック漏れ                            A: [画像] tanaka          1日前
        fix/null-check → develop                     R: [画像 ]
──────────────────────────────────────────────────────────────────────
 j/k:移動  Enter:開く  Tab/s:state切替  /:絞り込み  y:URL  b:ブランチ  r:再読込  q:終了
```

Review Outline（詳細の `2:Outline`）:

```text
 #482 feat: route cache                         OPEN
 feature/route-cache → develop (a1b2c3d4)  by kayashima
 1:Overview   2:Outline   3:Files (8)   4:Comments (4)
──────────────────────────────────────────────────────────────────────────────
 5 changed symbols (tests:2) in 3/3 files  │ method NewRouteCache  signature_changed
 ! v internal/cache  chg:3 api:2 fan-in:8  │ ! contract change + fan-in:5
     v route_cache.go  chg:2 api:1          │
       ~ function NewRouteCache api fan-in:5│ Signature
         function loadEntry                 │ - func NewRouteCache(r *redis.Client)
       > 2 tests                            │ + func NewRouteCache(r *redis.Client, ttl time.Duration)
   v cmd  chg:2 api:1                       │
                                           │ Used by
                                           │ * function main  cmd/server/main.go:42
                                           │
                                           │ Related diff
──────────────────────────────────────────────────────────────────────────────
 Tab/h/l:タブ  j/k:移動  Enter:開く/diff  gd/gr:呼出先/元  O:並び順
```

Outline は Tree-sitter で変更シンボルと1-hopの caller/callee を抽出し、シグネチャ変更、
高 fan-in、大きなファイルを先に確認しやすくする**注意マップ**です。解析結果はコードの正しさや
影響範囲の完全性を保証するものではありません。テストシンボルはファイルごとに初期状態で折り畳み、
広い画面では右側にシグネチャ・参照元/先・関連 hunk を表示します。

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
 j/k:移動  ]h/[h:hunk  ]f/[f:ファイル  za/zA:折畳  C:💬  c:コメント  q:戻る
```

PR 更新後に位置がずれたコメント（outdated）は捨てずに、各ファイル末尾の
「Orphaned comments」セクションにまとめて表示します。

## 必要環境

- Herdr 0.7.0 以上
- macOS または Linux
- Go 1.26.4 以上（インストール時の plugin build に使用）
- C コンパイラ（Tree-sitter grammar のビルドに使用。macOS は Xcode Command Line Tools、
  Linux は clang または gcc）
- Bitbucket Cloud の API トークン（後述）
- （任意）[hunk](https://github.com/modem-dev/hunk) — デフォルトの外部 diff ビューア。
  `brew install hunk` または `npm i -g hunkdiff`。**config.toml を作らなくても
  Files タブの Enter はデフォルトで hunk を起動します**（`diff_tool` 設定で別ツールに変更可）。
  未インストールの場合はインストール手順の案内画面が出るだけで、
  内蔵ビューア（`v`）だけでも一通り使えます。コメント連携（注釈表示 / draft note 投稿）には
  hunk 0.13 以上が必要です

### アバター表示（任意）

アバター表示には Herdr 0.7.4 以上と、Ghostty・kitty・WezTerm など
Kitty graphics protocol 対応ターミナルが必要です。`~/.config/herdr/config.toml` で
Herdr の graphics layer を有効にしてください。

```toml
[experimental]
kitty_graphics = true
```

設定後、Herdr を再起動するか `herdr server reload-config` を実行します。
PR 一覧では画面内に表示中の全PRについて投稿者と最大4人のレビューア、詳細ヘッダーでは
PR 投稿者、Comments と内蔵 diff では画面内に表示中のルートコメント・返信の全ユーザーが
対象です。一覧の投稿者とレビューアは固定列で左揃えし、タイトル・ブランチ列は最大64セルです。
レビューアが5人以上なら `+N` で省略数を表示し、狭い画面ではタイトル列、レビューア画像数の
順に縮めます。Overview のレビューアは承認済み・変更要求・未対応の順に画像付きで縦表示します。
画像APIや画像取得が利用できない場合も、名前と状態の文字表示は残ります。

Atlassian のプロフィール画像が「組織内のみ」公開の場合、Bitbucket REST API は
実画像ではなくイニシャル画像を返します。後述の Jira 認証が設定されていれば、同じ
`account_id` を Jira の安定版 User API で照会し、組織内の実画像を表示します。
Jira 認証が未設定または取得に失敗した場合は、従来どおりBitbucketのイニシャル画像を表示します。

特定ユーザーをローカル画像へ差し替える `avatar_overrides` も最優先で利用できます。
画像は PNG / JPEG / GIF / WebP、2 MB 以下、縦横 2048 px 以下にしてください。

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
**コメント投稿（`c`）を使う場合は、トークンに pullrequest への write スコープが必要です**
（閲覧だけなら read で十分）。

環境変数の代わりに `~/.config/herdr/plugins/config/boooowy.bitbucket-pr/config.toml` に
`email` / `api_token` を書くこともできます（そちらが優先）。

組織内限定のプロフィール画像も表示する場合は、Jira用の認証を設定します。

```sh
export JIRA_URL="https://your-company.atlassian.net"
export JIRA_USERNAME="<Atlassianアカウントのメールアドレス>"
export JIRA_API_TOKEN="<Jiraスコープを持つAPIトークン>"
```

3変数がすべて設定されている場合だけ `GET /rest/api/3/user/bulk` を使用し、画面内で
未解決のユーザーを最大10人ずつまとめて照会します。
いずれかが未設定ならJira APIは呼び出しません。Bitbucket用の
`ATLASSIAN_API_TOKEN`はJira認証には流用しません。

## 使い方

起動方法は3つ:

1. **キーバインド（推奨）** — `~/.config/herdr/config.toml` に例えば:

```toml
[[keys.command]]
key = "cmd+alt+9"                # 配列も可: ["prefix+p", "cmd+alt+9"]
type = "plugin_action"           # shell/pane/popup では動きません
command = "boooowy.bitbucket-pr.open"
description = "Bitbucket PR: open viewer for current repo"
```

   設定は herdr の再起動、または `herdr server reload-config` で反映されます。
   フォーカス中ペインの作業ディレクトリの `git remote origin` からリポジトリを自動判別し、
   PR 一覧を新しいタブで開きます。

2. **PR URL を Ctrl+クリック** — ターミナルに流れた
   `https://bitbucket.org/<ws>/<repo>/pull-requests/<id>` を Ctrl+クリックすると、
   その PR の詳細を直接開きます。

3. **CLI から実行** — キーバインドを設定していないときや動作確認に:

```sh
herdr plugin action invoke open --plugin boooowy.bitbucket-pr
```

### Review Outline の利用条件とキャッシュ

Outline は Bitbucket API のdiffだけでなく、起動元のローカルGitリポジトリにあるPRの
source/destination commitを直接解析します。対象リポジトリ内のペインからプラグインを開き、
両commitがローカルに存在する状態で利用してください。URLクリック時は、フォーカス中ペインの
checkoutがURLのリポジトリと一致する場合だけ関連付けます。

commitが無い場合、プラグインはcloneやfetchを自動実行しません。表示されたリポジトリで
PRブランチをfetchしてから `r` で再読込してください。Outlineだけが利用不可になり、Overview、
Files、Commentsは通常どおり利用できます。

解析はOutlineを初めて開いた時だけ非同期で実行します。結果はsource/destination commitの組を
キーに Herdr が渡す `HERDR_PLUGIN_STATE_DIR` 配下の `cache/` へ保存するため、同じPRを
再表示しても毎回解析しません。`r` は画面上の結果を破棄して再読込しますが、commitが同じなら
永続キャッシュを再利用します。

対応言語は Go、Rust、Python、TypeScript/TSX、JavaScript/JSX、Java です。未対応言語、生成物、
バイナリはスキップ理由を表示し、Filesタブで引き続き確認できます。

## キーバインド（ビューア内。`?` でその画面で使えるキーを表示）

| キー                                                   | 動作                                                                                                                                                                                                                                                                                                 |
| ------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `j` / `k` / `↑` / `↓`                                  | 移動                                                                                                                                                                                                                                                                                                 |
| `Ctrl-d` / `Ctrl-u`, `Ctrl-f` / `Ctrl-b`               | スクロール（Outline / Comments タブでは `Ctrl-d`/`Ctrl-u` が右プレビューのスクロール）                                                                                                                                                                                                              |
| `g` / `G`                                              | 先頭 / 末尾（Outlineでは `gg` が先頭）                                                                                                                                                                                                                                                               |
| `Enter`                                                | 開く（一覧→詳細、Outlineのディレクトリ/ファイル/テスト→展開、Outlineのシンボル→**関連diff**、Files→**diffツール**、Comments→**コードへジャンプ**）/ 内蔵ビューアでは hunk 折畳トグル                                                                                                                    |
| `v`                                                    | Files タブ: 内蔵 diff ビューアで開く / 内蔵ビューア内: **行選択の開始・解除**（`j`/`k` で範囲を伸ばし `c` で複数行コメント。`Esc` でも解除）                                                                                                                                                         |
| `Tab` / `Shift-Tab` / `1`-`4` / `l` / `h`（`→` / `←`） | 詳細タブ切替（Overview / Outline / Files / Comments）                                                                                                                                                                                                                                                |
| `s` / `Tab` / `l` / `h`（`→` / `←`）                   | state フィルタ切替（OPEN / MERGED / DECLINED / SUPERSEDED）（一覧）                                                                                                                                                                                                                                  |
| `/`                                                    | **絞り込み** — 一覧: 番号・タイトル・著者・ブランチ名 / Outline: シンボル・kind・シグネチャ・ファイルパス / Files: ファイルパス / Comments: コメント本文・著者・ファイルパス・行番号。入力しながら即座に絞り込み、`Enter` で確定、`Esc`（または `q`）で解除。空白区切りは AND 検索                     |
| `gd` / `gr`                                            | Outline: 選択シンボルの変更済み callee / caller へ移動。候補が複数なら選択画面を開き、PR外参照は場所をステータス表示                                                                                                                                                                                   |
| `Ctrl-o` / `Ctrl-i`                                    | Outline: シンボル移動履歴を戻る / 進む                                                                                                                                                                                                                                                               |
| `O`                                                    | Outline: ディレクトリの依存順 / アルファベット順を切替                                                                                                                                                                                                                                              |
| `]h` / `[h`                                            | 次 / 前の hunk（内蔵ビューア）                                                                                                                                                                                                                                                                       |
| `]f` / `[f`（`→` / `←`）                               | 次 / 前のファイル（内蔵ビューア）                                                                                                                                                                                                                                                                    |
| `za` / `zA`                                            | hunk 折畳 / 全折畳（内蔵ビューア）                                                                                                                                                                                                                                                                   |
| `w`                                                    | 長い行の折返し切替（内蔵ビューア）                                                                                                                                                                                                                                                                   |
| `c`                                                    | **コメント投稿** — diff 行:インラインコメント / `v` 選択中:**複数行コメント** / コメント上:返信（Comments タブの返信行では**入れ子返信**） / それ以外:PRコメント。投稿先はフッタに「c:返信→著者 L15」等で常に表示。エディタが popup で開き、保存して閉じると投稿（空なら中止）。**画面には自動反映** |
| `C`                                                    | インラインコメント表示切替（内蔵ビューア）                                                                                                                                                                                                                                                           |
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
show_avatars = true      # 対応環境で投稿者・レビューア・コメント投稿者のアバターを表示
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

# c キーのコメント編集エディタ（未設定なら $EDITOR、それも無ければ nvim）
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

# Bitbucket API がイニシャル画像を返すアカウントの任意上書き。
# 相対パスはこの config.toml があるディレクトリを基準に解決される。
[avatar_overrides]
"5ecb88cb7a6cb90c2bcfec71" = "avatars/fukaya.png"
```

## 制限事項

- approve・merge は行えません（`o` でブラウザへ）。
- コメントの **like（いいね）は非対応**です。Bitbucket Cloud の公式 REST API に like の
  エンドポイントが存在せず、Web UI が使う内部 API は API トークン認証を受け付けないため
  実装できません（公式 API が提供されたら対応予定）。
- resolve できるのはインライン（diff 上の）スレッドのみです（API の制約）。
- PR 一覧は50件ずつのオンデマンド取得です。カーソルが末尾付近に来ると次の50件を自動で読み込みます
  （ヘッダの「N件+」の `+` が未取得分の印）。
- `/` の絞り込みは**取得済みのページが対象**です（サーバー側検索ではありません）。絞り込み中に
  ヒットが画面を埋めない場合は、未取得ページを自動で追加取得して探し続けます
  （ヘッダは「12/48件+」のようにヒット数／取得済み件数を表示）。
- 起動時は前回取得した一覧を即表示し、裏で最新に更新します
  （キャッシュは plugin の state ディレクトリ配下 `cache/`）。
- アバターは中央を正方形に切り出して state ディレクトリ配下へ無期限で保存します。
  Jira画像URLは30日ごとに一括で再確認し、URLが変わった場合だけ画像を再取得します。
  可視範囲だけを遅延取得し、画像ダウンロードは最大4並列です。
  `avatar_overrides` の画像変更は更新時刻とサイズで検出します。上書き画像を読めない場合は
  Bitbucket API の画像へ戻り、API画像も取得できなければ名前だけを表示します。
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
  プレビュー側の背景色が明るくなり（`focus_bg`）、`c` の投稿先はフッタに常に表示されます。
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
