package cmd

import (
	"context"
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
		code  int
		want  string
	}{
		{
			name:  "no active account for the named tool",
			setup: func(*testing.T, *App, map[string]string) {},
			tool:  constants.ToolClaude, code: constants.ExitNotFound,
			want: "kae add claude <account>",
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
			tool: constants.ToolClaude, code: constants.ExitUsage, want: "CLAUDE_CONFIG_DIR",
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
			code, stderr := captureStderr(t, func() int {
				return runRelogin(context.Background(), app, commonOpts{Format: formatText}, tc.tool)
			})
			mustExit(t, tc.code, code, stderr)
			if ran {
				t.Fatal("no login may be launched")
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("the refusal must name %q: %q", tc.want, stderr)
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

// R2: --restore for the active account would put its expired copy back; it is
// refused before the backup and the login, and names the active re-login.
func TestAddRestoreRefusesTheActiveAccount(t *testing.T) {
	app := expiredActiveMain(t)
	before := claudeCreds(t, app)
	backups := backupCount(t, app)
	ran := false
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		ran = true
		return 0, nil
	})
	code, stderr := captureStderr(t, func() int {
		return runLogin(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main", true)
	})
	mustExit(t, constants.ExitUsage, code, stderr)
	if ran || backupCount(t, app) != backups {
		t.Fatalf("nothing may run or be backed up: ran=%v", ran)
	}
	if claudeCreds(t, app) != before || activeClaude(t, app) != "main" {
		t.Fatal("the refusal must change nothing")
	}
	if !strings.Contains(stderr, "kae relogin claude") {
		t.Errorf("the refusal must name the active re-login: %q", stderr)
	}
}

// --restore for an account that is not active still captures it and puts the
// active login back.
func TestAddRestoreOfAnotherAccountStillRestores(t *testing.T) {
	app := testApp(t, nil)
	captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
	seedClaude(t, app, sideToken, "side-uuid")
	mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "side"), "capture side")
	mustExit(t, constants.ExitOK, runSwitchQuiet(t, app, "main"), "switch main")
	live := claudeCreds(t, app)
	withInteractive(t, claudeLoginAs(t, app, reloggedToken, "side-uuid", &[]string{}))
	code, out, stderr := captureBoth(t, func() int {
		return runLogin(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "side", true)
	})
	mustExit(t, constants.ExitOK, code, stderr)
	if !strings.Contains(out, "restored the previous login") || claudeCreds(t, app) != live {
		t.Fatalf("main's login must be put back: %s", claudeCreds(t, app))
	}
	if got := snapshotPayload(t, app, testBackend(t, app), constants.ToolClaude, "side"); !strings.Contains(got, reloggedToken) {
		t.Fatalf("side must hold the new login: %s", got)
	}
}

