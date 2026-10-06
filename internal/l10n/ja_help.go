package l10n

// jaHelp renders `--help`, usage synopses and what a flag parse failure prints
// (internal/cmd/flagusage.go); the flag descriptions are jaFlags.
var jaHelp = map[string]string{
	"flag provided but not defined: %s":   "定義されていないフラグです: %s",
	"flag needs an argument: %s":          "フラグに値が指定されていません: %s",
	"invalid value %q for flag %s: %s":    "フラグ %[2]s の値 %[1]q は無効です: %[3]s",
	"invalid boolean value %q for %s: %s": "フラグ %[2]s の真偽値 %[1]q は無効です: %[3]s",
	"Usage of %s:":                        "%s の使い方:",
	"usage: %s":                           "使い方: %s",
	"usage: %s companion add <profile> <id> KEY=VALUE... (or one bare KEY for a token, value on stdin)": "使い方: %s companion add <profile> <id> KEY=VALUE...（トークンは KEY を 1 つだけ指定し、値は標準入力から渡します）",
	"usage: %s env set <tool> <account> KEY=VALUE... (or one bare KEY with the value on stdin)":         "使い方: %s env set <tool> <account> KEY=VALUE...（KEY を 1 つだけ指定し、値は標準入力から渡すこともできます）",
	"(default %s)": "（既定値: %s）",

	// kae help (printHelp in internal/cmd/cmd.go): one entry per section. The
	// synopsis column and the flag names are verbatim, and the descriptions start
	// at the column the English pads to.
	`kae - switch AI coding CLI subscription accounts (kagikae)

Two verbs by scope plus run: use = switch now (global), pin = bind this
directory (-s/--shared default, -i/--isolated), run = one process.
`: `kae - AI コーディング CLI のサブスクリプションアカウントの切替（kagikae）

範囲で分かれた 2 つの動詞と run があります。use は今すぐの切替（グローバル）、
pin はこのディレクトリの固定（既定は -s/--shared、-i/--isolated）、run は
1 つのプロセスだけへの適用です。
`,
	`Usage:
  kae [-f|--full]                      status summary: this directory's pin,
                                       global profile, tools, profiles
  kae init                             create config and directories
  kae edit                             open the config in $VISUAL / $EDITOR
  kae doctor [tool] [--json]           environment / auth health checks (alias: kae d)
  kae add <tool> [<account>]           register an account (official login
                                       flow + snapshot; --no-login snapshots
                                       the current login instead)
  kae use [-s|-i] [-P <profile>]       bare: resolve the profile and apply it
                                       idempotently (the former kae apply)
  kae use --auto [-P <profile>]       automatic apply; retain global isolation
                                       (--quiet suppresses success output)
  kae use [-s|-i] <profile>            switch every tool now (alias: kae u)
  kae use <tool> <account>             switch one tool now
  kae pin [-s|-i|-t] [<profile>]       bind this directory (alias: kae p);
                                       -s shares settings/sessions with the real
                                       home (credential private), -i isolates,
                                       -t keeps one store for the directory's
                                       tree across account switches (claude only)
  kae pin <tool> <account>             re-bind one tool inside a bound dir
  kae unpin                            remove the binding from .mise.toml
  kae uninstall [--dry-run] [--yes]    remove owned integrations and a recorded direct binary
  kae relogin [<tool>]                 run the tool's login flow into this
                                       directory's bound store, then capture the
                                       result back into that account's snapshot
  kae run [-s|-i|--env] <t|all> <n> -- C
                                       run C with an account applied (alias: kae r);
                                       -s (default) uses the real home and restores
                                       the previous login after; -i runs in the
                                       per-account isolated home (shared with use -i,
                                       no lock); --env injects env-profile vars only
  kae env set|unset|list ...           env-mode profiles (API keys)
  kae companion add|rm|list ...        bind git/gh/cloud CLI auth to a profile
                                       (per-directory; takes effect via kae pin)
  kae mise init [-P profile] [--auto] [--write]
                                       render the auth-mode tasks + opt-in hook
                                       (bind directories with kae pin instead)
  kae accounts [-f] [--json]           registered accounts
  kae ls [<target>] [-f] [--json]      places and accounts: groups account, pin,
                                       each relevant tool, repo, kae; a target
                                       (account|pin|repo|kae|<tool>) shows one
  kae ls <target> --current|--at N     print one place's path; a tool target
                                       takes -s <tool>, -i <tool> <account>,
                                       --project|--below|--home and --root
  kae open [<target>] [--at N|--pick]  open a place in the file manager (the
                                       current place unless --at or --pick; same
                                       targets and selectors as kae ls, no
                                       account; several places open the picker)
  kae cd [<target>] [--at N|--pick]    move the shell to a place; needs the kae
                                       shell function that eval "$(kae
                                       completion zsh)" (or bash) defines
  kae status [-f] [--json]             full status report (alias: kae s);
                                       -f/--full on status, accounts and ls adds
                                       the Identity and Driver columns
  kae preservation list [--json]       list preserved credential records
  kae preservation restore <id>        restore to the original credential store
  kae preservation rm <id>             delete a preserved record with confirmation
  kae backup list [--json]             list switch backups
  kae rollback [--to <backup-id>]      restore a backup
  kae completion <bash|zsh|fish>       print a shell completion script and the
                                       kae shell function (--no-function: the
                                       completion alone, for a completion file)
  kae version | --version | -v
  kae help | --help | -h
`: `使い方:
  kae [-f|--full]                      状態の概要: このディレクトリの固定、
                                       グローバルなプロファイル、ツール、プロファイル
  kae init                             設定ファイルとディレクトリを作成
  kae edit                             設定ファイルを $VISUAL / $EDITOR で開く
  kae doctor [tool] [--json]           環境と認証の健全性の検査（別名: kae d）
  kae add <tool> [<account>]           アカウントを登録（公式のログインと
                                       スナップショット。--no-login は現在の
                                       ログインをそのままスナップショットに保存）
  kae use [-s|-i] [-P <profile>]       引数なし: プロファイルを解決して、何度
                                       実行しても同じ結果になるよう適用（以前の
                                       kae apply）
  kae use --auto [-P <profile>]       自動の適用。グローバルな独立環境は保持
                                       （--quiet は成功時の出力を抑止）
  kae use [-s|-i] <profile>            すべてのツールを今すぐ切り替え（別名: kae u）
  kae use <tool> <account>             1 つのツールを今すぐ切り替え
  kae pin [-s|-i|-t] [<profile>]       このディレクトリを固定（別名: kae p）。
                                       -s は設定とセッションを実ホームと共有
                                       （認証情報は専用）、-i は独立環境、-t は
                                       アカウントを切り替えてもこのディレクトリの
                                       ツリーに 1 つのストアを保持（claude のみ）
  kae pin <tool> <account>             固定したディレクトリの中で 1 つのツールを
                                       固定し直し
  kae unpin                            .mise.toml から固定の内容を削除
  kae uninstall [--dry-run] [--yes]    kae が管理する連携と、記録された直接配置の
                                       バイナリを削除
  kae relogin [<tool>]                 このディレクトリの固定したストアでツールの
                                       ログインを実行し、結果をそのアカウントの
                                       スナップショットに取り込み直し
  kae run [-s|-i|--env] <t|all> <n> -- C
                                       アカウントを適用して C を実行（別名: kae r）。
                                       -s（既定）は実ホームを使い、終了後に元の
                                       ログインを復元。-i はアカウントごとの独立した
                                       ホームで実行（use -i と共有、ロックなし）。
                                       --env は環境変数プロファイルの変数だけを注入
  kae env set|unset|list ...           環境変数プロファイル（API キー）
  kae companion add|rm|list ...        git/gh/クラウド CLI の認証をプロファイルに
                                       結び付け（ディレクトリごと。kae pin で有効）
  kae mise init [-P profile] [--auto] [--write]
                                       auth モードのタスクと任意のフックを生成
                                       （ディレクトリの固定には kae pin を使用）
  kae accounts [-f] [--json]           登録済みのアカウント
  kae ls [<target>] [-f] [--json]      場所とアカウント: account、pin、関係する
                                       各ツール、repo、kae のグループ。対象
                                       （account|pin|repo|kae|<tool>）を指定すると
                                       その 1 つだけを表示
  kae ls <target> --current|--at N     1 つの場所のパスを表示。ツールの対象には
                                       -s <tool>、-i <tool> <account>、
                                       --project|--below|--home と --root を指定
  kae open [<target>] [--at N|--pick]  場所をファイルマネージャーで開く（--at と
                                       --pick がなければ現在の場所。対象と選択は
                                       kae ls と同じで、account はなし。場所が
                                       複数ならピッカーを表示）
  kae cd [<target>] [--at N|--pick]    シェルを場所へ移動。eval "$(kae
                                       completion zsh)"（または bash）が定義する
                                       kae のシェル関数が必要
  kae status [-f] [--json]             状態の詳しい報告（別名: kae s）。status、
                                       accounts、ls の -f/--full は識別子と
                                       ドライバーの列を追加
  kae preservation list [--json]       保全記録の一覧
  kae preservation restore <id>        元の認証ストアへ復元
  kae preservation rm <id>             確認のうえ保全記録を削除
  kae backup list [--json]             切替のバックアップの一覧
  kae rollback [--to <backup-id>]      バックアップを復元
  kae completion <bash|zsh|fish>       シェル補完のスクリプトと kae のシェル関数を
                                       表示（--no-function: 補完だけ。補完ファイル用）
  kae version | --version | -v
  kae help | --help | -h
`,
	`Flags (structured commands):
  --json                shorthand for --format json
  --format text|json    output format
  --dry-run             preview without writing (add --no-login/use/rollback)
  --yes                 answer confirmations yes without asking
  --no-restart          warn instead of restarting codex's managed daemon (use)
  --no-color            disable color
  --config <path>       explicit config file path
`: `フラグ（構造化出力に対応するコマンド）:
  --json                --format json の短縮形
  --format text|json    出力形式
  --dry-run             書き込まずに確認（add --no-login/use/rollback）
  --yes                 確認を求めずに yes と回答
  --no-restart          codex の管理デーモンを再起動せずに警告（use）
  --no-color            色の無効化
  --config <path>       明示する設定ファイルのパス
`,
	"Tools: %s": "対応ツール: %s",
}
