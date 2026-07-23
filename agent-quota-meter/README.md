# Agent Quota Meter

Herdr の Agents サイドバーに、Claude Code / Codex のコンテキスト使用量と
レートリミット残量を表示するプラグインです。

- plugin ID: `boooowy.agent-quota`
- version: `0.3.0`
- platforms: macOS / Linux

## 表示

```text
▼ Agents
 ● herdr-agent-quota · codex
   40%   ᗧ········ᗣ
   1m: 92% left
 ● my-other-repo · claude
   ctx 16%
   5h: 43% · 1w: 70% left
```

| Herdr の状態 | context | quota |
|---|---|---|
| `working` | Pac-Man アニメーション。1秒ごとに更新 | 表示 |
| `done` | `ctx N%` | 表示 |
| `blocked` | `ctx N%` | 表示 |
| `idle`（`done` / `working` 直後） | `ctx N%`を5分間表示 | 5分間表示 |
| `idle`（上記以外） | 非表示 | 非表示 |
| `unknown` | 非表示 | 非表示 |

`working` の Pac-Man は約11秒で左から右へ進み、コンテキスト使用率と
agent の稼働状態を同時に表します。Pac-Man は `working` の間だけ表示され、
完了後の5分間は静的な `ctx N%` 表示になります。サイドバーの quota は使用可能な残量です。

context使用率とquota残量は、通常・注意・警告・危険の4段階で
緑 → 黄 → 橙 → 赤へ変化します。quotaはwindowごとに個別着色されます。

| 段階 | 色 | context使用率 | quota残量 |
|---|---|---:|---:|
| 通常 | `#50fa7b` | 60%未満 | 60%以上 |
| 注意 | `#f1fa8c` | 60–79% | 30–59% |
| 警告 | `#ffb86c` | 80–89% | 15–29% |
| 危険 | `#ff5555` | 90%以上 | 15%未満 |

## 必要環境

- Herdr 0.7.0 以上。カスタムトークン表示は 0.7.4 以上を推奨
- macOS または Linux
- Go 1.26.4以上（インストール時のplugin buildに使用）
- 実行時にPython、Bash、`jq`などの追加依存は不要
- Claude Code の quota 表示: Claude Code にログイン済みであること
- Codex の quota 表示: `~/.codex/sessions` に `rate_limits` の記録があること

Claude Code または Codex の片方だけを使う構成でも動作します。

## インストール

GitHub からインストールします。

```bash
herdr plugin install boooowy/herdr_plugins/agent-quota-meter
```

ローカル開発では、このリポジトリ内のプラグインディレクトリをリンクします。

```bash
cd /path/to/herdr_plugins/agent-quota-meter
make plugin-link
```

`~/.config/herdr/config.toml` に Agents サイドバーの行テンプレートを追加します。

```toml
[ui.sidebar.agents]
rows = [
  ["state_icon", "workspace", "agent"],
  [
    { token = "$context_normal",  fg = "#50fa7b", bold = true, dim = false },
    { token = "$context_caution", fg = "#f1fa8c", bold = true, dim = false },
    { token = "$context_warning", fg = "#ffb86c", bold = true, dim = false },
    { token = "$context_danger",  fg = "#ff5555", bold = true, dim = false },
  ],
  [
    { token = "$quota_1_normal",  fg = "#50fa7b", bold = true, dim = false },
    { token = "$quota_1_caution", fg = "#f1fa8c", bold = true, dim = false },
    { token = "$quota_1_warning", fg = "#ffb86c", bold = true, dim = false },
    { token = "$quota_1_danger",  fg = "#ff5555", bold = true, dim = false },
    { token = "$quota_2_normal",  fg = "#50fa7b", bold = true, dim = false },
    { token = "$quota_2_caution", fg = "#f1fa8c", bold = true, dim = false },
    { token = "$quota_2_warning", fg = "#ffb86c", bold = true, dim = false },
    { token = "$quota_2_danger",  fg = "#ff5555", bold = true, dim = false },
    { token = "$quota_3_normal",  fg = "#50fa7b", bold = true, dim = false },
    { token = "$quota_3_caution", fg = "#f1fa8c", bold = true, dim = false },
    { token = "$quota_3_warning", fg = "#ffb86c", bold = true, dim = false },
    { token = "$quota_3_danger",  fg = "#ff5555", bold = true, dim = false },
  ],
  [
    { token = "$quota_4_normal",  fg = "#50fa7b", bold = true, dim = false },
    { token = "$quota_4_caution", fg = "#f1fa8c", bold = true, dim = false },
    { token = "$quota_4_warning", fg = "#ffb86c", bold = true, dim = false },
    { token = "$quota_4_danger",  fg = "#ff5555", bold = true, dim = false },
    { token = "$quota_5_normal",  fg = "#50fa7b", bold = true, dim = false },
    { token = "$quota_5_caution", fg = "#f1fa8c", bold = true, dim = false },
    { token = "$quota_5_warning", fg = "#ffb86c", bold = true, dim = false },
    { token = "$quota_5_danger",  fg = "#ff5555", bold = true, dim = false },
  ],
]
row_gap = 0
```

