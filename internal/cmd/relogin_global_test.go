package cmd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// The incident this file pins down: claude/main is active and its live login
// expired; the guidance kae printed was `kae add --restore claude main`, whose
// restore put the expired pre-login copy back over the new login. The active
// account's re-login is `kae relogin claude`, and --restore refuses it.

const (
	reloggedToken = "sk-ant-oat01-RELOGGED-MAIN-dddd"
	strangerToken = "sk-ant-oat01-STRANGER-eeee"
)

// expiredActiveMain captures main with a working login, makes it active, then
// leaves the live login the tombstone claude writes after a failed refresh.
func expiredActiveMain(t *testing.T) *App {
	t.Helper()
	app := testApp(t, nil)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	seedClaudeOAuth(t, app, `{"accessToken":"","refreshToken":"","expiresAt":0}`)
	chdirTemp(t)
	return app
}

// claudeLoginAs is a RunInteractive stand-in for `claude /login` that writes the
// real home's credential and identity cache as account uuid, recording argv and env.
func claudeLoginAs(t *testing.T, app *App, token, uuid string, seen *[]string) func(context.Context, []string, string, ...string) (int, error) {
	t.Helper()
	return func(_ context.Context, extraEnv []string, name string, args ...string) (int, error) {
		*seen = append(append([]string{name}, args...), extraEnv...)
		writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
			claudeOAuthPayload(token, app.Now().Add(8*time.Hour)))
		writeFile(t, filepath.Join(app.Env.Home, ".claude.json"),
			`{"oauthAccount":`+claudeOAuthAccount(uuid, uuid+"@example.com")+`}`)
		return 0, nil
	}
}

func activeClaude(t *testing.T, app *App) string {
	t.Helper()
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	return st.Active[constants.ToolClaude]
}

func backupCount(t *testing.T, app *App) int {
	t.Helper()
	metas, err := backup.List(app.Paths.BackupsDir())
	if err != nil {
		t.Fatal(err)
	}
	return len(metas)
}

func TestGlobalReloginKeepsTheNewLoginOfTheActiveAccount(t *testing.T) {
	for _, explicit := range []string{constants.ToolClaude, ""} {
		t.Run("tool="+explicit, func(t *testing.T) {
			app := expiredActiveMain(t)
			seen := []string{}
			withInteractive(t, claudeLoginAs(t, app, reloggedToken, "main-uuid", &seen))

			code, out, stderr := captureBoth(t, func() int {
				return runRelogin(context.Background(), app, commonOpts{Format: formatText}, explicit)
			})
			mustExit(t, constants.ExitOK, code, stderr)
			// The tool's own login, with nothing exported: a global login uses the real home.
			if !slices.Equal(seen, []string{"claude", "/login"}) {
				t.Fatalf("the global re-login must run claude /login with no isolation variable: %v", seen)
			}
			if got := claudeCreds(t, app); !strings.Contains(got, reloggedToken) {
				t.Fatalf("the new login must stay live: %s", got)
			}
			if got := snapshotPayload(t, app, testBackend(t, app), constants.ToolClaude, "main"); !strings.Contains(got, reloggedToken) {
				t.Fatalf("the new login must be captured into main: %s", got)
			}
			if got := activeClaude(t, app); got != "main" {
				t.Fatalf("active must stay main, got %q", got)
			}
			if !strings.Contains(stderr, "captured as claude/main") {
				t.Errorf("the flow must name the account it captures into: %q", stderr)
			}
			if strings.Contains(out+stderr, reloggedToken) || strings.Contains(out+stderr, mainToken) {
				t.Fatal("a credential reached the output")
			}
		})
	}
}

// After the re-login a switch away and back applies the new login, not the copy
// the snapshot held before it.
func TestGlobalReloginSurvivesASwitchRoundTrip(t *testing.T) {
	app := expiredActiveMain(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	// side exists before the re-login; capturing it moves active, so switch back.
	seedClaude(t, app, sideToken, "side-uuid")
	mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "side"), "capture side")
	mustExit(t, constants.ExitOK, runSwitchQuiet(t, app, "main"), "switch main")
	seedClaudeOAuth(t, app, `{"accessToken":"","refreshToken":"","expiresAt":0}`)

	withInteractive(t, claudeLoginAs(t, app, reloggedToken, "main-uuid", &[]string{}))
	code, stderr := captureStderr(t, func() int { return runRelogin(ctx, app, opts, constants.ToolClaude) })
	mustExit(t, constants.ExitOK, code, stderr)

	mustExit(t, constants.ExitOK, runSwitchQuiet(t, app, "side"), "switch side")
	mustExit(t, constants.ExitOK, runSwitchQuiet(t, app, "main"), "switch main again")
	if got := claudeCreds(t, app); !strings.Contains(got, reloggedToken) {
		t.Fatalf("switching back must apply the re-logged token: %s", got)
	}
}

