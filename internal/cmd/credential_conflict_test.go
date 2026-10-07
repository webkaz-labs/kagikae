package cmd

// A codex login file that carries one account's tokens under another's account
// id: the file a refresh in flight across a switch writes (docs/ACCEPTANCE.md
// § Fifth part: an old-account process refreshing its token). Both recaptures
// decline it, doctor flags it live and saved, and `kae add` refuses it.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// Fixture ids and emails; not real accounts.
const (
	conflictAcctMain = "acct-main"
	conflictAcctSide = "acct-side"
	conflictEmail    = "you@example.com"
	// rotatedRefresh is main's refresh token as main's refresh rotated it into the
	// live file; only the mixed fixtures carry it.
	rotatedRefresh = "rt-main-ROTATED"
)

// workspaceToken is a JWT whose "https://api.openai.com/auth" claim names
// account as the workspace; "" leaves the claim out. Every token carries the
// email, which must never reach output.
func workspaceToken(account string) string {
	claims := map[string]any{"email": conflictEmail}
	if account != "" {
		claims["https://api.openai.com/auth"] = map[string]any{"chatgpt_account_id": account}
	}
	body, _ := json.Marshal(claims)
	seg := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	return seg([]byte(`{"alg":"none"}`)) + "." + seg(body) + "."
}

// codexChatGPTLogin renders a codex auth.json. mode "" leaves auth_mode out.
func codexChatGPTLogin(mode, idToken, access, refresh, accountID string) string {
	doc := map[string]any{"tokens": map[string]any{
		"id_token": idToken, "access_token": access, "refresh_token": refresh, "account_id": accountID,
	}}
	if mode != "" {
		doc["auth_mode"] = mode
	}
	body, _ := json.Marshal(doc)
	return string(body)
}

func consistentLogin(account, refresh string) string {
	return codexChatGPTLogin("chatgpt", workspaceToken(account), workspaceToken(account), refresh, account)
}

// The two mixed shapes the brief's acceptance names: (a) every token is main's
// under side's account id; (b) a refresh response without an id_token rewrote
// only the access and refresh tokens, so the id_token still names side.
var mixedCodexLogins = map[string]string{
	"every token main's": codexChatGPTLogin("chatgpt",
		workspaceToken(conflictAcctMain), workspaceToken(conflictAcctMain), rotatedRefresh, conflictAcctSide),
	"id_token still side's": codexChatGPTLogin("chatgpt",
		workspaceToken(conflictAcctSide), workspaceToken(conflictAcctMain), rotatedRefresh, conflictAcctSide),
}

func codexAuthFile(app *App) string { return filepath.Join(app.Env.Home, ".codex", "auth.json") }

// seedCodexAccounts captures main and side as consistent logins, side last, so
// side is the active codex account.
func seedCodexAccounts(t *testing.T, app *App) {
	t.Helper()
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	for _, acc := range []struct{ name, id string }{{"main", conflictAcctMain}, {"side", conflictAcctSide}} {
		writeFile(t, codexAuthFile(app), consistentLogin(acc.id, "rt-"+acc.name))
		code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", acc.name) })
		mustExit(t, constants.ExitOK, code, out)
	}
}

// assertNoConflictPII fails if any id, email or token of the fixtures reached out.
func assertNoConflictPII(t *testing.T, out string) {
	t.Helper()
	for _, pii := range []string{conflictAcctMain, conflictAcctSide, conflictEmail, rotatedRefresh, "eyJ"} {
		if strings.Contains(out, pii) {
			t.Errorf("output contains %q: %q", pii, out)
		}
	}
}

// keptBackupID reads the backup id out of the mixed-login remedy.
func keptBackupID(t *testing.T, stderr string) string {
	t.Helper()
	const marker = "is kept in backup "
	i := strings.Index(stderr, marker)
	if i < 0 {
		t.Fatalf("no backup named in the refusal: %q", stderr)
	}
	rest := stderr[i+len(marker):]
	j := strings.IndexAny(rest, ", \n")
	if j < 0 {
		t.Fatalf("could not read the backup id out of %q", stderr)
	}
	return rest[:j]
}

