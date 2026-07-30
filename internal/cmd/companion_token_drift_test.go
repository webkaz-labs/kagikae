package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// withRunWithEnv swaps runner.RunWithEnv (the env-injecting probe seam) for the
// duration of fn, restoring it after. Used to stand in for `gh api user`.
func withRunWithEnv(t *testing.T, fake func(ctx context.Context, extraEnv []string, name string, args ...string) (string, string, int), fn func()) {
	t.Helper()
	saved := runner.RunWithEnv
	runner.RunWithEnv = fake
	defer func() { runner.RunWithEnv = saved }()
	fn()
}

// tokenDriftApp pins the current dir to profile "main" binding the gh token
// companion with the given data (e.g. an expected_login), with gh on PATH and
// GH_TOKEN set to ghToken in the ambient env ("" = pin not active).
func tokenDriftApp(t *testing.T, data config.CompanionData, ghToken string) *App {
	t.Helper()
	app := companionCLIApp(t)
	app.Config.Profiles["main"] = config.Profile{
		Accounts:   map[string]string{constants.ToolClaude: "main"},
		Companions: map[string]config.CompanionData{constants.CompanionGH: data},
	}
	app.Env.LookPath = func(string) (string, error) { return "/usr/bin/gh", nil }
	app.Env.Getenv = func(k string) string {
		if k == "GH_TOKEN" {
			return ghToken
		}
		return ""
	}
	chdirTemp(t)
	writeFile(t, fragmentRelPath, "# kae:profile=main\n")
	return app
}

func TestTokenDriftOptInGate(t *testing.T) {
	app := tokenDriftApp(t, config.CompanionData{"GH_TOKEN": "", "expected_login": "main"}, "tok")
	// live=false (not opted in) never probes, regardless of state.
	if got := app.companionTokenDriftChecks(context.Background(), false); got != nil {
		t.Fatalf("opt-out must skip the check, got %v", got)
	}
}

func TestTokenDriftMatchNoCheck(t *testing.T) {
	app := tokenDriftApp(t, config.CompanionData{"GH_TOKEN": "", "expected_login": "main"}, "tok")
	withRunWithEnv(t, func(_ context.Context, _ []string, _ string, _ ...string) (string, string, int) {
		return "main\n", "", 0
	}, func() {
		if got := app.companionTokenDriftChecks(context.Background(), true); len(got) != 0 {
			t.Fatalf("matching login must not drift, got %v", got)
		}
	})
}

func TestTokenDriftWrongAccount(t *testing.T) {
	app := tokenDriftApp(t, config.CompanionData{"GH_TOKEN": "", "expected_login": "main"}, "tok")
	withRunWithEnv(t, func(_ context.Context, _ []string, _ string, _ ...string) (string, string, int) {
		return "someone-else\n", "", 0
	}, func() {
		checks := app.companionTokenDriftChecks(context.Background(), true)
		if len(checks) != 1 || checks[0].Code != constants.CheckCompanionTokenDrift {
			t.Fatalf("expected one token-drift check, got %v", checks)
		}
		if !strings.Contains(checks[0].Message, "main") || !strings.Contains(checks[0].Message, "someone-else") {
			t.Errorf("message should name expected and live login: %q", checks[0].Message)
		}
	})
}

func TestTokenDriftProbeFailure(t *testing.T) {
	app := tokenDriftApp(t, config.CompanionData{"GH_TOKEN": "", "expected_login": "main"}, "tok")
	withRunWithEnv(t, func(_ context.Context, _ []string, _ string, _ ...string) (string, string, int) {
		return "", "Bad credentials", 1
	}, func() {
		checks := app.companionTokenDriftChecks(context.Background(), true)
		if len(checks) != 1 || !strings.Contains(checks[0].Message, "could not verify") {
			t.Fatalf("probe failure should warn 'could not verify', got %v", checks)
		}
	})
}

