package l10n

// jaChecks renders the messages of `kae doctor` checks, `adapter.Check.Message`
// and `Info.Warnings` among them: the text that is a Msg and reaches JSON in
// English. Keys are grouped by the localization stage 3 slice that adds them, so
// slices running in parallel append to their own section.
var jaChecks = map[string]string{
	// S1 use.

	// S2 pin-ls.

	// S3 status-doctor.
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
	"the %s credential bound to %s needs an interactive re-login in %s (%s); %s": "ディレクトリ %[2]s に固定した %[1]s の認証情報は、あと %[3]s で対話式の再ログインが必要になります（%[4]s）。%[5]s。",
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

	// S4 adapter.
}