func runCaptureQuiet(t *testing.T, app *App, name string) int {
	t.Helper()
	code, _ := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, name)
	})
	return code
}

func runSwitchQuiet(t *testing.T, app *App, name string) int {
	t.Helper()
	code, _, _ := captureBoth(t, func() int {
		return runSwitch(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, name)
	})
	return code
}

// A live login newer than the snapshot (claude refreshed it in-tool) is never
// overwritten by the older snapshot when the same account is applied again.
func TestUseOfTheActiveAccountKeepsANewerLiveLogin(t *testing.T) {
	app := testApp(t, nil)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
		claudeOAuthPayload(reloggedToken, app.Now().Add(8*time.Hour)))
	mustExit(t, constants.ExitOK, runSwitchQuiet(t, app, "main"), "switch main")
	if got := claudeCreds(t, app); !strings.Contains(got, reloggedToken) {
		t.Fatalf("the older snapshot was written over the newer live login: %s", got)
	}
}

// An aborted or failed flow leaves auth unchanged: nothing is captured and the
// state is as it was.
func TestGlobalReloginWithAnAbortedFlowChangesNothing(t *testing.T) {
	app := expiredActiveMain(t)
	before := claudeCreds(t, app)
	be := testBackend(t, app)
	snapBefore := snapshotPayload(t, app, be, constants.ToolClaude, "main")
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) { return 1, nil })
	code, stderr := captureStderr(t, func() int {
		return runRelogin(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude)
	})
	mustExit(t, constants.ExitAuthUnchanged, code, stderr)
	if claudeCreds(t, app) != before || snapshotPayload(t, app, be, constants.ToolClaude, "main") != snapBefore {
		t.Fatal("an aborted flow must change neither the live login nor the snapshot")
	}
	if got := activeClaude(t, app); got != "main" {
		t.Fatalf("active must stay main, got %q", got)
	}
}

func TestGlobalReloginRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, app *App, env map[string]string)
		tool  string
		// call replaces `kae relogin <tool>` for a row that has to enter elsewhere.
		call          func(app *App) int
		code          int
		want, notWant string
	}{
		{
			name:  "no active account for the named tool",
			setup: func(*testing.T, *App, map[string]string) {},
			tool:  constants.ToolClaude, code: constants.ExitNotFound,
			want: "kae add claude <account>",
		},
		{
			name: "an active account with no snapshot",
			setup: func(t *testing.T, app *App, _ map[string]string) {
				captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
				if err := os.RemoveAll(app.Paths.AccountDir(constants.ToolClaude, "main")); err != nil {
					t.Fatal(err)
				}
			},
			tool: constants.ToolClaude, code: constants.ExitNotFound, want: "kae add claude main",
		},
		{
			name:  "a tool with no login command",
			setup: func(*testing.T, *App, map[string]string) {},
			tool:  constants.ToolAgy, code: constants.ExitUnsupported, want: "no login command for agy",
		},
		{
			name: "omitted tool with several candidates",
			setup: func(t *testing.T, app *App, _ map[string]string) {
				captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
				seedCodex(t, app, "codex-token")
				code, _ := captureStdout(t, func() int {
					return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolCodex, "main")
				})
				mustExit(t, constants.ExitOK, code, "capture codex")
			},
			code: constants.ExitUsage, want: "relogin <tool>",
		},
		{
			name: "below a bound directory",
			setup: func(t *testing.T, app *App, env map[string]string) {
				captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
				env["CLAUDE_CONFIG_DIR"] = app.Paths.SharedDir("abcdef0123456789", constants.ToolClaude)
			},
			// The relogin wording: the bound directory above, not kae add's shell outside it.
			tool: constants.ToolClaude, code: constants.ExitUsage, want: "this directory is not bound, but this shell carries a bound directory's isolation (CLAUDE_CONFIG_DIR",
		},
		{
			name: "below a directory binding only another tool",
			setup: func(t *testing.T, app *App, env map[string]string) {
				captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
				env["CODEX_HOME"] = app.Paths.SharedDir("abcdef0123456789", constants.ToolCodex)
			},
			tool: constants.ToolClaude, code: constants.ExitUsage, want: "CODEX_HOME",
		},
		{
			name: "claude's credential variable set by kae",
			setup: func(t *testing.T, app *App, env map[string]string) {
				captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
				env["CLAUDE_SECURESTORAGE_CONFIG_DIR"] = app.credStoreDir(constants.ToolClaude, "main")
			},
			tool: constants.ToolClaude, code: constants.ExitUsage, want: "CLAUDE_SECURESTORAGE_CONFIG_DIR",
		},
		{
			// kae use -i: there is no bound directory to name.
			name: "claude's own global isolated home",
			setup: func(t *testing.T, app *App, env map[string]string) {
				captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
				env["CLAUDE_CONFIG_DIR"] = app.Paths.GlobalIsolatedHomeDir(constants.ToolClaude, "main")
			},
			tool: constants.ToolClaude, code: constants.ExitUsage,
			want: "kae's global isolated home for claude is active in this shell (CLAUDE_CONFIG_DIR", notWant: "bound directory",
		},
		{
			// The active account read under the tool lock is not the one read before it.
			name: "the active account changed before the flow",
			setup: func(t *testing.T, app *App, _ map[string]string) {
				captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
			},
			call: func(app *App) int {
				return runLoginRequest(context.Background(), app, commonOpts{Format: formatText},
					loginRequest{tool: constants.ToolClaude, explicitName: "side", activeOnly: true})
			},
			code: constants.ExitUnsafeRefused, want: "it is no longer claude/side",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			app := testApp(t, env)
			chdirTemp(t)
			tc.setup(t, app, env)
			backups := backupCount(t, app)
			ran := false
			withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
				ran = true
				return 0, nil
			})
			call := tc.call
			if call == nil {
				call = func(app *App) int {
					return runRelogin(context.Background(), app, commonOpts{Format: formatText}, tc.tool)
				}
			}
			code, stderr := captureStderr(t, func() int { return call(app) })
			mustExit(t, tc.code, code, stderr)
			if ran {
				t.Fatal("no login may be launched")
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("the refusal must name %q: %q", tc.want, stderr)
			}
			if tc.notWant != "" && strings.Contains(stderr, tc.notWant) {
				t.Errorf("the refusal must not mention %q: %q", tc.notWant, stderr)
			}
			if backupCount(t, app) != backups {
				t.Error("a refusal must not take a backup")
			}
		})
	}
}

// A global isolated home of another tool (kae use -i) does not redirect a claude
// login, so it is not a reason to refuse.
func TestGlobalReloginIgnoresAnotherToolsGlobalIsolation(t *testing.T) {
	env := map[string]string{}
	app := testApp(t, env)
	chdirTemp(t)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	seedClaudeOAuth(t, app, `{"accessToken":"","refreshToken":"","expiresAt":0}`)
	env["CODEX_HOME"] = app.Paths.GlobalIsolatedHomeDir(constants.ToolCodex, "main")
	withInteractive(t, claudeLoginAs(t, app, reloggedToken, "main-uuid", &[]string{}))
	code, stderr := captureStderr(t, func() int {
		return runRelogin(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude)
	})
	mustExit(t, constants.ExitOK, code, stderr)
}

