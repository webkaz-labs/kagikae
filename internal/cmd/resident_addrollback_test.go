package cmd

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The reconcile of `kae add` and `kae rollback` (docs/CLI.md § kae add Semantics,
// codex resident processes, and § `kae rollback --json`). Like
// resident_reconcile_test.go, these swap the runner seams and os.Stdout/os.Stderr,
// so none of them runs in parallel.

const (
	// The warning `kae add --no-login` gives, doctor's resident_drift finding.
	warnCaptureDiffers = "kae: warning: codex's managed daemon is not using the live credential's account, and neither are the sessions connected to it; to make it use the live account, run: codex app-server daemon restart"
	// The same in Japanese.
	warnCaptureDiffersJA = "kae: warning: codex の管理デーモンは現在の認証情報のアカウントを使っておらず、デーモンに接続したセッションも同じです。現在のアカウントを使わせるには codex app-server daemon restart を実行してください。"
	// The same finding when the daemon's account cannot be read.
	warnCaptureUnknown = "kae: warning: kae cannot read which account codex's managed daemon holds, so it cannot tell whether the daemon uses the live account; if it does not, run: codex app-server daemon restart"
	// Any line about the daemon contains this.
	daemonMention = "managed daemon"
	// The kae add (login flow) and kae rollback renderings of the switch's lines.
	addNoticeRestart = "kae: note: codex: the managed daemon (codex app-server daemon) is not using the account this kae add left live; kae restarts it now"
	addWarnOptedOut  = "kae: warning: codex: the managed daemon (codex app-server daemon) is not using the account this kae add left live, and --no-restart leaves it running; to move it to that account, run: codex app-server daemon restart"
	addWarnSession   = "kae: warning: codex sessions started before this kae add that are not connected to the managed daemon keep the previous account until they are restarted"
	addNoteRestarted = "kae: note: codex: restarted the managed daemon (codex app-server daemon); it now holds the account this kae add left live"
	addWarnFailed    = "kae: warning: codex: codex app-server daemon restart failed (exit 3); kae add's result is kept, and the managed daemon may still not be using the live account; to retry, run: codex app-server daemon restart"
	rbNoticeRestart  = "kae: note: codex: the managed daemon (codex app-server daemon) is not using the account this kae rollback puts back; kae restarts it after the rollback"
	rbNoticePlanned  = "kae: note: codex: the managed daemon (codex app-server daemon) is not using the account this kae rollback would put back; the rollback would restart it"
	rbWarnOptedOut   = "kae: warning: codex: the managed daemon (codex app-server daemon) is not using the account this kae rollback puts back, and --no-restart leaves it running; to move it to that account, run: codex app-server daemon restart"
	rbWarnSession    = "kae: warning: codex sessions started before this kae rollback that are not connected to the managed daemon keep the previous account until they are restarted"
	rbWarnSessionDry = "kae: warning: codex sessions started before this kae rollback that are not connected to the managed daemon would keep the previous account until they are restarted"
	rbNoteRestarted  = "kae: note: codex: restarted the managed daemon (codex app-server daemon); it now holds the account this kae rollback put back"
	rbWarnFailed     = "kae: warning: codex: codex app-server daemon restart failed (exit 3); kae rollback's result is kept, and the managed daemon may still not be using the live account; to retry, run: codex app-server daemon restart"
)

// loginAs replaces the interactive login flow with one that writes payload to the
// live auth.json; an empty payload leaves auth untouched.
func (f *residentFixture) loginAs(t *testing.T, payload string) {
	t.Helper()
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		if payload != "" {
			writeFile(t, filepath.Join(f.app.Env.Home, ".codex", "auth.json"), payload)
		}
		return 0, nil
	})
}

// add runs `kae add codex main` (the login flow) with opts.
func (f *residentFixture) add(t *testing.T, opts commonOpts, restore bool) (code int, stdout, stderr string) {
	t.Helper()
	return captureBoth(t, func() int {
		return runLogin(context.Background(), f.app, opts, constants.ToolCodex, "main", restore)
	})
}

