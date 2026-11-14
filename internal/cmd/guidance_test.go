package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// A lone name is always read as a profile, so `kae use main` and `kae use claude`
// end on an undefined-profile error. The error also names the one-tool form that
// does what was meant, worded for the command that failed (with the mode flags the
// user typed), and keeps exit 7.
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
	use, run, pin := oneToolForm{verb: hintUse}, oneToolForm{verb: hintRun}, oneToolForm{verb: hintPin}
	for _, tc := range []struct {
		name string
		form oneToolForm
		want string
	}{
		{"claude", use, `"claude" is a tool, not a profile; to target one tool's account, run: kae use claude <account>`},
		{"cl", use, `"cl" is a tool, not a profile; to target one tool's account, run: kae use claude <account>`},
		{"claude", run, `"claude" is a tool, not a profile; to target one tool's account, run: kae run claude <account>`},
		{"claude", pin, `"claude" is a tool, not a profile; to target one tool's account, run: kae profile set <profile> claude <account>, then: kae pin <profile>`},
		{"main", use, `"main" is an account name, not a profile; to target it on one tool, run: kae use claude main`},
		{"zeta", run, `"zeta" is an account name, not a profile; to target it on one tool, run: kae run claude zeta`},
		{"zeta", pin, `"zeta" is an account name, not a profile; to target it on one tool, run: kae profile set <profile> claude zeta, then: kae pin <profile>`},
		// The mode flags the user typed stay on the command the hint spells.
		{"main", oneToolForm{verb: hintUse, flags: "-i"}, `run: kae use -i claude main`},
		{"claude", oneToolForm{verb: hintRun, flags: "--env"}, `run: kae run --env claude <account>`},
		{"claude", oneToolForm{verb: hintPin, flags: "-i"}, `then: kae pin -i <profile>`},
		// Inside a bound directory a pin re-binds one tool; a new profile would
		// replace the whole binding.
		{"claude", oneToolForm{verb: hintPin, bound: true}, `run: kae pin claude <account>`},
		{"zeta", oneToolForm{verb: hintPin, bound: true}, `run: kae pin claude zeta`},
	} {
		t.Run(fmt.Sprintf("%+v %s", tc.form, tc.name), func(t *testing.T) {
			_, _, err := app.resolveTargets(tc.form, "all", tc.name)
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
		_, _, err := app.resolveTargets(use, "all", name)
		if err == nil || strings.Contains(err.Error(), "not a profile") {
			t.Fatalf("%q must not hint: %v", name, err)
		}
	}
}

// The profile-then-pin remedy is a message value, so the Japanese output carries
// no English "then:" joiner (docs/CLI.md § Localization, one language per line).
func TestPinHintRendersInJapanese(t *testing.T) {
	l10ntest.UseJapanese(t)
	app := overlayTestApp(t)
	_, _, err := app.resolveTargets(oneToolForm{verb: hintPin}, "all", "claude")
	if err == nil {
		t.Fatal("want an undefined-profile error")
	}
	got := l10n.Render(err)
	if !strings.Contains(got, "kae profile set <profile> claude <account>、続けて kae pin <profile>") || strings.Contains(got, "then") {
		t.Fatalf("not rendered in Japanese: %q", got)
	}
}

// Inside a bound directory `kae pin claude` and `kae pin main` (a tool name and an
// account name typed where a profile goes) point at `kae pin <tool> <account>`, not
// at a new profile whose pin would replace the whole binding, and change nothing.
func TestPinHintInABoundDirectoryRebindsOneTool(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	if code := runPin(context.Background(), app, commonOpts{Format: formatText}, "main", modeShared, false); code != constants.ExitOK {
		t.Fatalf("bind: exit %d", code)
	}
	before := readFile(t, fragmentRelPath)
	delete(app.Config.Profiles, "main") // so `main` is only an account name
	seedClaude(t, app, mainToken, "main-uuid")
	if code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	}); code != constants.ExitOK {
		t.Fatalf("capture: %s", out)
	}
	for _, tc := range []struct{ name, want string }{
		{"claude", "kae pin claude <account>"},
		{"main", "kae pin claude main"},
	} {
		code, stderr := captureStderr(t, func() int {
			return runPin(context.Background(), app, commonOpts{Format: formatText}, tc.name, modeShared, false)
		})
		if code != constants.ExitNotFound || !strings.Contains(stderr, tc.want) || strings.Contains(stderr, "profile set") {
			t.Errorf("pin %s: exit %d, want %d naming %q without a new profile:\n%s", tc.name, code, constants.ExitNotFound, tc.want, stderr)
		}
		if after := readFile(t, fragmentRelPath); after != before {
			t.Errorf("pin %s changed the binding:\n%s", tc.name, after)
		}
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
		{"account ls", func() int { return CmdAccount(context.Background(), []string{"ls"}) }, "has no ls subcommand"},
		{"profile add", func() int { return CmdProfile(context.Background(), []string{"add"}) }, "kae profile set <name> <tool> <account>"},
		{"profile list", func() int { return CmdProfile(context.Background(), []string{"list"}) }, "kae status"},
		{"profile ls", func() int { return CmdProfile(context.Background(), []string{"ls"}) }, "has no ls subcommand"},
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
// to confirm the account and the store first, even when the login has no name yet.
func TestVerifiedCaptureRemedyAsksBeforeCapturing(t *testing.T) {
	for _, tc := range []struct{ name, account, want string }{
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