// R2: --restore for the active account would put its expired copy back, so it is
// refused before the backup and the login, naming the active re-login; for an
// account that is not active it still captures and puts the active login back.
func TestAddRestoreFollowsActiveness(t *testing.T) {
	for _, tc := range []struct {
		target  string
		refused bool
	}{
		{target: "main", refused: true},
		{target: "side"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			app := testApp(t, nil)
			captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
			seedClaude(t, app, sideToken, "side-uuid")
			mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "side"), "capture side")
			mustExit(t, constants.ExitOK, runSwitchQuiet(t, app, "main"), "switch main")
			live := claudeCreds(t, app)
			backups := backupCount(t, app)
			ran := false
			login := claudeLoginAs(t, app, reloggedToken, tc.target+"-uuid", &[]string{})
			withInteractive(t, func(ctx context.Context, env []string, name string, args ...string) (int, error) {
				ran = true
				return login(ctx, env, name, args...)
			})
			code, out, stderr := captureBoth(t, func() int {
				return runLogin(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, tc.target, true)
			})
			if claudeCreds(t, app) != live || activeClaude(t, app) != "main" {
				t.Fatalf("main's login must be live and active afterwards: %s", claudeCreds(t, app))
			}
			if tc.refused {
				mustExit(t, constants.ExitUsage, code, stderr)
				if ran || backupCount(t, app) != backups {
					t.Fatalf("nothing may run or be backed up: ran=%v", ran)
				}
				if !strings.Contains(stderr, "kae relogin claude") {
					t.Errorf("the refusal must name the active re-login: %q", stderr)
				}
				return
			}
			mustExit(t, constants.ExitOK, code, stderr)
			if !strings.Contains(out, "restored the previous login") {
				t.Errorf("must say the previous login was restored: %q", out)
			}
			if got := snapshotPayload(t, app, testBackend(t, app), constants.ToolClaude, tc.target); !strings.Contains(got, reloggedToken) {
				t.Fatalf("%s must hold the new login: %s", tc.target, got)
			}
		})
	}
}

// R3: a login that is another account than an existing snapshot records is not
// captured under that name, and the live login goes back to the pre-login one
// with or without --restore. Each form is a row; codex carries its identity inside
// the credential, so its row comes from the owner comparison.
func TestAddRefusesAnotherAccountsLoginForAnExistingName(t *testing.T) {
	claudeStranger := func(t *testing.T, app *App) {
		writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
			claudeOAuthPayload(strangerToken, app.Now().Add(8*time.Hour)))
		writeFile(t, filepath.Join(app.Env.Home, ".claude.json"),
			`{"oauthAccount":`+claudeOAuthAccount("stranger-uuid", "stranger-uuid@example.com")+`}`)
	}
	claudeSeed := func(t *testing.T, app *App) {
		captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
		seedClaude(t, app, sideToken, "side-uuid")
		mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "side"), "capture side")
	}
	claudeLeaks := []string{strangerToken, mainToken, sideToken, "stranger-uuid", "main-uuid", "@example.com"}
	for _, tc := range []struct {
		name    string
		tool    string
		restore bool
		noLogin bool
		seed    func(t *testing.T, app *App) // captures main and side, side active
		other   func(t *testing.T, app *App) // makes another account's login live
		live    func(t *testing.T, app *App) string
		leaks   []string
	}{
		{name: "login flow", tool: constants.ToolClaude, seed: claudeSeed, other: claudeStranger, live: claudeCreds, leaks: claudeLeaks},
		{name: "login flow with --restore", tool: constants.ToolClaude, restore: true, seed: claudeSeed, other: claudeStranger, live: claudeCreds, leaks: claudeLeaks},
		{name: "--no-login", tool: constants.ToolClaude, noLogin: true, seed: claudeSeed, other: claudeStranger, live: claudeCreds, leaks: claudeLeaks},
		{
			name: "codex login flow", tool: constants.ToolCodex, seed: seedCodexAccounts,
			other: func(t *testing.T, app *App) {
				writeFile(t, codexAuthFile(app), consistentLogin(conflictAcctSide, "rt-new"))
			},
			live:  func(t *testing.T, app *App) string { return readFile(t, codexAuthFile(app)) },
			leaks: []string{conflictAcctMain, conflictAcctSide, conflictEmail, rotatedRefresh, "eyJ"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t, nil)
			tc.seed(t, app)
			be := testBackend(t, app)
			snapBefore := snapshotPayload(t, app, be, tc.tool, "main")
			identityBefore := ""
			if tc.tool == constants.ToolClaude {
				identityBefore = snapshotArtifact(t, app, be, tc.tool, "main", "oauth_account")
			}
			live := tc.live(t, app)
			var code int
			var out, stderr string
			if tc.noLogin {
				tc.other(t, app)
				live = tc.live(t, app)
				code, out, stderr = captureBoth(t, func() int {
					return runCapture(context.Background(), app, commonOpts{Format: formatText}, tc.tool, "main")
				})
			} else {
				withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
					tc.other(t, app)
					return 0, nil
				})
				code, out, stderr = captureBoth(t, func() int {
					return runLogin(context.Background(), app, commonOpts{Format: formatText}, tc.tool, "main", tc.restore)
				})
			}
			mustExit(t, constants.ExitUnsafeRefused, code, stderr)
			if got := snapshotPayload(t, app, be, tc.tool, "main"); got != snapBefore {
				t.Fatalf("main's snapshot must be unchanged: %s", got)
			}
			if tc.tool == constants.ToolClaude {
				if got := snapshotArtifact(t, app, be, tc.tool, "main", "oauth_account"); got != identityBefore {
					t.Fatalf("main's recorded identity must be unchanged: %s", got)
				}
				if got := activeClaude(t, app); got != "side" {
					t.Fatalf("active must stay side, got %q", got)
				}
			}
			if tc.live(t, app) != live {
				t.Fatalf("the live login must be the one before the command: %s", tc.live(t, app))
			}
			for _, want := range []string{"kae account rename " + tc.tool + " main", "kae account rm " + tc.tool + " main", "kae add " + tc.tool + " <account>"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal must name %q: %q", want, stderr)
				}
			}
			// Only a form that ran a login flow may say another account was picked in it.
			if mentions := strings.Contains(stderr, "login flow"); mentions == tc.noLogin {
				t.Errorf("login flow mentioned=%v for --no-login=%v: %q", mentions, tc.noLogin, stderr)
			}
			for _, secret := range tc.leaks {
				if strings.Contains(out+stderr, secret) {
					t.Fatalf("output leaked %q: %q", secret, out+stderr)
				}
			}
		})
	}
}