// A login that leaves another account live than the daemon holds restarts the
// daemon once, after the add has released its lock, under the real home's
// CODEX_HOME, and warns about sessions because the account changed.
func TestAddRestartsADaemonHoldingAnotherAccount(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.stubRestart(t, f.restartTo(t))
	f.loginAs(t, codexChatGPTAuth(residentMain, "codex-login-token"))
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "bound", ".codex"))
	f.app.pinnedGlobalScope()

	code, stdout, stderr := f.add(t, commonOpts{Format: formatText}, false)
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if f.restartCount() != 1 {
		t.Fatalf("restarted %d times, want once", f.restartCount())
	}
	call := f.restarts[0]
	if want := []string{"codex", "app-server", "daemon", "restart"}; !reflect.DeepEqual(call.argv, want) {
		t.Errorf("argv = %q, want %q", call.argv, want)
	}
	spec := f.holder.ResidentDaemon(f.app.Env)
	if !reflect.DeepEqual(call.env, spec.Env) {
		t.Errorf("extra env = %q, want the spec's %q", call.env, spec.Env)
	}
	if want := strings.TrimPrefix(spec.Env[0], "CODEX_HOME="); call.codexHome != want {
		t.Errorf("the restart sees CODEX_HOME=%s, want the real home %s", call.codexHome, want)
	}
	if !call.lockFree {
		t.Error("the restart ran while the add's codex lock was held")
	}
	for _, line := range []string{addNoticeRestart, addWarnSession, addNoteRestarted} {
		if !strings.Contains(stderr, line+"\n") {
			t.Errorf("stderr lacks %q:\n%s", line, stderr)
		}
	}
	assertNoResidentPII(t, stdout, stderr)
}

// A daemon that holds no account differs from the login the add leaves live,
// and is restarted.
func TestAddRestartsADaemonHoldingNoAccount(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide).answers(noAccountReply)
	f.stubRestart(t, f.restartTo(t))
	f.loginAs(t, codexChatGPTAuth(residentMain, "codex-login-token"))
	code, stdout, stderr := f.add(t, commonOpts{Format: formatText}, false)
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if f.restartCount() != 1 {
		t.Fatalf("restarted %d times, want once", f.restartCount())
	}
	for _, line := range []string{addNoticeRestart, addNoteRestarted} {
		if !strings.Contains(stderr, line+"\n") {
			t.Errorf("stderr lacks %q:\n%s", line, stderr)
		}
	}
	assertNoResidentPII(t, stdout, stderr)
}

// --restore compares the daemon with the login it restores, not the one the flow
// made: back on the daemon's own account it does nothing, and on another one it
// restarts. Either way the account is unchanged, so there is no session warning.
func TestAddRestoreComparesWithTheRestoredLogin(t *testing.T) {
	for _, tc := range []struct {
		daemon  string
		restart bool
	}{{residentSide, false}, {residentMain, true}} {
		f := newResidentFixture(t)
		f.withDaemon(t, tc.daemon)
		if tc.restart {
			f.stubRestart(t, f.restartTo(t))
		}
		f.loginAs(t, codexChatGPTAuth(residentMain, "codex-login-token"))
		code, stdout, stderr := f.add(t, commonOpts{Format: formatText}, true)
		mustExit(t, constants.ExitOK, code, stdout+stderr)
		if f.liveAccountID(t) != residentSide {
			t.Fatal("--restore did not put the previous login back")
		}
		if got := f.restartCount() == 1; got != tc.restart {
			t.Errorf("daemon on the %s login: restarted = %v, want %v", map[bool]string{true: "flow's", false: "restored"}[tc.restart], got, tc.restart)
		}
		if tc.restart {
			if !f.restarts[0].lockFree {
				t.Error("the restart ran while the add's codex lock was held")
			}
			if !strings.Contains(stderr, addNoteRestarted+"\n") {
				t.Errorf("stderr lacks the restart:\n%s", stderr)
			}
		} else if strings.Contains(stderr, daemonMention) {
			t.Errorf("a daemon on the restored account printed a line:\n%s", stderr)
		}
		if strings.Contains(stderr, addWarnSession) {
			t.Errorf("--restore left the account unchanged but warned about sessions:\n%s", stderr)
		}
		assertNoResidentPII(t, stdout, stderr)
	}
}

