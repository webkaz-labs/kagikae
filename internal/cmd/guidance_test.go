package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// A lone name is always read as a profile, so `kae use main` and `kae use claude`
// end on an undefined-profile error. The error also names the one-tool form that
// does what was meant, worded for the command that failed, and keeps exit 7.
func TestUndefinedProfileNamesTheOneToolForm(t *testing.T) {
	app := overlayTestApp(t)
	delete(app.Config.Profiles, "main") // `main` is an account name only
	seedClaude(t, app, mainToken, "main-uuid")
	for _, name := range []string{"main", "zeta"} {
		if code, out := captureStdout(t, func() int {
			return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, name)
		}); code != constants.ExitOK {
			t.Fatalf("capture %s: %s", name, out)
		}
	}
	const (
		tool    = `"claude" is a tool, not a profile; to target one tool's account, run: `
		account = ` is an account name, not a profile; to target it on one tool, run: `
	)
	for _, tc := range []struct {
		verb, name, want string
	}{
		{"use", "claude", tool + "kae use claude <account>"},
		{"use", "cl", `"cl" is a tool, not a profile; to target one tool's account, run: kae use claude <account>`},
		{"run", "claude", tool + "kae run claude <account>"},
		{"pin", "claude", tool + "kae profile set <profile> claude <account>, then kae pin <profile>"},
		{"use", "main", `"main"` + account + "kae use claude main"},
		{"run", "zeta", `"zeta"` + account + "kae run claude zeta"},
		{"pin", "zeta", `"zeta"` + account + "kae profile set <profile> claude zeta, then kae pin <profile>"},
	} {
		t.Run(tc.verb+" "+tc.name, func(t *testing.T) {
			_, _, err := app.resolveTargets(tc.verb, "all", tc.name)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a message containing %q, got %v", tc.want, err)
			}
			if exitOf(err) != constants.ExitNotFound {
				t.Fatalf("the hint must keep exit %d, got %d", constants.ExitNotFound, exitOf(err))
			}
		})
	}
	// A name that is neither adds nothing, and `c` matches several tools, so it is
	// not one.
	for _, name := range []string{"zzzzz", "c"} {
		_, _, err := app.resolveTargets("use", "all", name)
		if err == nil || strings.Contains(err.Error(), "not a profile") {
			t.Fatalf("%q must not hint: %v", name, err)
		}
	}
}

// `kae run <profile>` is `kae run all <profile>`, as `kae use <profile>` is, so a
// lone positional resolves to the profile's tools.
func TestRunLoneProfileResolvesLikeUse(t *testing.T) {
	app := overlayTestApp(t)
	target, name, ok := runTargetArgs("", []string{"main"})
	if !ok {
		t.Fatal("a lone positional must be accepted")
	}
	targets, profile, err := app.resolveTargets("run", target, name)
	if err != nil || profile != "main" || len(targets) == 0 {
		t.Fatalf("targets=%v profile=%q err=%v", targets, profile, err)
	}
	if _, _, err := app.resolveTargets("run", "all", "nosuch"); err == nil || exitOf(err) != constants.ExitNotFound {
		t.Fatalf("an undefined lone profile keeps exit 7: %v", err)
	}
}

// `kae pin <tool> <account>` re-binds, so outside a bound directory it has
// nothing to re-bind: exit 7 like unpin, and the remedy builds a profile and pins
// it rather than re-running the same form.
func TestRebindOutsideABoundDirectoryNamesProfileThenPin(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	code, stderr := captureStderr(t, func() int {
		return runRebind(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "side", false)
	})
	if code != constants.ExitNotFound {
		t.Fatalf("exit %d, want %d: %s", code, constants.ExitNotFound, stderr)
	}
	for _, want := range []string{"kae profile set <profile> claude side", "then: kae pin <profile>"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

// The account and profile groups have no add or list, and users expect both: the
// usage error (exit 64) names the command that exists.
func TestNounGroupsNameTheRealCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func() int
		want string
	}{
		{"account add", func() int { return CmdAccount(context.Background(), []string{"add"}) }, "kae add <tool> <account>"},
		{"account list", func() int { return CmdAccount(context.Background(), []string{"list"}) }, "kae accounts"},
		{"profile add", func() int { return CmdProfile(context.Background(), []string{"add"}) }, "kae profile set <name> <tool> <account>"},
		{"profile list", func() int { return CmdProfile(context.Background(), []string{"list"}) }, "kae status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stderr := captureStderr(t, tc.run)
			if code != constants.ExitUsage || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d, want %d naming %q:\n%s", code, constants.ExitUsage, tc.want, stderr)
			}
		})
	}
}

// The capture remedy never recommends capturing the live login outright: it asks
// to confirm the account and the store first, with and without a name.
func TestVerifiedCaptureRemedyAsksBeforeCapturing(t *testing.T) {
	for _, tc := range []struct{ name, account, want string }{
		{"named", "main", "first verify the live claude login belongs to account main"},
		{"unnamed", "", "first verify the live claude login is the account you mean to add"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := verifiedCaptureRemedy(constants.ToolClaude, tc.account).Error()
			if !strings.Contains(got, tc.want) || !strings.Contains(got, "only then") {
				t.Fatalf("remedy %q lacks %q", got, tc.want)
			}
		})
	}
}
