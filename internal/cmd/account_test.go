package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/state"
)

// captureClaude seeds and captures a claude account, leaving it active.
func captureClaude(t *testing.T, app *App, accountName, token string) {
	t.Helper()
	seedClaude(t, app, token, accountName+"-uuid")
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, "claude", accountName)
	})
	mustExit(t, constants.ExitOK, code, out)
}

// writeConfigFile writes config.toml to the app's config path and reloads it
// into app.Config so the in-memory and on-disk views agree.
func writeConfigFile(t *testing.T, app *App, content string) {
	t.Helper()
	writeFile(t, app.ConfigPath, content)
	cfg, _, err := config.Load(app.ConfigPath)
	if err != nil {
		t.Fatalf("load config fixture: %v", err)
	}
	// Keep the isolated file secret backend testApp set up; the fixture content
	// focuses on profiles, not the security section.
	cfg.Security.SecretBackend = secret.BackendFile
	app.Config = cfg
}

func TestAccountRmRemovesSnapshotAndSecrets(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken) // side now active
	// Switch active to main so side is removable without --force.
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, commonOpts{Format: formatText}, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	acc, found, _ := account.Load(app.Paths.AccountDir("claude", "side"))
	if !found {
		t.Fatal("side not captured")
	}
	be, _ := app.secretBackend()
	ref := acc.Artifacts[acc.ArtifactNames()[0]].SecretRef

	report, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "side", false)
	if err != nil {
		t.Fatalf("rm: %v", err)
	}
	if report.SecretsRemoved == 0 {
		t.Fatalf("expected secrets removed: %+v", report)
	}
	if _, err := os.Stat(app.Paths.AccountDir("claude", "side")); !os.IsNotExist(err) {
		t.Fatalf("snapshot dir not removed: %v", err)
	}
	if _, ok, _ := be.Get(ctx, ref); ok {
		t.Fatal("secret item not deleted")
	}
}

func TestAccountRmRefusesActiveWithoutForce(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken) // main active

	if _, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", false); exitOf(err) != constants.ExitUnsafeRefused {
		t.Fatalf("expected exit %d, got %v", constants.ExitUnsafeRefused, err)
	}

	report, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", true)
	if err != nil {
		t.Fatalf("rm --force: %v", err)
	}
	if !report.ActiveCleared {
		t.Fatal("active not cleared with --force")
	}
	st, _ := app.loadState()
	if _, ok := st.Active["claude"]; ok {
		t.Fatalf("active claude not dropped from state: %+v", st.Active)
	}
}

func TestAccountRmUnknownExitsNotFound(t *testing.T) {
	app := testApp(t, nil)
	if _, err := buildAccountRm(context.Background(), app, commonOpts{Format: formatText}, "claude", "ghost", false); exitOf(err) != constants.ExitNotFound {
		t.Fatalf("expected exit %d, got %v", constants.ExitNotFound, err)
	}
}

func TestAccountRmDropsProfileReference(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, commonOpts{Format: formatText}, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	writeConfigFile(t, app, "version = 1\n[profiles.alt.accounts]\nclaude = \"side\"\ncodex = \"main\"\n")

	report, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "side", false)
	if err != nil {
		t.Fatalf("rm: %v", err)
	}
	if len(report.ProfilesUpdated) != 1 || report.ProfilesUpdated[0] != "alt" {
		t.Fatalf("profile not named in report: %+v", report.ProfilesUpdated)
	}
	cfg, _, _ := config.Load(app.ConfigPath)
	if _, ok := cfg.Profiles["alt"].Accounts["claude"]; ok {
		t.Fatalf("profile claude reference not dropped: %+v", cfg.Profiles["alt"])
	}
	if cfg.Profiles["alt"].Accounts["codex"] != "main" {
		t.Fatalf("sibling profile key lost: %+v", cfg.Profiles["alt"])
	}
}

func TestAccountRmDryRunWritesNothing(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, commonOpts{Format: formatText}, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)

	if _, err := buildAccountRm(ctx, app, commonOpts{DryRun: true, Format: formatText}, "claude", "side", false); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if _, err := os.Stat(app.Paths.AccountDir("claude", "side")); err != nil {
		t.Fatalf("dry-run removed the snapshot dir: %v", err)
	}
}