各段階では4つの候補tokenのうち1つだけに値が入り、段階が変わると以前のtokenを
明示的に消去します。`dim = false` は、親のスタイルから薄い表示を継承しないための指定です。
既存設定との互換性のため、pluginは従来の `$context` / `$quota` も引き続きreportします。

値のないカスタムトークンだけの行は省略されるため、状態や取得データに応じて
各 agent は通常1〜3行で表示されます。quota windowが4つ以上ある場合だけ、
4・5個目を追加行に表示します。

設定を反映し、最初の収集を開始します。

```bash
herdr server reload-config
herdr plugin action invoke refresh --plugin boooowy.agent-quota
```

以降は次のイベントで ticker の起動と即時再描画が行われます。

- `pane.agent_detected`
- `pane.agent_status_changed`
- `workspace.focused`
- `pane.closed`

### macOS の Keychain 許可

Claude Code の quota 取得では、Keychain の `Claude Code-credentials` から
アクセストークンを読みます。初回に macOS の許可ダイアログが表示された場合は
許可してください。認証情報は Anthropic usage API の呼び出しだけに使用し、
ログ・画面・状態ファイルには出力しません。

## データと更新間隔

### コンテキスト使用量

- Claude Code: Herdr が検出した session ID と
  `~/.claude/projects/*/<session-id>.jsonl` を対応付け、最後の assistant usage を使用
- Codex: pane の作業ディレクトリと
  `~/.codex/sessions/**/rollout-*.jsonl` の `session_meta.cwd` を対応付け、
  最後の `token_count` を使用

Claude Code の分母はモデル名から判定します。`[1m]`、Fable、Mythos、Sonnet 5、
Opus 4.6 / 4.7 / 4.8、Sonnet 4.6 は1M、それ以外は200kです。
Codex はセッションが報告する `model_context_window` を使用します。

表示の再計算間隔は次のとおりです。

- `working`: 1秒
- ほかに `working` がいる間の `done` / `blocked`: 5秒
- 全 agent が非 `working`: 30秒
- 状態変更などのイベント発生時: sleep を中断して即時更新

表示中のpaneでは `done` が一瞬で `idle` になることがあるため、
`working → idle` も完了相当として扱い、静的なcontextとquotaを5分間残します。

### レートリミット

- Codex: 最近の session JSONL にある `rate_limits` をローカルで集計
- Claude Code: Anthropic OAuth usage API から取得
- quota collectorの実行は60秒デバウンス
- Claude Code の成功データは5分キャッシュ
- Claude Code で有効な成功データがない状態の取得失敗は、10分間再試行しない
- Claude Code の一時的な失敗では、30分以内の直近成功データを維持する

ticker は現在のキャッシュ値をサイドバーへ報告してから quota を収集し、
収集完了後は通常の待機を挟まず最新値を再報告します。

### TTL と ticker のライフサイクル

- ticker 動作中の metadata TTL: 5分
- 全 agent が非 `working` の状態が10分続くと ticker は終了
- 終了時の `done` / `blocked` 表示 TTL: 15分
- `done` / `working` から `idle` になったpaneは、context / quotaを5分後に消去
- それ以外の `idle` / `unknown` は、TTLを待たず context / quotaを明示的に消去
- ticker のロックはPIDとheartbeatで管理し、2分以上古いロックは次回起動時に回収

5分の通常TTLにより、quota収集中の一時停止でサイドバー行が消えて再描画されることを
防ぎます。

## 構成

| ファイル | 役割 |
|---|---|
| `herdr-plugin.toml` | plugin build、events、actions、dashboard paneの定義 |
| `main.go` | 単一バイナリのsubcommand入口 |
| `ticker.go` / `sessions.go` | agent監視、context計算、metadata報告、sessionログ読取 |
| `collect.go` / `state.go` | Claude/Codex quota収集、デバウンス、state JSON管理 |
| `lock.go` | tickerのsingleton lock、stale/旧Python ticker回収 |
| `dashboard.go` | dashboard overlayの表示と操作 |
| `scripts/build.sh` | pluginインストール時のGo build |

