# kagikae 利用ガイド

[導入](../README.ja.md) · [製品概要](PRODUCT.ja.md) · [英語の CLI 仕様](CLI.md)

日常操作、設定、診断と復旧をまとめています。詳細な引数・終了コード・JSON の
契約は [CLI.md](CLI.md) が正本です。例の `main` と `side` は、自分のアカウントに
付ける名前です。秘密値をコマンド履歴や問い合わせに貼り付けないでください。

## 登録と日常操作

`kae init` は既存設定を検証し、内容を保持します。設定が壊れている場合や
読み取れない場合は失敗します。別の設定更新と競合した場合は、その処理が
終わってから再実行してください。初期化だけではアカウント登録や認証の選択を
行いません。詳細は [CLI.md](CLI.md) § kae init Semantics を参照してください。

公式 CLI のログインを通じて登録します。すでにログイン済みなら、利用中の
アカウントとストアを確認してから `--no-login` で保存します。

```bash
kae add claude main
kae add claude side
kae add --no-login codex main
kae profile set main claude main
kae profile set main codex main
kae profile set side claude side
kae profile default main

kae use main
kae use claude side
kae status
kae ls
```

プロファイルを作るだけでは
自動切替の既定値にはならず、`profile default` の設定が必要です。

グローバル共有切替はライブの認証ストアを変更します。別アカウントを同時に使う
場合は、対応する独立モードやディレクトリ固定を選んでください。

```bash
cd ~/code/side-project
kae pin side
mise trust
kae status
kae ls --pins
```

