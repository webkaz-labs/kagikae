package l10n

// jaWarnings renders the text after `kae: warning:`, `kae: note:` and the bare `kae:`
// line that continues a warning, with the message values those lines embed (a
// refusal reason, a remedy, a consequence). Keys are grouped by source file.
var jaWarnings = map[string]string{
	// backup.go.
	"could not resolve current %s artifacts (%v); the pre-rollback backup covers only what this backup recorded, and a stale %s identity cache is left unchanged — fix that, then %s": "現在の %s の認証要素を解決できませんでした（%v）。ロールバック前のバックアップはこのバックアップが記録した範囲しか対象にならず、古い %s のログイン中アカウントの記録はそのまま残ります。原因を解消してから、%s。",

	"backup %s recorded %s/%s as the active account and that snapshot is no longer captured, so kae is leaving %s with no active account rather than naming one that no longer exists; %s": "バックアップ %s は %s/%s を現在有効なアカウントとして記録していますが、そのスナップショットはもう登録されていません。そのため kae は、存在しないアカウントを指す代わりに、%s の有効なアカウントを未設定のままにします。%s。",

	// companion.go.
	"could not resolve the %s token's login for drift detection; expected_login left unset": "%s のトークンのログインを解決できなかったため、トークンの取り違えを検出するための expected_login は未設定のままです。",
	// dircred.go.
	"kae cannot bind %s's credential to this directory, so %s may have no login here until you log in inside it (its settings and sessions are still isolated)": "kae は %s の認証情報をこのディレクトリに固定できないため、このディレクトリ内でログインするまで、ここでは %s にログインがない場合があります（設定とセッションは引き続き独立しています）。",

	"%s/%s has no captured credential, so this directory binds %s without one; %s; then re-bind this directory": "%s/%s には登録済みの認証情報がないため、このディレクトリは %s を認証情報なしで固定します。%s。そのあと、このディレクトリを固定し直してください。",

	"%s — so kae kept it rather than replacing it: the store is %s/%s's and shared, so a copy in it is not this bind's to spend; until you log in inside this directory it will run that other account": "%s。そのため kae はそのコピーを置き換えずに残しました。このストアは %s/%s のものとして共有されており、中にあるコピーはこの固定が使ってよいものではありません。このディレクトリ内でログインするまで、このディレクトリは別のアカウントで動作します。",

	"%s — so kae kept it rather than replacing it: the store is %s/%s's and shared, so a copy in it is not this bind's to spend": "%s。そのため kae はそのコピーを置き換えずに残しました。このストアは %s/%s のものとして共有されており、中にあるコピーはこの固定が使ってよいものではありません。",

	"%s, so this write replaces it": "%s。そのため、この書き込みでそのコピーを置き換えます。",

	"the %s identity cache in this directory still names the account it was bound to before, and kae could not remove it (%v), so the next bind may read it as this directory's own and replace the credential kae just kept; in this directory, run: kae relogin %s": "このディレクトリの %s のログイン中アカウントの記録は以前に固定していたアカウントを指したままで、kae は削除できませんでした（%v）。そのため次に固定するとき、このディレクトリ自身のものと読み取られて、kae がいま残した認証情報を置き換えるおそれがあります。このディレクトリで kae relogin %s を実行してください。",

	"could not apply %s's identity cache for account %s in this directory (%v); %s may display another account until you log in inside it": "このディレクトリで、アカウント %[2]s 用の %[1]s のログイン中アカウントの記録を適用できませんでした（%[3]v）。このディレクトリ内でログインするまで、%[4]s が別のアカウントを表示することがあります。",

	"could not migrate the pre-split %s credential in %s (%v); any copy still there is one nothing reads, and a refresh of it elsewhere would invalidate this account's": "分割前の %s の認証情報（%s）を移行できませんでした（%v）。まだそこに残っているコピーはどこからも読まれず、別の場所でそれをリフレッシュすると、このアカウントの認証情報が無効になります。",

	// dircred_checks.go.
	"to verify the bound account, in that directory run: kae status; stop other sessions using that account's credential; to log in inside that directory as the bound account, run: cd %s && kae relogin %s": "固定したアカウントを確認するには、そのディレクトリで kae status を実行してください。そのアカウントの認証情報を使っている他のセッションを止めてください。そのディレクトリ内で固定したアカウントとしてログインするには、cd %s && kae relogin %s を実行してください",

	"kae cannot launch a login for %s; before manual login in %s, verify the bound account and that mise activation, trust and the tool environment select its bound store; see docs/CLI.md Recovery guidance": "kae は %s のログインを起動できません。%s で手動ログインする前に、固定したアカウントと、mise の有効化・信頼設定・ツールの環境がその固定したストアを選んでいることを確認してください。docs/CLI.md の Recovery guidance を参照してください",

	// dircred_harvest.go.
	"kae could not read or date the %s credential in %s — nor tell which account it belonged to — so it is deleted without being kept anywhere; if that was a working login in a shape kae does not recognize, it is lost": "kae は %s の認証情報（%s）を読み取れず、新旧も判断できず、どのアカウントのものかも分からなかったため、どこにも残さず削除します。kae が認識できない形式の有効なログインだった場合、そのログインは失われます。",

	"kae cannot read or date the %s credential in %s, so it is left in place instead of deleted (a payload kae cannot judge may still be a working login); removing it tears the binding down too, so re-bind afterwards; if it is spent, in that directory run: kae unpin --purge": "kae は %s の認証情報（%s）を読み取れず新旧も判断できないため、削除せずそのまま残します（kae が判断できない中身でも、有効なログインの可能性があります）。削除すると固定も解除されるので、そのあとで固定し直してください。もう使えないものであれば、そのディレクトリで kae unpin --purge を実行してください。",

	"kae cannot tell which account the %s credential in %s belongs to, so it is left in place instead of deleted": "kae は %s の認証情報（%s）がどのアカウントのものか判断できないため、削除せずそのまま残します。",

	"account %s/%s no longer exists, so the %s credential this directory held for it is deleted without being kept anywhere (%s)": "アカウント %s/%s はもう存在しないため、このディレクトリがそのアカウント用に保持していた %s の認証情報（%s）は、どこにも残さず削除します。",

	"account %s/%s no longer exists, so the %s credential this directory held for it is left in place instead of deleted (%s); to remove it, run: kae unpin --purge, or re-bind to the account that holds it now and it is harvested": "アカウント %s/%s はもう存在しないため、このディレクトリがそのアカウント用に保持していた %s の認証情報（%s）は、削除せずそのまま残します。削除するには kae unpin --purge を実行してください。または、いまそれを保持しているアカウントに固定し直すと、退避されます。",

	"could not read snapshot %s/%s to harvest into, so the %s credential in %s is left in place instead of deleted (%v)": "退避先のスナップショット %s/%s を読み取れなかったため、%s の認証情報（%s）は削除せずそのまま残します（%v）。",

	"kae could not write it into that snapshot": "kae がそのスナップショットに書き込めなかった",

	"leaving the %s credential in %s in place instead of deleting it: it is newer than snapshot %s/%s and %s": "%s の認証情報（%s）は、スナップショット %s/%s より新しい一方で、%sため、削除せずそのまま残します。",

	"kae could not tell what reads the %s credential for %s/%s, so it did not harvest it before the rename; if a bound directory or an isolated home held a newer copy it stays under the old name (%s)": "kae は %[2]s/%[3]s の %[1]s の認証情報を何が読んでいるか判断できなかったため、アカウント名の変更前に退避しませんでした。固定したディレクトリか独立環境のホームにより新しいコピーがあった場合、それは古い名前（%[4]s）のまま残ります。",

	"%s; the rename leaves that copy under the old name, and re-binding will not reach it": "%s。アカウント名を変更しても、そのコピーは古い名前のまま残り、固定し直してもそこには届きません。",
	"so this bind leaves it in place": "この固定ではそのコピーをそのまま残します",
	"and this bind replaces it":       "この固定ではそのコピーを置き換えます",

	"the %s credential in %s belongs to an account other than %s/%s (%s), so kae is not harvesting it, %s": "%[2]s の %[1]s の認証情報は %[3]s/%[4]s とは別のアカウントのもの（%[5]s）のため、kae は退避しません。%[6]s。",

	"kae could not preserve the %s credential this directory held for %s/%s (%s), %s; %s": "このディレクトリが %[2]s/%[3]s 用に保持していた %[1]s の認証情報を、kae は保全できませんでした（%[4]s）。%[5]s。%[6]s。",

	"the %s credential already in %s is newer than snapshot %s/%s and kae is not harvesting it because %s": "%[2]s にすでにある %[1]s の認証情報はスナップショット %[3]s/%[4]s より新しいものの、%[5]sため、kae は退避しません",

	"kae is not harvesting the %s credential already in %s into snapshot %s/%s because %s": "%[5]sため、%[2]s にすでにある %[1]s の認証情報は、スナップショット %[3]s/%[4]s へ退避しません",

	"kae cannot read or date the copy already there, and a payload kae cannot judge may still be a login": "kae がすでにあるコピーを読み取れず新旧も判断できず、判断できない中身でも有効なログインかもしれない",

	"could not harvest the newer %s credential from %s into snapshot %s/%s: %v":       "より新しい %s の認証情報（%s）をスナップショット %s/%s へ退避できませんでした: %v",
	"harvested the %s credential for %s/%s but could not update its capture time: %v": "%[2]s/%[3]s の %[1]s の認証情報は退避しましたが、登録日時を更新できませんでした: %[4]v",

	"harvested the newer %s credential from %s into snapshot %s/%s (it is the copy that can still refresh)": "より新しい %s の認証情報を %s からスナップショット %s/%s へ退避しました（まだリフレッシュできるのはこのコピーです）。",
	// dircred_identity.go.
	"no %s identity is recorded for that account":           "そのアカウントに %s のログイン中アカウントの記録が保存されていない",
	"kae could not resolve where its identity cache is":     "kae がログイン中アカウントの記録の場所を解決できなかった",
	"its identity cache is shared with the real tool home":  "ログイン中アカウントの記録が、本物のツールのホームと共有されている",
	"the directory holds no identity cache to compare":      "このディレクトリには、比較できるログイン中アカウントの記録がない",
	"that account's recorded identity cannot be read":       "そのアカウントに保存された記録を読み取れない",
	"kae cannot read the identity records it would compare": "比較に使うログイン中アカウントの記録を kae が読み取れない",
	"its identity names a different account":                "ログイン中アカウントの記録が別のアカウントを示している",
	"this platform records no identity for it":              "このプラットフォームでは、それに対応するログイン中アカウントの記録が残らない",

	"%s's identity cache in this directory is shared with the real %s home (%s), so kae is not writing it here; %s may display an account other than %s until you log in inside the directory": "このディレクトリの %s のログイン中アカウントの記録は、本物の %s のホーム（%s）と共有されているため、kae はここへ書き込みません。このディレクトリ内でログインするまで、%s がアカウント %s ではない別のアカウントを表示することがあります。",

	// dircred_store.go.
	"could not remove the superseded %s credential for %s: %v": "%s の置き換え済みの認証情報（%s）を削除できませんでした: %v",

	"kae could not tell whether another binding still uses the %s credential for %s, so it is left in place rather than deleted": "別の固定がまだアカウント %[2]s の %[1]s の認証情報を使っているか kae が判断できなかったため、削除せずそのまま残します。",

	"%d other binding(s) still use the %s credential for %s, so it is left in place instead of deleted": "ほかの %[1]d 件の固定がまだアカウント %[3]s の %[2]s の認証情報を使っているため、削除せずそのまま残します。",

	"kae could not tell which directories read this credential":                  "この認証情報の参照元がどのディレクトリか、kae が判断できなかった",
	"kae could not resolve where one directory that reads it keeps its identity": "参照元の 1 つがログイン中アカウントの記録をどこに置いているか、kae が解決できなかった",
	"the directories that read this credential disagree about whose login it is": "この認証情報の参照元のあいだで、誰のログインかが食い違っている",

	"the directories that read this credential say it belongs to another account, and this directory does not read it yet": "この認証情報の参照元は別のアカウントのものだと示しており、このディレクトリはまだ参照元になっていない",

	"no directory that reads this credential could attribute it":                   "この認証情報の参照元のどれも、誰のものか判断できなかった",
	"no directory reads this credential yet, so nothing can say whose login it is": "この認証情報をまだ読んでいるディレクトリがなく、誰のログインかを示すものがない",
	// doctor.go.
	"companion and bound-directory checks are not per-tool and were skipped; to include them, run: kae doctor": "周辺ツールと固定したディレクトリの検査はツールごとではないため、省略しました。含めるには kae doctor を実行してください。",

	// fragment.go.
	"could not tell git to ignore %s: %v":                                                   "%s を無視するよう git に伝えられませんでした: %v",
	"the binding is in place; ignore %s yourself (machine-specific; must not be committed)": "固定は完了しています。%s は自分で無視してください（このマシン固有のもので、コミットしてはいけません）。",
	// fragment.go: the causes the warning above embeds.
	"git rev-parse returned %q":                                             "git rev-parse の出力が想定外です: %q",
	"resolve git common dir %q: %w":                                         "git の共通ディレクトリ %q を解決できません: %w",
	"git named %q as its common dir, but that is not an existing directory": "git が共通ディレクトリとして %q を示しましたが、存在するディレクトリではありません",
	// freshness.go.
	"could not read live %s state to refresh %s/%s: %v":             "スナップショット %[2]s/%[3]s を更新するための、現在の %[1]s の状態を読み取れませんでした: %[4]v",
	"%s; snapshot %s/%s left unchanged":                             "%s。スナップショット %s/%s は変更していません。",
	"%s is logged out; snapshot %s/%s left unchanged":               "%s はログアウトしています。スナップショット %s/%s は変更していません。",
	"%s logged out during the run%s; snapshot %s/%s left unchanged": "%[1]s が実行中にログアウトしました%[2]s。スナップショット %[3]s/%[4]s は変更していません。",
	"recapture of %s/%s failed: %v":                                 "スナップショット %s/%s の更新に失敗しました: %v",

	"kae could not preserve the live %s login it declined to adopt; it is lost once the previous state is restored": "kae は、取り込まないと判断した現在の %s のログインを保全できませんでした。以前の状態を復元すると、そのログインは失われます。",

	"the live %s login kae declined to adopt is preserved only in backup %s (that backup covers only the tools whose recapture kae declined) — to keep it as its own account, run: kae rollback --to %s, then kae add --no-login %s <account>": "kae が取り込まないと判断した現在の %[1]s のログインは、バックアップ %[2]s にだけ保全されています（このバックアップの対象は、kae が取り込みを見送ったツールだけです）。別のアカウントとして残すには、kae rollback --to %[3]s を実行し、続けて kae add --no-login %[4]s <account> を実行してください。",

	"the live %s login kae declined to adopt is preserved only in backup %s (restoring it reverts this whole switch) — to keep it as its own account, run: kae rollback --to %s, then kae add --no-login %s <account>": "kae が取り込まないと判断した現在の %[1]s のログインは、バックアップ %[2]s にだけ保全されています（これを復元すると、今回の切替がすべて元に戻ります）。別のアカウントとして残すには、kae rollback --to %[3]s を実行し、続けて kae add --no-login %[4]s <account> を実行してください。",

	"kae cannot read the %s identity records it would compare for %s/%s, so it cannot tell whose login is live": "%[2]s/%[3]s について比較に使う %[1]s のログイン中アカウントの記録を kae が読み取れず、現在のログインが誰のものか判断できません",

	"the live %s identity is not the one kae applied for %s/%s; %s was probably logged in again outside kae": "現在の %[1]s のログイン中アカウントの記録は、kae が %[2]s/%[3]s 用に適用したものと違います。%[4]s は kae の外で再ログインされた可能性が高いです",

	"the live %s credential needs a re-login while snapshot %s/%s still holds a usable one": "現在の %s の認証情報は再ログインが必要ですが、スナップショット %s/%s にはまだ使えるものが残っています",

	"kae cannot order the live %s credential against snapshot %s/%s, so it cannot tell which of the two can still refresh": "現在の %s の認証情報とスナップショット %s/%s の新旧を kae が決められず、どちらがまだリフレッシュできるか判断できません",

	"snapshot %s/%s holds a later %s credential than the live store, and %s's refresh token rotates single-use, so the live copy can no longer refresh": "スナップショット %[1]s/%[2]s には現在のストアより新しい %[3]s の認証情報があり、%[4]s のリフレッシュトークンは 1 回限りで更新されるため、現在のコピーはもうリフレッシュできません",

	// identity.go.
	"no login identity could be detected for %s; %s/%s was captured without one (identity is optional). To add it anytime, run: kae account set-identity %s %s <value>": "%[1]s のログイン識別子を検出できませんでした。%[2]s/%[3]s は識別子なしで登録しました（識別子は省略できます）。あとから追加するには、kae account set-identity %[4]s %[5]s <value> を実行してください。",

	// list_diagnostics.go.
	"config is invalid or unreadable; listing metadata from the resolved state directory; check the selected config file and its permissions before recovery; run: kae doctor": "設定ファイルが不正か読み取れないため、解決した状態ディレクトリからメタデータを一覧しています。復旧の前に、選んだ設定ファイルとその権限を確認してください。kae doctor を実行してください。",

	"metadata listing is incomplete; readable records are shown": "メタデータの一覧が不完全です。読み取れた記録だけを表示しています。",
	// An issue's code and hashed entry are tokens; the remedy is a message.
	"%s %s; %s": "%s %s。%s。",
	"check the resolved state directory exists as a directory and is accessible; see docs/CLI.md Recovery guidance":       "解決した状態ディレクトリがディレクトリとして存在し、アクセスできることを確認してください。docs/CLI.md の Recovery guidance を参照してください",
	"check metadata file and parent-directory permissions; keep the entry while investigating":                            "メタデータのファイルと親ディレクトリの権限を確認してください。調べている間はその項目を残してください",
	"check metadata format against docs/DATA-MODEL.md; do not infer an account or delete the entry to clear this warning": "メタデータの形式を docs/DATA-MODEL.md と照らし合わせて確認してください。この警告を消すためにアカウントを推測したり、項目を削除したりしないでください",
	"inspect the entry type without following symlinks; keep unexpected entries until their purpose is verified":          "symlink をたどらずに項目の種類を確認してください。想定外の項目は、目的を確認できるまで残してください",
	"see docs/CLI.md Recovery guidance before recovery":                                                                   "復旧の前に docs/CLI.md の Recovery guidance を参照してください",
	// login.go.
	"complete the %s login flow; the result is captured as %s when it exits (previous state backed up as %s)":                   "%s のログイン手順を完了してください。終了すると、結果を %s として登録します（以前の状態はバックアップ %s に保存しました）。",
	"complete the %s login flow; the result is captured as the detected account when it exits (previous state backed up as %s)": "%s のログイン手順を完了してください。終了すると、結果を検出したアカウントとして登録します（以前の状態はバックアップ %s に保存しました）。",
	"%s exited with %d; capturing whatever auth state is live now":                                                              "%s が終了コード %d で終了しました。いまの認証状態をそのまま登録します。",
	// ls.go.
	"%s is bound but its fragment could not be read (%v), so it is not listed": "%s は固定されていますが、フラグメントを読み取れません（%v）。そのため一覧に載せていません。",
	// lsplace.go.
	"the %s group is not listed: %v": "%s のグループは一覧に載せていません: %v",
	// miseinit.go.
	"preview only; to apply, run: %s --write":                            "プレビューのみです。適用するには %s --write を実行してください。",
	"%s mode binds %s only, so %s keeps the real home (docs/ROADMAP.md)": "%s モードが固定するのは %s だけのため、%s は本物のホームのままです（docs/ROADMAP.md）。",

	"the real %s home (%s) lists nothing to share, so kae cannot tell whether %d shared link(s) in %s are still wanted; leaving them in place. If that home is right, remove the links by hand; if it is not, unset %s (or fix it), then run: kae pin": "本物の %[1]s のホーム（%[2]s）に共有するものがないため、%[4]s にある %[3]d 件の共有リンクがまだ必要か kae は判断できません。リンクはそのまま残します。そのホームが正しい場合は、リンクを手動で削除してください。正しくない場合は、環境変数 %[5]s の設定を外すか修正してから kae pin を実行してください。",

	// modes.go.
	"this directory is bound (%s); you are changing GLOBAL state, which this directory will not see — to re-bind, run: kae pin": "このディレクトリは固定されています（%s）。いま変更しているのはグローバルの状態で、このディレクトリには反映されません。固定し直すには kae pin を実行してください。",

	// open.go.
	"no file manager opener on %s found, so the place is printed instead": "%s でファイルマネージャーを開くコマンドが見つからないため、代わりに場所を表示します。",
	"no %s found, so the place is printed instead":                        "%s が見つからないため、代わりに場所を表示します。",
	// ops.go.
	"could not re-resolve %s's credential store after the child (%v); continuing with the store resolved before it, which may no longer be the one %s reads": "子プロセスの終了後に %[1]s の認証ストアを解決し直せませんでした（%[2]v）。その前に解決したストアで続けますが、それはもう %[3]s が読んでいるストアではないかもしれません。",

	"could not take the backup lock: %v": "バックアップのロックを取得できませんでした: %v",
	"backup pruning failed: %v":          "バックアップの整理に失敗しました: %v",

	"could not resolve where %s keeps its credential now (%v), so this restore writes the store the backup recorded without checking whether %s has moved it": "いま %[1]s が認証情報をどこに置いているか解決できなかった（%[2]v）ため、この復元はバックアップが記録したストアへ、%[3]s がそれを移していないか確認せずに書き込みます。",

	"%s moved its credential to %s %q, which this backup has no record of; kae left it in place rather than deleting a credential it has no copy of, so %s stays logged in as whatever wrote it": "%[1]s は認証情報を %[2]s %[3]q に移しましたが、このバックアップにはその記録がありません。kae は、コピーを持っていない認証情報を削除せず、そのまま残しました。そのため %[4]s は、それを書き込んだアカウントのままログインした状態になります。",

	"re-apply it; run: kae use %s %s": "再適用するには kae use %s %s を実行してください",
	"re-apply the %s account you want; run: kae use %s <account> (kae accounts lists them)": "再適用したい %s のアカウントを指定して kae use %s <account> を実行してください（アカウントは kae accounts で一覧できます）",
	// pin.go.
	"this directory has a legacy overlay-mode block.":                             "このディレクトリには、古い overlay モードのブロックがあります。",
	"to migrate to isolated mode, run: kae unpin && kae pin --isolated <profile>": "独立モードへ移行するには、kae unpin && kae pin --isolated <profile> を実行してください。",
	"or, for shared-settings mode, run: kae unpin && kae pin --shared <profile>":  "共有モードにするには、kae unpin && kae pin --shared <profile> を実行してください。",
	"mise activation not detected; the binding takes effect once mise is active.": "mise の有効化を検出できませんでした。mise が有効になると、固定の内容が反映されます。",
	"to apply it in the current shell now, run:":                                  "いまのシェルにすぐ反映するには、次を実行してください:",

	"changing this directory from %s to %s moves no sessions; %s's stay in %s, which `kae pin %s` finds again": "このディレクトリを %[1]s から %[2]s に変えても、セッションは移動しません。%[3]s のセッションは %[4]s に残り、kae pin %[5]s で再び見つかります。",

	"the new tree store %s starts empty":                                                "ツリーモードの新しいストア %s は空の状態から始まります。",
	"could not resolve this directory (%v); removing the fragment without the pin lock": "このディレクトリを解決できませんでした（%v）。固定のロックを取らずにフラグメントを削除します。",

	"could not open the secret store (%v); this directory's per-directory credentials are left in place rather than deleted without being harvested": "シークレットストアを開けませんでした（%v）。このディレクトリ固有の認証情報は、退避しないまま削除することはせず、そのまま残します。",

	// pinindex.go.
	"%s is still bound to %s/%s, which no longer exists; to re-bind it, run: cd %s && kae pin %s %s": "%s は、もう存在しない %s/%s に固定されたままです。固定し直すには cd %s && kae pin %s %s を実行してください。",

	"%s is still bound to profile %s, which no longer exists; to re-bind it, run: cd %s && kae pin <profile>": "%s は、もう存在しないプロファイル %s に固定されたままです。固定し直すには cd %s && kae pin <profile> を実行してください。",

	// place.go.
	"%s is bound but its fragment could not be read (%v), so its binding is not applied here": "%s は固定されていますが、フラグメントを読み取れません（%v）。そのため、その固定の内容はここには適用されません。",

	"could not list the repository's files (%s), so project levels below this directory are not shown": "リポジトリのファイルを一覧できませんでした（%s）。そのため、このディレクトリより下のプロジェクトの階層は表示されません。",

	"the current directory could not be resolved (%v), so the claude session row is not listed": "カレントディレクトリを解決できませんでした（%v）。そのため claude のセッション記録の行は一覧に載せていません。",
	// preservation.go.
	"this may be the only surviving credential copy; deleting the explicitly selected ID": "これが残っている唯一の認証情報のコピーかもしれません。明示的に選ばれた ID を削除します。",
	// relogin.go.
	"preserved the existing credential as %s; its account ownership is unknown; run: kae preservation list":                              "既存の認証情報を %s として保全しました。どのアカウントのものかは不明です。kae preservation list を実行してください。",
	"complete the %s login flow; kae is running it against this directory's own store (%s), so it refreshes %s/%s and not the real home": "%s のログイン手順を完了してください。kae はこのディレクトリ専用のストア（%s）に対して実行するため、更新されるのは本物のホームではなく %s/%s です。",
	"%s exited with %d; kae is checking what is in the store now":                                                                        "%s が終了コード %d で終了しました。kae はいまストアにあるものを確認します。",
	"kae could not read this directory's %s credential, so it cannot tell whether the login flow changed anything":                       "kae はこのディレクトリの %s の認証情報を読み取れなかったため、ログイン手順で何かが変わったか判断できません。",

	"kae found no %s credential where it resolves this directory's store, so it is not reporting a login — the flow may have left nothing there, or it may have moved the credential to a store kae does not resolve for this directory": "kae がこのディレクトリのストアとして解決した場所に %s の認証情報がないため、ログインとしては報告しません。ログイン手順が何も残さなかったか、kae がこのディレクトリ用に解決しないストアへ認証情報を移した可能性があります。",

	"kae read no usable %s token in the payload now in this directory's store, so it is not reporting a login — blank tokens are what a failed refresh leaves behind, and a payload whose token keys changed upstream reads the same way": "このディレクトリのストアにある認証情報の中身から、使える %s のトークンを kae は読み取れなかったため、ログインとしては報告しません。トークンが空になるのはリフレッシュに失敗したときの状態で、上流でトークンのキーが変わった中身も同じように見えます。",

	"kae could not resolve where %s keeps this directory's credential (%v), so it cannot capture the login back into the account snapshot": "%[1]s がこのディレクトリの認証情報をどこに置いているか kae は解決できなかった（%[2]v）ため、ログインをアカウントのスナップショットへ取り込み直せません。",

	"kae could not resolve where %s keeps this directory's credential, so it cannot tell what the login flow is about to replace": "%s がこのディレクトリの認証情報をどこに置いているか kae は解決できなかったため、ログイン手順が何を置き換えようとしているか判断できません。",

	"kae could not read snapshot %s/%s (%v), so it cannot tell what the login flow is about to replace in %s": "kae はスナップショット %[1]s/%[2]s を読み取れなかった（%[3]v）ため、ログイン手順が %[4]s で何を置き換えようとしているか判断できません。",

	"%s; completing the login flow replaces it, and kae has it in no snapshot": "%s。ログイン手順を完了するとそのコピーは置き換えられ、kae はどのスナップショットにも保持していません。",
	"logged in, but kae could not read snapshot %s/%s to capture it back (%v)": "ログインしましたが、取り込み直すためのスナップショット %s/%s を kae は読み取れませんでした（%v）。",

	"the %s login now in this directory belongs to an account other than %s/%s (%s), so kae did not capture it into that snapshot; to re-bind, run: kae pin %s <account>": "このディレクトリにいまある %[1]s のログインは %[2]s/%[3]s とは別のアカウントのもの（%[4]s）のため、kae はそのスナップショットへ取り込みませんでした。固定し直すには kae pin %[5]s <account> を実行してください。",

	"kae cannot confirm the %s login now in this directory is %s/%s's (%s), so it did not capture it back and that snapshot still holds its own copy; kae read another account's name in %s — this login can be captured once %s/%s is the account named there; to apply the snapshot's own copy, run: kae use %s %s": "このディレクトリにいまある %[1]s のログインが %[2]s/%[3]s のものか kae は確認できません（%[4]s）。そのため取り込み直さず、そのスナップショットには元のコピーが残っています。kae は %[5]s で別のアカウントの名前を読み取りました。そこに名前があるアカウントが %[6]s/%[7]s になれば、このログインを取り込めます。スナップショット側のコピーを適用するには kae use %[8]s %[9]s を実行してください。",

	"kae cannot confirm the %s login now in this directory is %s/%s's (%s), so it did not capture it back and that snapshot still holds its own copy; to apply the snapshot's own copy, run: kae use %s %s": "このディレクトリにいまある %[1]s のログインが %[2]s/%[3]s のものか kae は確認できません（%[4]s）。そのため取り込み直さず、そのスナップショットには元のコピーが残っています。スナップショット側のコピーを適用するには kae use %[5]s %[6]s を実行してください。",

	// restorecred.go.
	"the live store": "現在のストア",
	"the newer copy is left only in backup %s (kae rollback --to %s)": "新しいコピーはバックアップ %s にだけ残ります（kae rollback --to %s）",
	"snapshot %s/%s": "スナップショット %s/%s",
	"to apply the newer copy afterwards, run: kae use %s %s":                              "あとで新しいコピーを適用するには、kae use %s %s を実行してください",
	"recorded an older %s credential for %s/%s than the one in %s":                        "%[2]s/%[3]s の %[1]s の認証情報として、「%[4]s」にあるものより古いものを記録しています",
	"so this rollback leaves %s without the copy that can still refresh":                  "このロールバックでは、%s にまだリフレッシュできるコピーが残らなくなります",
	"recorded a %s credential for %s/%s that carries no usable token, while %s holds one": "%[2]s/%[3]s の %[1]s の認証情報として、使えるトークンを含まないものを記録していますが、「%[4]s」には使えるものがあります",
	"recorded a %s credential for %s/%s that kae cannot compare with the one in %s":       "%[2]s/%[3]s の %[1]s の認証情報として、「%[4]s」にあるものと kae が比較できないものを記録しています",
	"so kae cannot tell which of the two %s can still refresh":                            "2 つのうちどちらの %s がまだリフレッシュできるか kae は判断できません",
	"backup %s %s, and %s's refresh token rotates single-use, %s; %s":                     "バックアップ %[1]s は、%[2]s。また、%[3]s のリフレッシュトークンは 1 回限りで更新されるため、%[4]s。%[5]s。",
	// run.go.
	"%s has no home-isolation env var; it keeps the real home (%s isolates claude and codex only)": "%s にはホームを独立させる環境変数がないため、本物のホームのままです（%s で独立させられるのは claude と codex だけです）。",

	"%s refreshed its credential during the run and %s/%s was already the active account, so restoring backup %s would put back a copy %s can no longer refresh; leaving the live %s credential as the child left it": "実行中に %[1]s が認証情報をリフレッシュし、%[2]s/%[3]s はすでに有効なアカウントでした。バックアップ %[4]s を復元すると、%[5]s がもうリフレッシュできないコピーを戻してしまうため、現在の %[6]s の認証情報は子プロセスが残したままにします。",

	"could not back up the live state kae declined to adopt: %v":                                                  "kae が取り込まないと判断した現在の状態をバックアップできませんでした: %v",
	"run -i: %s runs in %s\n  (shared with `kae use -i %s`; concurrent `kae use` in other shells is not blocked)": "run -i: %s は %s で実行します。\n  （kae use -i %s と共有します。ほかのシェルで同時に実行する kae use はブロックされません）",
	"previous auth state restored (backup %s)":                                                                    "以前の認証状態を復元しました（バックアップ %s）。",
	// storelink.go.
	"%s is not a kae link; leaving it unchanged. This directory's %s store is %s": "%[1]s は kae のリンクではないため、変更せずに残します。このディレクトリの %[2]s のストアは %[3]s です。",
	"could not update the store link %s: %v":                                      "ストアへのリンク %s を更新できませんでした: %v",
	// switch.go.
	"%s: %s": "%s: %s",
	"%d tools need a re-login before use: %s": "使う前に再ログインが必要なツールが %d 個あります: %s",
}