## Quota ダッシュボード

コマンドパレットの **Quota: open dashboard**、または次のコマンドで開きます。

```bash
herdr plugin action invoke open-dashboard --plugin boooowy.agent-quota
```

```text
  Agent Quota Meter
  ============================================

  Claude Code
    5h       ■■■■■■■■■■■■■■■■■■■■■□□□  89.0%  リセット: 07/22 23:59
    1w       ■■■□□□□□□□□□□□□□□□□□□□□□  13.0%  リセット: 07/28 15:59
    (取得: 12:34:56)

  Codex
    1m       ■■■■■■■■■■■■■■■■■■■■■■■□  97.0%  リセット: 08/12 18:53
    (取得: 12:34:30)

  [q] 閉じる / 30秒ごとに自動更新
```

ダッシュボードのバーとパーセントは、サイドバーの残量とは逆に使用率を表します。
30秒ごとに再描画し、`q` で閉じます。

## 状態ファイル

Herdr から起動した場合は `HERDR_PLUGIN_STATE_DIR` に保存します。通常は
`~/.local/state/herdr/plugins/boooowy.agent-quota` です。Herdr 外で直接実行した場合は
`~/.cache/herdr-agent-quota` を使用します。

| パス | 内容 |
|---|---|
| `claude.json` | Claude Code quota と取得状態 |
| `codex.json` | Codex quota と取得状態 |
| `last_update` | 60秒デバウンス用timestamp |
| `ascii` | 存在する場合はPac-ManをASCII表示に切り替え |

ticker の singleton lock は plugin ID に依存しない
`~/.cache/herdr-agent-quota/ticker.lock/pid` に保存します。1行目がPID、2行目が
所有するplugin IDです。plugin IDを変更しても新しいtickerが旧tickerを停止して
lockを引き継ぐため、複数のPac-Man更新が競合しません。テストや特殊な起動環境では
`AGENT_QUOTA_RUNTIME_DIR` でruntime directoryを変更できます。初回移行時は
現IDのstate directoryと旧 `ntj.agent-quota` のlockも安全に回収します。

## 既知の制限

- 同じディレクトリで複数の Codex pane を開いた場合、paneごとのsession IDを利用できないため、
  最新sessionの同じcontext使用率が各paneに表示されます
- Claude Code のcontextはsession IDでpaneと対応付けます
- セッションにusage / token_countがまだ記録されていない場合、contextは表示されません
- quotaの取得元に利用可能なwindowがない場合、quotaは表示されません

`ᗧ` や `ᗣ` が表示できないフォントではASCIIモードを利用できます。

```bash
touch ~/.local/state/herdr/plugins/boooowy.agent-quota/ascii
herdr plugin action invoke refresh --plugin boooowy.agent-quota
```

Herdr の起動元で `AGENT_QUOTA_ASCII=1` を設定する方法にも対応しています。

## 開発

```bash
make test         # go test -race ./...
make vet          # go vet ./...
make build        # ./bin/agent-quota-meter にビルド
make plugin-link  # ビルドしてherdrへ登録
```

## トラブルシューティング

### 表示されない

```bash
herdr plugin action invoke refresh --plugin boooowy.agent-quota
herdr plugin log list --plugin boooowy.agent-quota --limit 10
```

`idle` / `unknown` の agent は仕様上、contextとquotaを表示しません。

### Claude Code の quota だけ表示されない

- Claude Code のログイン状態を確認する
- macOS では Keychain のアクセス許可を確認する
- `credentials unavailable` の場合は Claude Code を一度起動し、認証情報を更新する
- 有効な成功データがない状態でAPI取得に失敗すると10分間バックオフするため、
  直後のrefreshでも再取得しない場合がある

### Codex の quota だけ表示されない

最近のCodex sessionに `rate_limits` がない可能性があります。Codexを一度実行し、
session JSONLを更新してください。

### contextだけ表示されない

agentで一度promptを実行し、session JSONLにusage / token_countを記録してください。
tickerのPIDは次のコマンドで確認できます。

```bash
RUNTIME_DIR=~/.cache/herdr-agent-quota
ps -p "$(sed -n '1p' "$RUNTIME_DIR/ticker.lock/pid")"
```

## アンインストール

```bash
herdr plugin unlink boooowy.agent-quota
```

`~/.config/herdr/config.toml` の `[ui.sidebar.agents]` を削除し、設定を再読み込みします。

```bash
herdr server reload-config
```
