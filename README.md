# gh-review-hook

Claude Code の [Stop hook](https://docs.anthropic.com/ja/docs/claude-code/hooks) として動作する GitHub PR レビューチェッカーです。

Claude Code のセッション終了時に自動実行され、以下をチェックします:

- CI チェックの合否
- [Greptile](https://www.greptile.com/) による AI コードレビューの結果
- [CodeRabbit](https://coderabbit.ai/) によるレビューコメント
- base branch から遅れていないこと
- base branch とのマージコンフリクト

問題が検出された場合、exit code 2 で終了しフィードバックを出力します。Claude Code はこれをブロックシグナルとして扱います。PR が base branch から遅れている場合は、履歴の破壊を避けるため rebase ではなく merge で更新するよう促します。

## インストール

```bash
go install github.com/xpadev-net/gh-review-hook/cmd/gh-review-hook@latest
```

Go 1.26 以上が必要です。インストール後、`~/go/bin` が PATH に含まれていることを確認してください。

## セットアップ

インストール後、以下のコマンドで Claude Code の settings.json にフックを登録します:

```bash
gh-review-hook install
```

インタラクティブなメニューで登録先を選択できます:

- `~/.claude/settings.json` — 全プロジェクト共通（git 管理対象）
- `~/.claude/settings.local.json` — 全プロジェクト共通（git 管理対象外）
- `./.claude/settings.json` — このプロジェクト専用（git 管理対象）
- `./.claude/settings.local.json` — このプロジェクト専用（git 管理対象外）

## 必要な環境

- **GitHub 認証**: `GITHUB_TOKEN` 環境変数、または `gh auth login` 済みの [GitHub CLI](https://cli.github.com/)
- **Greptile**: リポジトリに Greptile が導入されている場合のみ使用されます

## 使い方

通常は Stop hook として自動実行されます。手動で実行する場合:

```bash
# カレントブランチの PR をチェック
gh-review-hook

# PR 番号を指定
gh-review-hook 123

# GitHub PR URL を指定
gh-review-hook https://github.com/owner/repo/pull/123
```

## ライセンス

MIT