func TestTokenDriftPinInactive(t *testing.T) {
	// GH_TOKEN empty in env = the pin is not active; warn without probing.
	app := tokenDriftApp(t, config.CompanionData{"GH_TOKEN": "", "expected_login": "main"}, "")
	probed := false
	withRunWithEnv(t, func(_ context.Context, _ []string, _ string, _ ...string) (string, string, int) {
		probed = true
		return "main\n", "", 0
	}, func() {
		checks := app.companionTokenDriftChecks(context.Background(), true)
		if len(checks) != 1 || !strings.Contains(checks[0].Message, "not active") {
			t.Fatalf("empty token env should warn 'pin not active', got %v", checks)
		}
	})
	if probed {
		t.Error("must not call the network probe when the token is absent from the env")
	}
}

func TestTokenDriftNoExpectedLoginSkips(t *testing.T) {
	// No expected_login recorded → not a candidate → no check even when opted in.
	app := tokenDriftApp(t, config.CompanionData{"GH_TOKEN": ""}, "tok")
	withRunWithEnv(t, func(_ context.Context, _ []string, _ string, _ ...string) (string, string, int) {
		return "main\n", "", 0
	}, func() {
		if got := app.companionTokenDriftChecks(context.Background(), true); got != nil {
			t.Fatalf("no expected_login means no candidate, got %v", got)
		}
	})
}

func TestTokenDriftSkipsWhenTokenUnbound(t *testing.T) {
	// expected_login present but the token knob is gone (e.g. `kae companion rm gh
	// GH_TOKEN`): stale metadata, not a live candidate.
	app := tokenDriftApp(t, config.CompanionData{"expected_login": "main"}, "tok")
	withRunWithEnv(t, func(context.Context, []string, string, ...string) (string, string, int) {
		return "main\n", "", 0
	}, func() {
		if got := app.companionTokenDriftChecks(context.Background(), true); got != nil {
			t.Fatalf("expected_login without a bound token must not be checked, got %v", got)
		}
	})
}

func TestResolveTokenDriftOptIn(t *testing.T) {
	app := tokenDriftApp(t, config.CompanionData{"GH_TOKEN": "", "expected_login": "main"}, "tok")
	// --yes opts in non-interactively.
	if !app.resolveTokenDriftOptIn(commonOpts{Yes: true}, "") {
		t.Error("--yes should opt in to the token drift check")
	}
	// JSON cannot prompt, so it skips (no --yes).
	if app.resolveTokenDriftOptIn(commonOpts{Format: formatJSON}, "") {
		t.Error("--json without --yes must skip the network check")
	}
	// A tool-filtered report never runs companion checks.
	if app.resolveTokenDriftOptIn(commonOpts{Yes: true}, constants.ToolClaude) {
		t.Error("tool-filtered doctor must not run token drift")
	}
	// No candidate (no expected_login) → false even with --yes.
	bare := tokenDriftApp(t, config.CompanionData{"GH_TOKEN": ""}, "tok")
	if bare.resolveTokenDriftOptIn(commonOpts{Yes: true}, "") {
		t.Error("no eligible candidate must skip even with --yes")
	}
}

// TestTokenDriftNeverPrintsTheToken is the redaction test AGENTS.md requires of
// a new output path, and it found a real leak: the probe authenticates with the
// bound token through its environment, and its stderr went into the doctor
// message (and into --json) verbatim. A tool that echoes a request header, a
// proxy trace, or a URL with the token in it would have published the secret.
func TestTokenDriftNeverPrintsTheToken(t *testing.T) {
	const token = "ghp_notarealtokenvalue0123456789"
	app := tokenDriftApp(t, config.CompanionData{"GH_TOKEN": "", "expected_login": "main"}, token)
	for _, tc := range []struct {
		name           string
		stdout, stderr string
		code           int
	}{
		{"probe failed and echoed the token", "", "error: request failed with Authorization: bearer " + token, 1},
		{"probe succeeded but echoed the token", token + "\n", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withRunWithEnv(t, func(_ context.Context, _ []string, _ string, _ ...string) (string, string, int) {
				return tc.stdout, tc.stderr, tc.code
			}, func() {
				checks := app.companionTokenDriftChecks(context.Background(), true)
				if len(checks) == 0 {
					t.Fatal("expected a drift check to inspect")
				}
				for _, c := range checks {
					if strings.Contains(c.Message, token) {
						t.Errorf("the bound token reached a doctor message: %q", c.Message)
					}
				}
			})
		})
	}
}
