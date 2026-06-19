package cmd

import (
	"context"
	"os"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
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

func TestAccountRenameUnknownOldExitsNotFound(t *testing.T) {
	app := testApp(t, nil)
	if _, err := buildAccountRename(context.Background(), app, commonOpts{Format: formatText}, "claude", "ghost", "new"); exitOf(err) != constants.ExitNotFound {
		t.Fatalf("expected exit %d, got %v", constants.ExitNotFound, err)
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