// backupHolds reports whether backup id holds a payload of tool containing want.
func backupHolds(t *testing.T, app *App, id, tool, want string) bool {
	t.Helper()
	metas, err := backup.List(app.Paths.BackupsDir())
	if err != nil {
		t.Fatal(err)
	}
	be := testBackend(t, app)
	for _, meta := range metas {
		if meta.ID != id {
			continue
		}
		for _, rec := range meta.Artifacts {
			if rec.Tool != tool || rec.SecretRef == "" {
				continue
			}
			data, ok, err := be.Get(context.Background(), rec.SecretRef)
			if err == nil && ok && strings.Contains(string(data), want) {
				return true
			}
		}
	}
	return false
}

func TestSwitchAwayDeclinesAMixedCodexLogin(t *testing.T) {
	for name, mixed := range mixedCodexLogins {
		t.Run(name, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatText}
			seedCodexAccounts(t, app)
			writeFile(t, codexAuthFile(app), mixed)

			code, out, stderr := captureBoth(t, func() int { return runSwitch(ctx, app, opts, "codex", "main") })
			mustExit(t, constants.ExitOK, code, out)
			if !strings.Contains(stderr, "the live codex login carries one account's tokens under another account's id, "+
				"so kae cannot file it as codex/side; snapshot codex/side left unchanged") {
				t.Errorf("expected the mixed-login refusal naming codex/side: %q", stderr)
			}
			id := keptBackupID(t, stderr)
			if !strings.Contains(stderr, "the live codex login kae declined to adopt is kept in backup "+id+
				", but it mixes two accounts, so do not add it as an account; "+
				"an account whose login was overwritten may need a fresh codex login\n") {
				t.Errorf("the remedy must keep the backup and offer no add, in full: %q", stderr)
			}
			if strings.Contains(stderr, "kae add --no-login") || strings.Contains(stderr, "kae rollback --to") {
				t.Errorf("adopting a mixed login is never a remedy: %q", stderr)
			}
			assertNoConflictPII(t, stderr+out)

			if !backupHolds(t, app, id, "codex", rotatedRefresh) {
				t.Errorf("backup %s does not hold the declined login", id)
			}
			be := testBackend(t, app)
			if got := snapshotPayload(t, app, be, "codex", "side"); !strings.Contains(got, "rt-side") || strings.Contains(got, rotatedRefresh) {
				t.Errorf("snapshot codex/side changed: %s", got)
			}
			if live := readFile(t, codexAuthFile(app)); !strings.Contains(live, "rt-main") || strings.Contains(live, rotatedRefresh) {
				t.Errorf("the switch did not apply main's snapshot: %s", live)
			}
		})
	}
}

func TestRunSharedDeclinesAMixedCodexLogin(t *testing.T) {
	for name, mixed := range mixedCodexLogins {
		t.Run(name, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatText}
			seedCodexAccounts(t, app)

			withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
				writeFile(t, codexAuthFile(app), mixed)
				return 0, nil
			})
			code, out, stderr := captureBoth(t, func() int {
				return runRun(ctx, app, opts, runModeShared, "codex", "side", []string{"codex"})
			})
			mustExit(t, constants.ExitOK, code, out)
			if !strings.Contains(stderr, "so kae cannot file it as codex/side; snapshot codex/side left unchanged") {
				t.Errorf("expected the mixed-login refusal naming codex/side: %q", stderr)
			}
			id := keptBackupID(t, stderr)
			assertNoConflictPII(t, stderr+out)
			if strings.Contains(stderr, "kae add --no-login") {
				t.Errorf("adopting a mixed login is never a remedy: %q", stderr)
			}
			meta, found, err := backup.Latest(app.Paths.BackupsDir())
			if err != nil || !found || meta.ID != id || meta.Reason != constants.BackupReasonRunUnattributable {
				t.Fatalf("the refusal must name the run-unattributable backup: %+v found=%v err=%v id=%s", meta, found, err, id)
			}
			if !backupHolds(t, app, id, "codex", rotatedRefresh) {
				t.Errorf("backup %s does not hold the declined login", id)
			}
			be := testBackend(t, app)
			if got := snapshotPayload(t, app, be, "codex", "side"); strings.Contains(got, rotatedRefresh) {
				t.Errorf("the mixed login was filed under codex/side: %s", got)
			}
		})
	}
}

