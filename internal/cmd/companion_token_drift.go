package cmd

import (
	"context"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// tokenDriftCandidate is a token companion in the pinned profile that can be
// checked for login drift: it declares a LoginProbe and has a recorded
// expected_login to compare the live login against.
type tokenDriftCandidate struct {
	spec          companion.Spec
	expectedLogin string
}

// tokenDriftCandidates returns the bound profile name and the token companions
// in it eligible for a live drift check: a pinned directory, a named profile,
// and a token companion that both declares a LoginProbe and has a recorded
// expected_login. It is the shared gate for the opt-in prompt (skip when none)
// and for companionTokenDriftChecks.
func (app *App) tokenDriftCandidates() (profile string, cands []tokenDriftCandidate) {
	info, pinned, err := readDirFragment()
	if err != nil || !pinned {
		return "", nil
	}
	prof, ok := app.Config.Profiles[info.Profile]
	if !ok {
		return "", nil
	}
	for _, spec := range companion.All() {
		if len(spec.LoginProbe) == 0 {
			continue
		}
		data, bound := prof.Companions[spec.ID]
		if !bound {
			continue
		}
		// expected_login is metadata of the token knob; without the token bound it
		// is stale (e.g. after `kae companion rm <id> <token-knob>`), so skip it.
		tk, ok := spec.TokenKnob()
		if !ok {
			continue
		}
		if _, tokenBound := data[tk.Name]; !tokenBound {
			continue
		}
		if expected := data[constants.CompanionKnobExpectedLogin]; expected != "" {
			cands = append(cands, tokenDriftCandidate{spec: spec, expectedLogin: expected})
		}
	}
	return info.Profile, cands
}

// companionTokenDriftChecks compares the live login a pinned profile's token
// companions resolve to against their recorded expected_login. It is the token
// counterpart to companionDriftChecks (git): a token that resolves to a
// different account — or no token in the environment because the pin is not
// active — means a command like `gh` would act as the wrong identity, the silent
// wrong-account operation companion-auth exists to prevent. The probe is a
// network call, so the check is opt-in: it returns nil unless the caller passes
// live (set by --yes or the doctor prompt; see resolveTokenDriftOptIn).
func (app *App) companionTokenDriftChecks(ctx context.Context, live bool) []adapter.Check {
	if !live {
		return nil
	}
	profile, cands := app.tokenDriftCandidates()
	if len(cands) == 0 {
		return nil
	}
	checks := []adapter.Check{}
	for _, c := range cands {
		if _, err := app.Env.LookPath(c.spec.Binary); err != nil {
			continue // companion_binary already warns; skip a confusing probe error
		}
		envVar := c.spec.TokenEnvVar()
		// The token reaches the probe through the ambient environment (the pin's
		// mise fragment injects it); an empty value means the pin is not active
		// here, the token-side analogue of git drift's "key unset" case.
		if app.Env.Getenv(envVar) == "" {
			checks = append(checks, adapter.Check{
				Tool: c.spec.ID, Code: constants.CheckCompanionTokenDrift, Status: constants.StatusWarn,
				Message: tokenDriftInactiveMessage(profile, c.spec.ID, envVar, c.expectedLogin),
			})
			continue
		}
		stdout, stderr, code := runner.RunWithEnv(ctx, nil, c.spec.LoginProbe[0], c.spec.LoginProbe[1:]...)
		got := sanitizeIdentity(stdout)
		if code == 0 && got == c.expectedLogin {
			continue // the live token resolves to the bound login; no drift
		}
		checks = append(checks, adapter.Check{
			Tool: c.spec.ID, Code: constants.CheckCompanionTokenDrift, Status: constants.StatusWarn,
			Message: tokenDriftMismatchMessage(profile, c.spec.ID, c.expectedLogin, got, code, stderr),
		})
	}
	return checks
}

// tokenDriftInactiveMessage frames the pin-not-active case: the token env var is
// empty, so the bound token never reaches the tool and a command would fall back
// to whatever credential the tool finds on its own.
func tokenDriftInactiveMessage(profile, id, envVar, expected string) string {
	return fmt.Sprintf(
		"profile %s: %s is bound to login %q but %s is unset here; the pin is not active in this shell (run `mise env`, or `mise trust` if untrusted), so %s would act as the wrong account",
		profile, id, sanitizeIdentity(expected), envVar, id,
	)
}

// tokenDriftMismatchMessage frames a token whose live login differs from the
// binding. A non-zero exit means the probe could not confirm the login (invalid
// token or no network); exit zero with a different login means the directory's
// token is for the wrong account. got is already sanitized.
func tokenDriftMismatchMessage(profile, id, expected, got string, code int, stderr string) string {
	expSafe := sanitizeIdentity(expected)
	if code != 0 {
		return fmt.Sprintf(
			"profile %s: could not verify the %s token's login against the bound %q (%s); the token may be invalid or the network unreachable",
			profile, id, expSafe, runner.Snippet(stderr),
		)
	}
	return fmt.Sprintf(
		"profile %s: the %s token resolves to login %q but the binding expects %q; this directory's token is for the wrong account",
		profile, id, got, expSafe,
	)
}
