package l10n

// jaChecks renders the messages of `kae doctor` checks, `adapter.Check.Message`
// and `Info.Warnings` among them: the text that is a Msg and reaches JSON in
// English. Keys are grouped by the localization stage 3 slice that adds them, so
// slices running in parallel append to their own section.
var jaChecks = map[string]string{
	// S1 use.

	// S2 pin-ls.

	// S3 status-doctor.

	// S4 adapter.
	// adapter.go.
	"%s not found in PATH":                       "%s が PATH に見つかりません。",
	"%s found in PATH":                           "%s が PATH にあります。",
	"%s is group/world readable; expected 0600":  "%s がグループまたは全員に読み取れる権限になっています。0600 にしてください。",
	"%s is set and overrides the switched login": "%s が設定されているため、切り替えたログインより優先されます。",
	"driver: %s":                                 "ドライバー: %s",

	// agy.go.
	"no gemini/antigravity keychain item; log in with the Antigravity app first":                                                           "gemini/antigravity のキーチェーン項目がありません。先に Antigravity アプリでログインしてください。",
	"%s is set: agy may bypass the keychain here and use a file credential kae does not model, so a switch may not reach the tool":         "%s が設定されています。ここでは agy がキーチェーンを使わず、kae が扱わないファイルの認証情報を使う場合があるため、切替がツールに届かない場合があります。",
	"agy adapter on this platform uses file-based credential storage only":                                                                 "このプラットフォームの agy アダプターは、ファイル形式の認証情報だけを扱います。",
	"no credential file found; agy likely uses the OS keyring on this platform, which kae cannot switch yet":                               "認証情報のファイルが見つかりません。このプラットフォームでは agy が OS のキーリングを使っている可能性が高く、kae はまだ切り替えられません。",
	"gemini/antigravity keychain item found":                                                                                               "gemini/antigravity のキーチェーン項目があります。",
	"file-based credential found":                                                                                                          "ファイル形式の認証情報があります。",
	"no file-based credential under ~/.gemini/antigravity-cli/ (agy has not been set up on this machine)":                                  "~/.gemini/antigravity-cli/ にファイル形式の認証情報がありません（このマシンでは agy を設定していません）。",
	"no file-based credential under ~/.gemini/antigravity-cli/ (agy likely uses the OS keyring, which kae cannot switch on this platform)": "~/.gemini/antigravity-cli/ にファイル形式の認証情報がありません（agy は OS のキーリングを使っている可能性が高く、このプラットフォームでは kae は切り替えられません）。",
	"driver: %s (file-based; keyring switching unsupported on this platform)":                                                              "ドライバー: %s（ファイル形式。このプラットフォームではキーリングの切替に対応していません）",

	// claude.go.
	"CLAUDE_CONFIG_DIR is relative: claude resolves it against its own working directory, so kae writes the identity cache (and, under the file driver, the credential) where claude does not read it — set an absolute path. The keychain item is unaffected: its service name hashes the variable's raw value, not a resolved path.": "CLAUDE_CONFIG_DIR が相対パスです。claude は自身の作業ディレクトリを基準に解決するため、kae はログイン中アカウントの記録（ファイルドライバーでは認証情報も）を、claude が読まない場所に書きます。絶対パスを設定してください。キーチェーン項目は影響を受けません。サービス名は、解決後のパスではなく変数の値そのものから作られるためです。",
	"CLAUDE_SECURESTORAGE_CONFIG_DIR is relative: claude resolves it against its own working directory, so under the file driver kae writes the credential where claude does not read it — set an absolute path. The keychain item is unaffected: its service name hashes the variable's raw value, not a resolved path.":              "CLAUDE_SECURESTORAGE_CONFIG_DIR が相対パスです。claude は自身の作業ディレクトリを基準に解決するため、ファイルドライバーでは kae が認証情報を、claude が読まない場所に書きます。絶対パスを設定してください。キーチェーン項目は影響を受けません。サービス名は、解決後のパスではなく変数の値そのものから作られるためです。",
	"live subscription credential found":                         "現在のサブスクリプションの認証情報があります。",
	"no live subscription credential (log in with claude first)": "現在のサブスクリプションの認証情報がありません（先に claude でログインしてください）。",

	// codex.go.
	"CODEX_HOME is relative: codex canonicalizes it against its own working directory, so both the store kae writes and the keyring account it derives from that path can differ from codex's — set an absolute path. codex refuses to start outright when the relative path does not exist in its working directory.": "CODEX_HOME が相対パスです。codex は自身の作業ディレクトリを基準に正規化するため、kae が書く認証ストアも、そのパスから導くキーリングのアカウントも、codex のものと食い違う場合があります。絶対パスを設定してください。相対パスが codex の作業ディレクトリに存在しない場合、codex は起動を拒否します。",
	"no Codex Auth keychain item for this codex home; log in with codex first":                                             "この codex ホームに対応する Codex Auth のキーチェーン項目がありません。先に codex でログインしてください。",
	"no auth.json and no %s keychain item for this codex home; log in with codex first":                                    "auth.json も、この codex ホームに対応する %s のキーチェーン項目もありません。先に codex でログインしてください。",
	"no auth.json found; either codex is not logged in or a keyring kae cannot read on this platform holds the credential": "auth.json が見つかりません。codex がログインしていないか、このプラットフォームで kae が読めないキーリングに認証情報があります。",
	"credential store: %s (auth.json)":                            "認証ストア: %s（auth.json）",
	"credential store: %s (%s keychain item for this codex home)": "認証ストア: %s（この codex ホームに対応する %s のキーチェーン項目）",
	"%s keychain item for this codex home found":                  "この codex ホームに対応する %s のキーチェーン項目があります。",
	"auth.json found": "auth.json があります。",
	"no %s keychain item for this codex home; log in with codex first": "この codex ホームに対応する %s のキーチェーン項目がありません。先に codex でログインしてください。",
	"no auth.json; log in with codex first":                            "auth.json がありません。先に codex でログインしてください。",
	"a %s keychain item exists for this codex home, but kae resolved the %s store (auth.json) — so codex may be reading a credential kae does not switch; check cli_auth_credentials_store in config.toml and re-verify the store rows in docs/VALIDATION.md": "この codex ホームに対応する %s のキーチェーン項目がありますが、kae は %s ストア（auth.json）に解決しました。そのため codex は、kae が切り替えない認証情報を読んでいる可能性があります。config.toml の cli_auth_credentials_store を確認し、docs/VALIDATION.md のストアの行を再検証してください。",

	// copilot.go.
	"COPILOT_HOME is relative: copilot resolves it against its own working directory, so kae only writes the file copilot reads while both run from the same directory (set an absolute path)": "COPILOT_HOME が相対パスです。copilot は自身の作業ディレクトリを基準に解決するため、kae が copilot の読むファイルに書けるのは、両方が同じディレクトリから動いている間だけです（絶対パスを設定してください）。",
	"active account recorded in config.json":                              "config.json に有効なアカウントが記録されています。",
	"no active account in config.json; log in with `copilot login` first": "config.json に有効なアカウントがありません。先に copilot login でログインしてください。",

	// cursor.go.
	"access token found in the keychain":                                      "キーチェーンにアクセストークンがあります。",
	"no access token in the keychain; log in with `cursor-agent login` first": "キーチェーンにアクセストークンがありません。先に cursor-agent login でログインしてください。",

	// opencode.go.
	"XDG_DATA_HOME is relative: opencode joins it against its working directory while kae ignores it per the XDG spec, so kae switches a different auth.json than opencode reads (set an absolute path)": "XDG_DATA_HOME が相対パスです。opencode は作業ディレクトリを基準に解決しますが、kae は XDG 仕様に従って無視するため、kae が切り替える auth.json は opencode が読むものと別になります（絶対パスを設定してください）。",
	"auth.json has no openai entry; only the ChatGPT subscription login is switched (API-key providers belong to env mode)":                                                                              "auth.json に openai の項目がありません。切り替えるのは ChatGPT サブスクリプションのログインだけです（API キーのプロバイダーは env モードの対象です）。",
	"openai (ChatGPT subscription) login found in auth.json":                                       "auth.json に openai（ChatGPT サブスクリプション）のログインがあります。",
	"no openai (ChatGPT subscription) login in auth.json; log in with `opencode auth login` first": "auth.json に openai（ChatGPT サブスクリプション）のログインがありません。先に opencode auth login でログインしてください。",
}
