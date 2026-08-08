package cmd

import (
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// The auth_missing sentence went from a pre-assembled string through `errf("%s", msg)` to a
// format with two arguments. Both cases must be byte-identical to what shipped, and a `%`
// in a warning must not be interpreted (the old form was immune by construction).
func TestR6AuthMissingSentenceIsUnchanged(t *testing.T) {
	// The pre-refactor construction, verbatim.
	old := func(tool string, warnings []string) string {
		message := "no live " + tool + " auth state found; log in with the official CLI first"
		if len(warnings) > 0 {
			message += " (" + strings.Join(warnings, "; ") + ")"
		}
		return message
	}
	cases := [][]string{nil, {}, {"one"}, {"one", "two"}, {"a 100% relative XDG_DATA_HOME"}}
	for _, w := range cases {
		got := errf(constants.ExitAuthMissing,
			"no live %s auth state found; log in with the official CLI first%s",
			"claude", warningsDetail(w)).Error()
		if want := old("claude", w); got != want {
			t.Errorf("warnings=%v\n got=%q\nwant=%q", w, got, want)
		}
	}
}

// Ask 4: can a second tool be declined in one run? Both refusals are claude-only by
// construction, which is what makes the pairing unobservable. Asserted on the adapters
// rather than trusted, because the day either becomes false the comment is wrong.
func TestR6OnlyClaudeCanBeDeclined(t *testing.T) {
	identityOnly, rotating := []string{}, []string{}
	for _, tool := range constants.Tools {
		if rotatesSingleUse(tool) {
			rotating = append(rotating, tool)
		}
		for _, goos := range []string{"darwin", "linux"} {
			app := testApp(t, nil)
			app.Env.GOOS = goos
			// `planTool` resolves a keychain-backed tool by *reading* the store, and with
			// no runner installed that read went to the operator's own login keychain. It
			// never wrote, so this is not the pollution `pinHereAs` caused — but the
			// `continue` below is decided by whether the read found anything, so which
			// tools this guard actually inspected depended on which accounts the person
			// running it happened to be logged into. A developer and CI checked different
			// sets. A uniform miss makes the set the same everywhere.
			var plan toolPlan
			var err error
			runner.With(&runnertest.Fake{Code: 44, Stderr: "security: " + keychain.NotFoundMarker}, func() {
				plan, err = app.planTool(t.Context(), tool, "main")
			})
			if err != nil {
				continue // a tool this platform cannot resolve cannot be declined either
			}
			for _, sp := range plan.Specs {
				if sp.IdentityOnly {
					identityOnly = append(identityOnly, tool+"/"+goos)
				}
			}
		}
	}
	t.Logf("rotatesSingleUse=%v  IdentityOnly=%v", rotating, identityOnly)
	if len(rotating) != 1 || rotating[0] != constants.ToolClaude {
		t.Errorf("the preserve arm is no longer claude-only (%v): the declined pairing is now "+
			"observable and needs a test", rotating)
	}
	for _, entry := range identityOnly {
		if !strings.HasPrefix(entry, constants.ToolClaude+"/") {
			t.Errorf("a second tool declares an IdentityOnly artifact (%s): keepSnapshotIdentity "+
				"can now decline two plans in one run", entry)
		}
	}
}
