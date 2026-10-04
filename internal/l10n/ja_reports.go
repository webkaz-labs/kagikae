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

	// S4 adapter.
}
