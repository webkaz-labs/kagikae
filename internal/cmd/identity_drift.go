package cmd

import (
	"bytes"
	"context"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// identityDriftChecks compares each tool's live identity-only artifacts against
// what the active account's snapshot holds. kae applies the identity artifact
// together with the credential, so the live value is expected to still be the one
// kae wrote; a divergence means it was rewritten outside kae — a manual login in
// that tool, or upstream changing how it maintains the field.
//
// That second case is the reason this check exists. Every other guard kae has is
// *structural*: it refuses when the live layout is not the expected one. The
// failure that motivated this one changed no layout at all — Claude Code's
// self-heal of /oauthAccount turned out to be gated behind a 24h TTL that every
// token refresh renews — so all the structure guards stayed green while the UI
// kept naming the previous account, and nobody noticed until a human did. A
// drifting identity is the offline signal that an upstream *behaviour* assumption
// (docs/VALIDATION.md "Upstream Behaviour Assumptions") may no longer hold.
//
// Only the identifying keys are compared (identityDiffers). The same TTL means
// the rest of the payload legitimately changes under kae's feet, and comparing it
// made this check accuse itself.
//
// Offline by construction: stored bytes against live bytes, no probe and no
// network, so nothing about the login leaves the machine. The payload is PII (an
// email address), so a finding names only the tool, account, and artifact — never
// the value on either side.
func (app *App) identityDriftChecks(ctx context.Context, be secret.Backend, toolFilter string) []adapter.Check {
	st, err := app.loadState()
	if err != nil {
		return nil
	}
	checks := []adapter.Check{}
	for _, tool := range app.enabledTools() {
		if toolFilter != "" && tool != toolFilter {
			continue
		}
		active := st.Active[tool]
		if active == "" {
			continue // nothing applied yet: kae has no expected identity to compare
		}
		// Inside a kae-owned isolated home (kae pin, kae use -i) this shell's live
		// state is not what a global switch applied: the per-directory materializers
		// copy the credential but not the identity (the attribution gap tracked in
		// docs/ROADMAP.md), and state.Active names the *global* account. Comparing
		// the two frames would warn on every pinned directory, so skip — doctor must
		// not cry wolf about a gap that is already documented elsewhere.
		if envVar := isolationEnvVar(tool); envVar != "" && app.isKaeManagedHome(app.Env.Getenv(envVar)) {
			continue
		}
		acc, found, err := account.Load(app.Paths.AccountDir(tool, active))
		if err != nil || !found {
			continue
		}
		ad, err := adapter.ForTool(tool)
		if err != nil {
			continue
		}
		specs, err := ad.Artifacts(ctx, app.Env)
		if err != nil {
			continue // unsupported platform or refused layout; other checks report it
		}
		for _, sp := range specs {
			if !sp.IdentityOnly {
				continue
			}
			art, ok := acc.Artifacts[sp.Name]
			if !ok || !art.Present {
				continue // this snapshot does not track the identity yet
			}
			live, err := artifact.ReadLive(ctx, sp)
			if err != nil {
				continue // unreadable live state is auth_present's finding, not this one
			}
			// An unreadable stored payload (err) is not drift — nothing to compare
			// against — and credential_stale / secret_orphan cover it.
			differs, err := identityArtifactDiffers(ctx, be, art.SecretRef, art.Present, sp, live)
			if err != nil || !differs {
				continue
			}
			checks = append(checks, adapter.Check{
				Tool: tool, Code: constants.CheckIdentityDrift, Status: constants.StatusWarn,
				Message: identityDriftMessage(tool, active, sp.Name, live.Present),
			})
		}
	}
	return checks
}

// identityArtifactDiffers reports whether a live identity-only artifact still
// names the account the snapshot recorded. It is the identity counterpart of
// snapshotArtifactDiffers and treats presence the same way, but two present
// payloads are compared through identityDiffers instead of byte-for-byte. err is
// the backend read error; the caller chooses whether to propagate it. A stored
// payload that has gone missing counts as a difference, as it does for a
// credential.
func identityArtifactDiffers(ctx context.Context, be secret.Backend, storedRef string, storedPresent bool, sp artifact.Spec, live artifact.Value) (bool, error) {
	if storedPresent != live.Present {
		return true, nil
	}
	if !live.Present {
		return false, nil
	}
	stored, found, err := be.Get(ctx, storedRef)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}
	return identityDiffers(sp, stored, live.Data), nil
}

// identityDiffers compares two payloads of the same identity artifact on the keys
// that name the account (spec IdentityKeys) and ignores the rest.
//
// Byte comparison — the right predicate for a credential, where one differing bit
// is a different credential — is the wrong one here, because an identity payload
// carries fields the tool rewrites on its own: claude renews
// /oauthAccount.profileFetchedAt (and the plan fields with it) whenever its 24h
// cache TTL lapses and it refetches the profile. kae applies the timestamp that
// was live at capture time, so any snapshot older than a day makes claude refetch
// on its next start and rewrite it — and a byte comparison then reported a
// correctly switched account as drift, told the user to re-apply the very bytes
// that caused it, and blamed an upstream change that had not happened.
//
// Without IdentityKeys, or when either side is not a JSON object (so there is
// nothing to key on), it falls back to the byte comparison rather than declaring
// two payloads it cannot read equal.
func identityDiffers(sp artifact.Spec, stored, live []byte) bool {
	storedObj, storedOK := freshness.DecodeObject(stored)
	liveObj, liveOK := freshness.DecodeObject(live)
	if len(sp.IdentityKeys) == 0 || !storedOK || !liveOK {
		return !bytes.Equal(stored, live)
	}
	for _, key := range sp.IdentityKeys {
		// Both sides are canonical JSON produced by the same pointer read, so a
		// value comparison needs no re-encoding. A key missing on one side only
		// (nil vs a value) is a difference.
		if !bytes.Equal(storedObj[key], liveObj[key]) {
			return true
		}
	}
	return false
}

// identityDriftMessage frames a live identity artifact that no longer matches the
// applied snapshot. An absent live value is stated as such — the tool may simply
// not have rebuilt it yet — while a different value is the wrong-account-on-screen
// case. Neither form contains the payload: an identity is PII, and the tool,
// account, and artifact name are enough to act on.
func identityDriftMessage(tool, accountName, artifactName string, livePresent bool) string {
	tail := fmt.Sprintf(
		"re-apply it with: kae use %s %s. If it drifts again, an upstream behaviour assumption may have changed (docs/VALIDATION.md \"Upstream Behaviour Assumptions\")",
		tool, accountName,
	)
	if !livePresent {
		return fmt.Sprintf(
			"account %s: the live %s identity is gone while this account is active; %s may rebuild it on its next run — otherwise %s",
			accountName, artifactName, tool, tail,
		)
	}
	return fmt.Sprintf(
		"account %s: the live %s identity differs from the one kae applied, so %s can name the wrong account; something outside kae rewrote it (a manual login, or a change in how %s maintains it) — %s",
		accountName, artifactName, tool, tool, tail,
	)
}