func TestAccountRenameRoundTripAndResolves(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken) // main active

	report, err := buildAccountRename(ctx, app, commonOpts{Format: formatText}, "claude", "main", "main2")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if !report.ActiveUpdated || report.SecretsMoved == 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err := os.Stat(app.Paths.AccountDir("claude", "main")); !os.IsNotExist(err) {
		t.Fatalf("old snapshot dir not removed: %v", err)
	}
	acc, found, _ := account.Load(app.Paths.AccountDir("claude", "main2"))
	if !found || acc.Name != "main2" {
		t.Fatalf("renamed snapshot missing/wrong: %+v", acc)
	}
	st, _ := app.loadState()
	if st.Active["claude"] != "main2" {
		t.Fatalf("active not updated: %+v", st.Active)
	}
	// The renamed account must resolve through kae use.
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, commonOpts{Format: formatText}, "claude", "main2") })
	mustExit(t, constants.ExitOK, code, out)
}

func TestAccountRenameRefusesExistingTarget(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	captureClaude(t, app, "side", sideToken)
	if _, err := buildAccountRename(ctx, app, commonOpts{Format: formatText}, "claude", "main", "side"); exitOf(err) != constants.ExitUnsafeRefused {
		t.Fatalf("expected exit %d, got %v", constants.ExitUnsafeRefused, err)
	}
}

func TestAccountRenameRechecksDestinationAfterTakingLocks(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	app.beforeAccountRenameLocksForTest = func() {
		captureClaude(t, app, "side", sideToken)
	}

	_, err := buildAccountRename(ctx, app, commonOpts{Format: formatText}, "claude", "main", "side")
	if exitOf(err) != constants.ExitUnsafeRefused || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("concurrent destination capture was not refused: exit=%d err=%v", exitOf(err), err)
	}
	if acc, found, loadErr := account.Load(app.Paths.AccountDir("claude", "side")); loadErr != nil || !found || acc.Name != "side" {
		t.Fatalf("concurrently captured destination was changed: found=%v account=%+v err=%v", found, acc, loadErr)
	}
}

func TestAccountRenameRechecksSourceAfterTakingLocks(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	app.beforeAccountRenameLocksForTest = func() {
		if err := os.RemoveAll(app.Paths.AccountDir("claude", "main")); err != nil {
			t.Fatal(err)
		}
	}

	_, err := buildAccountRename(ctx, app, commonOpts{Format: formatText}, "claude", "main", "side")
	if exitOf(err) != constants.ExitNotFound {
		t.Fatalf("concurrently removed source was not rechecked: exit=%d err=%v", exitOf(err), err)
	}
	if _, statErr := os.Stat(app.Paths.AccountDir("claude", "side")); !os.IsNotExist(statErr) {
		t.Fatalf("rename wrote a destination after source disappeared: %v", statErr)
	}
}

func TestAccountRenameUnknownOldExitsNotFound(t *testing.T) {
	app := testApp(t, nil)
	if _, err := buildAccountRename(context.Background(), app, commonOpts{Format: formatText}, "claude", "ghost", "new"); exitOf(err) != constants.ExitNotFound {
		t.Fatalf("expected exit %d, got %v", constants.ExitNotFound, err)
	}
}

