// Package gh declares the gh companion: it binds the GitHub CLI's token per
// profile via the GH_TOKEN env var, resolved from the secret backend at mise
// eval time (never written to disk).
package gh

import (
	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

func init() {
	companion.Register(companion.Spec{
		ID:     constants.CompanionGH,
		Binary: "gh",
		Kind:   companion.KindToken,
		Knobs:  []companion.Knob{{Name: "GH_TOKEN", EnvVar: "GH_TOKEN"}},
		// `gh api user` reads GH_TOKEN from the environment and prints the login
		// the token belongs to; --jq narrows it to the bare login string.
		LoginProbe: []string{"gh", "api", "user", "--jq", ".login"},
	})
}