// A flow that changes nothing (auth_unchanged) and a flow whose capture fails
// reconcile nothing, though the daemon differs from what is live.
func TestAddWithoutASuccessfulCaptureDoesNotReconcile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		account string
		code    int
	}{
		{"auth_unchanged", "", "main", constants.ExitAuthUnchanged},
		// No account name and a login with no identity: detection fails after
		// the flow changed auth (finishLoginFailure).
		{"detect failure", `{"auth_mode":"apikey","OPENAI_API_KEY":"sk-fixture"}`, "", constants.ExitUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newResidentFixture(t)
			f.withDaemon(t, residentMain)
			f.loginAs(t, tc.payload)
			code, stdout, stderr := captureBoth(t, func() int {
				return runLogin(context.Background(), f.app, commonOpts{Format: formatText}, constants.ToolCodex, tc.account, false)
			})
			mustExit(t, tc.code, code, stdout+stderr)
			if f.restartCount() != 0 {
				t.Error("restarted")
			}
			for _, banned := range []string{daemonMention, addWarnSession} {
				if strings.Contains(stderr, banned) {
					t.Errorf("stderr carries %q:\n%s", banned, stderr)
				}
			}
		})
	}
}

// --no-restart leaves the daemon running, names the manual step and keeps the
// session warning.
func TestAddNoRestart(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.loginAs(t, codexChatGPTAuth(residentMain, "codex-login-token"))
	code, stdout, stderr := f.add(t, commonOpts{Format: formatText, NoRestart: true}, false)
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if f.restartCount() != 0 {
		t.Error("restarted under --no-restart")
	}
	for _, line := range []string{addWarnOptedOut, addWarnSession} {
		if !strings.Contains(stderr, line+"\n") {
			t.Errorf("stderr lacks %q:\n%s", line, stderr)
		}
	}
	assertNoResidentPII(t, stdout, stderr)
}

func TestAddResidentsInJapanese(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	f.stubRestart(t, f.restartTo(t))
	f.loginAs(t, codexChatGPTAuth(residentMain, "codex-login-token"))
	l10ntest.UseJapanese(t)
	code, stdout, stderr := f.add(t, commonOpts{Format: formatText}, false)
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	for _, line := range []string{
		"kae: note: codex: 管理デーモン（codex app-server daemon）は、この kae add の後に有効なアカウントを使っていません。kae が再起動します。",
		"kae: warning: この kae add より前に起動し、管理デーモンに接続していない codex セッションは、再起動するまで前のアカウントを使います。",
		"kae: note: codex: 管理デーモン（codex app-server daemon）を再起動しました。この kae add の後に有効なアカウントを使っています。",
	} {
		if !strings.Contains(stderr, line+"\n") {
			t.Errorf("stderr lacks %q:\n%s", line, stderr)
		}
	}
	assertNoResidentPII(t, stdout, stderr)
}

