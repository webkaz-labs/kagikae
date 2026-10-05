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

[mise](https://mise.jdx.dev) では v0.21.0 から署名付き Packslip 配布を使えます。
検証済みの最小 mise バージョンは 2026.9.3 です。

```bash
mise use -g packslip:github.com/webkaz-labs/kagikae@0.21.0
kae version
kae init
```

mise が署名とアーカイブを検証します。`mise packslip pins` で受理済みの署名者を
確認でき、検証エラー時に信頼記録や公開後待機・lockfile の方針を緩める必要は
ありません。旧リリースには Packslip がないため、
`mise use -g github:webkaz-labs/kagikae@vX.Y.Z` を使います。
Packslip の選択対象は macOS の arm64 と GNU/Linux の amd64/arm64 です。v0.24.0 より前の
リリースは macOS の amd64 も選択対象です。musl 環境は
この選択対象に含めず、直接アーカイブを使う導入経路とは区別します。
任意の自動初期化、更新、バックエンド移行、削除は
[利用ガイド](docs/GUIDE.ja.md#mise-での導入更新移行) を参照してください。

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
ローカルビルドでは、kae 管理の補完ファイルに必要な場合だけ
`kae completion --refresh` を実行します。mise 2026.9.3 を有効化したシェルでは、
Packslip の補完ローダーが選択中の版に追従します。このローダーは補完だけを読み込み、
`kae cd` に必要な kae シェル関数は定義しません。`kae cd` を使う場合は rc に
`eval "$(kae completion zsh)"` も追加してください。競合する kae の更新フックや
静的登録は整理してください。手動で読む場合は bash/zsh で
`eval "$(mise completion bash --tool kae)"`（zsh は `bash` を `zsh` に変更）、
fish で `mise completion fish --tool kae | source` を使い、版変更後に再実行します。
動的候補も選択中の `kae` を使うよう、mise の環境を有効化してください。
隔離検証では Bash・Zsh の登録と版切り替えを実行しています。fish は資材取得までで、
実シェルでの検証は未実施です。

macOS の arm64 と Linux の amd64/arm64 向けバイナリ、チェックサム、ビルド来歴の証明は
[GitHub Releases](https://github.com/webkaz-labs/kagikae/releases) にあります。
Intel Mac（darwin/amd64）は v0.24.0 から対象外です。これ以降のリリースには
darwin/amd64 のアーカイブが無く、シェルインストーラーも導入を拒否します。`go install` による
ソースからのビルドも Intel Mac では未検証です。それより前の
リリースで公開済みの darwin/amd64 アーカイブはそのまま残ります。
Windows 向けリリースは保留中です。ログインには対象ツールの公式 CLI が必要です。

## 最初の設定

`use` はグローバル切替、`pin` は現在のディレクトリへの固定、`run` は子プロセスの
実行です。既定は共有環境の `-s`、独立した環境を選ぶ場合は `-i` を付けます。
`pin` にはもう 1 つ、アカウントを替えても同じ環境を保つツリーモードの `-t` があります。

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

`kae` と `kae ls` は、アカウントごとの利用枠を短く出します（例: `5h 16% (2h13m) · 7d 95% (3d4h)`。括弧内はリセットまでの残り時間）。
`kae`、`kae ls`、`kae accounts` の表は、既定ではドライバー（`Driver`）列と識別子（`Identity`）列（ログインしたメールアドレスなど）を省きます。
`--full`（`-f`）を付けると表示し、`--json` には常に含まれます。
ツールが手元に書いた記録を優先し、それが無いときは前回の記録を使います。

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

あわせて `./.config/<tool>` から、そのツールのストア
（`~/.local/share/kagikae/isolation/<pin-id>/` 以下）への symlink を張ります。
ストアの場所はディレクトリのパスのハッシュで決まるため、リンクがないと、ここから
ストアを名指すのはフラグメントの `[env]` 行だけになります。リンクも同じ仕組みで
除外します。実体のコピーではありませんが、symlink を辿ってコピーするプログラムは
ストアそのものを読みます。同期フォルダ配下に固定する前に
[docs/SECURITY.md](docs/SECURITY.md) § Store links in a bound directory を
参照してください。

```bash
kae pin -i side          # 設定やセッションも独立させる
kae pin -t side          # ツリーモード（Claude のみ）: このディレクトリ以下で 1 つの
                         # 環境を保ち、アカウントを替えても会話や設定を残す
kae pin claude main      # このディレクトリの Claude だけ変更
kae pin side --no-link   # このディレクトリにストアへのリンクを置かない
kae ls --pins            # ディレクトリをまたいで固定状態を一覧（kae ls pin と同じ）
kae unpin               # 現在のディレクトリの固定を解除
```

`kae ls` は、アカウントとプロファイルに続けて「場所」（ツールが設定を読むディレクトリ、
固定したディレクトリ、リポジトリのルート、kae 自身のディレクトリ）を番号付きで一覧します。
`kae ls claude` のように対象を 1 つ指定でき、パスは次のように取り出せます。

```bash
cd "$(kae ls claude --current)"                  # ここで claude が使うユーザーレベル
cd "$(kae ls codex --current --project --root)"  # .codex/ を持つ最も近いプロジェクト
kae ls claude --at 2                             # kae ls claude の 2 番目の場所
kae cd claude                                    # 同じ移動を kae シェル関数で
kae open repo                                    # リポジトリのルートをファイルマネージャーで開く
kae cd                                           # 対象なし: すべての場所からピッカーで選ぶ
kae cd claude --pick                             # 現在の場所があっても claude の場所から選ぶ
```

ユーザーレベルはシェルの環境ではなく、そのツールを固定している最も近い固定ディレクトリに
記録された固定から決まります。claude の一覧にはユーザーレベルの直後にもう 1 行あり、
このディレクトリのセッション記録を置く `projects/` 以下のディレクトリです（claude が
まだ書き込んでいなければ `(missing)`）。ユーザーレベルに従って動きます。
`.claude/` と `.codex/` の探索規則は
[docs/CLI.md](docs/CLI.md) § kae ls Semantics にあります。`kae cd` と `kae open` は
同じ対象と選択子を取り、既定は現在の場所です。対象がない場合や複数の場所に一致した
場合、または `--pick` を付けた場合は、端末上に絞り込めるピッカーを開きます（文字入力で
絞り込み、Enter で決定、Esc で絞り込みの解除かキャンセル。キャンセルは終了コード `130`）。
端末がない場合（標準入力を `/dev/null` にした場合など）は、候補を一覧して使い方エラー
になります（[docs/CLI.md](docs/CLI.md) § kae open and kae cd Semantics）。`kae cd` には、`eval "$(kae completion zsh)"` などで補完スクリプトを
読み込むと定義される kae シェル関数が必要です。関数がないと、代わりに実行する
`cd "$(kae ls … --current)"` を表示します。

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
`eval "$(kae completion zsh)"` を利用できます。この読み込み（と mise フック）は
`kae cd` を動かす kae シェル関数も定義しますが、補完ファイルには含まれません。
補完ファイルを手で書く場合は `kae completion zsh --no-function > ~/.zfunc/_kae` の
ように `--no-function` を付けます。登録済みファイルの更新は `kae completion --refresh` です。

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
終了コードと標準エラー出力も残してください。
ロケールが日本語なら `kae` の表示は日本語になります。英語の表示が必要なときは
`KAE_LANG=en kae doctor` のように `KAE_LANG=en` を付けてください（`--json` の出力は常に英語です）。
共有前にはアカウント名、メール、ID、絶対パスなどの個人情報を取り除き、
認証情報そのものは添付しないでください。

バックアップ一覧と保全記録一覧は、不正な設定ファイルがあってもメタデータを
確認できます。一覧が不完全なら、読めた行と問題を報告して非ゼロで終了します。
読めない行を削除して済ませず、[診断と一覧の問題](docs/GUIDE.ja.md#診断と一覧の問題)
に従って確認してください。

削除・復元前には対象と保存先を確認します。`--yes` や `--force` は、あらゆる
拒否条件を解除する指定ではありません。詳細は[利用ガイド](docs/GUIDE.ja.md)と
[セキュリティ仕様](docs/SECURITY.md)を参照してください。

## アンインストール

`kae uninstall --dry-run` で対象を確認します。追加のプロジェクトは
`--dir ~/code/side-project` で指定できます。対話的に確認して実行するか、
確認済みの内容を `kae uninstall --yes` で適用してください。
認識できる生成設定を解除した後、導入記録が一致する直接インストール版だけを
最後に削除します。公式 installer と `mise run install` は v0.21.0 から記録を残します。
mise 管理版・導入記録のない実行ファイルには手動手順を案内し、カスタム設定や
実行ファイルの削除が残れば未完了として報告します。設定、認証、スナップショット、
バックアップ、作業ストア、削除履歴は保持します。独自のシェル設定も確認し、
解除後は新しいシェルを開いてください。復旧と対象範囲は
[CLI 仕様](docs/CLI.md#kae-uninstall-semantics)を参照してください。

## ドキュメントと開発

| 文書 | 内容 |
|---|---|
| [製品概要](docs/PRODUCT.ja.md) | 目的、使い分け、対応範囲 |
| [利用ガイド](docs/GUIDE.ja.md) | 日常操作、設定、診断、復旧、安全性 |
| [英語 README](README.md) | コマンド一覧と詳しい導入説明 |
| [CLI 仕様](docs/CLI.md) | コマンドの詳細、終了コード、JSON 契約 |
| [開発ガイド](AGENTS.md) | 設計・検証・計画・リリース文書への案内 |

日本語文書は利用者向けです。内部設計、詳細 JSON 仕様、実機検証記録は英語の
正本を参照します。JSON のトークンは英語のままです。CLI の表示言語はロケールに従います（[利用ガイド](docs/GUIDE.ja.md)）。

開発時の全体検証は `mise run check`、説明文のみの検証は `mise run docs-check`。
変更内容による追加検証は [AGENTS.md](AGENTS.md#validation) に従ってください。

ライセンスは [MIT](LICENSE) です。
