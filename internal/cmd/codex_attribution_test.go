package cmd

// codex's counterparts of TestSwitchAwaySkipsRecaptureAfterOutsideLogin and
// TestRunSharedRefusesToRecaptureAForeignLogin: codex's identity is inside the
// credential, so both recaptures ask the adapter's owner comparison
// (liveOwnerDiffers) whether the live login is the snapshot's account
// (docs/ADAPTERS.md § Recapture attribution).

import (
	"context"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// outsideRefresh marks the live login a case writes, so the tests can tell
// whether it reached the snapshot or the backup.
const outsideRefresh = "rt-OUTSIDE"

// The owner values the cases add to credential_conflict_test.go's fixtures; not
// real accounts. Like those, none may reach output.
const (
	strangerUser  = "user-stranger"
	teamWorkspace = "acct-side-team"
	otherEmail    = "other@example.com"
	renamedEmail  = "renamed@example.com"
	apiKeyFixture = "sk-" + outsideRefresh
)

// assertNoAttributionPII extends assertNoConflictPII to the values above and the
// live login's refresh token.
func assertNoAttributionPII(t *testing.T, out string) {
	t.Helper()
	assertNoConflictPII(t, out)
	for _, pii := range []string{strangerUser, teamWorkspace, otherEmail, renamedEmail, apiKeyFixture, outsideRefresh} {
		if strings.Contains(out, pii) {
			t.Errorf("output contains %q: %q", pii, out)
		}
	}
}

// chatgptLoginOf renders a consistent ChatGPT login naming workspace, user and
// email in both tokens' claims, under account id workspace.
func chatgptLoginOf(workspace, user, email, refresh string) string {
	token := ownerToken(workspace, user, email)
	return codexChatGPTLogin("chatgpt", token, token, refresh, workspace)
}

type codexAttributionCase struct {
	// side is side's captured login; "" captures the usual consistent one.
	side    string
	live    string
	decline bool
}

// codexAttributionCases is the fixture table the brief's acceptance names. side
// is the active account throughout, whose workspace is conflictAcctSide and user
// conflictUser(conflictAcctSide).
func codexAttributionCases() map[string]codexAttributionCase {
	sideUser := conflictUser(conflictAcctSide)
	return map[string]codexAttributionCase{
		"different user, same workspace": {
			live:    chatgptLoginOf(conflictAcctSide, strangerUser, conflictEmail, outsideRefresh),
			decline: true,
		},
		"same user, different workspace": {
			live:    chatgptLoginOf(teamWorkspace, sideUser, conflictEmail, outsideRefresh),
			decline: true,
		},
		"same user and workspace, email changed": {
			live: chatgptLoginOf(conflictAcctSide, sideUser, renamedEmail, outsideRefresh),
		},
		"token rotation only": {
			live: chatgptLoginOf(conflictAcctSide, sideUser, conflictEmail, outsideRefresh),
		},
		"api key login": {
			live: `{"auth_mode":"apikey","OPENAI_API_KEY":"` + apiKeyFixture + `"}`,
		},
		"user id missing live, emails differ": {
			live:    chatgptLoginOf(conflictAcctSide, "", otherEmail, outsideRefresh),
			decline: true,
		},
		"user ids and emails missing on both sides": {
			side: chatgptLoginOf(conflictAcctSide, "", "", "rt-side"),
			live: chatgptLoginOf(conflictAcctSide, "", "", outsideRefresh),
		},
	}
}

// seedCodexAttribution captures main and then side (so side is active), side
// as tc.side when the case sets one.
func seedCodexAttribution(t *testing.T, app *App, tc codexAttributionCase) {
	t.Helper()
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	side := tc.side
	if side == "" {
		side = consistentLogin(conflictAcctSide, "rt-side")
	}
	for _, acc := range []struct{ name, login string }{
		{"main", consistentLogin(conflictAcctMain, "rt-main")}, {"side", side},
	} {
		writeFile(t, codexAuthFile(app), acc.login)
		code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", acc.name) })
		mustExit(t, constants.ExitOK, code, out)
	}
}

// checkCodexAttribution asserts one case's outcome on codex/side: declined with
// the outside-login reason and the live login kept in the named backup, or
// recaptured.
func checkCodexAttribution(t *testing.T, app *App, tc codexAttributionCase, stderr, remedyScope string) {
	t.Helper()
	const reason = "the live codex identity is not the one kae applied for codex/side; " +
		"codex was probably logged in again outside kae; snapshot codex/side left unchanged"
	snapshot := snapshotPayload(t, app, testBackend(t, app), "codex", "side")
	if !tc.decline {
		if strings.Contains(stderr, "outside kae") {
			t.Errorf("the recapture must proceed: %q", stderr)
		}
		if !strings.Contains(snapshot, outsideRefresh) {
			t.Errorf("snapshot codex/side was not refreshed from the live login: %s", snapshot)
		}
		return
	}
	if !strings.Contains(stderr, reason) {
		t.Errorf("expected the outside-login refusal naming codex/side: %q", stderr)
	}
	id := backupIDFromWarning(t, stderr)
	if !strings.Contains(stderr, "preserved only in backup "+id+" ("+remedyScope+") — "+
		"to keep it as its own account, run: kae rollback --to "+id+", then kae add --no-login codex <account>\n") {
		t.Errorf("the remedy must name the backup and the two-step, in full: %q", stderr)
	}
	if !backupHolds(t, app, id, "codex", outsideRefresh) {
		t.Errorf("backup %s does not hold the declined login", id)
	}
	if !strings.Contains(snapshot, "rt-side") || strings.Contains(snapshot, outsideRefresh) {
		t.Errorf("an outside login was filed under codex/side: %s", snapshot)
	}
}

func TestSwitchAwaySkipsCodexRecaptureAfterOutsideLogin(t *testing.T) {
	for name, tc := range codexAttributionCases() {
		t.Run(name, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatText}
			seedCodexAttribution(t, app, tc)
			writeFile(t, codexAuthFile(app), tc.live)

			code, out, stderr := captureBoth(t, func() int { return runSwitch(ctx, app, opts, "codex", "main") })
			mustExit(t, constants.ExitOK, code, out)
			assertNoAttributionPII(t, stderr+out)
			checkCodexAttribution(t, app, tc, stderr, "restoring it reverts this whole switch")
		})
	}
}

