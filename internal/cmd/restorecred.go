package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// Restoring a backup writes a credential kae recorded at some earlier moment over
// whatever is live now. For a tool whose refresh token rotates single-use
// (rotatesSingleUse) that is not a regression to an older login but a logout of the
// only copy that could still refresh — reported as success, with every offline check
// green, until the tool fails up to an access token's ~8h later. docs/ROADMAP.md
// § Every credential copy owns the design; this file is the two restore paths' share
// of it, and both of them order copies with the same `supersedes`.
//
// The two paths answer differently on purpose. `kae run -s` **skips** the restore:
// it put the account there itself, so the newer copy is the same account's and the
// restore is a no-op apart from destroying it. `kae rollback` **warns**: the user
// explicitly asked to go back, so kae says what it costs and where the newer copy
// still is, and restores anyway.

// backupRecord picks one tool's artifact record out of a backup.
func backupRecord(meta backup.Meta, tool, name string) (backup.ArtifactRecord, bool) {
	for _, rec := range meta.Artifacts {
		if rec.Tool == tool && rec.Name == name {
			return rec, true
		}
	}
	return backup.ArtifactRecord{}, false
}

// backupCredentialFreshness reads the freshness of the credential meta recorded for
// tool. usable is false unless the recorded payload is a login this backup could
// still be handing back — known to the tool's parser, not a tombstone, and readable
// now. Known ways to get a false: no credential record for the tool, one recorded as
// absent, a payload gone from or unreadable in the secret store, a shape the parser
// does not recognize, and a tombstone. **Not a closed set**, which is why the check is
// a property of the payload rather than a list of causes.
//
// The strictness is what keeps the two callers honest, and it was a review finding
// rather than the first draft. Returning a *readable but dead* copy hands `supersedes`
// a zero cutoff, so any live login supersedes it — and `run -s` would then skip the
// restore, leaving the account it applied temporarily in the real home forever. That
// is the same outcome the absent case exists to prevent (a backup recording no
// credential is always restored as absent), so "present but dead" must not take the
// other branch. For `kae rollback` the strictness costs nothing worth having: a
// tombstone is unambiguously dead however it compares, and framing it as "older than
// the copy in X" would be a claim about ordering that a tombstone does not support.
//
// The `err != nil` arm converges with `!found` and cannot be killed on its own: every
// backend that fails a read reports the payload as absent, so no fixture can reach the
// arm alone (measured 2026-08-05). It stays as the statement that an unreadable payload
// is not a credential, and the behaviour it guards is pinned through the pair.
func backupCredentialFreshness(ctx context.Context, be secret.Backend, meta backup.Meta, tool string) (info freshness.Info, usable bool) {
	rec, ok := backupRecord(meta, tool, credentialArtifactName(tool))
	if !ok || !rec.Present {
		return freshness.Info{}, false
	}
	data, found, err := be.Get(ctx, rec.SecretRef)
	if err != nil || !found {
		return freshness.Info{}, false
	}
	recorded := freshnessOf(tool, data)
	if !recorded.Known || recorded.Revoked {
		return freshness.Info{}, false
	}
	return recorded, true
}