// A snapshot that records no identity cannot disagree, so the capture proceeds
// (the recapture guards' semantics).
func TestAddIntoAnIdentitylessSnapshotIsAllowed(t *testing.T) {
	app := testApp(t, nil)
	seedClaudeOAuth(t, app, `{"accessToken":"`+mainToken+`","refreshToken":"r","expiresAt":1577836800000}`)
	mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "main"), "capture main without identity")
	withInteractive(t, claudeLoginAs(t, app, reloggedToken, "other-uuid", &[]string{}))
	code, stderr := captureStderr(t, func() int {
		return runLogin(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main", false)
	})
	mustExit(t, constants.ExitOK, code, stderr)
	if got := snapshotPayload(t, app, testBackend(t, app), constants.ToolClaude, "main"); !strings.Contains(got, reloggedToken) {
		t.Fatalf("the login must be captured: %s", got)
	}
}

// A recorded identity that is not a record (`"oauthAccount": null`, the shape kae
// captures from a logged-out cache) names no account, so a live record is not
// refused against it: doctor's identity_record_invalid remedy repairs it, and the
// re-logins work. The reverse — a recorded record, a live non-record — is refused.
func TestAddOverARecordlessIdentity(t *testing.T) {
	const nullIdentity = `{"oauthAccount":null}`
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	for _, tc := range []struct {
		name     string
		recorded string // ~/.claude.json when main is captured
		run      func(t *testing.T, app *App) int
		code     int
	}{
		{
			name: "the doctor remedy, kae add --no-login", recorded: nullIdentity, code: constants.ExitOK,
			run: func(t *testing.T, app *App) int {
				// doctor names that exact command for this snapshot.
				var remedy string
				for _, c := range buildDoctor(ctx, app, constants.ToolClaude, doctorOptIns{}).Checks {
					if c.Code == constants.CheckIdentityRecordInvalid {
						remedy = c.Message.Error()
					}
				}
				if !strings.Contains(remedy, "kae add --no-login claude main") {
					t.Fatalf("doctor must name the --no-login remedy: %q", remedy)
				}
				writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
					claudeOAuthPayload(reloggedToken, app.Now().Add(8*time.Hour)))
				writeFile(t, filepath.Join(app.Env.Home, ".claude.json"),
					`{"oauthAccount":`+claudeOAuthAccount("main-uuid", "main-uuid@example.com")+`}`)
				code, _ := captureStderr(t, func() int { return runCapture(ctx, app, opts, constants.ToolClaude, "main") })
				return code
			},
		},
		{
			name: "kae add login flow", recorded: nullIdentity, code: constants.ExitOK,
			run: func(t *testing.T, app *App) int {
				withInteractive(t, claudeLoginAs(t, app, reloggedToken, "main-uuid", &[]string{}))
				code, _ := captureStderr(t, func() int { return runLogin(ctx, app, opts, constants.ToolClaude, "main", false) })
				return code
			},
		},
		{
			name: "global kae relogin", recorded: nullIdentity, code: constants.ExitOK,
			run: func(t *testing.T, app *App) int {
				withInteractive(t, claudeLoginAs(t, app, reloggedToken, "main-uuid", &[]string{}))
				code, _ := captureStderr(t, func() int { return runRelogin(ctx, app, opts, constants.ToolClaude) })
				return code
			},
		},
		{
			name: "a recorded record against a live non-record", code: constants.ExitUnsafeRefused,
			recorded: `{"oauthAccount":` + claudeOAuthAccount("main-uuid", "main-uuid@example.com") + `}`,
			run: func(t *testing.T, app *App) int {
				writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
					claudeOAuthPayload(reloggedToken, app.Now().Add(8*time.Hour)))
				writeFile(t, filepath.Join(app.Env.Home, ".claude.json"), nullIdentity)
				code, _ := captureStderr(t, func() int { return runCapture(ctx, app, opts, constants.ToolClaude, "main") })
				return code
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t, nil)
			chdirTemp(t)
			writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
				claudeOAuthPayload(mainToken, app.Now().Add(time.Hour)))
			writeFile(t, filepath.Join(app.Env.Home, ".claude.json"), tc.recorded)
			mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "main"), "capture main")
			mustExit(t, tc.code, tc.run(t, app), tc.name)
			captured := strings.Contains(snapshotPayload(t, app, testBackend(t, app), constants.ToolClaude, "main"), reloggedToken)
			if captured != (tc.code == constants.ExitOK) {
				t.Fatalf("captured=%v for exit %d", captured, tc.code)
			}
		})
	}
}