同じディレクトリで `kae pin claude main` を実行すると、そのツールの固定先を
変更します。`kae unpin` は固定を解除しますが、再固定に使う作業ストアは残ります。
`--purge` は認証コピーの削除を伴うため、通常の解除と区別してください。
削除前の保全と例外は [CLI.md](CLI.md#kae-pin-and-mise-init-semantics) にあります。

## 実行範囲を選ぶ

```bash
kae run claude main
kae run -i claude side
kae use -i main
kae use -s -P main
```

`run -s` は子プロセスの実行中、同じツールへの kae の共有切替をロックします。
終了後の復元には条件があり、途中で更新された認証を無条件に上書きしません。
ロック競合なら、先に動いている処理が終わってから再試行します。

`use -i` はグローバルの mise 設定を通じて専用ホームを選びます。`run -i` のような
子プロセス単位の指定とは異なり、mise が有効なターミナルに影響します。
自動適用の `kae use --auto --quiet` はこの手動選択を維持します。
`kae use -s -P main` は、指定プロファイルのツールを共有環境に戻します。

独立環境で共有する項目は `isolated_shared_items` で明示します。ツールごとの
対応と設定形式は [CLI.md](CLI.md)・[DATA-MODEL.md](DATA-MODEL.md) を確認してください。

## mise と補完の設定

設定の所有範囲を区別します。

| 場所・操作 | 用途 |
|---|---|
| プロジェクトの `.config/mise/conf.d/kagikae.toml` | `kae pin` が管理するディレクトリ固定 |
| グローバル mise 設定配下の `conf.d/kagikae.toml` | グローバル独立環境と、任意登録した補完フック |
| `kae mise init` | プロジェクトのタスク・補完・任意の自動切替フックを生成 |

自動切替フックを生成する場合の例です。

```bash
kae mise init --auto --write -P main
```

手書きフックの `kae use --quiet` は `kae use --auto --quiet` に変更します。
既定プロファイルか `-P main` を指定し、mise の有効化と信頼設定を確認してください。
グローバルのフラグメントへの移行では、kae が所有する既知のブロックが対象です。
独自に変更したブロックは、内容を確認して手動で移行します。
詳しくは [CLI.md](CLI.md#global-mise-integration-ownership) を参照してください。

```bash
kae completion zsh --install
kae completion --refresh
```

`--install` は対話的な初回登録です。`--refresh` は登録済みファイルを更新するもので、
初回登録の代わりにはなりません。zsh で新しい補完が出ない場合は、`fpath` と
補完キャッシュを確認します。再生成例は [README.md](../README.md#shell-completion)
にあります。

## mise での導入・更新・移行

Packslip 配布は v0.21.0 から利用でき、mise 2026.9.3 で検証しています。
通常の導入後は `kae init` を明示的に実行します。自動化したい場合だけ、
グローバル mise の `config.toml` と同じ設定ディレクトリ配下にある
ユーザー管理の `conf.d/kagikae-install.toml` に次を保存します。
`MISE_CONFIG_DIR` や XDG による設定先の変更も反映してください。

```toml
[tools]
"packslip:github.com/webkaz-labs/kagikae" = { version = "0.21.0", postinstall = '"$MISE_TOOL_INSTALL_PATH/kae" init' }
```

同じ範囲に競合するツール指定を残さず、`mise install` で適用します。
`conf.d/kagikae.toml` は kae の生成物なので、この指定を入れてはいけません。
フックは新しく導入したバイナリを絶対パスで呼び、実際のインストール時だけ
実行されます。導入済み版に後からフックを付けても実行されないため、その場合は
`kae init` を明示実行します。壊れた設定で失敗したら内容を修復し、設定や認証を
削除して処理を通そうとしないでください。

更新確認は `mise upgrade --dry-run packslip:github.com/webkaz-labs/kagikae` のように
対象を限定します。通常は設定済み範囲内の更新で、`--bump` は指定を書き換えます。
正確な版を選ぶなら
`mise use --dry-run --path <設定ファイル> packslip:github.com/webkaz-labs/kagikae@X.Y.Z`
で対象を確認し、`--dry-run` を外して適用します。既存の postinstall 指定、
公開後待機、lockfile、旧版保持方針を維持してください。プロジェクトごとの版指定や
他プロジェクトが使う版を消さないことも確認します。

GitHub バックエンドから移る場合は、まず旧バックエンドで v0.21.0 を導入し、
`kae uninstall --dry-run` で競合する補完・フックを確認します。適用すると認識済みの
ディレクトリ固定やグローバル連携も解除されるため、範囲を確認してから実行し、
必要な固定は移行後に再登録します。独自変更は手動で解決します。
`mise config ls --tracked-configs` で旧指定の場所を確認し、
`mise unuse --path <設定ファイル> --no-prune github:webkaz-labs/kagikae` でその指定だけを
外して、同じ署名付きバージョンの Packslip 指定を追加します。旧バイナリは保持される
ので、検証失敗時は旧指定を戻して調査できます。署名なし経路への自動切替や
`mise packslip forget` による信頼記録の解除で問題を隠さないでください。

mise 2026.9.3 を有効化したシェルでは、Packslip の補完ローダーが版選択に追従します。
手動読み込みは [README.ja.md](../README.ja.md) の手順を使い、版変更後に再実行します。
動的候補を問い合わせる `kae` も同じ版になるよう mise の環境を有効化してください。
不要な旧補完登録・更新フックと併用しないでください。自動登録を解除すると既存の
カスタム登録が戻る経路と、静的資材を手動で再読み込みする経路は別々に扱います。
削除は次節の連携解除後に、報告された設定元の `mise unuse --path` を実行します。
再導入で初期化を走らせたくない場合は、ユーザー管理の postinstall 指定も外します。

## アンインストールと再導入

最初に対象を確認し、追加で調べるプロジェクトを `--dir` で指定します。

```bash
kae uninstall --dry-run --json --dir ~/code/side-project
kae uninstall --yes --dir ~/code/side-project
```

生成時の内容と一致する固定設定・フック・補完を解除します。独自の変更、
読み取れない場所、移行途中の記録は残して未完了と報告します。既知の固定先と
明示した場所が対象であり、未登録のプロジェクトやシェルの独自設定は手動で
確認してください。処理後はツールを終了し、新しいシェルを開きます。

公式の直接導入と `mise run install` は v0.21.0 から導入記録を保存します。
記録と実行中のバイナリが一致し、連携解除が完了した場合に、その実行ファイルを
削除します。mise 管理のバイナリは削除せず、設定元を特定できた場合は対応する
`mise unuse --path` の手順を表示します。他のプロジェクトで使うバージョンを
残すかも確認してください。

設定、アカウント、認証情報、作業ストア、バックアップ、導入履歴は残ります。
再導入後は `kae init` で既存設定を検証し、必要な固定・補完だけを再登録します。
途中で導入記録の確定に失敗した場合は、同じ場所へ対応インストーラーで再導入
して修復します。導入記録がある場所への旧版インストーラー経路での上書きは
拒否します。詳細な拒否条件と JSON は
[CLI.md](CLI.md) § kae uninstall Semantics を参照してください。

## 設定と周辺ツール

アカウントを登録してからプロファイルを編集します。`profile set`・`unset`・
`default` で通常の組み合わせを管理できます。TOML の詳細、設定パス、秘密情報の
バックエンドは [DATA-MODEL.md](DATA-MODEL.md) を参照してください。

```toml
default_profile = "main"

[profiles.main.accounts]
claude = "main"
codex = "main"
```

周辺ツールの設定は任意です。git の設定やトークンなどをプロファイルへ登録し、
`pin` でディレクトリに適用します。

```bash
kae companion add main git email=you@example.com name="Your Name"
kae companion add main gh GH_TOKEN
kae pin main
```

トークン値は標準入力から渡します。API キーを子プロセスだけに渡したい場合は
`kae env set` と `kae run --env` を使います。子プロセスは渡された秘密値を読めるため、
実行するコマンドを確認してください。適用範囲は
[ADAPTERS-COMPANION.md](ADAPTERS-COMPANION.md) と [SECURITY.md](SECURITY.md) が正本です。

## 認証の復旧

グローバル復旧は固定ディレクトリの外で、意図したグローバルストアを確認して行います。
最初に対象ツール、アカウント、グローバルか固定先かを確認し、同じ認証を使う
他のセッションを停止します。期限情報が不明なことと、認証が失効したことは別です。

| 状況 | 次の操作 |
|---|---|
| グローバルの保存済みアカウントに再ログインしたい | 対応ツールで `kae add --restore <tool> <account>` |
| 固定ディレクトリのアカウントに再ログインしたい | その場所で `kae status` を確認し、対応する `kae relogin <tool>` |
| kae にログイン機能がないツール | 公式ツールでログインし、対象ストアと本人のアカウントを確認して `--no-login` で保存 |
| グローバル切替前の内容を戻したい | `kae backup list` で対象を確認し、`kae rollback --to <backup-id>` |
| 元のストアに保全した認証を戻したい | `kae preservation list` の記録を確認し、`kae preservation restore <id>` |

`relogin` は固定先のストアを選択します。対応フローと拒否条件は
[CLI.md](CLI.md#kae-relogin-semantics) を参照してください。手動で固定先にログインする
場合は、mise の有効化・信頼・有効な環境変数を確認します。ディレクトリ移動だけでは
保存先の確認になりません。

バックアップと保全記録は別の復元経路です。保全記録は元のストアへ戻すもので、
グローバルホームへ自動的に振り替えません。より新しい認証がある場合の扱いなど、
コマンドが示す条件を確認してください。

`rollback --dry-run` は復元対象や件数の確認であり、バックエンドや現ストアを含む
復元成功の保証ではありません。復元してもサービス側で失効した認証は更新されません。

## 診断と一覧の問題

```bash
kae doctor --json
kae status --json
kae backup list --json
kae preservation list --json
```

フィルターなしの `doctor` で固定状態も含めて確認します。トークン companion の
ライブ識別確認にはネットワーク接続と確認が必要になる場合があります。
`--yes` はその確認を受け入れる指定であり、保存先や整合性の拒否条件を解除しません。

バックアップ・保全記録の一覧は設定ファイルが不正でも、解決された状態ディレクトリの
メタデータを読みます。不完全な一覧は読めた行を残し、`complete: false` と `issues` を
返して非ゼロで終了します。設定警告だけで一覧が完全な場合とは区別してください。

- `metadata_unreadable` は、ファイルと親ディレクトリの権限を確認します。
- `metadata_invalid` は、[データモデル](DATA-MODEL.md)と照合して形式を確認します。
- 問題項目の名前を隠すためのハッシュは、復元用の ID ではありません。
- `pending`・`deleting` の保全記録は、そのまま復元元に使える状態ではありません。

読めない項目を安易に削除すると、残された復旧手段を失うことがあります。
詳細な分類と対処は [CLI.md](CLI.md#recovery-guidance) を参照してください。

## 安全性と問い合わせ

共有切替で書き換える対象はアダプターの許可リストに限定します。
秘密情報の保存先は設定したバックエンドに従います。通常のレポートで秘密値を
伏せていても、メール、アカウント名、ID、絶対パスなどのメタデータは確認が必要です。
子プロセスや公式ログイン画面の出力は、そのプログラムの出力です。

問い合わせには、秘密情報を除いたコマンド、OS、インストール方法、バージョン、
実行範囲、終了コード、関連する標準エラー出力を添えます。生の認証ファイルを
添付しないでください。[GitHub Issues](https://github.com/webkaz-labs/kagikae/issues)
で報告できます。

上流のバージョン警告だけで互換・非互換を断定せず、
[上流認証調査手順](../.claude/skills/upstream-auth-drift/SKILL.md)に従って確認します。
内部設計・検証手順は [AGENTS.md](../AGENTS.md#documentation-map) から参照できます。