// liveLoginMatchesBackup reports whether the identity live now is the same account
// record meta recorded for tool — the attribution guard supersedes owes in the
// global frame, and the counterpart of dirIdentityConfirms for a bound directory.
//
// The reference side is the **backup**, not the account snapshot, because by the
// time run -s asks, its own recapture has already rewritten that snapshot with
// whatever the child left live: a child that logged in as somebody else has filed
// that login under this account's name, so the snapshot can no longer say who was
// there before. The backup can.
//
// Positive evidence is required, so every absent or unreadable side answers false.
// Treating "kae cannot tell" as a match is what would let a child's login as another
// account be kept as though it were this one — and a payload that is well-formed
// JSON but not an object names no account at all, which is why the decodability gate
// sits above the comparison: identityDiffers falls back to a byte comparison for
// those, right for the drift check and wrong for attribution in *both* directions
// (dirIdentityConfirms carries the measurement).
//
// Two of the refusals here **converge** with the ones after them, which was measured
// rather than assumed (2026-08-05) and is written down so nobody adds a test that
// cannot fail. `!live.Present` leaves an empty payload that the decodability gate
// declines anyway, and `!confirmed` cannot be reached while the only tool that gets
// here declares an identity-only artifact on every platform. They are a statement of
// what this function requires, not filters that carry weight of their own. The
// decodability gate, by contrast, is load-bearing in exactly one shape — both sides
// non-records *and* byte-identical, which is what `/oauthAccount: null` propagated to
// a store produces — and that shape is pinned by a test.
func liveLoginMatchesBackup(ctx context.Context, be secret.Backend, meta backup.Meta,
	tool string, specs []artifact.Spec,
) bool {
	confirmed := false
	for _, sp := range specs {
		if !sp.IdentityOnly {
			continue
		}
		rec, ok := backupRecord(meta, tool, sp.Name)
		if !ok || !rec.Present {
			return false
		}
		stored, found, err := be.Get(ctx, rec.SecretRef)
		if err != nil || !found {
			return false
		}
		live, err := artifact.ReadLive(ctx, sp)
		if err != nil || !live.Present {
			return false
		}
		_, storedIsRecord := freshness.DecodeObject(stored)
		_, liveIsRecord := freshness.DecodeObject(live.Data)
		if !storedIsRecord || !liveIsRecord {
			return false
		}
		if identityDiffers(sp, stored, live.Data) {
			return false
		}
		confirmed = true
	}
	return confirmed
}

// restoreWouldKillNewerLogin reports whether restoring meta's credential for plan's
// tool would write an older copy of that account's own login over the newer one now
// live. It is the run -s decision: true means skip the restore of that tool and
// leave the live store holding the copy that can still refresh.
//
// It can only be true for `kae run -s <tool> <the account that was already active>`.
// With a different account active before the run, the backup and the live store hold
// two *different* chains and putting the previous one back is exactly what run -s
// promises. With the same one, the pre-child copy the restore puts back is the copy
// the child's refresh invalidated.
//
// The account check therefore stays even though liveLoginMatchesBackup is the
// stronger evidence, and this is the one place the two are **not** interchangeable:
// on this path the live identity cache is not an independent observation, because
// run -s applied the target snapshot's identity into it moments earlier. A snapshot
// whose recorded identity is wrong — captured before v0.16.0 wrote identities, or
// mis-captured — would then have the live cache agreeing with the backup about an
// account whose credential is not there, and the skip would leave the target's token
// live while kae still records the previous account. Mutating this line away is not
// observable in a test built from correct snapshots, which is why it is stated here
// rather than pinned by one; the rollback path, where both identities are genuine
// reads, deliberately does without it.
//
// A backup that recorded no credential is left alone even when the child logged in:
// the restore's job is then to take the credential away again, and keeping the login
// instead would leave run -s having applied an account permanently.
func (app *App) restoreWouldKillNewerLogin(ctx context.Context, be secret.Backend,
	meta backup.Meta, plan toolPlan,
) bool {
	if !rotatesSingleUse(plan.Tool) || meta.ActiveBefore[plan.Tool] != plan.Account {
		return false
	}
	recorded, usable := backupCredentialFreshness(ctx, be, meta, plan.Tool)
	if !usable {
		return false
	}
	sp, ok := specByName(plan.Specs, credentialArtifactName(plan.Tool))
	if !ok {
		return false
	}
	// plan was re-resolved after the child (refreshPlan), so this reads the store the
	// tool writes now rather than the one it wrote before. The state check converges
	// with supersedes' own live-side guard — readLiveCredential zeroes the Info for
	// everything that is not a usable login — so it states the precondition rather
	// than filtering, and mutating it away survives the suite by construction.
	_, live, liveState := readLiveCredential(ctx, plan.Tool, sp)
	if liveState != liveUsable || !supersedes(live, recorded) {
		return false
	}
	return liveLoginMatchesBackup(ctx, be, meta, plan.Tool, plan.Specs)
}

