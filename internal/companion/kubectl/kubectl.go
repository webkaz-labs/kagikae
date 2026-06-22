// Package kubectl declares the kubectl companion: it binds the active
// kubeconfig per profile by pointing KUBECONFIG at a user-provided config path.
// The path is non-secret and lives in config.toml; kae references it, it never
// generates or copies the kubeconfig (which may hold cluster credentials).
package kubectl

import (
	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

func init() {
	companion.Register(companion.Spec{
		ID:           constants.CompanionKubectl,
		Binary:       "kubectl",
		Kind:         companion.KindConfigDir,
		Knobs:        []companion.Knob{{Name: "KUBECONFIG", EnvVar: "KUBECONFIG"}},
		EnvConflicts: []string{"KUBECONFIG"},
	})
}
