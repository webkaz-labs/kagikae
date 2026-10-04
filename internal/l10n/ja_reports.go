package l10n

// jaReports renders the human reports on stdout: tables, headers and summaries.
// Keys are grouped by the localization stage 3 slice that adds them, so slices
// running in parallel append to their own section.
var jaReports = map[string]string{
	// S1 use.

	// S2 pin-ls.

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
