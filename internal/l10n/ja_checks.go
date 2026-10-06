package l10n

// jaChecks renders the messages of `kae doctor` checks, `adapter.Check.Message`
// and `Info.Warnings` among them: the text that is a Msg and reaches JSON in
// English. Keys are grouped by source file.
var jaChecks = map[string]string{
	// freshness.go (shared with doctor and pin).
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
	"confirm account %s and the intended global store outside a bound directory; stop other sessions using that credential, then, to log in as that account, run: kae add --restore %s %s (captures the new login and restores the previous live state)": "固定したディレクトリの外で、アカウント %s と意図したグローバルの認証ストアを確認してください。その認証情報を使っている他のセッションを止めてから、そのアカウントでログインするには kae add --restore %s %s を実行してください（新しいログインを登録し、直前の状態に戻します）",
	"kae cannot launch a login for %s; log in again in %s as account %s using the intended global store outside a bound directory; %s":                                                                                                                   "kae は %s のログインを起動できません。固定したディレクトリの外で、意図したグローバルの認証ストアを使って、%s でアカウント %s として再度ログインしてください。%s",

	// config (internal/config) and doctor.go.
	"unknown config key %q ignored":                                   "設定ファイルの不明なキー %q は無視しました。",
	"[tools.%s] ignored: %s was removed; use %s instead":              "[tools.%s] は無視しました。%s は削除されました。代わりに %s を使ってください。",
	"profiles.%s.accounts.%s ignored: %s was removed; use %s instead": "profiles.%s.accounts.%s は無視しました。%s は削除されました。代わりに %s を使ってください。",
	"config %s: %v":      "設定ファイル %s: %v",
	"config: %s":         "設定ファイル: %s",
	"secret backend: %s": "シークレットストア: %s",
	"secret backend: %s (plaintext file backend; secrets are stored unencrypted)": "シークレットストア: %s（平文のファイルバックエンドです。シークレットは暗号化されずに保存されます）",
	"%s not found in PATH; the %s binding has no effect until it is installed":    "%s が PATH に見つかりません。インストールするまで、%s の固定の内容は効果がありません。",
	"profile %s: %s token %s is not stored; run: kae companion add %s %s %s":      "プロファイル %s: %s のトークン %s が保存されていません。kae companion add %s %s %s を実行してください。",
	"snapshot %q is stale: %s": "スナップショット %q は失効しています: %s。",
	"snapshot %q %s":           "スナップショット %q: %s。",
	"could not read %s (%v), so kae cannot say which account is active for any tool; run: kae use <tool> <account> (rewrites it)":                                                           "%s を読み取れませんでした（%v）。そのため、どのツールでどのアカウントが有効か kae には判断できません。kae use <tool> <account> を実行してください（読み取れなかったファイルを書き直します）。",
	"state records %s/%s as active but its snapshot could not be read (%v), so kae cannot confirm it":                                                                                       "状態ファイルは %s/%s を有効なアカウントとして記録していますが、そのスナップショットを読み取れませんでした（%v）。そのため kae は確認できません。",
	"state records %s/%s as active but that snapshot no longer exists, so kae cannot say which %s account is live; to pick one, run: kae use %s <account> (kae ls shows the captured ones)": "状態ファイルは %s/%s を有効なアカウントとして記録していますが、そのスナップショットはもう存在しません。そのため、%s のどのアカウントが現在使われているか kae には判断できません。アカウントを選ぶには kae use %s <account> を実行してください（登録済みのアカウントは kae ls で確認できます）。",
	"secret item for %s/%s has no snapshot dir; to remove it, run: kae account rm %s %s":                                                                                                    "%s/%s のシークレット項目に対応するスナップショットのディレクトリがありません。削除するには kae account rm %s %s を実行してください。",
	"snapshot %q declares a stored %s payload the secret backend does not have, so applying it cannot restore that artifact; %s":                                                            "スナップショット %q は %s の保存データを持つと記録していますが、シークレットストアにありません。そのため、このスナップショットを適用しても、その認証要素を復元できません。%s。",

	// dircred_checks.go.
	"the %s credential bound to %s is stale: %s; %s":                             "ディレクトリ %[2]s に固定した %[1]s の認証情報は失効しています: %[3]s。%[4]s。",
	"the %s credential bound to %s needs an interactive re-login in %s (%s); %s": "ディレクトリ %[2]s に固定した %[1]s の認証情報は、あと %[3]sで対話的な再ログインが必要になります（%[4]s）。%[5]s。",
	"the %s credential bound to %s is older than another copy of %s/%s (%s); %s's refresh token rotates single-use, so if the two are copies of one login only the newer one can still refresh and the session in that directory cannot be renewed past %s; %s": "ディレクトリ %[2]s に固定した %[1]s の認証情報は、%[3]s/%[4]s の別のコピー（%[5]s）より古くなっています。%[6]s のリフレッシュトークンは 1 回使うと入れ替わるため、2 つが同じログインのコピーであれば、更新できるのは新しいほうだけです。そのディレクトリのセッションは %[7]s を過ぎると更新できません。%[8]s。",
	"the store bound to %s": "%s に固定したストア",
	"re-bind that directory from the newer snapshot, no login needed; run: cd %s && kae pin %s %s":                                                                                                       "新しいスナップショットからそのディレクトリを固定し直してください。ログインは不要です。cd %s && kae pin %s %s を実行してください",
	"the directory bound to %s/%s (%s) keeps its own copy of that account's credential; another directory or `kae use -i` on the same account will invalidate it — to re-bind it, run: cd %s && kae pin": "%s/%s に固定したディレクトリ（%s）は、そのアカウントの認証情報を独自のコピーとして持っています。同じアカウントで別のディレクトリまたは kae use -i を使うと、このコピーは無効になります。固定し直すには cd %s && kae pin を実行してください。",
	"the %s identity cache in %s names an account other than %s/%s, which that directory binds: either something logged in there as another account — in which case that directory is running an account its binding does not name — or kae could not apply the identity when it bound the directory, and %s displays the wrong account while running the bound one (kae cannot tell those apart offline). To make the binding true again: %s; to keep what is there instead, bind the directory to that account": "%[2]s の %[1]s のログイン中アカウントの記録が、そのディレクトリが固定している %[3]s/%[4]s とは別のアカウントを指しています。原因は 2 つ考えられます。1 つは、そこで別のアカウントとしてログインした場合で、そのディレクトリは固定の内容に反するアカウントで動いています。もう 1 つは、ディレクトリを固定したときに kae がこの記録を適用できなかった場合で、%[5]s は固定したアカウントで動きながら別のアカウントを表示します。kae はオフラインでは両者を区別できません。固定を正しい状態に戻す手順: %[6]s。そのままにしておく場合は、そのディレクトリをそのアカウントに固定し直してください。",

	// identity_drift.go.
	"%s/%s: recorded identity is not an account record; kae cannot use it to attribute credentials; %s":                                                                                               "%s/%s: 保存されたログイン識別子は、アカウントの記録の形式ではありません。そのため kae は、認証情報の持ち主の判断にそれを使えません。%s。",
	"account %s: no %s identity recorded yet; start %s only after verifying its account and global store, then %s":                                                                                    "アカウント %[1]s: %[2]s のログイン中アカウントの記録がまだ保存されていません。%[3]s を起動するのは、そのアカウントとグローバルの認証ストアを確認したあとにしてください。%[4]s。",
	"to re-apply it, run: kae use %s %s. If it drifts again, an upstream behaviour assumption may have changed (docs/VALIDATION.md \"Upstream Behaviour Assumptions\")":                               "再適用するには kae use %s %s を実行してください。再びずれる場合は、上流の動作の前提が変わった可能性があります（docs/VALIDATION.md の Upstream Behaviour Assumptions を参照）",
	"account %s: the live %s identity is missing while this account is active; %s may rebuild it on its next run — otherwise %s":                                                                      "アカウント %[1]s: このアカウントが有効なのに、現在の %[2]s のログイン中アカウントの記録がありません。%[3]s は次回の実行時に再作成することがあります。再作成されない場合は、%[4]s。",
	"account %s: the live %s identity differs from the one kae applied, so %s can name the wrong account; something outside kae rewrote it (a manual login, or a change in how %s maintains it) — %s": "アカウント %[1]s: 現在の %[2]s のログイン中アカウントの記録が、kae が適用したものと異なります。そのため %[3]s が別のアカウントを表示することがあります。kae の外で書き換えられています（手動のログイン、または %[4]s がこの記録を保守する方法の変更など）。%[5]s。",

	// upstream_version.go.
	"kae cannot read when %s's behaviour assumptions were last verified (%q is not YYYY-MM-DD), so their age is unknown":                                                                                                                                                                                                    "kae は %s の動作の前提を最後に検証した日付を読み取れません（%q は YYYY-MM-DD の形式ではありません）。そのため、検証からの経過日数が分かりません。",
	"kae's %s behaviour assumptions were last verified on %s (%d days ago) and nothing has re-checked them since; the version signal only fires when %s is upgraded, so re-verify the rows in docs/VALIDATION.md \"Upstream Behaviour Assumptions\"":                                                                        "kae の %s の動作の前提は、%s（%d 日前）に最後に検証されたきりで、その後は再確認されていません。バージョンの検出は %s をアップグレードしたときにしか働かないため、docs/VALIDATION.md の Upstream Behaviour Assumptions の各行を再検証してください。",
	"installed %s %s is past %s, the version kae's behaviour assumptions were last verified against; the layout guards still pass when only the behaviour changed (a field the tool stops maintaining, a cache it stops refreshing), so re-verify the assumptions in docs/VALIDATION.md \"Upstream Behaviour Assumptions\"": "インストール済みの %s %s は、kae が動作の前提を最後に検証したバージョン %s を超えています。動作だけが変わった場合（ツールが保守しなくなった項目や、更新しなくなったキャッシュなど）は、構造の検査は通り続けるため、docs/VALIDATION.md の Upstream Behaviour Assumptions の前提を再検証してください。",

	// resident_drift.go.
	"%s's managed daemon holds a different account from the live credential, so sessions connected to it keep using that account; to make it use the live account, run: %s": "%s の管理デーモンは現在の認証情報とは別のアカウントを使っているため、デーモンに接続したセッションはそのアカウントを使い続けます。現在のアカウントを使わせるには %s を実行してください。",
	"kae cannot read which account %s's managed daemon holds, so it cannot tell whether the daemon uses the live account; if it does not, run: %s":                          "kae は %s の管理デーモンが使っているアカウントを読み取れないため、デーモンが現在のアカウントを使っているか判断できません。使っていない場合は %s を実行してください。",

	// companion_drift.go and companion_token_drift.go.
	"profile %s: git %s is unset here but the binding sets %q; the binding is not active in this shell, so a commit would use the wrong identity; run: mise env, mise trust if untrusted, or kae pin if the binding itself no longer exists": "プロファイル %s: git の %s がここでは未設定ですが、固定の内容では %q が設定されています。このシェルでは固定の内容が有効になっていないため、コミットが別の作者情報で行われます。mise env を実行してください。信頼していない場合は mise trust を、固定自体がもう存在しない場合は kae pin を実行してください。",
	"profile %s: git %s is %q here but the binding sets %q; a repo-local override or an inactive binding makes commits use the wrong identity (check: git config --show-origin %s)":                                                          "プロファイル %s: git の %s はここでは %q ですが、固定の内容では %q が設定されています。リポジトリ固有の上書きか、有効になっていない固定の内容のため、コミットが別の作者情報で行われます（確認: git config --show-origin %s）。",
	"profile %s: %s is bound to login %q but %s is unset here; the binding is not active in this shell, so %s would act as the wrong account; run: mise env, or mise trust if untrusted":                                                     "プロファイル %s: %s はログイン %q に固定されていますが、ここでは %s が未設定です。このシェルでは固定の内容が有効になっていないため、%s は別のアカウントとして動作します。mise env を実行してください。信頼していない場合は mise trust を実行してください。",
	"profile %s: could not verify the %s token's login against the bound %q (%s); the token may be invalid or the network unreachable":                                                                                                       "プロファイル %s: %s のトークンのログインを、固定したログイン %q と照合して確認できませんでした（%s）。トークンが無効か、ネットワークに接続できない可能性があります。",
	"profile %s: the %s token resolves to login %q but the binding expects %q; this directory's token is for the wrong account":                                                                                                              "プロファイル %s: %s のトークンはログイン %q に解決されますが、固定の内容が期待するのは %q です。このディレクトリのトークンは別のアカウントのものです。",

	// pinindex.go.
	"the bound-directory index could not be read completely; some bound-directory checks could not run and shared credential attribution is unavailable; restore readable bound-directory records before retrying": "固定したディレクトリの索引を最後まで読み取れませんでした。一部の固定したディレクトリの検査を実行できず、共有している認証情報の持ち主の判断もできません。読み取れる固定したディレクトリの記録を復旧してから、もう一度実行してください。",
	"%s was bound with kae pin but its recorded path no longer exists; it may have been deleted or moved, so kae left its per-directory store unchanged":                                                           "%s は kae pin で固定されましたが、記録されたパスがもう存在しません。削除または移動された可能性があるため、kae はそのディレクトリ専用のストアを変更していません。",
	"%s is bound but its fragment could not be read (%v), so its binding was not checked":                                                                                                                          "%s は固定されていますが、フラグメントを読み取れませんでした（%v）。そのため固定の内容は検査していません。",
	"%s is bound to %s/%s, which is not captured; to re-bind it, run: cd %s && kae pin %s <account>":                                                                                                               "%s は %s/%s に固定されていますが、そのアカウントは登録されていません。固定し直すには cd %s && kae pin %s <account> を実行してください。",

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
