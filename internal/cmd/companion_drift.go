package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// companionDriftChecks compares the live git commit identity in the current
// directory against what the pinned profile's git companion binds. It is the
// live counterpart to companionChecks (which is config-level only) and the
// commit-misidentity guard: a repo-local `git config user.email`, an inactive or
// untrusted pin (the mise env never applies), or any GIT_CONFIG_* the user set
// themselves makes the identity git will actually commit with diverge from the
// bound one — exactly the silent wrong-author commit companion-auth exists to
// prevent.
//
// Scope is deliberately the git companion only. Its expected values
// (email/name/signingkey) are non-secret and stored inline in config.toml, and
// `git config --get` is an offline, deterministic probe — so the check needs no
// network and can never print a secret. token companions (gh/cloudflare) keep no
// expected identity to compare against (only their secret token), and a live
// check would require a network call, so they are out of scope here.
//
// The probe runs only inside a pinned directory whose profile binds git, with
// git on PATH; companion_binary already warns when git is missing, so the probe
// skips rather than emitting a confusing error.
func (app *App) companionDriftChecks(ctx context.Context) []adapter.Check {
	info, pinned, err := readDirFragment()
	if err != nil || !pinned {
		return nil
	}
	// An empty/unknown profile name fails this lookup (config rejects "" as a
	// profile name), so it doubles as the not-named guard.
	profile, ok := app.Config.Profiles[info.Profile]
	if !ok {
		return nil
	}
	data, bound := profile.Companions[constants.CompanionGit]
	if !bound {
		return nil
	}
	spec, ok := companion.For(constants.CompanionGit)
	if !ok || spec.Kind != companion.KindGitConfig {
		return nil
	}
	if _, err := app.Env.LookPath(spec.Binary); err != nil {
		return nil
	}
	checks := []adapter.Check{}
	for _, knob := range spec.Knobs {
		want := data[knob.Name]
		if want == "" {
			continue // knob unset in this profile; nothing to compare
		}
		// The git-config template renders [user] with each knob as a field, so a
		// knob named X is git config key user.X (see internal/companion/git and
		// TestGitCompanionTemplateMatchesDriftKeys).
		key := "user." + knob.Name
		stdout, _, code := runner.Run(ctx, spec.Binary, "config", "--get", key)
		got := strings.TrimSpace(stdout)
		if code == 0 && got == want {
			continue
		}
		checks = append(checks, adapter.Check{
			Tool: constants.CompanionGit, Code: constants.CheckCompanionDrift, Status: constants.StatusWarn,
			Message: companionDriftMessage(info.Profile, key, want, got, code),
		})
	}
	return checks
}

// companionDriftMessage frames a git identity mismatch. A non-zero exit means
// the key is not set anywhere git can see it (the pin is not taking effect in
// this shell); exit zero means git reports a value — possibly empty, if the key
// is explicitly set to "" — that differs from the binding, i.e. a repo-local
// override or an inactive pin. Both end in a wrong-author commit. Values are
// sanitized for display so a hostile .git/config cannot inject terminal escapes
// through doctor output.
func companionDriftMessage(profile, key, want, got string, code int) string {
	wantSafe := sanitizeIdentity(want)
	if code != 0 {
		return fmt.Sprintf(
			"profile %s: git %s is unset here but the binding sets %q; the pin is not active in this shell (run `mise env`, or `mise trust` if untrusted), so a commit would use the wrong identity",
			profile, key, wantSafe,
		)
	}
	return fmt.Sprintf(
		"profile %s: git %s is %q here but the binding sets %q; a repo-local override or an inactive pin makes commits use the wrong identity (check: git config --show-origin %s)",
		profile, key, sanitizeIdentity(got), wantSafe, key,
	)
}