func TestRunSharedRefusesToRecaptureAForeignCodexLogin(t *testing.T) {
	for name, tc := range codexAttributionCases() {
		t.Run(name, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatText}
			seedCodexAttribution(t, app, tc)

			withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
				writeFile(t, codexAuthFile(app), tc.live)
				return 0, nil
			})
			code, out, stderr := captureBoth(t, func() int {
				return runRun(ctx, app, opts, runModeShared, "codex", "side", []string{"codex"})
			})
			mustExit(t, constants.ExitOK, code, out)
			assertNoAttributionPII(t, stderr+out)
			checkCodexAttribution(t, app, tc, stderr,
				"that backup covers only the tools whose recapture kae declined")
		})
	}
}

// A snapshot payload kae cannot read is no evidence: the owner comparison has
// nothing to compare, so another user's login is recaptured, on both paths
// (docs/ADAPTERS.md § Recapture attribution). The payload goes missing just before
// the recapture: `kae run -s` must still apply it to the child first.
func TestCodexRecaptureProceedsWhenTheSnapshotPayloadIsMissing(t *testing.T) {
	foreign := codexAttributionCase{live: chatgptLoginOf(conflictAcctSide, strangerUser, conflictEmail, outsideRefresh)}
	opts := commonOpts{Format: formatText}
	for name, recapture := range map[string]func(ctx context.Context, app *App, dropSnapshot func()) (int, string, string){
		"kae use": func(ctx context.Context, app *App, dropSnapshot func()) (int, string, string) {
			writeFile(t, codexAuthFile(app), foreign.live)
			dropSnapshot()
			return captureBoth(t, func() int { return runSwitch(ctx, app, opts, "codex", "main") })
		},
		"kae run -s": func(ctx context.Context, app *App, dropSnapshot func()) (int, string, string) {
			withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
				writeFile(t, codexAuthFile(app), foreign.live)
				dropSnapshot()
				return 0, nil
			})
			return captureBoth(t, func() int {
				return runRun(ctx, app, opts, runModeShared, "codex", "side", []string{"codex"})
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			seedCodexAttribution(t, app, foreign)
			acc, found, err := account.Load(app.Paths.AccountDir("codex", "side"))
			if err != nil || !found {
				t.Fatalf("load codex/side: found=%v err=%v", found, err)
			}
			ref := acc.Artifacts[credentialArtifactName(constants.ToolCodex)].SecretRef
			dropSnapshot := func() {
				if err := testBackend(t, app).Delete(ctx, ref); err != nil {
					t.Error(err)
				}
			}

			code, out, stderr := recapture(ctx, app, dropSnapshot)
			mustExit(t, constants.ExitOK, code, out)
			assertNoAttributionPII(t, stderr+out)
			checkCodexAttribution(t, app, foreign, stderr, "")
		})
	}
}
