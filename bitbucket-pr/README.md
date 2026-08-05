# bitbucket-pr — Bitbucket PR Viewer

Bitbucket Cloud のプルリクエストを herdr のペイン内で閲覧・レビューするビューアです。
PR 一覧 → 詳細（説明・変更シンボル・レビュアー・変更ファイル・コメント）→ hunk 単位の diff の閲覧に加え、
インラインコメント・返信・PRコメントの投稿、承認・承認取消・Decline も行えます。
merge は非対応です（`o` でブラウザへ）。

- plugin ID: `boooowy.bitbucket-pr`
- version: `0.27.0`
- platforms: macOS / Linux

はじめに読むもの: [セットアップ](#セットアップ) → [画面と機能](#画面と機能) → [キーバインド](#キーバインドビューア内-でその画面で使えるキーを表示) → [設定リファレンス](#設定リファレンスconfigtoml全項目省略可)

## セットアップ

### 1. 必要環境

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
- （任意）アバター表示には Herdr 0.7.4 以上と Kitty graphics 対応ターミナルが必要です
  → [アバター表示](#アバター表示任意)

### 2. インストール

```sh
herdr plugin install boooowy/herdr_plugins/bitbucket-pr
```

ローカル開発では:

```sh
cd bitbucket-pr && make plugin-link
```

### 3. 認証設定

次の環境変数を読みます。

```sh
export ATLASSIAN_USER_ID="<Atlassianアカウントのメールアドレス>"
export ATLASSIAN_API_TOKEN="<APIトークン>"
```

API トークンは <https://id.atlassian.com/manage-profile/security/api-tokens> で作成します
（App Password は 2026年6月に廃止済みのため使えません）。
**コメント投稿・resolve・承認・承認取消・Declineを使う場合は、トークンに
pullrequest への write スコープが必要です**
（閲覧だけなら read で十分）。

環境変数の代わりに `~/.config/herdr/plugins/config/boooowy.bitbucket-pr/config.toml` に
`email` / `api_token` を書くこともできます（そちらが優先）。

組織内限定のプロフィール画像も表示したい場合は Jira 用の認証も設定します
→ [アバター表示](#アバター表示任意)。

### 4. 起動方法を選ぶ

主な使い方は次の2パターンです。どちらも herdr 側のキーバインド1つで起動でき、
併用もできます（設定は herdr の再起動、または `herdr server reload-config` で反映）。

#### パターンA: アクティブなペインのリポジトリのPRを開く

作業中のリポジトリのPRをすぐ見たいとき。`~/.config/herdr/config.toml` に:

```toml
[[keys.command]]
key = "cmd+alt+9"
type = "plugin_action"
command = "boooowy.bitbucket-pr.open"
description = "Bitbucket PR: open viewer for current repo"
```

- フォーカス中ペインの作業ディレクトリ （フォアグラウンドプロセスの cwd → ペインの cwd の順に試行）の `git remote origin` から リポジトリを自動判別し、PR 一覧を新しいタブで開きます
- 判別できないペインからも起動したい場合のフォールバック（任意）:

  ```toml
  # ~/.config/herdr/plugins/config/boooowy.bitbucket-pr/config.toml
  default_workspace = "myworkspace"   # 環境変数 BITBUCKET_WORKSPACE でも可
  default_repo = "my-app"             # 2つ揃って初めてフォールバックが効きます
  ```

  `default_repo` が無く workspace だけ分かる場合は、自動でパターンBのピッカーへ
  フォールバックします

- Outline タブだけはローカル checkout が必要です
  → [Review Outline の利用条件とキャッシュ](#review-outline-の利用条件とキャッシュ)

#### パターンB: リポジトリ一覧 → PR一覧

workspace 内のリポジトリを横断して選びたいとき、ローカルに clone していない
リポジトリのPRを見たいとき。

```toml
[[keys.command]]
key = "cmd+alt+0"
type = "plugin_action"
command = "boooowy.bitbucket-pr.open-picker"
description = "Bitbucket PR: pick a repository"
```

- workspace のリポジトリ一覧（更新順）を検索して選び、そのままPR一覧へ進みます。
  ローカル checkout が無いリポジトリのPRも閲覧できます（Outline のみ不可）
- **workspace の特定が必須です。** bitbucket.org のリポジトリ内のペインから起動すれば
  自動判別されますが、それ以外の場所から起動するなら設定が必要です
  （未設定だと「workspaceを特定できません」のトーストで終了します）:

  ```toml
  # ~/.config/herdr/plugins/config/boooowy.bitbucket-pr/config.toml
  default_workspace = "myworkspace"   # 環境変数 BITBUCKET_WORKSPACE でも可
  ```

- ローカル checkout の有無表示（`●`）と `C` の clone を使う場合（任意）:

  ```toml
  repo_roots = ["~/Documents/workspace"]  # ローカルcheckoutの探索ルート
  clone_dir = ""                          # C の clone 先。未設定なら repo_roots の先頭
  clone_protocol = "ssh"                  # ssh | https
  ```

- 画面例・`●`/`◌` マークの意味・検索・clone の挙動 → [リポジトリピッカー](#リポジトリピッカー)

#### その他の起動方法

- **PR URL を Ctrl+クリック** — ターミナルに流れた
  `https://bitbucket.org/<ws>/<repo>/pull-requests/<id>` を Ctrl+クリックすると、
  その PR の詳細を直接開きます（設定不要）。
- **CLI から実行** — キーバインドを設定していないときや動作確認に:

  ```sh
  herdr plugin action invoke open --plugin boooowy.bitbucket-pr
  herdr plugin action invoke open-picker --plugin boooowy.bitbucket-pr
  ```

## 画面と機能

### Review Outline

詳細の `2:Outline` タブ:

```text
 #482 feat: route cache                         OPEN
 feature/route-cache → develop (a1b2c3d4)  by kayashima
 1:Overview   2:Outline   3:Files (8)   4:Comments (4)
──────────────────────────────────────────────────────────────────────────────
 5 changed symbols (tests:2) in 3/3 files  │ method NewRouteCache  signature_changed
 ! v internal/cache  chg:3 api:2 [bloat]   │ ! contract change + fan-in:5
     v route_cache.go  chg:2 api:1 [bloat]  │ ! bloat: シグネチャは同じまま本体が
       ~ fn NewRouteCache api fan-in:5 [bloat:+44]│   20行 → 64行 (+44) に増えています
         fn loadEntry                       │ Signature
       > 2 tests                            │ - func NewRouteCache(r *redis.Client)
   v cmd  chg:2 api:1                       │ + func NewRouteCache(r *redis.Client, ttl ...)
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

コードスメルチップ（AI が書いた PR に頻出する設計上の引っかかりの先出し。ゴールド背景
`#c69726`・ダーク文字 `#232323` の塗りバッジで表示、上の画面例では `[ ]` で表現）:

- `god` — caller が 5 箇所以上かつ 3 ディレクトリ以上に散っているシンボル（いわゆる
  神ヘルパー）。責務の境界を跨いで依存されており、正しさ以前に分割を検討する価値があります
- `bloat:+N` — シグネチャは据え置きのまま body が +30 行以上かつ 1.5 倍以上に肥大した
  既存の関数/メソッド
- `big:N` — 新規追加された 80 行以上の関数/メソッド（新規ファイルの巨大関数も対象）

ファイル/ディレクトリ行には配下に存在するスメルの種別チップ（`bloat` 等、件数なし）が
集約表示され、`/bloat` `/肥大` など日英どちらのキーワードでも該当シンボルだけに絞り込めます。
型定義（struct/class 等）とテストコードは、正当に大きくなりやすいためスメル判定の対象外です。
判定根拠と望ましい対処は右側プレビューに日本語で表示されます。

### diff ビューと外部 diff ツール連携

内蔵 diff ビュー（`v` キー・hunk 単位・インラインコメント埋め込み）:

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
Comments タブのインラインコメントで Enter するとコード文脈へジャンプします。

Files タブでファイルを Enter すると（また、どのタブでも `D` を押すと）、PR 全体の diff が
**外部 diff ツール**（デフォルト: [hunk](https://github.com/modem-dev/hunk)、config で任意ツールに変更可）の
**ポップアップ**（95%×95%、`difftool_placement` で tab/overlay/split にも変更可）で開き、
**カーソル文脈のファイルが先頭に表示**されます。他のファイルも同じ画面でそのまま閲覧できます
（ファイルを絞り込んで開くわけではありません）。q でツールを閉じればすぐビューアに戻れます。

表示順は patch の並べ替えと `--agent-context` の両方で指定します。hunk は agent-context の
ファイル配列順で diff を並べ替えるため、patch 側だけを並べ替えても上書きされてしまいます。
そのため agent-context には PR の**全ファイル**を希望順（カーソルのファイルが先頭、以降は
patch 順）で載せ、コメントが無いファイルも空の注釈付きで含めています。

hunk とは双方向に連携します（hunk 0.13 以上）:

- **既存コメントの表示** — PR のインラインコメント（返信含む）が hunk の diff 内に
  注釈として表示されます。hunk の Next/Previous Comment ショートカットで注釈間を移動できます。
- **Draft note の投稿 / メモ取込** — hunk 内で `+`（または `c`）で入力した draft note は、
  hunk を閉じたときに一覧確認画面が出て、`y` で Bitbucket のインラインコメントとして
  一括投稿、`m` で**ローカルのレビューメモとして取込**（ビューアの Memo タブに反映、
  投稿されない）、`n` で破棄です。投稿後はビューアの Comments に自動反映されます。
  注意: hunk の note は単一行のみです（hunk の仕様。`c` は選択中 hunk の最初の変更行に、
  マウスの `+` はその行に付きます）。**複数行コメントは内蔵ビューアの `v` 選択**で投稿できます。

### レビューメモ（annotation → コーディングエージェントへ）

diff を読みながら気づきを**ローカルメモ**として書き溜め、まとめて Markdown で
クリップボードにコピーして、そのままコーディングエージェントの Prompt に貼り付ける
ワークフローです（[rinkaku](https://www.m3tech.blog/entry/rinkaku-pr-review) の
annotation 機能を参考にしています）。メモは Bitbucket には投稿されません。

- **`m`（diff ビュー）** — カーソル行（`v` 選択中はその範囲）に紐づくメモを追加。
  コメント投稿と同じくエディタが popup で開き、保存して閉じると確定（空なら中止）。
  該当 diff 行の抜粋はメモ作成時にスナップショットされます
- **`m`（PR詳細）** — カーソル文脈にアンカー: Files タブは**選択ファイル**、Outline タブは
  **選択シンボルの定義行**（dir/ファイル行はそのパス）、Comments タブは
  **選択スレッドの file+行**（diff 抜粋付き）、それ以外は PR 全体メモ
- **hunk の draft note からも取込可** — hunk を閉じたときの確認画面で `m` を選ぶと、
  hunk 内で書いた note がそのままレビューメモになります（内蔵ビューアを開く必要なし。
  該当 diff 行の抜粋も自動付与）
- **`M` / `5`** — 詳細の **Memo タブ**でメモを一覧（1メモ=1行。`/` で絞り込み、
  `Enter` でコードへジャンプ、`e` で再編集、`d` で削除）
- **Memo タブの `y`** — 全メモを以下の形式の Markdown でクリップボードにコピー:

````markdown
# PR #123 Fix config loading (feat/config → main) レビューメモ

### src/foo.go L42

引数名を cfg → opts に直したい

```diff
+ func NewFoo(cfg Config) *Foo {
```

### (PR全体)

テスト追加を依頼したい
````

メモは PR ごとに state ディレクトリの JSON に永続化され、ビューアを閉じても
`r` 再読込しても残ります（消えるのは `d` 削除時のみ）。

### リポジトリピッカー

```text
 Bitbucket リポジトリ — myworkspace   48件+      ● ~/Documents/workspace/my-app
──────────────────────────────────────────────────────────────────────
 ● my-app                       BFFのGoサービス                 3日前
   legacy-batch                                                2週間前
 ◌ cloning-now                  大きいリポジトリ                1ヶ月前
──────────────────────────────────────────────────────────────────────
 j/k:移動  Enter:PR一覧  /:検索  C:clone  o:ブラウザ  y:clone URL  r:再読込  q:終了
```

- 一覧は**更新が新しい順**で、カーソルが末尾に近づくと次のページを自動取得します。
  `/` の絞り込み中に取得済みページで足りなければサーバー側の名前検索（`q=name~`）も
  併用し、まだページインしていないリポジトリもヒットします。
- 行頭マークが**ローカル checkout の有無**です（`●` あり / `◌` clone中 / 空白 なし）。
  カーソル行の checkout パスはヘッダ右端に表示されます。判別は
  config の `repo_roots` 配下の走査（`.git/config` の origin を読む）と、
  プラグインが学習した対応表（`repo-dirs.json` — 通常起動や clone のたびに育つ）の
  マージです。
- `Enter` で PR 一覧へ。checkout が無くても Overview / Files / Comments / diff は
  全て使えます（Outline のみ「C でclone」を案内）。`q`/`Esc` で PR 一覧から
  ピッカーへ戻り、別リポジトリへ切り替えられます。

- `C` は確認モーダル（`y` で実行）後に `git clone` を非同期実行します。clone 先は
  `clone_dir`（未設定なら `repo_roots` の先頭）`/<repo>`。既に同名の checkout が
  あれば clone せず関連付けだけ行います。完了時は herdr のトースト通知が出ます。
- clone URL は API の `links.clone` から取得し、デフォルトは **ssh** です。
  API トークンが URL や `.git/config` に書き込まれることはありません。
  `clone_protocol = "https"` の場合は git の credential helper 設定が必要です。
- `--depth`（shallow clone）は Outline が必要とする PR 両端の commit が欠けるため
  非推奨です。`clone_args = ["--filter=blob:none"]` は動作しますが、初回の
  Outline 解析時に blob の遅延取得が発生して遅くなります。

### Review Outline の利用条件とキャッシュ

Outline は Bitbucket API のdiffだけでなく、起動元のローカルGitリポジトリにあるPRの
source/destination commitを直接解析します。対象リポジトリ内のペインからプラグインを開き、
両commitがローカルに存在する状態で利用してください。URLクリック時は、フォーカス中ペインの
checkoutがURLのリポジトリと一致する場合だけ関連付けます。

checkout が関連付いていない場合でも、プラグインが学習した対応表（`repo-dirs.json`）に
該当リポジトリの checkout があれば自動で関連付けます。それも無ければ Outline タブに
「C でclone」の案内を表示し、`C` → `y` で clone して解析をやり直せます（ピッカーの
clone と同じ動作・同じ設定を使います）。

commitが無い場合、プラグインはfetchを自動実行しません。表示されたリポジトリで
PRブランチをfetchしてから `r` で再読込してください（Outline タブの `F` → `y` でも
不足commitのfetchができます）。Outlineだけが利用不可になり、Overview、
Files、Commentsは通常どおり利用できます。

解析はOutlineを初めて開いた時だけ非同期で実行します。結果はsource/destination commitの組を
キーに Herdr が渡す `HERDR_PLUGIN_STATE_DIR` 配下の `cache/` へ保存するため、同じPRを
再表示しても毎回解析しません。`r` は画面上の結果を破棄して再読込しますが、commitが同じなら
永続キャッシュを再利用します。

対応言語は Go、Rust、Python、TypeScript/TSX、JavaScript/JSX、Java、Bash です。Bashは
`.sh`、`.bash`と、`#!/bin/bash`または`#!/usr/bin/env bash`で始まる拡張子なしファイルを
解析します。関数呼び出しは名前を静的に確定できるものだけを関連付け、変数経由の実行、
`eval`、動的な`source`は対象外です。未対応言語、生成物、バイナリはスキップ理由を表示し、
Filesタブで引き続き確認できます。

### アバター表示（任意）

Kitty graphics 対応環境では、PR 投稿者・レビューアと表示中のコメント投稿者・返信者の
アバターもユーザー名の横に表示します。承認済みレビューアには画像右下のチェックで状態を示します。

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
実画像ではなくイニシャル画像を返します。次の Jira 認証が設定されていれば、同じ
`account_id` を Jira の安定版 User API で照会し、組織内の実画像を表示します。

```sh
export JIRA_URL="https://your-company.atlassian.net"
export JIRA_USERNAME="<Atlassianアカウントのメールアドレス>"
export JIRA_API_TOKEN="<Jiraスコープを持つAPIトークン>"
```

3変数がすべて設定されている場合だけ `GET /rest/api/3/user/bulk` を使用し、画面内で
未解決のユーザーを最大10人ずつまとめて照会します。
いずれかが未設定ならJira APIは呼び出しません。Jira 認証が未設定または取得に失敗した場合は、
従来どおりBitbucketのイニシャル画像を表示します。Bitbucket用の
`ATLASSIAN_API_TOKEN`はJira認証には流用しません。

特定ユーザーをローカル画像へ差し替える `avatar_overrides` も最優先で利用できます。
画像は PNG / JPEG / GIF / WebP、2 MB 以下、縦横 2048 px 以下にしてください。

## キーバインド（ビューア内。`?` でその画面で使えるキーを表示）

| キー                                                   | 動作                                                                                                                                                                                                                                                                                                          |
| ------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `j` / `k` / `↑` / `↓`                                  | 移動                                                                                                                                                                                                                                                                                                          |
| `Ctrl-d` / `Ctrl-u`, `Ctrl-f` / `Ctrl-b`               | スクロール（Outline / Comments タブでは `Ctrl-d`/`Ctrl-u` が右プレビューのスクロール）                                                                                                                                                                                                                        |
| `g` / `G`                                              | 先頭 / 末尾（Outlineでは `gg` が先頭）                                                                                                                                                                                                                                                                        |
| `Enter`                                                | 開く（一覧→詳細、Outlineのディレクトリ/ファイル/テスト→展開、Outlineのシンボル→**関連diff**、Files→**diffツール**、Comments・Memo→**コードへジャンプ**）/ 内蔵ビューアでは hunk 折畳トグル                                                                                                                    |
| `v`                                                    | Files タブ: 内蔵 diff ビューアで開く / 内蔵ビューア内: **行選択の開始・解除**（`j`/`k` で範囲を伸ばし `c` で複数行コメント。`Esc` でも解除）                                                                                                                                                                  |
| `Tab` / `Shift-Tab` / `1`-`5` / `l` / `h`（`→` / `←`） | 詳細タブ切替（Overview / Outline / Files / Comments / Memo）                                                                                                                                                                                                                                                  |
| `s` / `Tab` / `l` / `h`（`→` / `←`）                   | state フィルタ切替（OPEN / MERGED / DECLINED / SUPERSEDED）（一覧）                                                                                                                                                                                                                                           |
| `/`                                                    | **絞り込み** — 一覧: 番号・タイトル・著者・ブランチ名 / Outline: シンボル・kind・シグネチャ・ファイルパス / Files: ファイルパス / Comments: コメント本文・著者・ファイルパス・行番号 / Memo: 本文・ファイルパス。入力しながら即座に絞り込み、`Enter` で確定、`Esc`（または `q`）で解除。空白区切りは AND 検索 |
| `gd` / `gr`                                            | Outline: 選択シンボルの変更済み callee / caller へ移動。候補が複数なら選択画面を開き、PR外参照は場所をステータス表示                                                                                                                                                                                          |
| `Ctrl-o` / `Ctrl-i`                                    | Outline: シンボル移動履歴を戻る / 進む                                                                                                                                                                                                                                                                        |
| `O`                                                    | Outline: ディレクトリの依存順 / アルファベット順を切替                                                                                                                                                                                                                                                        |
| `F`                                                    | Outline: 不足している PR の commit を fetch（`y` で確定）                                                                                                                                                                                                                                                     |
| `]h` / `[h`                                            | 次 / 前の hunk（内蔵ビューア）                                                                                                                                                                                                                                                                                |
| `]f` / `[f`（`→` / `←`）                               | 次 / 前のファイル（内蔵ビューア）                                                                                                                                                                                                                                                                             |
| `za` / `zA`                                            | hunk 折畳 / 全折畳（内蔵ビューア）                                                                                                                                                                                                                                                                            |
| `w`                                                    | 長い行の折返し切替（内蔵ビューア）                                                                                                                                                                                                                                                                            |
| `c`                                                    | **コメント投稿** — diff 行:インラインコメント / `v` 選択中:**複数行コメント** / コメント上:返信（Comments タブの返信行では**入れ子返信**） / それ以外:PRコメント。投稿先はフッタに「c:返信→著者 L15」等で常に表示。エディタが popup で開き、保存して閉じると投稿（空なら中止）。**画面には自動反映**          |
| `C`                                                    | インラインコメント表示切替（内蔵ビューア） / **clone**（リポジトリピッカー、および checkout 未関連付け時の Outline タブ。`y` で確定）                                                                                                                                                                         |
| `x`                                                    | Comments タブ: 選択コメントを**削除**（中央の確認画面で `y` を押す。返信付きコメントは Bitbucket 仕様でソフト削除）                                                                                                                                                                                           |
| `s`                                                    | Comments タブ: スレッドを **resolve / 再オープン**（インラインスレッドのみ。返信行では**親スレッドに作用** — フッタに「s:親をresolve」と表示。解決済みは一覧に ✓・スレッドに [resolved] 表示）                                                                                                                |
| `m` / `M`                                              | **レビューメモ** — 内蔵ビューア: カーソル行（`v` 選択中はその範囲）にメモを追加 / PR詳細: Files タブは選択ファイル、Outline タブは選択シンボルの定義行、Comments タブは選択スレッドの行、それ以外は PR 全体メモ。`M` で Memo タブへ。メモはローカル保存で Bitbucket には投稿されません                        |
| `d` / `e`                                              | Memo タブ: メモを**削除**（`y` で確定）/ エディタで**再編集**                                                                                                                                                                                                                                                 |
| `a` / `A` / `X`                                        | PR詳細: **承認 / 自分の承認取消 / Decline**（OPENのPRのみ。すべて `y` で確定、他キーで取消）                                                                                                                                                                                                                  |
| `D`                                                    | PR 全体の diff を diff ツールで開く。**カーソル文脈のファイルが先頭・選択状態**で開く（Files: 選択ファイル / Outline: 選択シンボルのファイル / Comments: 選択スレッド・ファイル見出しのファイル / Memo: 選択メモのファイル / Overview・PR全体コメント: 自然順）。他のファイルも従来どおり全て閲覧できます              |
| `r`                                                    | 再読込（キャッシュ破棄）                                                                                                                                                                                                                                                                                      |
| `o`                                                    | ブラウザで開く                                                                                                                                                                                                                                                                                                |
| `y`                                                    | PR URL をクリップボードにコピー（一覧 / 詳細）。Memo タブでは**全メモを Markdown でコピー**                                                                                                                                                                                                                   |
| `b`                                                    | source branch 名をクリップボードにコピー（一覧 / 詳細）                                                                                                                                                                                                                                                       |
| `?`                                                    | その画面のキーバインドを中央に表示。`j`/`k`・`Ctrl-d`/`Ctrl-u` でスクロール、`q`/`Esc`/`?` で閉じる                                                                                                                                                                                                           |
| `q` / `Esc`                                            | 戻る / 終了                                                                                                                                                                                                                                                                                                   |

## 設定リファレンス（config.toml、全項目省略可）

起動に最低限必要な設定は[セットアップ](#4-起動方法を選ぶ)を参照してください。ここは全項目の一覧です。

設定ファイルは `~/.config/herdr/plugins/config/boooowy.bitbucket-pr/config.toml`。
**ファイル自体が無くても、以下に示す値がそのままデフォルトとして使われて動作します**
（外部 diff ツールは hunk、表示は popup 95%×95% など）。

```toml
# 認証（未設定なら環境変数 ATLASSIAN_USER_ID / ATLASSIAN_API_TOKEN）
email = ""
api_token = ""

default_workspace = ""   # git remote で判別できないときのフォールバック
                         # （未設定なら環境変数 BITBUCKET_WORKSPACE）
default_repo = ""
default_state = "OPEN"   # 一覧の初期フィルタ

# リポジトリピッカー（open-picker）
repo_roots = []          # ローカルcheckoutの探索ルート（例: ["~/Documents/workspace"]）。
                         # 直下（グループディレクトリは1段下まで）の .git/config を読んで
                         # bitbucket.org の checkout を「ローカルあり」として表示する
clone_dir = ""           # C キーの clone 先。未設定なら repo_roots の先頭
clone_protocol = "ssh"   # ssh | https（httpsは git credential helper の設定が必要）
clone_args = []          # git clone の追加フラグ（例: ["--filter=blob:none"]。--depth は非推奨）
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
#   split:   現在のタブを分割して表示
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

- merge は行えません（`o` でブラウザへ）。DeclineしたPRの再オープンはブラウザで行ってください。
- コメントの **like（いいね）は非対応**です。Bitbucket Cloud の公式 REST API に like の
  エンドポイントが存在せず、Web UI が使う内部 API は API トークン認証を受け付けないため
  実装できません（公式 API が提供されたら対応予定）。
- resolve できるのはインライン（diff 上の）スレッドのみです（API の制約）。
- PR 一覧は50件ずつのオンデマンド取得です。カーソルが末尾付近に来ると次の50件を自動で読み込みます
  （ヘッダの「N件+」の `+` が未取得分の印）。
- `/` の絞り込みは**取得済みのページが対象**です（サーバー側検索ではありません）。絞り込み中に
  ヒットが画面を埋めない場合は、未取得ページを自動で追加取得して探し続けます
  （ヘッダは「12/48件+」のようにヒット数／取得済み件数を表示）。
  例外としてリポジトリピッカーの `/` は、未取得分が残っている間はサーバー側の名前検索
  （`q=name~`、最初の検索語のみ）も併用します。
- ピッカーの「ローカルあり」判定は `repo_roots` 走査と学習済み対応表によります。ssh の
  Host エイリアス（`git@bb-work:...` 等）を origin に使う checkout は走査では見つかりませんが、
  そのリポジトリ内のペインから一度 `open` すれば学習されて以後表示されます。
- ピッカーで別リポジトリへ切り替えると、前のリポジトリで開いた外部 diff ツールのタブは
  追跡対象から外れます（タブ自体は残ります）。
- **横断PRビュー（「自分が作成したPR」「レビュー待ちPR」の一覧）は非対応**です。
  Bitbucket の公開APIには「自分がレビュアーのPR」を横断取得するエンドポイントが存在せず
  （`role=reviewer` パラメータも無視される。実測確認済み）、リポジトリ毎の走査でしか
  実現できずレート制限リスクが大きいため見送りました。ブラウザの Bitbucket
  ダッシュボード（For you）を利用してください。
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
  `code`・**太字**・リンク（URLは隠す）・引用・罫線、コードブロックは背景色付きで
  シンタックスハイライト）。テーブルは列幅を揃えて描画します（ヘッダ太字・`:-:`/`--:` の
  中央/右揃え対応、幅が足りない列は … で切り詰め）。画像は再現されません（`o` でブラウザへ）。
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
touch /tmp/bitbucket-pr-debug.log   # API取得処理ごとの所要時間を含む詳細ログを有効化

# API 応答の目視（ペイン外で実行）
./bin/bitbucket-pr dump prs
./bin/bitbucket-pr dump pr 482
./bin/bitbucket-pr dump diff 482
```
