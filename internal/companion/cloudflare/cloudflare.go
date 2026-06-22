// Package cloudflare declares the cloudflare companion: it binds the Cloudflare
// API token (used by wrangler, flarectl, terraform-cloudflare) per profile via
// the CLOUDFLARE_API_TOKEN env var, resolved from the secret backend at mise
// eval time (never written to disk).
package cloudflare

import (
	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

func init() {
	companion.Register(companion.Spec{
		ID:     constants.CompanionCloudflare,
		Binary: "wrangler",
		Kind:   companion.KindToken,
		Knobs:  []companion.Knob{{Name: "CLOUDFLARE_API_TOKEN", EnvVar: "CLOUDFLARE_API_TOKEN"}},
	})
}