// Two tools declined by one run, for different reasons: each warning and each
// remedy belongs to its own plan, and one backup covers both.
func TestRunSharedDeclinesTwoToolsAtOnce(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaude(t, app, mainToken, "main")
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	writeFile(t, codexAuthFile(app), consistentLogin(conflictAcctMain, "rt-main"))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "main") })
	app.Config.Profiles["main"] = config.Profile{Accounts: map[string]string{"claude": "main", "codex": "main"}}

	const foreign = "sk-ant-oat01-FOREIGN-LOGIN"
	mixed := mixedCodexLogins["every token main's"]
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		seedClaude(t, app, foreign, "stranger")
		writeFile(t, codexAuthFile(app), mixed)
		return 0, nil
	})
	code, out, stderr := captureBoth(t, func() int {
		return runRun(ctx, app, opts, runModeShared, "all", "main", []string{"claude"})
	})
	mustExit(t, constants.ExitOK, code, out)

	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	pairs := map[string][2]string{
		"claude": {
			"the live claude identity is not the one kae applied for claude/main",
			"to keep it as its own account, run: kae rollback --to ",
		},
		"codex": {
			"so kae cannot file it as codex/main; snapshot codex/main left unchanged",
			"may need a fresh codex login",
		},
	}
	for tool, want := range pairs {
		i := -1
		for k, line := range lines {
			if strings.Contains(line, want[0]) {
				i = k
			}
		}
		if i < 0 || i+1 >= len(lines) || !strings.Contains(lines[i+1], want[1]) || !strings.Contains(lines[i+1], " "+tool+" ") {
			t.Errorf("%s: its reason must be followed by its own remedy: %q", tool, stderr)
		}
	}
	meta, found, err := backup.Latest(app.Paths.BackupsDir())
	if err != nil || !found || meta.Reason != constants.BackupReasonRunUnattributable || len(meta.Tools) != 2 {
		t.Fatalf("one backup must cover both declined tools: %+v found=%v err=%v", meta, found, err)
	}
	if !backupHolds(t, app, meta.ID, "codex", rotatedRefresh) || !backupHolds(t, app, meta.ID, "claude", foreign) {
		t.Errorf("backup %s must hold both declined logins", meta.ID)
	}
	assertNoConflictPII(t, stderr+out)
}

// doctorConflictRows runs doctor --json for codex and returns the
// credential_account_conflict rows, failing on any personal data in the output.
func doctorConflictRows(t *testing.T, app *App) []struct{ Tool, Code, Status, Message string } {
	t.Helper()
	_, stdout, stderr := captureBoth(t, func() int {
		return runDoctor(context.Background(), app, commonOpts{Format: formatJSON}, constants.ToolCodex)
	})
	var raw struct {
		Checks []struct{ Tool, Code, Status, Message string } `json:"checks"`
	}
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, stdout)
	}
	assertNoConflictPII(t, stdout+stderr)
	var rows []struct{ Tool, Code, Status, Message string }
	for _, c := range raw.Checks {
		if c.Code == constants.CheckCredentialAccountConflict {
			rows = append(rows, c)
		}
	}
	return rows
}

func TestDoctorFlagsAMixedLiveLoginAndSnapshot(t *testing.T) {
	for name, mixed := range mixedCodexLogins {
		t.Run(name, func(t *testing.T) {
			app := testApp(t, nil)
			seedCodexAccounts(t, app)
			writeSnapshotPayload(t, app, "codex", "main", mixed)
			writeFile(t, codexAuthFile(app), mixed)

			rows := doctorConflictRows(t, app)
			if len(rows) != 2 {
				t.Fatalf("want a live and a snapshot finding, got %+v", rows)
			}
			for _, row := range rows {
				if row.Tool != constants.ToolCodex || row.Status != constants.StatusWarn {
					t.Errorf("row = %+v", row)
				}
			}
			if !strings.HasPrefix(rows[0].Message, "the live codex login carries") ||
				!strings.HasPrefix(rows[1].Message, `snapshot "main" carries`) {
				t.Errorf("messages = %q / %q", rows[0].Message, rows[1].Message)
			}
		})
	}
}