func TestAccountRenameRefusesTheGloballyIsolatedAccountWithoutWriting(t *testing.T) {
	for _, tool := range []string{constants.ToolClaude, constants.ToolCodex} {
		t.Run(tool, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatText}
			if tool == constants.ToolClaude {
				captureClaude(t, app, "main", mainToken)
			} else {
				seedCodex(t, app, "codex-main-token")
				code, out := captureStdout(t, func() int {
					return runCapture(ctx, app, opts, tool, "main")
				})
				mustExit(t, constants.ExitOK, code, out)
			}
			code, out := captureStdout(t, func() int {
				return runUseIsolated(ctx, app, opts, tool, "main")
			})
			mustExit(t, constants.ExitOK, code, out)

			metaPath := app.Paths.AccountDir(tool, "main") + "/account.toml"
			before := readFile(t, metaPath)
			for _, dryRun := range []bool{false, true} {
				_, err := buildAccountRename(ctx, app,
					commonOpts{Format: formatText, DryRun: dryRun}, tool, "main", "side")
				if exitOf(err) != constants.ExitUnsafeRefused {
					t.Fatalf("dry_run=%v: exit=%d err=%v", dryRun, exitOf(err), err)
				}
				message := err.Error()
				for _, want := range []string{
					"stop every process", "kae run -i", "kae use -i",
					"kae use -s " + tool + " main", "kae account rename " + tool + " main side",
				} {
					if !strings.Contains(message, want) {
						t.Fatalf("dry_run=%v: refusal does not contain %q: %s", dryRun, want, message)
					}
				}
				if got := readFile(t, metaPath); got != before {
					t.Fatalf("dry_run=%v: refusal rewrote the old snapshot", dryRun)
				}
				if _, err := os.Stat(app.Paths.AccountDir(tool, "side")); !os.IsNotExist(err) {
					t.Fatalf("dry_run=%v: refusal created the new snapshot: %v", dryRun, err)
				}
				st, err := app.loadState()
				if err != nil || st.Synced[tool] != "main" {
					t.Fatalf("dry_run=%v: refusal changed synced: state=%+v err=%v", dryRun, st, err)
				}
			}
		})
	}
}

func TestAccountRenameGlobalIsolationRefusalUsesTheJSONErrorContract(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	code, out := captureStdout(t, func() int {
		return runUseIsolated(ctx, app, commonOpts{Format: formatText}, "claude", "main")
	})
	mustExit(t, constants.ExitOK, code, out)
	_, renameErr := buildAccountRename(ctx, app,
		commonOpts{Format: formatJSON, DryRun: true}, "claude", "main", "side")
	code, out = captureStdout(t, func() int {
		return finish(commonOpts{Format: formatJSON}, renameErr)
	})
	mustExit(t, constants.ExitUnsafeRefused, code, out)
	var report errorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode JSON error: %v\n%s", err, out)
	}
	if report.OK || report.ErrorCode != constants.CodeUnsafeRefused ||
		!strings.Contains(report.Message, "kae use -s claude main") {
		t.Fatalf("unexpected JSON refusal: %+v", report)
	}
}

func TestAccountRenameRefusesCrashMismatchUntilUseSharedReconcilesIt(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	captureClaude(t, app, "main", mainToken)
	code, out := captureStdout(t, func() int {
		return runUseIsolated(ctx, app, opts, "claude", "main")
	})
	mustExit(t, constants.ExitOK, code, out)
	fragmentBefore := readFile(t, app.Paths.MiseGlobalFragmentFile())

	// Model a process death after state was saved but before the atomic fragment
	// replacement: state no longer selects the account, while the old fragment
	// still exports its paths.
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	delete(st.Synced, constants.ToolClaude)
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, app.Paths.MiseGlobalFragmentFile()); got != fragmentBefore {
		t.Fatal("crash fixture did not retain the old fragment")
	}

	for _, dryRun := range []bool{true, false} {
		_, renameErr := buildAccountRename(ctx, app,
			commonOpts{Format: formatText, DryRun: dryRun}, "claude", "main", "side")
		if exitOf(renameErr) != constants.ExitUnsafeRefused ||
			!strings.Contains(renameErr.Error(), "kae use -s claude main") {
			t.Fatalf("dry_run=%v: mismatched fragment was not refused with recovery guidance: exit=%d err=%v",
				dryRun, exitOf(renameErr), renameErr)
		}
	}
	if _, err := os.Stat(app.Paths.AccountDir("claude", "side")); !os.IsNotExist(err) {
		t.Fatalf("refused rename wrote its destination: %v", err)
	}

	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	if _, err := os.Stat(app.Paths.MiseGlobalFragmentFile()); !os.IsNotExist(err) {
		t.Fatalf("use -s did not reconcile the stale fragment from empty synced state: %v", err)
	}
	if _, err := buildAccountRename(ctx, app, opts, "claude", "main", "side"); err != nil {
		t.Fatalf("rename remained blocked after use -s reconciliation: %v", err)
	}
}