// `kae add --no-login` leaves the live login as it is: a daemon on another
// account gets a warning only, with or without --dry-run, and no session warning;
// absent and matching daemons print nothing.
func TestAddNoLoginOnlyWarns(t *testing.T) {
	differs := []residentEntry{daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeWarned)}
	unknown := []residentEntry{daemonEntry(constants.ResidentObservedUnknown, constants.ResidentOutcomeWarned)}
	for _, tc := range []struct {
		name      string
		daemon    string // "" for none
		answer    string // replaces the daemon's answer; "" keeps it holding daemon
		dryRun    bool
		warning   string // "" for none
		residents []residentEntry
	}{
		{"differs", residentMain, "", false, warnCaptureDiffers, differs},
		{"differs dry-run", residentMain, "", true, warnCaptureDiffers, differs},
		{"differs, no account", residentMain, noAccountReply, false, warnCaptureDiffers, differs},
		{"unknown", residentMain, apiKeyReply, false, warnCaptureUnknown, unknown},
		{"matches", residentSide, "", false, "", []residentEntry{}},
		{"absent", "", "", false, "", []residentEntry{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []string{formatText, formatJSON} {
				f := newResidentFixture(t)
				if tc.daemon != "" {
					d := f.withDaemon(t, tc.daemon)
					if tc.answer != "" {
						d.answers(tc.answer)
					}
				}
				opts := commonOpts{Format: format, DryRun: tc.dryRun}
				code, stdout, stderr := captureBoth(t, func() int {
					return runCapture(context.Background(), f.app, opts, constants.ToolCodex, "side")
				})
				mustExit(t, constants.ExitOK, code, stdout+stderr)
				if f.restartCount() != 0 {
					t.Errorf("%s: restarted", format)
				}
				if tc.warning != "" && !strings.Contains(stderr, tc.warning+"\n") {
					t.Errorf("%s: stderr lacks %q:\n%s", format, tc.warning, stderr)
				}
				if tc.warning == "" && strings.Contains(stderr, daemonMention) {
					t.Errorf("%s: printed a daemon line:\n%s", format, stderr)
				}
				if strings.Contains(stderr, "codex sessions started before") {
					t.Errorf("%s: warned about sessions though the account is unchanged:\n%s", format, stderr)
				}
				if format == formatJSON {
					wantCodexResidents(t, stdout, tc.residents...)
				}
				assertNoResidentPII(t, stdout, stderr)
			}
		})
	}
}

// A tool without resident processes carries `residents: []` in the capture report.
func TestAddNoLoginResidentsEmptyForClaude(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentMain)
	code, stdout, stderr := captureBoth(t, func() int {
		return runCapture(context.Background(), f.app, commonOpts{Format: formatJSON}, constants.ToolClaude, "side")
	})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if got, ok := codexResidents(t, stdout)[constants.ToolClaude]; !ok || len(got) != 0 {
		t.Errorf("claude residents = %+v (present %v), want []", got, ok)
	}
	if strings.Contains(stderr, daemonMention) {
		t.Errorf("a claude capture printed a daemon line:\n%s", stderr)
	}
}

func TestAddNoLoginWarningInJapanese(t *testing.T) {
	f := newResidentFixture(t)
	f.withDaemon(t, residentMain)
	l10ntest.UseJapanese(t)
	code, stdout, stderr := captureBoth(t, func() int {
		return runCapture(context.Background(), f.app, commonOpts{Format: formatText}, constants.ToolCodex, "side")
	})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if !strings.Contains(stderr, warnCaptureDiffersJA+"\n") {
		t.Errorf("stderr lacks %q:\n%s", warnCaptureDiffersJA, stderr)
	}
}

// newRollbackFixture is a residentFixture after `kae use main` over the profile (both
// tools), so the newest backup holds side's codex login and live is main's. The
// switch ran with no daemon; the caller starts one.
func newRollbackFixture(t *testing.T) *residentFixture {
	t.Helper()
	f := newResidentFixture(t)
	f.use(t, context.Background(), commonOpts{Format: formatText}, "all", "main")
	if f.liveAccountID(t) != residentMain {
		t.Fatal("the setup switch did not apply main")
	}
	return f
}

// rollback runs `kae rollback` with opts.
func (f *residentFixture) rollback(t *testing.T, opts commonOpts) (code int, stdout, stderr string) {
	t.Helper()
	return captureBoth(t, func() int { return runRollback(context.Background(), f.app, opts, "") })
}