func TestDoctorConflictMessagesInJapanese(t *testing.T) {
	app := testApp(t, nil)
	seedCodexAccounts(t, app)
	mixed := mixedCodexLogins["every token main's"]
	writeSnapshotPayload(t, app, "codex", "main", mixed)
	writeFile(t, codexAuthFile(app), mixed)
	ctx := context.Background()
	be := testBackend(t, app)
	accounts, err := account.List(app.Paths.AccountsDir())
	if err != nil {
		t.Fatal(err)
	}
	rows := append(app.credentialConflictLiveChecks(ctx, constants.ToolCodex),
		credentialConflictSnapshotChecks(ctx, be, accounts, constants.ToolCodex)...)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	captureErr := errCredentialConflict("codex", "side")
	reason := credentialConflictReason("codex", "side")
	l10ntest.UseJapanese(t)
	const fact = "あるアカウントのトークンを別のアカウントの ID のもとに持っています。"
	want := "現在の codex のログインは、" + fact +
		"そのため kae はどのアカウントとしても保存しません。使うつもりのアカウントで codex に改めてログインしてください。"
	if got := l10n.Render(rows[0].Message); got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
	if got := l10n.Render(rows[1].Message); !strings.HasPrefix(got, `スナップショット "main" は、`+fact+
		"そのため、このスナップショットに切り替えると、一部が別のアカウントのものであるログインが適用されます。") {
		t.Errorf("Render = %q", got)
	}
	if got := l10n.Render(reason); got != "現在の codex のログインは、"+fact+"そのため kae は codex/side として保存できません" {
		t.Errorf("Render(reason) = %q", got)
	}
	if got := l10n.Render(captureErr); !strings.HasPrefix(got, "現在の codex のログインは、"+fact+"そのため kae は codex/side として取り込みません。") {
		t.Errorf("Render(capture) = %q", got)
	}
}

// A run whose child changed nothing over a snapshot that is already mixed has
// nothing to recapture: no warning and no backup beyond the run's own.
func TestRunSharedLeavesAnUnchangedMixedSnapshotAlone(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	seedCodexAccounts(t, app)
	mixed := mixedCodexLogins["every token main's"]
	writeSnapshotPayload(t, app, "codex", "side", mixed)

	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) { return 0, nil })
	for range 2 {
		code, out, stderr := captureBoth(t, func() int {
			return runRun(ctx, app, opts, runModeShared, "codex", "side", []string{"codex"})
		})
		mustExit(t, constants.ExitOK, code, out)
		if strings.Contains(stderr, "carries one account's tokens") {
			t.Errorf("a no-op run warned about the snapshot's own login: %q", stderr)
		}
	}
	metas, err := backup.List(app.Paths.BackupsDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range metas {
		if meta.Reason == constants.BackupReasonRunUnattributable {
			t.Errorf("a no-op run took a declined-copy backup: %+v", meta)
		}
	}
}

// doctor's snapshot conflict check reads through credentialHealthChecks' read
// cache: each snapshot payload reaches the backend once for all its checks.
func TestDoctorConflictReadsEachSnapshotOnce(t *testing.T) {
	app := testApp(t, nil)
	seedCodexAccounts(t, app)
	writeSnapshotPayload(t, app, "codex", "main", mixedCodexLogins["every token main's"])
	counter := &countingBackend{Backend: testBackend(t, app), gets: map[string]int{}}

	checks := app.credentialHealthChecks(context.Background(), counter, constants.ToolCodex)
	conflicts := 0
	for _, c := range checks {
		if c.Code == constants.CheckCredentialAccountConflict {
			conflicts++
		}
	}
	if conflicts != 1 {
		t.Fatalf("want the snapshot finding, got %+v", checks)
	}
	for _, name := range []string{"main", "side"} {
		ref := account.SecretRef("codex", name, "auth")
		if got := counter.gets[ref]; got != 1 {
			t.Errorf("%s read %d times, want 1 (gets: %v)", ref, got, counter.gets)
		}
	}
}

// The live finding needs no secret backend, so an unavailable one does not hide it.
func TestDoctorFlagsAMixedLiveLoginWithoutABackend(t *testing.T) {
	app := testApp(t, nil)
	writeFile(t, codexAuthFile(app), mixedCodexLogins["every token main's"])
	app.Config.Security.SecretBackend = "unavailable-for-test"
	rows := doctorConflictRows(t, app)
	if len(rows) != 1 || !strings.HasPrefix(rows[0].Message, "the live codex login carries") {
		t.Fatalf("want the live finding, got %+v", rows)
	}
}

