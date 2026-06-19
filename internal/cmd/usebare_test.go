package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
)

// applyTestApp captures claude main+side and defines matching profiles;
// the live (and recorded) account is "side" afterwards.
func applyTestApp(t *testing.T, envVars map[string]string) *App {
	t.Helper()
	app := testApp(t, envVars)
	app.Config.Profiles = map[string]config.Profile{
		"main": {Accounts: map[string]string{constants.ToolClaude: "main"}},
		"side": {Accounts: map[string]string{constants.ToolClaude: "side"}},
	}
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	seedClaude(t, app, mainToken, "main-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, out)
	seedClaude(t, app, sideToken, "side-uuid")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") })
	mustExit(t, constants.ExitOK, code, out)
	return app
}

func decodeBareUseReport(t *testing.T, out string) bareUseReport {
	t.Helper()
	var report bareUseReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid bare-use JSON: %v: %s", err, out)
	}
	return report
}

func TestBareUseNoOpTakesNoLockAndApplyTakesLock(t *testing.T) {
	app := applyTestApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatJSON}

	// Recorded state is side (last capture). Hold the claude lock: a
	// matching bare use must still succeed because the no-op path takes no lock.
	held, err := lock.Acquire(app.Paths.LocksDir(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	code, out := captureStdout(t, func() int { return runUseBare(ctx, app, opts, false, "side", false) })
	mustExit(t, constants.ExitOK, code, out)
	report := decodeBareUseReport(t, out)
	if report.Changed || !report.OK || len(report.Results) != 0 || report.BackupID != "" {
		t.Fatalf("expected unchanged no-op report: %s", out)
	}

	// A diverging apply goes through switch all and must hit the held lock.
	code, out = captureStdout(t, func() int { return runUseBare(ctx, app, opts, false, "main", false) })
	mustExit(t, constants.ExitLockBusy, code, out)
	held.Release()

	// With the lock free the diverging apply applies and reports per-tool results.
	code, out = captureStdout(t, func() int { return runUseBare(ctx, app, opts, false, "main", false) })
	mustExit(t, constants.ExitOK, code, out)
	report = decodeBareUseReport(t, out)
	if !report.Changed || len(report.Results) != 1 || report.Results[0].Account != "main" || report.BackupID == "" {
		t.Fatalf("expected applied report: %s", out)
	}
	creds := readFile(t, app.Env.Home+"/.claude/.credentials.json")
	if !strings.Contains(creds, mainToken) {
		t.Fatalf("credentials not switched: %s", creds)
	}

	// Re-running is a no-op again.
	code, out = captureStdout(t, func() int { return runUseBare(ctx, app, opts, false, "main", false) })
	mustExit(t, constants.ExitOK, code, out)
	if report = decodeBareUseReport(t, out); report.Changed {
		t.Fatalf("expected idempotent re-run: %s", out)
	}
}

func TestBareUseQuietSuppressesSuccessOutput(t *testing.T) {
	app := applyTestApp(t, nil)
	ctx := context.Background()

	code, out := captureStdout(t, func() int { return runUseBare(ctx, app, commonOpts{Format: formatText}, false, "main", true) })
	mustExit(t, constants.ExitOK, code, out)
	if out != "" {
		t.Fatalf("quiet bare use must print nothing, got: %s", out)
	}
	code, out = captureStdout(t, func() int { return runUseBare(ctx, app, commonOpts{Format: formatText}, false, "main", true) })
	mustExit(t, constants.ExitOK, code, out)
	if out != "" {
		t.Fatalf("quiet no-op must print nothing, got: %s", out)
	}

	// --quiet suppresses only the human report; --json still emits so a script
	// can read `changed` (docs/RELEASE.md).
	code, out = captureStdout(t, func() int { return runUseBare(ctx, app, commonOpts{Format: formatJSON}, false, "main", true) })
	mustExit(t, constants.ExitOK, code, out)
	report := decodeBareUseReport(t, out)
	if report.Profile == nil || *report.Profile != "main" {
		t.Fatalf("quiet --json must still emit the JSON report: %s", out)
	}
}

func TestBareUseProfileResolutionOrder(t *testing.T) {
	app := applyTestApp(t, map[string]string{constants.EnvKaeProfile: "side"})
	app.Config.DefaultProfile = "main"

	// --profile/-P beats $KAE_PROFILE beats default_profile.
	if got, err := app.resolveBareUseProfile("explicit"); err != nil || got != "explicit" {
		t.Fatalf("explicit flag must win: %q %v", got, err)
	}
	if got, err := app.resolveBareUseProfile(""); err != nil || got != "side" {
		t.Fatalf("$KAE_PROFILE must beat default_profile: %q %v", got, err)
	}

	noEnv := applyTestApp(t, nil)
	noEnv.Config.DefaultProfile = "main"
	if got, err := noEnv.resolveBareUseProfile(""); err != nil || got != "main" {
		t.Fatalf("default_profile fallback: %q %v", got, err)
	}
	noEnv.Config.DefaultProfile = ""
	if _, err := noEnv.resolveBareUseProfile(""); err == nil || exitOf(err) != constants.ExitUsage {
		t.Fatalf("missing profile must be a usage error, got %v", err)
	}
}

func TestBareUseUnknownProfile(t *testing.T) {
	app := applyTestApp(t, nil)
	ctx := context.Background()
	code, out := captureStdout(t, func() int { return runUseBare(ctx, app, commonOpts{Format: formatText}, false, "nope", false) })
	mustExit(t, constants.ExitNotFound, code, out)
}

func TestApplyTombstone(t *testing.T) {
	// kae apply folded into bare kae use in v0.8.0; it exits ExitUsage (64) and
	// names the replacement for one release.
	for _, args := range [][]string{nil, {"--profile", "main"}} {
		if code := CmdApply(context.Background(), args); code != constants.ExitUsage {
			t.Errorf("tombstone must exit %d, got %d (args=%v)", constants.ExitUsage, code, args)
		}
	}
}

func TestUseUsage(t *testing.T) {
	// More than two positionals is a usage error (validated before any
	// environment access). Bare use (zero positionals) is now valid (it folds
	// the former apply), so it is exercised via runUseBare with a temp HOME, not
	// here against the real environment.
	if code := CmdUse(context.Background(), []string{"a", "b", "c"}); code != constants.ExitUsage {
		t.Fatalf("kae use with three positionals must be a usage error, got %d", code)
	}
}