// rollbackResidents decodes a rollback report and returns each restored tool's
// residents, failing on a null array.
func rollbackResidents(t *testing.T, out string) map[string][]residentEntry {
	t.Helper()
	var report struct {
		Restored []struct {
			Tool      string          `json:"tool"`
			Residents []residentEntry `json:"residents"`
		} `json:"restored"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid report: %v: %s", err, out)
	}
	byTool := map[string][]residentEntry{}
	for _, r := range report.Restored {
		if r.Residents == nil {
			t.Errorf("%s: residents is null, want an array: %s", r.Tool, out)
		}
		byTool[r.Tool] = r.Residents
	}
	return byTool
}

// A rollback that puts back another account than the daemon holds restarts it
// after the rollback has released its locks; each restored entry carries its
// residents, [] for claude.
func TestRollbackRestartsADaemonHoldingAnotherAccount(t *testing.T) {
	f := newRollbackFixture(t)
	f.withDaemon(t, residentMain)
	f.stubRestart(t, f.restartTo(t))
	code, stdout, stderr := f.rollback(t, commonOpts{Format: formatJSON})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if f.liveAccountID(t) != residentSide {
		t.Fatal("the rollback did not restore side")
	}
	got := rollbackResidents(t, stdout)
	if c, ok := got[constants.ToolClaude]; !ok || len(c) != 0 {
		t.Errorf("claude residents = %+v (present %v), want []", c, ok)
	}
	want := []residentEntry{daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestarted), sessionEntry}
	if !reflect.DeepEqual(got[constants.ToolCodex], want) {
		t.Errorf("codex residents = %+v, want %+v", got[constants.ToolCodex], want)
	}
	if f.restartCount() != 1 {
		t.Fatalf("restarted %d times, want once", f.restartCount())
	}
	if !f.restarts[0].lockFree {
		t.Error("the restart ran while the rollback's codex lock was held")
	}
	for _, line := range []string{rbNoticeRestart, rbWarnSession, rbNoteRestarted} {
		if !strings.Contains(stderr, line+"\n") {
			t.Errorf("stderr lacks %q:\n%s", line, stderr)
		}
	}
	if strings.Index(stderr, rbNoticeRestart) > strings.Index(stderr, rbNoteRestarted) {
		t.Errorf("the outcome precedes the notice:\n%s", stderr)
	}
	assertNoResidentPII(t, stdout, stderr)
}

// A daemon that holds no account differs from the credential the backup puts
// back, and is restarted after the rollback.
func TestRollbackRestartsADaemonHoldingNoAccount(t *testing.T) {
	f := newRollbackFixture(t)
	f.withDaemon(t, residentMain).answers(noAccountReply)
	f.stubRestart(t, f.restartTo(t))
	code, stdout, stderr := f.rollback(t, commonOpts{Format: formatJSON})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	want := []residentEntry{daemonEntry(constants.ResidentObservedDiffers, constants.ResidentOutcomeRestarted), sessionEntry}
	if got := rollbackResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, want) {
		t.Errorf("codex residents = %+v, want %+v", got, want)
	}
	if f.restartCount() != 1 {
		t.Fatalf("restarted %d times, want once", f.restartCount())
	}
	assertNoResidentPII(t, stdout, stderr)
}

// The probe runs before the write and compares the daemon with the credential the
// backup puts back, not the one live: a daemon already on the backup's account
// matches and is left alone, though it differs from the live login.
func TestRollbackComparesTheDaemonWithTheBackup(t *testing.T) {
	f := newRollbackFixture(t)
	f.withDaemon(t, residentSide)
	code, stdout, stderr := f.rollback(t, commonOpts{Format: formatJSON})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	want := []residentEntry{daemonEntry(constants.ResidentObservedMatches, constants.ResidentOutcomeNone), sessionEntry}
	if got := rollbackResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, want) {
		t.Errorf("codex residents = %+v, want %+v", got, want)
	}
	if strings.Contains(stderr, daemonMention+" (codex app-server daemon)") {
		t.Errorf("a matching daemon printed a line:\n%s", stderr)
	}
}

// --dry-run stops at the notice with the planned outcome; --no-restart warns and
// leaves the daemon running; neither restarts.
func TestRollbackSuppressesTheRestart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    commonOpts
		outcome string
		line    string
		session string
		live    string
	}{
		{"dry-run", commonOpts{DryRun: true, Yes: true}, constants.ResidentOutcomePlanned, rbNoticePlanned, rbWarnSessionDry, residentMain},
		{"no-restart", commonOpts{NoRestart: true, Yes: true}, constants.ResidentOutcomeOptedOut, rbWarnOptedOut, rbWarnSession, residentSide},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRollbackFixture(t)
			f.withDaemon(t, residentMain)
			opts := tc.opts
			opts.Format = formatJSON
			code, stdout, stderr := f.rollback(t, opts)
			mustExit(t, constants.ExitOK, code, stdout+stderr)
			if f.restartCount() != 0 {
				t.Error("restarted")
			}
			want := []residentEntry{daemonEntry(constants.ResidentObservedDiffers, tc.outcome), sessionEntry}
			if got := rollbackResidents(t, stdout)[constants.ToolCodex]; !reflect.DeepEqual(got, want) {
				t.Errorf("codex residents = %+v, want %+v", got, want)
			}
			for _, line := range []string{tc.line, tc.session} {
				if !strings.Contains(stderr, line+"\n") {
					t.Errorf("stderr lacks %q:\n%s", line, stderr)
				}
			}
			if f.liveAccountID(t) != tc.live {
				t.Error("the live account after the run is not the expected one")
			}
			assertNoResidentPII(t, stdout, stderr)
		})
	}
}

// A rollback whose restore fails restarts nothing; its notice was on stderr
// before the failing write.
func TestFailedRollbackDoesNotRestart(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a read-only directory")
	}
	f := newRollbackFixture(t)
	f.withDaemon(t, residentMain)
	codexHome := filepath.Join(f.app.Env.Home, ".codex")
	if err := os.Chmod(codexHome, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(codexHome, 0o700) })
	code, stdout, stderr := f.rollback(t, commonOpts{Format: formatText})
	if code == constants.ExitOK {
		t.Fatalf("the rollback succeeded through a read-only home:\n%s%s", stdout, stderr)
	}
	if f.restartCount() != 0 {
		t.Error("restarted after a failed restore")
	}
	if !strings.Contains(stderr, rbNoticeRestart+"\n") {
		t.Errorf("the notice did not precede the write:\n%s", stderr)
	}
	if strings.Contains(stderr, rbNoteRestarted) {
		t.Errorf("reported a restart:\n%s", stderr)
	}
}

func TestRollbackResidentsInJapanese(t *testing.T) {
	f := newRollbackFixture(t)
	f.withDaemon(t, residentMain)
	f.stubRestart(t, f.restartTo(t))
	l10ntest.UseJapanese(t)
	code, stdout, stderr := f.rollback(t, commonOpts{Format: formatText, NoRestart: true})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	line := "kae: warning: codex: 管理デーモン（codex app-server daemon）は、この kae rollback で戻すアカウントを使っていませんが、--no-restart が指定されているため再起動しません。そのアカウントに切り替えるには、codex app-server daemon restart を実行してください。"
	if !strings.Contains(stderr, line+"\n") {
		t.Errorf("stderr lacks %q:\n%s", line, stderr)
	}
	assertNoResidentPII(t, stdout, stderr)
}

// --no-restart is a flag of add and rollback, offered by completion.
func TestAddAndRollbackOfferNoRestart(t *testing.T) {
	for _, command := range []string{"add", "rollback"} {
		if !strings.Contains(strings.Join(flagCompletions(command), " "), "--no-restart") {
			t.Errorf("%s flags = %q", command, flagCompletions(command))
		}
	}
}

// --no-restart reaches opts through add's and rollback's own parsers.
func TestAddAndRollbackParseNoRestart(t *testing.T) {
	for _, flags := range [][]string{nil, {"--no-restart"}, {"--no-restart", "--yes"}} {
		want := len(flags) != 0
		if opts, _, _, ok := parseAddFlags(flags); !ok || opts.NoRestart != want {
			t.Errorf("add %q: ok=%v NoRestart=%v, want %v", flags, ok, opts.NoRestart, want)
		}
		if opts, _, ok := parseRollbackFlags(flags); !ok || opts.NoRestart != want {
			t.Errorf("rollback %q: ok=%v NoRestart=%v, want %v", flags, ok, opts.NoRestart, want)
		}
	}
}

// --restore whose restore fails reconciles nothing, though the flow left another
// account live than the daemon holds.
func TestAddRestoreFailureDoesNotReconcile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through a read-only directory")
	}
	f := newResidentFixture(t)
	f.withDaemon(t, residentSide)
	codexHome := filepath.Join(f.app.Env.Home, ".codex")
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		writeFile(t, filepath.Join(codexHome, "auth.json"), codexChatGPTAuth(residentMain, "codex-login-token"))
		// The capture only reads; the restore has to write auth.json here.
		if err := os.Chmod(codexHome, 0o500); err != nil {
			t.Fatal(err)
		}
		return 0, nil
	})
	t.Cleanup(func() { _ = os.Chmod(codexHome, 0o700) })
	code, stdout, stderr := f.add(t, commonOpts{Format: formatText}, true)
	if code == constants.ExitOK {
		t.Fatalf("the restore succeeded through a read-only home:\n%s%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "restoring the previous login failed") {
		t.Fatalf("the failure was not the restore's:\n%s", stderr)
	}
	if f.restartCount() != 0 {
		t.Error("restarted after a failed restore")
	}
	for _, banned := range []string{daemonMention, "codex sessions started before"} {
		if strings.Contains(stderr, banned) {
			t.Errorf("stderr carries %q:\n%s", banned, stderr)
		}
	}
}

// The rollback's probe runs before its locks: every connection to the daemon,
// the first probe's and the restart's re-probes, finds the codex lock free.
func TestRollbackProbesWithoutTheLock(t *testing.T) {
	f := newRollbackFixture(t)
	f.withDaemon(t, residentMain)
	f.stubRestart(t, f.restartTo(t))
	dials, held := 0, 0
	f.app.dialUnix = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials++
		if l, err := lock.Acquire(f.app.Paths.LocksDir(), constants.ToolCodex); err == nil {
			l.Release()
		} else {
			held++
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	code, stdout, stderr := f.rollback(t, commonOpts{Format: formatText})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if dials < 2 {
		t.Fatalf("dialled the daemon %d times, want the probe and a re-probe", dials)
	}
	if held != 0 {
		t.Errorf("%d of %d probes ran while the rollback held the codex lock", held, dials)
	}
}

// A rollback reads the backup's codex payload from kae's secret store once: the
// probe, the superseded-credential warning and the restore share the cache.
func TestRollbackReadsTheBackupPayloadOnce(t *testing.T) {
	f := newRollbackFixture(t)
	f.withDaemon(t, residentSide)
	fileBE, err := secret.Resolve(secret.BackendFile, f.app.Env.GOOS, f.app.Paths.SecretsDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	counter := &countingBackend{Backend: fileBE, gets: map[string]int{}}
	f.app.backendForTest = counter
	meta, found, err := latestRestorable(f.app.Paths.BackupsDir())
	if err != nil || !found {
		t.Fatalf("no backup to roll back to (found %v, err %v)", found, err)
	}
	rec, ok := backupRecord(meta, constants.ToolCodex, credentialArtifactName(constants.ToolCodex))
	if !ok {
		t.Fatal("the backup has no codex credential record")
	}
	code, stdout, stderr := f.rollback(t, commonOpts{Format: formatText})
	mustExit(t, constants.ExitOK, code, stdout+stderr)
	if got := counter.gets[rec.SecretRef]; got != 1 {
		t.Errorf("read the backup's codex payload %d times, want once", got)
	}
}

// The rollback's keychain read cache opens under its locks: the resident probe's
// lock-free read of codex's live keyring item is not reused for the pre-rollback
// backup, which reads the item again under the lock.
func TestRollbackKeychainCacheStartsUnderTheLocks(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"), "cli_auth_credentials_store = \"keyring\"\n")
	sim := &keychainSim{payload: codexChatGPTAuth(residentMain, "codex-main-token"), present: true}
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	var reads int
	runner.With(sim, func() {
		code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, constants.ToolCodex, "main") })
		mustExit(t, constants.ExitOK, code, out)
		sim.payload = codexChatGPTAuth(residentSide, "codex-side-token")
		code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, constants.ToolCodex, "side") })
		mustExit(t, constants.ExitOK, code, out)
		// The newest backup records side; main is live after the switch.
		code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, constants.ToolCodex, "main") })
		mustExit(t, constants.ExitOK, code, out)

		before := sim.readW
		code, stdout, stderr := captureBoth(t, func() int { return runRollback(ctx, app, opts, "") })
		mustExit(t, constants.ExitOK, code, stdout+stderr)
		reads = sim.readW - before
	})
	if !strings.Contains(sim.payload, residentSide) {
		t.Fatal("the rollback did not restore side")
	}
	// The probe's read before the locks, and the backup's under them.
	if reads < 2 {
		t.Errorf("read codex's live keyring item %d times during the rollback, want the probe's and the backup's (2 or more)", reads)
	}
}

// A restart that fails after kae add or kae rollback keeps the command's result
// and names the retry, in English and Japanese. The daemon held no account, so
// the warning must not claim it keeps a previous one.
func TestAddAndRollbackRestartFailureWarns(t *testing.T) {
	failing := func(*restartCall) int { return 3 }
	for _, tc := range []struct {
		name     string
		run      func(t *testing.T) (code int, stdout, stderr string)
		english  string
		japanese string
	}{
		{
			"add",
			func(t *testing.T) (int, string, string) {
				f := newResidentFixture(t)
				f.withDaemon(t, residentSide).answers(noAccountReply)
				f.stubRestart(t, failing)
				f.loginAs(t, codexChatGPTAuth(residentMain, "codex-login-token"))
				return f.add(t, commonOpts{Format: formatText}, false)
			},
			addWarnFailed,
			"kae: warning: codex: codex app-server daemon restart に失敗しました（終了コード 3）。kae add の結果はそのまま有効ですが、管理デーモン（codex app-server daemon）は現在有効なアカウントをまだ使っていない可能性があります。再試行するには、codex app-server daemon restart を実行してください。",
		},
		{
			"rollback",
			func(t *testing.T) (int, string, string) {
				f := newRollbackFixture(t)
				f.withDaemon(t, residentMain).answers(noAccountReply)
				f.stubRestart(t, failing)
				return f.rollback(t, commonOpts{Format: formatText})
			},
			rbWarnFailed,
			"kae: warning: codex: codex app-server daemon restart に失敗しました（終了コード 3）。kae rollback の結果はそのまま有効ですが、管理デーモン（codex app-server daemon）は現在有効なアカウントをまだ使っていない可能性があります。再試行するには、codex app-server daemon restart を実行してください。",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, japanese := range []bool{false, true} {
				want := tc.english
				if japanese {
					want = tc.japanese
				}
				wantStderrLine(t, japanese, want, func() (int, string, string) { return tc.run(t) })
			}
		})
	}
}