// R3: a login that is another account than an existing snapshot records is not
// captured under that name, and the live login goes back to the pre-login one
// with or without --restore. Each form is a row.
func TestAddRefusesAnotherAccountsLoginForAnExistingName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		restore bool
		noLogin bool
	}{
		{name: "login flow"},
		{name: "login flow with --restore", restore: true},
		{name: "--no-login", noLogin: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t, nil)
			captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
			seedClaude(t, app, sideToken, "side-uuid")
			mustExit(t, constants.ExitOK, runCaptureQuiet(t, app, "side"), "capture side")
			be := testBackend(t, app)
			snapBefore := snapshotPayload(t, app, be, constants.ToolClaude, "main")
			identityBefore := snapshotArtifact(t, app, be, constants.ToolClaude, "main", "oauth_account")
			live := claudeCreds(t, app)
			var code int
			var out, stderr string
			if tc.noLogin {
				writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"),
					claudeOAuthPayload(strangerToken, app.Now().Add(8*time.Hour)))
				writeFile(t, filepath.Join(app.Env.Home, ".claude.json"),
					`{"oauthAccount":`+claudeOAuthAccount("stranger-uuid", "stranger-uuid@example.com")+`}`)
				live = claudeCreds(t, app)
				code, out, stderr = captureBoth(t, func() int {
					return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
				})
			} else {
				withInteractive(t, claudeLoginAs(t, app, strangerToken, "stranger-uuid", &[]string{}))
				code, out, stderr = captureBoth(t, func() int {
					return runLogin(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main", tc.restore)
				})
			}
			mustExit(t, constants.ExitUnsafeRefused, code, stderr)
			if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); got != snapBefore {
				t.Fatalf("main's snapshot must be unchanged: %s", got)
			}
			if got := snapshotArtifact(t, app, be, constants.ToolClaude, "main", "oauth_account"); got != identityBefore {
				t.Fatalf("main's recorded identity must be unchanged: %s", got)
			}
			if claudeCreds(t, app) != live {
				t.Fatalf("the live login must be the one before the command: %s", claudeCreds(t, app))
			}
			if got := activeClaude(t, app); got != "side" {
				t.Fatalf("active must stay side, got %q", got)
			}
			for _, want := range []string{"kae account rename claude main", "kae account rm claude main", "kae add claude <account>"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the refusal must name %q: %q", want, stderr)
				}
			}
			for _, secret := range []string{strangerToken, mainToken, sideToken, "stranger-uuid", "main-uuid", "@example.com"} {
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

// codex carries its identity inside the credential, so the same refusal comes
// from the owner comparison.
func TestAddRefusesAnotherCodexAccountsLoginForAnExistingName(t *testing.T) {
	app := testApp(t, nil)
	seedCodexAccounts(t, app) // main and side, side active
	live := readFile(t, codexAuthFile(app))
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		writeFile(t, codexAuthFile(app), consistentLogin(conflictAcctSide, "rt-new"))
		return 0, nil
	})
	code, out, stderr := captureBoth(t, func() int {
		return runLogin(context.Background(), app, commonOpts{Format: formatText}, constants.ToolCodex, "main", false)
	})
	mustExit(t, constants.ExitUnsafeRefused, code, stderr)
	if readFile(t, codexAuthFile(app)) != live {
		t.Fatal("the live codex login must be the pre-login one")
	}
	assertNoConflictPII(t, out+stderr)
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

// An auto-detected name comes from the email's local part, so two accounts can
// land on one name; the second is refused rather than filed over the first.
func TestAddRefusesAnAutoNamedCollision(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
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
	code, out, stderr := captureBoth(t, func() int { return runLogin(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "", false) })
	mustExit(t, constants.ExitOK, code, stderr)
	if !strings.Contains(out, "claude/you") {
		t.Fatalf("the fixture must auto-name the account you: %q", out)
	}
	snapBefore := snapshotPayload(t, app, testBackend(t, app), constants.ToolClaude, "you")
	withInteractive(t, login("second-uuid", sideToken))
	code, stderr = captureStderr(t, func() int { return runLogin(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "", false) })
	mustExit(t, constants.ExitUnsafeRefused, code, stderr)
	if got := snapshotPayload(t, app, testBackend(t, app), constants.ToolClaude, "you"); got != snapBefore {
		t.Fatalf("the first account's snapshot must be unchanged: %s", got)
	}
	if !strings.Contains(claudeCreds(t, app), mainToken) {
		t.Fatal("the live login must be restored to the first account's")
	}
}

// `kae add --restore <tool>` without a name, landing on the active account: the
// previous login is that account's own older copy, so the new login stays live.
func TestAddRestoreAutoNamedActiveKeepsTheNewLogin(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	withInteractive(t, claudeLoginAs(t, app, mainToken, "you", &[]string{}))
	code, stderr := captureStderr(t, func() int { return runLogin(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "", false) })
	mustExit(t, constants.ExitOK, code, stderr)
	seedClaudeOAuth(t, app, `{"accessToken":"","refreshToken":"","expiresAt":0}`)
	withInteractive(t, claudeLoginAs(t, app, reloggedToken, "you", &[]string{}))
	code, out, stderr := captureBoth(t, func() int { return runLogin(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "", true) })
	mustExit(t, constants.ExitOK, code, stderr)
	if !strings.Contains(claudeCreds(t, app), reloggedToken) {
		t.Fatalf("the new login must stay live: %s", claudeCreds(t, app))
	}
	if !strings.Contains(out, "new login stays live") || activeClaude(t, app) != "you" {
		t.Fatalf("must say so and keep the account active: %q", out)
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
