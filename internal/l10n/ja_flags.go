package l10n

// jaFlags renders the flag descriptions a usage block prints (FlagUsage). Each is
// one noun phrase without a closing 「。」 and without backquotes, which the
// flag package would take as the value's name (docs/L10N-JA.md § 訳さないもの).
var jaFlags = map[string]string{
	// Common flags (internal/cmd/app.go).
	"output format: text or json":             "出力形式: text または json",
	"shorthand for --format json":             "--format json の短縮形",
	"answer confirmations yes without asking": "確認を求めずに yes と回答",
	"disable color in human text output":      "人向けテキスト出力の色の無効化",
	"explicit config file path":               "明示する設定ファイルのパス",
	"print planned actions without writing":   "書き込まずに変更予定を表示",
	"shared environment (default)":            "共有環境（既定）",
	"alias for --shared":                      "--shared の別名",
	"isolated environment":                    "独立環境",
	"alias for --isolated":                    "--isolated の別名",
	"profile to resolve (default: $KAE_PROFILE, then config default_profile)": "解決するプロファイル（既定: $KAE_PROFILE、次に設定の default_profile）",
	"alias for --profile": "--profile の別名",

	// kae add.
	"restore the previous login after capturing (login flow only)":                                                    "登録後に元のログインを復元（ログインを伴う登録のみ）",
	"snapshot the current live auth state without launching a login flow":                                             "ログインを起動せず、現在の認証状態をスナップショットとして保存",
	"record this login identity for the account when auto-detection is unavailable (e.g. agy on current Antigravity)": "自動検出できない場合にアカウントに記録するログイン識別子（例: 現行の Antigravity での agy）",

	// kae use.
	"apply a resolved profile while preserving global isolated selections":                      "グローバルな独立環境の選択を保ったまま、解決したプロファイルを適用",
	"suppress the success report (for hooks; bare use)":                                         "成功の報告の抑止（フック用、引数なしの kae use のみ）",
	"do not restart codex's managed daemon when the daemon holds another account; warn instead": "codex の管理デーモンが別のアカウントを使っていても再起動せず、警告だけを表示",

	// Account tables (status, accounts, ls).
	"show every column of the account tables, Identity and Driver included": "アカウント表の全列を表示（識別子とドライバーを含む）",
	"alias for --full": "--full の別名",

	// Places (ls, open, cd).
	"list every directory bound with kae pin (alias of kae ls pin)":                    "kae pin で固定したディレクトリをすべて一覧（kae ls pin と同じ）",
	"print the current place's path for the target":                                    "対象の現在の場所のパスを表示",
	"the place number N of the target's kae ls listing":                                "対象の kae ls の一覧での場所の番号 N",
	"the nearest effective ancestor project level":                                     "上位のディレクトリで最も近い有効なプロジェクトレベル",
	"a project level below the current directory":                                      "現在のディレクトリより下のプロジェクトレベル",
	"the tool's real home":                                                             "ツールの実ホーム",
	"the directory holding a project level's .claude/ or .codex/":                      "プロジェクトレベルの .claude/ または .codex/ を持つディレクトリ",
	"choose among the target's places in the picker, even when it has a current place": "現在の場所があっても、対象の場所からピッカーで選択",

	// kae pin, kae unpin.
	"one private store for this directory's tree, kept across account switches (claude only)": "アカウントの切替をまたいで保つ、このディレクトリのツリー専用のストア（claude のみ）",
	"alias for --tree": "--tree の別名",
	"do not leave ./.config/<tool> links to this directory's stores (and remove the ones kae made here)": "このディレクトリのストアへの ./.config/<tool> のリンクの抑止（kae がここに作ったリンクも削除）",
	"also delete this directory's per-directory keychain credentials (sessions and settings are kept)":   "このディレクトリ専用のキーチェーンの認証情報も削除（セッションと設定は保持）",

	// kae run.
	"inject the env-profile vars only (no home redirect, no lock)": "環境変数プロファイルの変数だけを注入（ホームの差し替えなし、ロックなし）",

	// kae mise init.
	"rendered integration (auth only; bind directories with kae pin)": "生成する連携（auth のみ、ディレクトリの固定は kae pin で）",
	"add a [hooks.enter] running kae use --auto --quiet":              "kae use --auto --quiet を実行する [hooks.enter] を追加",
	"write/update .mise.toml in the current directory":                "現在のディレクトリの .mise.toml を書き込みまたは更新",

	// kae account rm, kae profile rm.
	"remove even the active account, dropping it from state":    "有効なアカウントも含めて削除（状態からも除去）",
	"remove even the default profile, clearing default_profile": "既定のプロファイルも含めて削除（default_profile も解除）",

	// kae profile default.
	"clear default_profile": "default_profile の解除",

	// kae rollback.
	"backup id to restore (default: most recent restorable)": "復元するバックアップの ID（既定: 復元できる最新のもの）",

	// kae completion.
	"register the completion script interactively":                                                                  "補完スクリプトを対話的に登録",
	"print the completion alone, without the kae shell function (the shape of a completion file)":                   "kae のシェル関数を含めず、補完だけを表示（補完ファイルの形）",
	"rewrite already-registered completion files from this binary (no shell arg; never creates a new registration)": "登録済みの補完ファイルをこのバイナリで書き直し（シェルの引数なし、新規の登録はしない）",

	// kae __install, kae uninstall.
	"absolute direct executable destination":               "実行ファイルを直接置く先の絶対パス",
	"release or local_build":                               "release または local_build",
	"required release version":                             "必須のリリースバージョン",
	"additional project directory to inspect (repeatable)": "追加で調べるプロジェクトのディレクトリ（複数指定可）",
}