func TestAccountRenameRefusesUnreadableGlobalFragment(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	st.Synced = map[string]string{"claude": "main"}
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	path := app.Paths.MiseGlobalFragmentFile()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err = buildAccountRename(ctx, app,
		commonOpts{Format: formatText, DryRun: true}, "claude", "main", "side")
	if exitOf(err) != constants.ExitUnsafeRefused || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("unreadable fragment was not refused: exit=%d err=%v", exitOf(err), err)
	}
}

// setFailBackend wraps a real backend and refuses every Set of a key naming one
// account, which is how a test stops `kae account rename` inside its copy stage
// — the failure a secret backend can genuinely produce there.
type setFailBackend struct {
	secret.Backend
	failFor string
}

type deleteFailBackend struct {
	secret.Backend
	failFor string
}

func (b deleteFailBackend) Delete(ctx context.Context, key string) error {
	if strings.Contains(key, b.failFor) {
		return fmt.Errorf("backend refused %s", key)
	}
	return b.Backend.Delete(ctx, key)
}

func (b setFailBackend) Set(ctx context.Context, key string, value []byte) error {
	if strings.Contains(key, b.failFor) {
		return fmt.Errorf("backend refused %s", key)
	}
	return b.Backend.Set(ctx, key, value)
}

// A rename that dies while copying payloads must leave the old account whole and
// still active, because the new snapshot is not complete and nothing may point at
// it. buildAccountRename's three-stage order is what guarantees that: kae flipped
// state.Active *first* through v0.15.3, which left state naming a snapshot dir
// that did not exist yet.
func TestAccountRenameCopyFailureLeavesOldAccountActive(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken) // main active
	app.backendForTest = setFailBackend{Backend: testBackend(t, app), failFor: "main2"}

	if _, err := buildAccountRename(ctx, app, commonOpts{Format: formatText}, "claude", "main", "main2"); err == nil {
		t.Fatal("rename reported success even though the secret copy failed")
	}
	st, _ := app.loadState()
	if st.Active["claude"] != "main" {
		t.Fatalf("active moved off the only complete snapshot: %+v", st.Active)
	}
	if _, found, err := account.Load(app.Paths.AccountDir("claude", "main")); err != nil || !found {
		t.Fatalf("old snapshot dir did not survive: found=%v err=%v", found, err)
	}
	// Intact means usable, not merely present: the payload the surviving metadata
	// declares must still be in the backend, or the account it is active for
	// cannot be switched to.
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, commonOpts{Format: formatText}, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
}

func TestAccountRenameCleanupFailureLeavesRecoverableOldAccount(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	realBackend := testBackend(t, app)
	app.backendForTest = deleteFailBackend{Backend: realBackend, failFor: "/main/"}

	if _, err := buildAccountRename(ctx, app, commonOpts{Format: formatText}, "claude", "main", "side"); err == nil {
		t.Fatal("rename reported success even though old-secret cleanup failed")
	}
	st, err := app.loadState()
	if err != nil || st.Active["claude"] != "side" {
		t.Fatalf("stage 2 pointer did not remain on the complete new account: state=%+v err=%v", st, err)
	}
	for _, name := range []string{"main", "side"} {
		if _, found, loadErr := account.Load(app.Paths.AccountDir("claude", name)); loadErr != nil || !found {
			t.Fatalf("%s snapshot absent after cleanup failure: found=%v err=%v", name, found, loadErr)
		}
	}
	if _, retryErr := buildAccountRename(ctx, app, commonOpts{Format: formatText}, "claude", "main", "side"); exitOf(retryErr) != constants.ExitUnsafeRefused {
		t.Fatalf("rerun should refuse the existing destination: exit=%d err=%v", exitOf(retryErr), retryErr)
	}

	app.backendForTest = realBackend
	if _, err := buildAccountRm(ctx, app, commonOpts{Format: formatText}, "claude", "main", false); err != nil {
		t.Fatalf("documented cleanup of the old remnant failed: %v", err)
	}
}

func TestAccountRenameRewritesProfileReference(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken)
	writeConfigFile(t, app, "version = 1\n[profiles.alt.accounts]\nclaude = \"main\"\n")

	report, err := buildAccountRename(ctx, app, commonOpts{Format: formatText}, "claude", "main", "main2")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if len(report.ProfilesUpdated) != 1 || report.ProfilesUpdated[0] != "alt" {
		t.Fatalf("profile not named: %+v", report.ProfilesUpdated)
	}
	cfg, _, _ := config.Load(app.ConfigPath)
	if cfg.Profiles["alt"].Accounts["claude"] != "main2" {
		t.Fatalf("profile reference not rewritten: %+v", cfg.Profiles["alt"])
	}
}

