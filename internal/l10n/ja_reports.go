package l10n

// jaReports renders the human reports on stdout: tables, headers and summaries.
// Keys are grouped by the localization stage 3 slice that adds them, so slices
// running in parallel append to their own section.
var jaReports = map[string]string{
	// S1 use.

	// S2 pin-ls.
	"Bound this directory: profile %s (%s)":                                                         "このディレクトリを固定しました: プロファイル %s（%s）",
	"Wrote %s (ignored via %s); your mise.toml is left unchanged.":                                  "%s を書き込みました（%s で Git の対象外にしています）。mise.toml は変更していません。",
	"Wrote %s; your mise.toml is left unchanged.":                                                   "%s を書き込みました。mise.toml は変更していません。",
	"mise applies it on the next prompt; to apply it now in bash or zsh, run: eval \"$(mise env)\"": "mise が次のプロンプトで反映します。今すぐ bash か zsh に反映する場合は、eval \"$(mise env)\" を実行してください。",
	"Removed %s and the legacy kagikae block from .mise.toml":                                       "%s と、.mise.toml の古い kagikae ブロックを削除しました",
	"Removed %s": "%s を削除しました",
	"Removed the legacy kagikae block from .mise.toml":                                        ".mise.toml の古い kagikae ブロックを削除しました",
	"Re-bound %s to account %s (%s; sessions/settings unchanged)":                             "%s をアカウント %s に固定し直しました（%s。セッションと設定は変更していません）",
	"Linked %s to this directory's store (ignored via %s).":                                   "%s をこのディレクトリのストアにリンクしました（%s で Git の対象外にしています）。",
	"Linked %s to this directory's store.":                                                    "%s をこのディレクトリのストアにリンクしました。",
	"Linked %s to this directory's stores (ignored via %s).":                                  "%s をこのディレクトリのストアにリンクしました（%s で Git の対象外にしています）。",
	"Linked %s to this directory's stores.":                                                   "%s をこのディレクトリのストアにリンクしました。",
	"Removed the store link %s.":                                                              "ストアへのリンク %s を削除しました。",
	"Removed the store links %s.":                                                             "ストアへのリンク %s を削除しました。",
	"Removed the %s credential this account's bindings shared; no binding still uses it (%s)": "このアカウントの固定が共有していた %s の認証情報を削除しました。まだ使っている固定はありません（%s）",
	"Removed the superseded per-directory %s credential (%s)":                                 "置き換え済みの、ディレクトリごとの %s の認証情報を削除しました（%s）",
	"`kae pin %s <account>` does not apply in this directory: %s":                             "kae pin %s <account> はこのディレクトリでは使えません: %s",
	"Bound directories: (none); run: kae pin <profile>":                                       "固定したディレクトリ: なし。kae pin <profile> を実行してください",
	"Bound directories:":                                                                      "固定したディレクトリ:",
	"Directory":                                                                               "ディレクトリ",
	"Current":                                                                                 "現在",
	"Profile":                                                                                 "プロファイル",
	"Mode":                                                                                    "モード",
	"Accounts":                                                                                "アカウント",
	"Profiles: (none defined); run: kae edit":                                                 "プロファイル: 未定義。kae edit を実行してください",
	"Profiles:":                 "プロファイル:",
	"(active)":                  "（有効）",
	"Accounts: (none); run: %s": "アカウント: なし。%s を実行してください",
	"Accounts:":                 "アカウント:",
	"%s places: (none — kae resolves places for %s only)": "%s の場所: なし（kae が場所を解決するのは %s だけです）",
	"%s places:": "%s の場所:",
	"%s and %s":  "%s と %s",
	"Level":      "レベル",
	"Path":       "パス",
	"In effect":  "有効",
	"Source":     "取得元",
	"Applies":    "適用先",
	"Repository: (none — the current directory is not in a Git repository)": "リポジトリ: なし（現在のディレクトリは Git リポジトリの中にありません）",
	"Repository:":      "リポジトリ:",
	"Root":             "ルート",
	"kae directories:": "kae のディレクトリ:",
	"Kind":             "種別",

	// S3 status-doctor.
	// Column headers of the account tables (columnHeader in text.go).
	"Tool":       "ツール",
	"Account":    "アカウント",
	"Identity":   "識別子",
	"Active":     "有効",
	"Driver":     "ドライバー",
	"Auth":       "認証",
	"Credential": "認証情報",
	"Limit":      "利用枠",
	"Notes":      "備考",
	"Captured":   "登録日時",

	// Table cells (status.go, credentialCell).
	"present":            "あり",
	"absent":             "なし",
	"%d warning(s)":      "警告 %d 件",
	"re-login now":       "今すぐ再ログイン",
	"%d day(s) left":     "残り %d 日",
	"%d hour(s) left":    "残り %d 時間",
	"under an hour left": "残り 1 時間未満",

	// status.go, doctor.go and edit.go report lines and the doctor prompt.
	"This directory: profile %s (bound, %s)":                                      "このディレクトリ: プロファイル %s（固定、%s）",
	"Global isolated homes (kae use -i / run -i share these):":                    "グローバルの独立環境のホーム（kae use -i と kae run -i が共有します）:",
	"Global active profile: %s":                                                   "グローバルで有効なプロファイル: %s",
	"Global active profile: (none)":                                               "グローバルで有効なプロファイル: （なし）",
	"no captured accounts; run: kae add <tool> <account>":                         "登録済みのアカウントがありません。kae add <tool> <account> を実行してください。",
	"platform: %s, secret backend: %s":                                            "プラットフォーム: %s、シークレットストア: %s",
	"no blocking problems found":                                                  "先に進めない問題は見つかりませんでした。",
	"errors found; fix them before switching":                                     "エラーが見つかりました。切り替える前に修正してください。",
	"Check token companion identity over the network (e.g. gh api user)? [y/N]: ": "周辺ツールのトークンのログインを確認するため、ネットワークに接続します（例: gh api user）。確認する場合は y を入力してください [y/N]: ",
	"Config OK: %s": "設定は問題ありません: %s",

	// S4 adapter.
}