func TestAddRefusesAMixedCodexLogin(t *testing.T) {
	for name, mixed := range mixedCodexLogins {
		t.Run(name, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			writeFile(t, codexAuthFile(app), mixed)

			code, out, stderr := captureBoth(t, func() int {
				return runCapture(ctx, app, commonOpts{Format: formatText}, "codex", "side")
			})
			mustExit(t, constants.ExitUnsafeRefused, code, out+stderr)
			if !strings.Contains(stderr, "so kae will not capture it as codex/side") {
				t.Errorf("expected the capture refusal: %q", stderr)
			}
			assertNoConflictPII(t, stderr+out)

			_, jsonOut := captureStdout(t, func() int {
				return runCapture(ctx, app, commonOpts{Format: formatJSON}, "codex", "side")
			})
			var report struct {
				ErrorCode string `json:"error_code"`
			}
			if err := json.Unmarshal([]byte(jsonOut), &report); err != nil || report.ErrorCode != constants.CodeUnsafeRefused {
				t.Errorf("JSON error code: %q (%v) in %s", report.ErrorCode, err, jsonOut)
			}
			assertNoConflictPII(t, jsonOut)

			if _, found, err := account.Load(app.Paths.AccountDir("codex", "side")); err != nil || found {
				t.Errorf("a refused capture wrote a snapshot: found=%v err=%v", found, err)
			}
			if st, err := app.loadState(); err != nil || st.Active["codex"] != "" {
				t.Errorf("a refused capture changed the active account: %v %v", st.Active, err)
			}
		})
	}
}

// The Unknown shapes and a Consistent file keep today's behaviour: add captures,
// the switch-away recapture files the changed live login, and doctor is silent.
func TestCodexLoginsKaeCannotJudgeKeepTodaysBehaviour(t *testing.T) {
	main, side := workspaceToken(conflictAcctMain), workspaceToken(conflictAcctSide)
	for name, live := range map[string]string{
		"api key mode":          codexChatGPTLogin("apikey", main, main, "rt-x", conflictAcctSide),
		"external token mode":   codexChatGPTLogin("chatgptAuthTokens", main, main, "rt-x", conflictAcctSide),
		"claims absent":         codexChatGPTLogin("chatgpt", workspaceToken(""), workspaceToken(""), "rt-x", conflictAcctSide),
		"broken JWTs":           codexChatGPTLogin("chatgpt", "not-a-jwt", "a.b", "rt-x", conflictAcctSide),
		"no account id":         codexChatGPTLogin("chatgpt", main, "opaque", "rt-x", ""),
		"consistent":            codexChatGPTLogin("chatgpt", side, side, "rt-x", conflictAcctSide),
		"consistent, mode none": codexChatGPTLogin("", side, side, "rt-x", conflictAcctSide),
	} {
		t.Run(name, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatText}
			writeFile(t, codexAuthFile(app), consistentLogin(conflictAcctMain, "rt-main"))
			captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "main") })
			writeFile(t, codexAuthFile(app), live)
			code, out, stderr := captureBoth(t, func() int { return runCapture(ctx, app, opts, "codex", "side") })
			mustExit(t, constants.ExitOK, code, out+stderr)

			changed := strings.Replace(live, "rt-x", "rt-y", 1)
			writeFile(t, codexAuthFile(app), changed)
			code, out, stderr = captureBoth(t, func() int { return runSwitch(ctx, app, opts, "codex", "main") })
			mustExit(t, constants.ExitOK, code, out)
			if strings.Contains(stderr, "carries one account's tokens") {
				t.Errorf("a login kae cannot judge was declined: %q", stderr)
			}
			be := testBackend(t, app)
			if got := snapshotPayload(t, app, be, "codex", "side"); !strings.Contains(got, "rt-y") {
				t.Errorf("the switch-away recapture did not file the changed login: %s", got)
			}
			if rows := doctorConflictRows(t, app); len(rows) != 0 {
				t.Errorf("doctor flagged a login it cannot judge: %+v", rows)
			}
		})
	}
}