// With no live credential (a logout leaves only claude's identity cache) there is
// nothing to refuse against the snapshot: the capture is auth_missing, as for a new
// name.
func TestAddWithNoLiveCredentialIsAuthMissing(t *testing.T) {
	app := testApp(t, nil)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"), `{}`)
	code, stderr := captureStderr(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitAuthMissing, code, stderr)
}

func TestGlobalLoginRemedyFollowsActiveness(t *testing.T) {
	for _, tc := range []struct {
		active bool
		en, ja string
		notEN  string
	}{
		{true, "run: kae relogin claude", "kae relogin claude を実行", "--restore"},
		{false, "run: kae add --restore claude main", "kae add --restore claude main を実行", "kae relogin"},
	} {
		m := globalLoginRemedy(constants.ToolClaude, "main", tc.active)
		if !strings.Contains(m.Error(), tc.en) || strings.Contains(m.Error(), tc.notEN) {
			t.Errorf("active=%v: %q", tc.active, m.Error())
		}
		func() {
			l10ntest.UseJapanese(t)
			if got := l10n.Render(m); !strings.Contains(got, tc.ja) {
				t.Errorf("active=%v ja: %q", tc.active, got)
			}
		}()
	}
}

// A live copy worse than the existing snapshot is not captured over it: nothing
// backs up a snapshot, so the overwrite could not be undone. Each path is a row.
func TestAddRefusesALiveCopyWorseThanTheSnapshot(t *testing.T) {
	const tombstone = `{"accessToken":"","refreshToken":"","expiresAt":0}`
	for _, tc := range []struct {
		name    string
		noLogin bool
	}{
		{name: "--no-login", noLogin: true},
		{name: "login flow leaving a tombstone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t, nil)
			captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
			be := testBackend(t, app)
			snapBefore := snapshotPayload(t, app, be, constants.ToolClaude, "main")
			var code int
			var stderr string
			if tc.noLogin {
				seedClaudeOAuth(t, app, tombstone)
				code, stderr = captureStderr(t, func() int {
					return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
				})
			} else {
				withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
					seedClaudeOAuth(t, app, tombstone)
					return 0, nil
				})
				code, stderr = captureStderr(t, func() int {
					return runLogin(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main", false)
				})
				if got := claudeCreds(t, app); !strings.Contains(got, mainToken) {
					t.Fatalf("the pre-login login must be restored: %s", got)
				}
			}
			mustExit(t, constants.ExitUnsafeRefused, code, stderr)
			if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); got != snapBefore {
				t.Fatalf("the usable snapshot must be unchanged: %s", got)
			}
			if !strings.Contains(stderr, "needs a re-login while snapshot claude/main still holds a usable one") ||
				!strings.Contains(stderr, "kae relogin claude") {
				t.Errorf("the refusal must say why and name the re-login: %q", stderr)
			}
			if strings.Contains(stderr, mainToken) {
				t.Fatal("a credential reached the output")
			}
		})
	}
}

