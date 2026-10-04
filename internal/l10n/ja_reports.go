package l10n

// jaReports renders the human reports on stdout: tables, headers and summaries.
// Keys are grouped by the localization stage 3 slice that adds them, so slices
// running in parallel append to their own section.
var jaReports = map[string]string{
	// S1 use: switch.go, useauto.go, usebare.go, capture.go, env.go, text.go
	// (printResultWarnings) and the lead-time cells of freshness.go.
	"Would switch profile to %s": "プロファイル %s に切り替える予定です",
	"%s -> %s (driver: %s)":      "%s -> %s（ドライバー: %s）",
	"  patch %s %s":              "  %s の %s を書き換えます",
	"  replace %s":               "  %s を置き換えます",
	"  preserve all other keys, settings, skills, hooks, history": "  ほかのキー、設定、スキル、フック、履歴はそのまま残します",
	"Switched %s -> %s":                      "切り替えました: %s -> %s",
	"Active profile: %s":                     "有効なプロファイル: %s",
	"Backup: %s; to undo, run: kae rollback": "バックアップ: %s。元に戻すには kae rollback を実行してください",
	"Would globally isolate %s -> %s":        "グローバル独立環境にする予定です: %s -> %s",
	"  home: %s":                             "  ホーム: %s",
	"Would write %s":                         "%s を書き込む予定です",
	"Globally isolated %s -> %s (private home; real ~/.%s left unchanged)": "グローバル独立環境にしました: %s -> %s（専用のホーム。実際の ~/.%s は変更していません）",
	"Wrote %s (regenerated from kae state).":                               "%s を書き込みました（kae の状態から再生成したものです）。",
	"Preserved global isolated %s -> %s (unchanged)":                       "グローバル独立環境のため変更しません: %s -> %s",
	"No shared changes":                                            "共有環境への変更はありません",
	"Profile %s already active (no changes)":                       "プロファイル %s はすでに有効です（変更なし）",
	"  warning: %s":                                                "  警告: %s",
	"Would capture %s/%s (driver: %s)":                             "登録する予定です: %s/%s（ドライバー: %s）",
	"Captured %s/%s (driver: %s)":                                  "登録しました: %s/%s（ドライバー: %s）",
	"Stored %d variable(s) in env profile %s/%s: %s":               "環境変数プロファイル %[2]s/%[3]s に環境変数 %[1]d 件を保存しました: %[4]s",
	"Deleted env profile %s/%s":                                    "環境変数プロファイル %s/%s を削除しました",
	"Removed %d variable(s) from env profile %s/%s":                "環境変数プロファイル %[2]s/%[3]s から環境変数 %[1]d 件を削除しました",
	"no env profiles; run: kae env set <tool> <account> KEY=VALUE": "環境変数プロファイルがありません。kae env set <tool> <account> KEY=VALUE を実行してください",
	"Variables":          "変数",
	"%d day(s) left":     "残り %d 日",
	"%d hour(s) left":    "残り %d 時間",
	"under an hour left": "残り 1 時間未満",
	"refreshed %s/%s snapshot from the live store before switching away": "別のアカウントに切り替える前に、現在の認証ストアから %s/%s のスナップショットを更新しました。",

	// S2 pin-ls.

	// S3 status-doctor.

	// S4 adapter.
}