// warnRestoringSupersededCredential reports every tool whose credential in meta is
// provably older than a copy of that same account kae can still see, so a rollback
// does not print "Rolled back to" while handing the tool a token that is already
// rejected. Warnings only, and emitted before the write they describe: going back is
// what the user asked for, and what kae can add is the cost and where the newer copy
// is. preID names the backup rollback took of the live state moments ago.
//
// Two places can hold the newer copy, and which one does decides the remedy — so
// they are compared against **each other** and not only against the recorded copy.
//
// The **account snapshot** covers everything that refreshed this account and was
// harvested back into it — a bound directory, run -s's recapture, a switch-away. It
// is that account's own record, so it needs no attribution, and its copy survives the
// rollback untouched: applying it is one `kae use` away.
//
// The **live store** covers the account that was refreshed in place with nothing
// harvesting it. What makes those two copies comparable is the identity attribution
// (liveLoginMatchesBackup), not `state.Active`: both sides here are payloads observed
// from the real store — one recorded when the backup was taken, one live now — so a
// mislabelled active account can neither create the finding nor hide it, and
// requiring the label to agree would only silence the case where it is wrong.
//
// The live store wins whenever it holds the later of the two, because that is the
// copy the rollback is about to overwrite, and after that the pre-rollback backup is
// the only place still holding it. Offering the `kae use` remedy there would name a
// copy that is not the newest — the remedy misdirection this repo has shipped before.
func (app *App) warnRestoringSupersededCredential(ctx context.Context, be secret.Backend,
	meta backup.Meta, preID string, current map[string][]artifact.Spec,
) {
	for _, tool := range meta.Tools {
		if !rotatesSingleUse(tool) {
			continue
		}
		// The account the restored credential belongs to. Without one, nothing names
		// the chain being restored and neither comparison below has a sound side.
		accountName := meta.ActiveBefore[tool]
		if accountName == "" {
			continue
		}
		recorded, usable := backupCredentialFreshness(ctx, be, meta, tool)
		if !usable {
			continue
		}
		snap := app.snapshotCredentialFreshness(ctx, be, tool, accountName)
		live := app.attributedLiveFreshness(ctx, be, meta, tool, current[tool])
		where, remedy := "", ""
		switch {
		case supersedes(live, recorded) && supersedes(live, snap):
			where = "the live store"
			remedy = fmt.Sprintf("the newer copy is left only in backup %s (kae rollback --to %s)", preID, preID)
		case supersedes(snap, recorded):
			where = fmt.Sprintf("snapshot %s/%s", tool, accountName)
			remedy = fmt.Sprintf("apply the newer copy afterwards with: kae use %s %s", tool, accountName)
		default:
			continue
		}
		fmt.Fprintf(os.Stderr,
			"kae: warning: backup %s recorded an older %s credential for %s/%s than the one in %s, and "+
				"%s's refresh token rotates single-use, so this rollback hands %s a token it can no longer "+
				"refresh; %s\n",
			meta.ID, tool, tool, accountName, where, tool, tool, remedy)
	}
}

// snapshotCredentialFreshness reads the freshness of accountName's snapshotted
// credential. An account that cannot be loaded or read reads as the zero Info, which
// supersedes treats as having no deadline worth comparing.
func (app *App) snapshotCredentialFreshness(ctx context.Context, be secret.Backend,
	tool, accountName string,
) freshness.Info {
	acc, found, err := account.Load(app.Paths.AccountDir(tool, accountName))
	if err != nil || !found {
		return freshness.Info{}
	}
	snap, err := app.accountFreshness(ctx, be, acc)
	if err != nil {
		return freshness.Info{}
	}
	return snap
}

// attributedLiveFreshness reads the freshness of the credential live now, but only
// when it is usable *and* demonstrably the same account's login as the one meta
// recorded. Anything else reads as the zero Info, so an unattributable copy can never
// win a comparison rather than being excluded by a separate branch the caller has to
// remember.
func (app *App) attributedLiveFreshness(ctx context.Context, be secret.Backend, meta backup.Meta,
	tool string, specs []artifact.Spec,
) freshness.Info {
	sp, ok := specByName(specs, credentialArtifactName(tool))
	if !ok {
		return freshness.Info{}
	}
	_, live, liveState := readLiveCredential(ctx, tool, sp)
	if liveState != liveUsable || !liveLoginMatchesBackup(ctx, be, meta, tool, specs) {
		return freshness.Info{}
	}
	return live
}
