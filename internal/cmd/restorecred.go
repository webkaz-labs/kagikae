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

// recordedCredential is what a backup holds for one tool's credential. It carries two
// questions rather than one verdict, because the restore paths want **opposite** things
// from a payload that is there but dead: `run -s` must restore it, `kae rollback` must
// report it. One shared "usable" gate silenced the second, which is the worse of the two.
//
// Orderable is derived rather than a second field, so the fourth combination — not
// present yet orderable — is unrepresentable by the type instead of by one careful
// constructor, which is the shape that rots.
type recordedCredential struct {
	Info freshness.Info
	// Present is false when the backup has no readable credential record for this tool:
	// no record, one recorded as absent, or a payload gone from the secret store. Nothing
	// is being handed back, so neither path says anything about it — `run -s` restores the
	// absence and `kae rollback` removes the credential, which is what the backup says.
	Present bool
}

// Orderable reports whether the recorded copy can take part in an ordering at all. A copy
// with no comparable deadline is "superseded" by any live login, so `run -s` skipping on
// one would leave the account it applied for a single child in the real home for good.
func (r recordedCredential) Orderable() bool { return r.Present && orderable(r.Info) }

// readRecordedCredential reads what meta recorded for tool's credential. Known ways to
// come back not-Present or not-Orderable are on those two, and are **not a closed
// set** — each is a property of the payload rather than a list of causes.
//
// The `err != nil` arm converges with `!found` and cannot be killed on its own: every
// backend that fails a read reports the payload as absent, so no fixture can reach the
// arm alone (measured 2026-08-05). It stays as the statement that an unreadable payload
// is not a credential, and the behaviour it guards is pinned through the pair.
func readRecordedCredential(ctx context.Context, be secret.Backend, meta backup.Meta, tool string) recordedCredential {
	rec, ok := backupRecord(meta, tool, credentialArtifactName(tool))
	if !ok || !rec.Present {
		return recordedCredential{}
	}
	data, found, err := be.Get(ctx, rec.SecretRef)
	if err != nil || !found {
		return recordedCredential{}
	}
	return recordedCredential{Info: freshnessOf(tool, data), Present: true}
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
		if !identityComparable(stored, live.Data) {
			return false
		}
		if identityDiffers(sp, stored, live.Data) {
			return false
		}
		confirmed = true
	}
	return confirmed
}

// liveCredentialFreshness reads tool's credential through specs and reports it only when
// it is a copy `supersedes` could order. ok is false for a missing spec and for every
// state readLiveCredential declines, which is the same set `orderable` rejects — stated
// here so the two callers below share one reading of "is there a live copy worth
// comparing" instead of each remembering the three steps.
//
// A caller that ignored ok would still behave correctly, because readLiveCredential
// zeroes the Info for exactly those states and `supersedes` rejects a zero newer side —
// so mutating the check away survives the suite by construction (measured 2026-08-05).
// It is the precondition stated, not a filter carrying its own weight.
func liveCredentialFreshness(ctx context.Context, tool string, specs []artifact.Spec) (freshness.Info, bool) {
	sp, ok := specByName(specs, credentialArtifactName(tool))
	if !ok {
		return freshness.Info{}, false
	}
	_, live, state := readLiveCredential(ctx, tool, sp)
	return live, state == liveUsable
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
	// Orderable, not merely Present: a recorded copy kae cannot order is "superseded" by
	// any live login, and skipping on that would leave the account this run applied
	// temporarily in the real home for good.
	recorded := readRecordedCredential(ctx, be, meta, plan.Tool)
	if !recorded.Orderable() {
		return false
	}
	// plan was re-resolved after the child (refreshPlan), so this reads the store the tool
	// writes now rather than the one it wrote before.
	live, ok := liveCredentialFreshness(ctx, plan.Tool, plan.Specs)
	if !ok || !supersedes(live, recorded.Info) {
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
		recorded := readRecordedCredential(ctx, be, meta, tool)
		if !recorded.Present {
			continue
		}
		snap := app.snapshotCredentialFreshness(ctx, be, tool, accountName)
		live := app.attributedLiveFreshness(ctx, be, meta, tool, current[tool])
		where, remedy := "", ""
		switch {
		case supersedes(live, recorded.Info) && supersedes(live, snap):
			where = "the live store"
			remedy = fmt.Sprintf("the newer copy is left only in backup %s (kae rollback --to %s)", preID, preID)
		case supersedes(snap, recorded.Info):
			where = fmt.Sprintf("snapshot %s/%s", tool, accountName)
			remedy = fmt.Sprintf("apply the newer copy afterwards with: kae use %s %s", tool, accountName)
		default:
			continue
		}
		// Three wordings, because `Orderable()` is false for two reasons that license
		// different statements — the same split readLiveCredential draws between
		// liveNothing and liveUnreadable, and flattening it told a user to undo a rollback
		// that had just restored a credential which was probably fine.
		//
		// Each says what kae **observed**, never what the tool can do, because the two come
		// apart. `Revoked` is the adapter reading that the payload carries no usable token,
		// and it is derived from the token fields being empty *or absent* — so a login whose
		// token keys were renamed upstream lands here while working perfectly
		// (docs/VALIDATION.md, the claude row on what `Revoked` can and cannot distinguish).
		// "carries no usable token" survives both readings; "cannot log in" did not. A
		// payload kae cannot parse at all gets less again: only that the two are not
		// comparable.
		//
		// The remedy is the same in every case — where the other copy is, is what a user
		// acts on — and each names `for <tool>/<account>`, so a message about the wrong tool
		// is detectable. The ordering pair is the default so neither string is written twice.
		cause := fmt.Sprintf("recorded an older %s credential for %s/%s than the one in %s",
			tool, tool, accountName, where)
		consequence := fmt.Sprintf("so this rollback leaves %s without the copy that can still refresh", tool)
		switch {
		case recorded.Orderable():
		case recorded.Info.Revoked:
			cause = fmt.Sprintf("recorded a %s credential for %s/%s that carries no usable token, while %s holds one",
				tool, tool, accountName, where)
		default:
			cause = fmt.Sprintf("recorded a %s credential for %s/%s that kae cannot compare with the one in %s",
				tool, tool, accountName, where)
			consequence = fmt.Sprintf("so kae cannot tell which of the two %s can still refresh", tool)
		}
		fmt.Fprintf(os.Stderr,
			"kae: warning: backup %s %s, and %s's refresh token rotates single-use, %s; %s\n",
			meta.ID, cause, tool, consequence, remedy)
	}
}

// snapshotCredentialFreshness reads the freshness of accountName's snapshotted
// credential. An account that cannot be loaded or read reads as the zero Info, which
// supersedes treats as having no deadline worth comparing.
//
// A snapshot that reads *successfully* but cannot be ordered — a tombstone, or one whose
// deadline no longer parses — comes back **as it is**, not zeroed. That is safe only
// because every consumer routes it through `supersedes`, which gates its own newer side
// on `orderable` and degrades the other side to a zero cutoff either way. A consumer that
// compared this reading directly (`needsRelogin`, or a "the snapshot is newer" bool of
// its own) would be the next instance of the subset bug `orderable` exists to prevent.
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
	live, ok := liveCredentialFreshness(ctx, tool, specs)
	if !ok || !liveLoginMatchesBackup(ctx, be, meta, tool, specs) {
		return freshness.Info{}
	}
	return live
}
