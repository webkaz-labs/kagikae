package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/state"
)

// accountFreshness reads acc's stored credential and reports its expiry and
// refresh-token presence. It returns the first artifact whose payload parses as
// the tool's known credential format; a not-datable account (copilot pointer,
// agy blob) yields Known=false. Shared by the switch-time stale warning
// (docs/RELEASE.md §B) and doctor credential-health (§D).
func (app *App) accountFreshness(ctx context.Context, be secret.Backend, acc account.Account) (freshness.Info, error) {
	for _, name := range acc.ArtifactNames() {
		art := acc.Artifacts[name]
		if !art.Present {
			continue
		}
		data, found, err := be.Get(ctx, art.SecretRef)
		if err != nil {
			return freshness.Info{}, err
		}
		if !found {
			continue
		}
		if info := freshness.Inspect(acc.Tool, data); info.Known {
			return info, nil
		}
	}
	return freshness.Info{}, nil
}

// staleSnapshotWarning returns a switch-time warning when acc's snapshot
// credential is past expiry with no usable refresh token, so a switch to it
// cannot self-heal and the user must re-capture. An expired credential that
// still carries a refresh token returns "" — the tool refreshes it on next use
// (docs/RELEASE.md §B). A fresh, undated, or not-datable account returns "".
func (app *App) staleSnapshotWarning(ctx context.Context, be secret.Backend, acc account.Account) (string, error) {
	info, err := app.accountFreshness(ctx, be, acc)
	if err != nil {
		return "", err
	}
	if !snapshotExpired(info, app.Now()) || info.HasRefresh {
		return "", nil
	}
	return fmt.Sprintf("snapshot credential expired %s and has no refresh token; re-capture with: kae add --no-login %s %s",
		info.ExpiresAt.UTC().Format(time.RFC3339), acc.Tool, acc.Name), nil
}

// snapshotExpired reports whether a known, dated credential is past expiry.
func snapshotExpired(info freshness.Info, now time.Time) bool {
	return info.Known && !info.ExpiresAt.IsZero() && info.ExpiresAt.Before(now)
}

// recaptureActiveBeforeSwitch refreshes each switched tool's currently-active
// account snapshot from the live store before kae use switches away, so a later
// switch back applies a live token (symmetric with run -s). It rewrites the
// snapshot only when the live store and the snapshot diverge, so a no-op switch
// costs no write; the divergence read is coalesced with the switch's other
// keychain reads (docs/RELEASE.md §A/§C). Best-effort: a logged-out or
// unreadable active account is left untouched with a warning, never aborting
// the switch. Only kae use / bare use reach here — use -i / pin / run -i write
// kae-owned isolation dirs and never the real store.
func (app *App) recaptureActiveBeforeSwitch(ctx context.Context, be secret.Backend, st *state.State, plans []toolPlan) {
	for _, plan := range plans {
		active := st.Active[plan.Tool]
		if active == "" {
			continue // nothing previously active for this tool
		}
		acc, found, err := account.Load(app.Paths.AccountDir(plan.Tool, active))
		if err != nil || !found {
			continue // never captured: nothing to refresh
		}
		// plan.Specs are account-agnostic for a given tool, so they read the live
		// store regardless of which account is the switch target.
		values, anyPresent, err := readLiveValues(ctx, plan.Specs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kae: warning: could not read live %s state to refresh %s/%s: %v\n",
				plan.Tool, plan.Tool, active, err)
			continue
		}
		if !anyPresent {
			fmt.Fprintf(os.Stderr, "kae: warning: %s is logged out; snapshot %s/%s left unchanged\n",
				plan.Tool, plan.Tool, active)
			continue
		}
		if !valuesDiverge(ctx, be, plan.Specs, acc, values) {
			continue // live already matches the snapshot: skip the write
		}
		// Same tool/driver/specs as the target plan; only the account differs
		// (copy so a future toolPlan field is not silently dropped).
		activePlan := plan
		activePlan.Account = active
		if err := app.persistSnapshot(ctx, be, activePlan, values); err != nil {
			fmt.Fprintf(os.Stderr, "kae: warning: recapture of %s/%s failed: %v\n", plan.Tool, active, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "kae: refreshed %s/%s snapshot from the live store before switching away\n",
			plan.Tool, active)
	}
}

// valuesDiverge reports whether freshly-read live values differ from acc's
// stored snapshot: a missing artifact record, a presence mismatch, or any
// payload difference. An unreadable stored payload is treated as divergence so
// recapture errs toward refreshing rather than silently keeping a stale token.
func valuesDiverge(ctx context.Context, be secret.Backend, specs []artifact.Spec, acc account.Account, values []artifact.Value) bool {
	for i, sp := range specs {
		art, ok := acc.Artifacts[sp.Name]
		if !ok || art.Present != values[i].Present {
			return true
		}
		if !values[i].Present {
			continue
		}
		stored, found, err := be.Get(ctx, art.SecretRef)
		if err != nil || !found || !bytes.Equal(stored, values[i].Data) {
			return true
		}
	}
	return false
}