func TestAccountRenameRecomputesProfileReferencesUnderConfigLock(t *testing.T) {
	for _, tc := range []struct {
		name       string
		initial    string
		concurrent string
		want       string
		wantReport bool
	}{
		{
			name:       "new reference is included",
			initial:    "version = 1\n[security]\nsecret_backend = \"file\"\n",
			concurrent: "version = 1\n[security]\nsecret_backend = \"file\"\n[profiles.alt.accounts]\nclaude = \"main\"\n",
			want:       "side",
			wantReport: true,
		},
		{
			name:       "removed reference is not recreated",
			initial:    "version = 1\n[security]\nsecret_backend = \"file\"\n[profiles.alt.accounts]\nclaude = \"main\"\n",
			concurrent: "version = 1\n[security]\nsecret_backend = \"file\"\n[profiles.alt.accounts]\nclaude = \"alt\"\n",
			want:       "alt",
			wantReport: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testApp(t, nil)
			ctx := context.Background()
			captureClaude(t, app, "main", mainToken)
			writeConfigFile(t, app, tc.initial)
			// Write as a separate process would: leave this App's cached config stale.
			app.beforeAccountRenameLocksForTest = func() { writeFile(t, app.ConfigPath, tc.concurrent) }

			report, err := buildAccountRename(ctx, app, commonOpts{Format: formatText}, "claude", "main", "side")
			if err != nil {
				t.Fatal(err)
			}
			if got := len(report.ProfilesUpdated) == 1 && report.ProfilesUpdated[0] == "alt"; got != tc.wantReport {
				t.Fatalf("reported profiles=%v want alt=%v", report.ProfilesUpdated, tc.wantReport)
			}
			cfg, _, err := config.Load(app.ConfigPath)
			if err != nil || cfg.Profiles["alt"].Accounts["claude"] != tc.want {
				t.Fatalf("profile value=%q want %q err=%v", cfg.Profiles["alt"].Accounts["claude"], tc.want, err)
			}
		})
	}
}

func TestAccountSetIdentityRecordsValue(t *testing.T) {
	app := testApp(t, nil)
	captureClaude(t, app, "main", mainToken)
	report, err := buildAccountSetIdentity(app, commonOpts{Format: formatText}, "claude", "main", "you@example.com")
	if err != nil {
		t.Fatalf("set-identity: %v", err)
	}
	if report.Identity != "you@example.com" {
		t.Fatalf("report identity=%q, want you@example.com", report.Identity)
	}
	acc, found, _ := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main"))
	if !found || acc.Identity != "you@example.com" {
		t.Fatalf("found=%v identity=%q, want you@example.com", found, acc.Identity)
	}
}

func TestAccountSetIdentityDryRunDoesNotWrite(t *testing.T) {
	app := testApp(t, nil)
	captureClaude(t, app, "main", mainToken)
	before, _, _ := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main"))
	if _, err := buildAccountSetIdentity(app, commonOpts{Format: formatText, DryRun: true}, "claude", "main", "you@example.com"); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	acc, _, _ := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main"))
	if acc.Identity != before.Identity {
		t.Fatalf("dry-run changed identity %q -> %q", before.Identity, acc.Identity)
	}
}

func TestAccountSetIdentityUnknownExitsNotFound(t *testing.T) {
	app := testApp(t, nil)
	if _, err := buildAccountSetIdentity(app, commonOpts{Format: formatText}, "claude", "ghost", "x"); exitOf(err) != constants.ExitNotFound {
		t.Fatalf("want ExitNotFound, got %v", err)
	}
}

func TestAccountSetIdentityEmptyValueExitsUsage(t *testing.T) {
	app := testApp(t, nil)
	captureClaude(t, app, "main", mainToken)
	if _, err := buildAccountSetIdentity(app, commonOpts{Format: formatText}, "claude", "main", "  \t "); exitOf(err) != constants.ExitUsage {
		t.Fatalf("want ExitUsage, got %v", err)
	}
}
