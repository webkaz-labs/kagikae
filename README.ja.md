# kagikae

[English](README.md) | 日本語

`kae` は、AI コーディング CLI の保存済みアカウントを切り替えるツールです。
普段の設定や作業環境を共有したまま認証を切り替え、必要ならプロジェクトごとの
アカウント固定や、独立した作業環境も選べます。

Claude Code、Codex CLI、Antigravity、OpenCode、Cursor CLI、GitHub Copilot CLI の
アダプターがあります。使えるモードと OS はツールごとに異なります。
詳細は[対応範囲](#対応範囲)を確認してください。

保存した認証情報が使えるのは、サービス側が受け付ける間です。期限切れ・失効・
更新によって再ログインが必要になります。バックアップの復元は、認証の有効性を
取り戻す操作ではありません。

## インストール

最新のリリースを `~/.local/bin/kae` に配置する方法です。インストーラーは
リリースのチェックサムを検証してからコピーします。

```bash
curl -fsSL https://raw.githubusercontent.com/webkaz-labs/kagikae/main/scripts/install.sh | sh
```

バージョンと配置先を指定する場合は、`vX.Y.Z` を実在するリリースタグに置き換えます。

```bash
curl -fsSL https://raw.githubusercontent.com/webkaz-labs/kagikae/main/scripts/install.sh |
  sh -s -- --version vX.Y.Z --install-dir ~/.local/bin
```

[mise](https://mise.jdx.dev) で管理する場合も、タグを指定します。

```bash
mise use -g github:webkaz-labs/kagikae@vX.Y.Z
kae version
```

Go からビルドする場合は次のとおりです。実行ファイル名は `kagikae` になるので、
以下の利用例に合わせるには `kae` のエイリアスなどを用意してください。

```bash
go install github.com/webkaz-labs/kagikae@latest
```

更新時は、利用したインストール方法で目的のバージョンを再指定します。
シェルが参照する実行ファイルも確認してください。

```bash
command -v kae
kae version
```

`~/.local/bin` を使う場合は、そのディレクトリを `PATH` に追加してください。
シェルインストーラーは登録済みの補完ファイルも更新します。mise 経由の更新や
ローカルビルドでは、必要に応じて `kae completion --refresh` を実行します。

macOS と Linux の amd64/arm64 向けバイナリ、チェックサム、ビルド来歴の証明は
[GitHub Releases](https://github.com/webkaz-labs/kagikae/releases) にあります。
Windows 向けリリースは保留中です。ログインには対象ツールの公式 CLI が必要です。

## 最初の設定

`use` はグローバル切替、`pin` は現在のディレクトリへの固定、`run` は子プロセスの
実行です。既定は共有環境の `-s`、独立した環境を選ぶ場合は `-i` を付けます。

```bash
kae init
kae doctor

# 公式のログイン手順を通して、それぞれのアカウントを保存
kae add claude main
kae add claude side

# 登録済みアカウントをプロファイルにまとめる
kae profile set main claude main
kae profile set side claude side
kae profile default main

kae use main
kae use claude side
kae ls
kae status
```

すでにログインしている状態を保存する場合は、本人の意図したアカウントであることを
確認してから `--no-login` を使います。別のツールは個別に登録します。

```bash
kae add --no-login codex main
kae profile set main codex main
```

`kae use --dry-run` で変更予定を確認できます。`kae rollback` は復元可能な
グローバルバックアップから戻す操作です。失効した認証の更新方法は
[復旧ガイド](docs/GUIDE.ja.md#認証の復旧)を参照してください。

## プロジェクト・worktree ごとに固定

```bash
cd ~/code/side-project
kae pin side
mise trust
```

mise の有効化と設定への信頼が必要です。`pin` の直後から `trust` までの間は、
mise が未信頼の設定を拒否することがあります。

`pin` はプロジェクト内の `.config/mise/conf.d/kagikae.toml` を管理します。
利用者の `mise.toml` は編集しません。このフラグメントはマシン固有なので、
Git リポジトリ内では共通の exclude ファイルを使って追跡対象から除外します。

```bash
kae pin -i side          # 設定やセッションも独立させる
kae pin claude main      # このディレクトリの Claude だけ変更
kae ls --pins            # ディレクトリをまたいで固定状態を一覧
kae unpin               # 現在のディレクトリの固定を解除
```

worktree も別のディレクトリとして扱います。

```bash
git worktree add ../main-app-review -b review
cd ../main-app-review
kae pin side
```

同一アカウントの Claude の固定先は、そのアカウントの認証ストアを共有します。
これは上流プロセスの同時更新やログイン継続を保証するものではありません。
仕組みは [ADAPTERS.md](docs/ADAPTERS.md) を参照してください。

## プロセス単位の実行と自動切替

```bash
kae run claude main
kae run -i claude side
kae run codex main -- codex exec "go test ./..."

# 値は標準入力から渡す
kae env set claude ci ANTHROPIC_API_KEY
kae run --env claude ci -- claude -p "review this"
```

共有モードの `run` は子プロセスの実行中、そのツールの共有ストアをロックします。
同じツールを別アカウントで同時利用する場合は、対応している `run -i` や
ディレクトリ固定を選びます。

自動切替には既定プロファイルか明示的な `-P` 指定が必要です。

```bash
kae profile default main
kae use --auto --quiet
```

`--auto` は手動で選択したグローバル独立環境を維持します。
プロファイル内のツールを共有環境に戻すには `kae use -s -P main` を使います。
フックの設定方法は[利用ガイド](docs/GUIDE.ja.md#mise-と補完の設定)にまとめています。

## 周辺ツールとシェル補完

プロファイルには、git の署名設定、gh・Cloudflare のトークン、kubectl の
設定パスも必要なものだけ登録できます。ディレクトリへの `pin` を通じて適用します。

```bash
kae companion add main git email=you@example.com name="Your Name"
kae companion add main gh GH_TOKEN
kae pin main
```

各ツールの適用範囲は [ADAPTERS-COMPANION.md](docs/ADAPTERS-COMPANION.md) が正本です。
補完は bash・zsh・fish に対応しています。

```bash
kae completion zsh --install
```

対話的に登録先を選びます。設定ファイルに直接読み込みを書く場合は、zsh なら
`eval "$(kae completion zsh)"` を利用できます。登録済みファイルの更新は
`kae completion --refresh` です。

## 対応範囲

ツールごとの切替対象・保持対象は [ADAPTERS.md](docs/ADAPTERS.md)、対応する
モードの区分は [PRODUCT.md](docs/PRODUCT.md#tool-tiers) が正本です。

- 共有切替では、認証に必要な許可対象だけを変更します。
- ディレクトリ固定と `-i` は、対応するツールで利用できます。
- Codex のディレクトリ別 keyring 固定は、実機での能力確認が済むまで無効です。
- Cursor の Linux アダプターは未対応です。Linux バイナリがあっても、すべての
  アダプターが利用できるわけではありません。

## 困ったとき・安全な利用

```bash
kae doctor --json
kae status --json
kae version
```

まずフィルターなしの `doctor` で、固定状態も含めて確認します。
終了コードと標準エラー出力も残してください。共有前にはアカウント名、メール、
ID、絶対パスなどの個人情報を取り除き、認証情報そのものは添付しないでください。

バックアップ一覧と保全記録一覧は、不正な設定ファイルがあってもメタデータを
確認できます。一覧が不完全なら、読めた行と問題を報告して非ゼロで終了します。
読めない行を削除して済ませず、[診断と一覧の問題](docs/GUIDE.ja.md#診断と一覧の問題)
に従って確認してください。

削除・復元前には対象と保存先を確認します。`--yes` や `--force` は、あらゆる
拒否条件を解除する指定ではありません。詳細は[利用ガイド](docs/GUIDE.ja.md)と
[セキュリティ仕様](docs/SECURITY.md)を参照してください。

## アンインストール

`kae uninstall` コマンドはありません。先に `kae` を呼ぶシェル・mise フックを
解除してから、利用した管理方法で実行ファイルを削除してください。
バイナリの削除では、設定、固定先、補完、スナップショット、バックアップ、保全記録は
消えません。`kae unpin` はディレクトリ固定の解除であり、製品全体の削除ではありません。

## ドキュメントと開発

| 文書 | 内容 |
|---|---|
| [製品概要](docs/PRODUCT.ja.md) | 目的、使い分け、対応範囲 |
| [利用ガイド](docs/GUIDE.ja.md) | 日常操作、設定、診断、復旧、安全性 |
| [英語 README](README.md) | コマンド一覧と詳しい導入説明 |
| [CLI 仕様](docs/CLI.md) | コマンドの詳細、終了コード、JSON 契約 |
| [開発ガイド](AGENTS.md) | 設計・検証・計画・リリース文書への案内 |

日本語文書は利用者向けです。内部設計、詳細 JSON 仕様、実機検証記録は英語の
正本を参照します。CLI の表示と JSON のトークンは英語のままです。

開発時の全体検証は `mise run check`、説明文のみの検証は `mise run docs-check`。
変更内容による追加検証は [AGENTS.md](AGENTS.md#validation) に従ってください。

ライセンスは [MIT](LICENSE) です。
