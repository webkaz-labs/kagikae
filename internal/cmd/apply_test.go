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

// applyTestApp captures claude work+personal and defines matching profiles;
// the live (and recorded) account is "personal" afterwards.
func applyTestApp(t *testing.T, envVars map[string]string) *App {
	t.Helper()
	app := testApp(t, envVars)
	app.Config.Profiles = map[string]config.Profile{
		"work":     {Accounts: map[string]string{constants.ToolClaude: "work"}},
		"personal": {Accounts: map[string]string{constants.ToolClaude: "personal"}},
	}
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	seedClaude(t, app, workToken, "work-uuid")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "work") })
	mustExit(t, constants.ExitOK, code, out)
	seedClaude(t, app, personalToken, "personal-uuid")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "personal") })
	mustExit(t, constants.ExitOK, code, out)
	return app
}

func decodeApplyReport(t *testing.T, out string) applyReport {
	t.Helper()
	var report applyReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid apply JSON: %v: %s", err, out)
	}
	return report
}

func TestApplyNoOpTakesNoLockAndApplyTakesLock(t *testing.T) {
	app := applyTestApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatJSON}

	// Recorded state is personal (last capture). Hold the claude lock: a
	// matching apply must still succeed because the no-op path takes no lock.
	held, err := lock.Acquire(app.Paths.LocksDir(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	code, out := captureStdout(t, func() int { return runApply(ctx, app, opts, "personal", false) })
	mustExit(t, constants.ExitOK, code, out)
	report := decodeApplyReport(t, out)
	if report.Changed || !report.OK || len(report.Results) != 0 || report.BackupID != "" {
		t.Fatalf("expected unchanged no-op report: %s", out)
	}

	// A diverging apply goes through switch all and must hit the held lock.
	code, out = captureStdout(t, func() int { return runApply(ctx, app, opts, "work", false) })
	mustExit(t, constants.ExitLockBusy, code, out)
	held.Release()

	// With the lock free the diverging apply applies and reports per-tool results.
	code, out = captureStdout(t, func() int { return runApply(ctx, app, opts, "work", false) })
	mustExit(t, constants.ExitOK, code, out)
	report = decodeApplyReport(t, out)
	if !report.Changed || len(report.Results) != 1 || report.Results[0].Account != "work" || report.BackupID == "" {
		t.Fatalf("expected applied report: %s", out)
	}
	creds := readFile(t, app.Env.Home+"/.claude/.credentials.json")
	if !strings.Contains(creds, workToken) {
		t.Fatalf("credentials not switched: %s", creds)
	}

	// Re-running is a no-op again.
	code, out = captureStdout(t, func() int { return runApply(ctx, app, opts, "work", false) })
	mustExit(t, constants.ExitOK, code, out)
	if report = decodeApplyReport(t, out); report.Changed {
		t.Fatalf("expected idempotent re-run: %s", out)
	}
}

func TestApplyQuietSuppressesSuccessOutput(t *testing.T) {
	app := applyTestApp(t, nil)
	ctx := context.Background()

	code, out := captureStdout(t, func() int { return runApply(ctx, app, commonOpts{Format: formatText}, "work", true) })
	mustExit(t, constants.ExitOK, code, out)
	if out != "" {
		t.Fatalf("quiet apply must print nothing, got: %s", out)
	}
	code, out = captureStdout(t, func() int { return runApply(ctx, app, commonOpts{Format: formatText}, "work", true) })
	mustExit(t, constants.ExitOK, code, out)
	if out != "" {
		t.Fatalf("quiet no-op must print nothing, got: %s", out)
	}
}

func TestApplyProfileResolutionOrder(t *testing.T) {
	app := applyTestApp(t, map[string]string{constants.EnvKaeProfile: "personal"})
	app.Config.DefaultProfile = "work"

	// --profile beats $KAE_PROFILE beats default_profile.
	if got, err := app.resolveApplyProfile("explicit"); err != nil || got != "explicit" {
		t.Fatalf("explicit flag must win: %q %v", got, err)
	}
	if got, err := app.resolveApplyProfile(""); err != nil || got != "personal" {
		t.Fatalf("$KAE_PROFILE must beat default_profile: %q %v", got, err)
	}

	noEnv := applyTestApp(t, nil)
	noEnv.Config.DefaultProfile = "work"
	if got, err := noEnv.resolveApplyProfile(""); err != nil || got != "work" {
		t.Fatalf("default_profile fallback: %q %v", got, err)
	}
	noEnv.Config.DefaultProfile = ""
	if _, err := noEnv.resolveApplyProfile(""); err == nil || exitOf(err) != constants.ExitUsage {
		t.Fatalf("missing profile must be a usage error, got %v", err)
	}
}

func TestApplyUnknownProfile(t *testing.T) {
	app := applyTestApp(t, nil)
	ctx := context.Background()
	code, out := captureStdout(t, func() int { return runApply(ctx, app, commonOpts{Format: formatText}, "nope", false) })
	mustExit(t, constants.ExitNotFound, code, out)
}

func TestSyncTombstone(t *testing.T) {
	// kae sync was renamed to kae apply (docs/SCOPE-MODEL.md §8); it exits
	// ExitUsage (64) and names the replacement for one release.
	for _, args := range [][]string{nil, {"--profile", "work"}} {
		if code := CmdSync(context.Background(), args); code != constants.ExitUsage {
			t.Errorf("tombstone must exit %d, got %d (args=%v)", constants.ExitUsage, code, args)
		}
	}
}

func TestUseUsage(t *testing.T) {
	// Argument validation happens before any environment access; one and two
	// positionals are both valid since v0.5.0 (profile / tool+account).
	if code := CmdUse(context.Background(), nil); code != constants.ExitUsage {
		t.Fatalf("kae use without arguments must be a usage error, got %d", code)
	}
	if code := CmdUse(context.Background(), []string{"a", "b", "c"}); code != constants.ExitUsage {
		t.Fatalf("kae use with three positionals must be a usage error, got %d", code)
	}
}
