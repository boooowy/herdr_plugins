# herdr_plugins

自作 herdr プラグイン置き場。各プラグインはこのリポジトリ直下の個別ディレクトリに置き、そのプラグインのディレクトリを指定して `herdr plugin link <プラグインのディレクトリ>` で個別に登録する。

- [hint-copy](hint-copy/) — 画面上の URL・パス・IP・SHA などをヒントラベルでキーボードコピー(tmux-thumbs 方式)
- [agent-quota-meter](agent-quota-meter/) — Claude Code / Codex のコンテキスト使用量とレートリミット残量を Agents サイドバーに表示
- [bb-pr](bb-pr/) — Bitbucket Cloud の PR を herdr 内で閲覧（一覧・詳細・hunk 単位 diff・レビューコメント、読み取り専用）