// A newer live copy of the same account is still captured.
func TestAddCapturesANewerLiveCopy(t *testing.T) {
	app := testApp(t, nil)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
		claudeOAuthPayload(reloggedToken, app.Now().Add(8*time.Hour)))
	mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "main"), "capture newer main")
	if got := snapshotPayload(t, app, testBackend(t, app), constants.ToolClaude, "main"); !strings.Contains(got, reloggedToken) {
		t.Fatalf("the newer copy must be captured: %s", got)
	}
}

// An auto-detected name comes from the email's local part. A second login landing
// on an existing name is refused when it is another account (two accounts with
// one local part); `kae add --restore <tool>` landing on the active account keeps
// the new login live, since the previous login is that account's own older copy.
func TestAddAutoNamedSecondLogin(t *testing.T) {
	for _, tc := range []struct {
		name    string
		uuid    string
		restore bool
		code    int
	}{
		{name: "another account under the same name", uuid: "second-uuid", code: constants.ExitUnsafeRefused},
		{name: "the active account with --restore", uuid: "first-uuid", restore: true, code: constants.ExitOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatText}
			login := func(uuid, token string) func(context.Context, []string, string, ...string) (int, error) {
				return func(context.Context, []string, string, ...string) (int, error) {
					writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
						claudeOAuthPayload(token, app.Now().Add(8*time.Hour)))
					writeFile(t, filepath.Join(app.Env.Home, ".claude.json"),
						`{"oauthAccount":`+claudeOAuthAccount(uuid, "you@example.com")+`}`)
					return 0, nil
				}
			}
			withInteractive(t, login("first-uuid", mainToken))
			code, out, stderr := captureBoth(t, func() int { return runLogin(ctx, app, opts, constants.ToolClaude, "", false) })
			mustExit(t, constants.ExitOK, code, stderr)
			if !strings.Contains(out, "claude/you") {
				t.Fatalf("the fixture must auto-name the account you: %q", out)
			}
			snapBefore := snapshotPayload(t, app, testBackend(t, app), constants.ToolClaude, "you")
			seedClaudeOAuth(t, app, `{"accessToken":"","refreshToken":"","expiresAt":0}`)

			withInteractive(t, login(tc.uuid, reloggedToken))
			code, out, stderr = captureBoth(t, func() int { return runLogin(ctx, app, opts, constants.ToolClaude, "", tc.restore) })
			mustExit(t, tc.code, code, stderr)
			snapshot := snapshotPayload(t, app, testBackend(t, app), constants.ToolClaude, "you")
			if tc.code != constants.ExitOK {
				if snapshot != snapBefore {
					t.Fatalf("the first account's snapshot must be unchanged: %s", snapshot)
				}
				if strings.Contains(claudeCreds(t, app), reloggedToken) {
					t.Fatal("the live login must be restored to the pre-login one")
				}
				return
			}
			if !strings.Contains(claudeCreds(t, app), reloggedToken) || !strings.Contains(snapshot, reloggedToken) {
				t.Fatalf("the new login must stay live and be captured: %s", claudeCreds(t, app))
			}
			if !strings.Contains(out, "new login stays live") || activeClaude(t, app) != "you" {
				t.Fatalf("must say so and keep the account active: %q", out)
			}
		})
	}
}

// Inside a bound directory (or an isolated home) the login flow would write
// into the store kae's own variable names, so kae add refuses before anything,
// also after the global-scope masking `kae add` applies. --no-login reads the
// masked, real store and is not refused.
func TestAddLoginRefusesKaeIsolationInTheShell(t *testing.T) {
	env := map[string]string{}
	app := testApp(t, env)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	env["CLAUDE_CONFIG_DIR"] = app.Paths.SharedDir("abcdef0123456789", constants.ToolClaude)
	app.applyGlobalScope()
	backups := backupCount(t, app)
	ran := false
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		ran = true
		return 0, nil
	})
	code, stderr := captureStderr(t, func() int {
		return runLogin(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main", false)
	})
	mustExit(t, constants.ExitUsage, code, stderr)
	if ran || backupCount(t, app) != backups {
		t.Fatalf("nothing may run or be backed up: ran=%v", ran)
	}
	for _, want := range []string{"CLAUDE_CONFIG_DIR", "kae relogin claude"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal must name %q: %q", want, stderr)
		}
	}
	mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "main"), "--no-login reads the real store")
}
