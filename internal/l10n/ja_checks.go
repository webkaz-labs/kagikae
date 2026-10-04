package l10n

// jaChecks renders the messages of `kae doctor` checks, `adapter.Check.Message`
// and `Info.Warnings` among them: the text that is a Msg and reaches JSON in
// English. Keys are grouped by the localization stage 3 slice that adds them, so
// slices running in parallel append to their own section.
var jaChecks = map[string]string{
	// S1 use: the freshness messages of freshness.go, shared with doctor and pin.
	"snapshot credential is stale: %s":               "スナップショットの認証情報が失効しています: %s。",
	"snapshot credential %s":                         "スナップショットの認証情報は、%s。",
	"%s; %s":                                         "%s。%s",
	"%s emptied it after a failed token refresh":     "トークンの更新に失敗し、%s が認証情報を空にしました",
	"it expired %s and its refresh token expired %s": "認証情報は %s に期限切れになり、リフレッシュトークンも %s に期限切れになりました",
	"it expired %s and has no refresh token":         "認証情報は %s に期限切れになり、リフレッシュトークンもありません",
	"needs an interactive re-login in %s (%s)":       "あと %sで対話的な再ログインが必要になります（%s）",
	"%d day(s)":     "%d 日",
	"%d hour(s)":    "%d 時間",
	"under an hour": "1 時間未満",
	" (%s)":         "（%s）",
	"%s: %s":        "%s: %s",
	"confirm account %s and the intended global store outside a bound directory; stop other sessions using that credential, then, to log in as that account, run: kae add --restore %s %s (captures the new login and restores the previous live state)": "固定したディレクトリの外で、アカウント %s と意図したグローバルの認証ストアを確認してください。その認証情報を使っている他のセッションを止めてから、そのアカウントでログインするには kae add --restore %s %s を実行してください（新しいログインを登録し、それまでの現在の状態を復元します）",
	"kae cannot launch a login for %s; log in again in %s as account %s using the intended global store outside a bound directory; %s":                                                                                                                   "kae は %s のログインを起動できません。固定したディレクトリの外で、意図したグローバルの認証ストアを使って、%s でアカウント %s として再度ログインしてください。%s",

	// S2 pin-ls.

	// S3 status-doctor.

	// S4 adapter.
}
