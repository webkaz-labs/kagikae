package cmd

// Tests of pruneDirCredentials, the directory credential freshness read and
// the purge path.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// prunableApp is the shared setup of the sweep tests: a temp-HOME app on the
// keychain driver (darwin — a file store has nothing invisible to sweep) plus the
// pin id of a directory that is not the test's cwd, since the sweep takes the id
// from its caller.
func prunableApp(t *testing.T) (*App, string) {
	t.Helper()
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	return app, paths.PinID(t.TempDir())
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

// The teardown mirrors the write gate: a per-directory keychain item exists only
// where the adapter declares the item bindable, so that is exactly what a sweep
// removes. The stale store here is an isolated store for an account the directory no
// longer binds, holding the tombstone a failed refresh leaves behind — nothing to
// harvest, so the sweep is free to remove it, which keeps this test about the thing it
// is named for: *which* item the sweep addresses.
//
// The payload is the tombstone **as measured**: blank tokens *and* `expiresAt: 0`
// (docs/VALIDATION.md's claude row). It used to carry a future deadline, which is not a
// shape claude has been observed to produce and which the sweep now keeps rather than
// deletes — blank tokens with a live deadline are indistinguishable from an upstream
// token-key rename, and that is a working login. So this fixture is now the positive
// control for the one arm that licenses a delete, and the sibling below is its negative.
func TestPruneDirCredentialsRemovesSupersededItem(t *testing.T) {
	app, pinID := prunableApp(t)
	stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
	bound := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	mkdirs(t, stale, bound)

	const tombstone = `{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":0}}`
	fake := &runnertest.Fake{Stdout: tombstone, Code: 0}
	var lines []string
	runner.With(fake, func() {
		lines = app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, "", map[string]bool{bound: true}, fragmentInfo{}, false)
	})

	args := strings.Join(fake.Args, " ")
	if !strings.Contains(args, "delete-generic-password") || !strings.Contains(args, sha8Of(stale)) {
		t.Fatalf("the superseded item was not deleted: %q %v", fake.Name, fake.Args)
	}
	if strings.Contains(args, sha8Of(bound)) {
		t.Fatalf("the bound store's item must survive: %v", fake.Args)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], stale) {
		t.Fatalf("the removal must be reported: %v", lines)
	}
}

// The negative control for the arm above, in the two shapes that are **not** the measured
// tombstone. `Revoked` is derived from token fields that are empty *or absent*, so both of
// these read as revoked while carrying a live deadline — and an upstream rename of the
// token keys is exactly the second one. docs/VALIDATION.md justifies that wide reading by
// saying it makes every path *decline* to touch the copy; for this consumer it would have
// licensed the delete instead, which is the defect this pins.
func TestPruneDirCredentialsKeepsARevokedLookingCopyWithALiveDeadline(t *testing.T) {
	for _, tc := range []struct{ name, oauth string }{
		{"blank tokens, live deadline", `{"accessToken":"","refreshToken":"","expiresAt":%d}`},
		{"token keys renamed away", `{"tokenV2":"sk-ant-oat01-LIVE","expiresAt":%d}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, pinID := prunableApp(t)
			oauth := fmt.Sprintf(tc.oauth, app.Now().Add(time.Hour).UnixMilli())
			stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
			bound := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
			mkdirs(t, stale, bound)

			fake := &runnertest.Fake{Stdout: `{"claudeAiOauth":` + oauth + `}`, Code: 0}
			var lines []string
			var stderr string
			runner.With(fake, func() {
				_, stderr = captureStderr(t, func() int {
					lines = app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, "",
						map[string]bool{bound: true}, fragmentInfo{}, false)
					return 0
				})
			})
			if strings.Contains(strings.Join(fake.Args, " "), "delete-generic-password") {
				t.Fatalf("a copy kae cannot judge must be kept, not deleted: %v", fake.Args)
			}
			if len(lines) != 0 {
				t.Fatalf("nothing was removed, so nothing may be reported as removed: %v", lines)
			}
			if !strings.Contains(stderr, "cannot read or date the claude credential") {
				t.Fatalf("keeping it must say why: %q", stderr)
			}
		})
	}
}

// A usable copy whose account no longer exists is the case where "delete it" and
// "keep it" are both defensible, so it turns on what the user asked for. `kae unpin
// --purge` asked for these credentials to go: keeping one would strand a live token
// no kae command can address. A `kae pin` sweep asked to *bind* something, and
// deleting there destroys a login nobody named — which `kae account rename` reaches
// through kae's own re-bind remedy, making the renamed account's newest copy the
// casualty (reading-type review, round 2).
func TestPruneDirCredentialsDeletesALostAccountsCopyOnlyWhenPurging(t *testing.T) {
	for _, purging := range []bool{false, true} {
		t.Run(map[bool]string{false: "housekeeping", true: "purge"}[purging], func(t *testing.T) {
			sim := &keychainSim{}
			runner.With(sim, func() {
				app := testApp(t, map[string]string{"USER": "me"})
				app.Env.GOOS = "darwin"
				// Captured, then removed, so the item exists under the account claude's own
				// rule derives while the snapshot is gone — what `rm` and `rename` both leave.
				captureClaudeFromKeychain(t, app, sim, "side", sideToken, app.Now().Add(time.Hour))
				if err := os.RemoveAll(app.Paths.AccountDir(constants.ToolClaude, "side")); err != nil {
					t.Fatal(err)
				}
				pinID := paths.PinID(t.TempDir())
				stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
				mkdirs(t, stale)
				sim.payload = claudeOAuthPayload("sk-ant-oat01-REFRESHED-cccc", app.Now().Add(8*time.Hour))
				sim.ops = nil

				var lines []string
				_, stderr := captureStderr(t, func() int {
					lines = app.pruneDirCredentials(context.Background(), testBackend(t, app),
						pinID, "", nil, fragmentInfo{}, purging)
					return 0
				})

				deleted := strings.Contains(strings.Join(sim.ops, ","), "delete")
				if deleted != purging {
					t.Fatalf("purging=%v: deleted=%v (ops %v)", purging, deleted, sim.ops)
				}
				if len(lines) != map[bool]int{false: 0, true: 1}[purging] {
					t.Fatalf("purging=%v: reported %v", purging, lines)
				}
				// Asserted per direction, because accepting either message in both made the
				// test pass on a run that fell into the wrong branch entirely.
				want := "left in place"
				if purging {
					want = "deleted without being kept anywhere"
				}
				if !strings.Contains(stderr, want) {
					t.Fatalf("purging=%v: stderr must say %q: %q", purging, want, stderr)
				}
				if !purging && !strings.Contains(stderr, "kae unpin --purge") {
					t.Fatalf("housekeeping must name the command that would remove it: %q", stderr)
				}
			})
		})
	}
}

// The fourth arm of the delete gate, and the one nothing covered: the account exists but
// kae could not read its credential snapshot. A later run may still harvest this copy, so
// the item stays — flipping it to "delete" survived the whole suite (execution-type
// review, round 3).
func TestPruneDirCredentialsKeepsWhenTheSnapshotPayloadIsUnreadable(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		captureClaudeFromKeychain(t, app, sim, "side", sideToken, app.Now().Add(time.Hour))
		// The snapshot still declares its payload; the payload itself is gone from the
		// backend, which is the state `doctor secret_missing` reports.
		be := testBackend(t, app)
		acc, found, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "side"))
		if err != nil || !found {
			t.Fatalf("load side: found=%v err=%v", found, err)
		}
		if err := be.Delete(ctx, acc.Artifacts[credentialArtifactName(constants.ToolClaude)].SecretRef); err != nil {
			t.Fatal(err)
		}
		pinID := paths.PinID(t.TempDir())
		stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
		mkdirs(t, stale)
		sim.payload = claudeOAuthPayload("sk-ant-oat01-REFRESHED-cccc", app.Now().Add(8*time.Hour))
		sim.ops = nil

		var lines []string
		_, stderr := captureStderr(t, func() int {
			lines = app.pruneDirCredentials(ctx, be, pinID, "", nil, fragmentInfo{}, true)
			return 0
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a copy a later run could still harvest must not be deleted: %v", sim.ops)
		}
		if len(lines) != 0 {
			t.Fatalf("nothing was removed, so nothing may be reported: %v", lines)
		}
		if !strings.Contains(stderr, "could not read snapshot") {
			t.Fatalf("the branch that kept it must be the one that says so: %q", stderr)
		}
	})
}

// The opposite: a store kae cannot *read* is kept. An unrecognized payload is what
// an upstream format change looks like from here, and it cannot be told apart from a
// working login — so the sweep must not treat "I could not parse it" as "there is
// nothing in it". The pre-change code deleted unconditionally, and the fixtures that
// hid this were payloads no real store holds.
// The double (keychainSim, not runnertest.Fake) is load-bearing: the sweep probes the
// item's *attributes* before deciding, and a flat fake answers that probe with the
// same body it answers the payload read with. A first version of this test used one
// and passed because the probe read "absent" — proving nothing about the branch it
// names. Only a double that tells `-w` from an attributes read reaches the gate.
func TestPruneDirCredentialsKeepsUnreadableCredential(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		// Captured first, so the item exists under the account claude's own rule derives
		// rather than one this test made up: the sweep probes attributes with that
		// account, and a hand-set one answers "absent" and skips the gate entirely.
		captureClaudeFromKeychain(t, app, sim, "side", sideToken, app.Now().Add(time.Hour))
		pinID := paths.PinID(t.TempDir())
		stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
		mkdirs(t, stale)
		// Then the payload becomes one kae cannot judge: it is claude's shape, so the
		// artifact layer's structure guard passes it through, but it carries no deadline,
		// so nothing can order it against the snapshot. A payload the structure guard
		// *rejects* lands in the same state by a different route (the read errors), which
		// is why this fixture is the one that reaches the branch — measured, because the
		// rejected-shape fixture passed this test without ever getting there.
		sim.payload = `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r"}}`
		sim.ops = nil

		var lines []string
		_, stderr := captureStderr(t, func() int {
			lines = app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, "", nil, fragmentInfo{}, false)
			return 0
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a credential kae cannot read must not be deleted: %v", sim.ops)
		}
		if len(lines) != 0 {
			t.Fatalf("nothing was removed, so nothing may be reported: %v", lines)
		}
		if !strings.Contains(stderr, "cannot read") {
			t.Fatalf("keeping it must be explained: %q", stderr)
		}
	})
}

// Two stores a sweep must never touch: one the binding still points at, and one
// whose credential is not a bindable keychain item at all. codex's keyring item is
// the second case — kae never wrote it for this directory, so removing it would
// delete a login kae does not own (the same asymmetry writeDirCredential refuses).
func TestPruneDirCredentialsSkipsBoundAndUnbindableStores(t *testing.T) {
	app, pinID := prunableApp(t)
	claudeStore := app.Paths.SharedDir(pinID, constants.ToolClaude)
	codexStore := app.Paths.SharedDir(pinID, constants.ToolCodex)
	mkdirs(t, claudeStore, codexStore)
	seedKeyringCodex(t, codexStore)

	fake := &runnertest.Fake{Code: 0}
	var lines []string
	runner.With(fake, func() {
		lines = app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, "", map[string]bool{claudeStore: true}, fragmentInfo{}, false)
	})

	if fake.Name != "" {
		t.Fatalf("nothing was superseded, yet the keychain was touched: %q %v", fake.Name, fake.Args)
	}
	if len(lines) != 0 {
		t.Fatalf("nothing to report: %v", lines)
	}
}

// onlyTool is what keeps a single-tool re-bind from sweeping a sibling tool's
// store, which the same fragment still binds.
func TestPruneDirCredentialsHonorsToolFilter(t *testing.T) {
	app, pinID := prunableApp(t)
	claudeStore := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
	codexStore := app.Paths.IsolatedConfigDir(pinID, constants.ToolCodex, "side")
	mkdirs(t, claudeStore, codexStore)

	fake := &runnertest.Fake{Code: 0}
	runner.With(fake, func() {
		app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, constants.ToolCodex, nil, fragmentInfo{}, false)
	})
	if strings.Contains(strings.Join(fake.Args, " "), sha8Of(claudeStore)) {
		t.Fatalf("a sibling tool's store must be out of scope: %v", fake.Args)
	}
}

// The delete primitive treats "no such item" as success, so the sweep probes
// first: a store that never had an item must not be announced as cleaned up, and
// nothing should be deleted for it either.
func TestPruneDirCredentialsReportsNothingWhenNoItemExists(t *testing.T) {
	app, pinID := prunableApp(t)
	stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
	mkdirs(t, stale)

	fake := &runnertest.Fake{Stderr: "security: " + keychain.NotFoundMarker, Code: 44}
	var lines []string
	runner.With(fake, func() {
		lines = app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, "", nil, fragmentInfo{}, false)
	})

	if len(lines) != 0 {
		t.Fatalf("an absent item must not be reported as removed: %v", lines)
	}
	if args := strings.Join(fake.Args, " "); strings.Contains(args, "delete-generic-password") {
		t.Fatalf("nothing to delete, yet a delete ran: %v", fake.Args)
	}
}

// captureClaudeFromKeychain captures an account on the darwin driver, where the
// live credential comes from the keychain double rather than a file, with a known
// expiry so a later copy can be put ahead of it.
func captureClaudeFromKeychain(t *testing.T, app *App, sim *keychainSim, accountName, token string, expiresAt time.Time) {
	t.Helper()
	seedClaude(t, app, token, accountName+"-uuid")
	// account "" makes the double match whatever account claude's own rule derives for
	// the environment under test — hardcoding one made a capture fail with auth_missing
	// wherever that rule falls back (no USER in the env), which reads as a broken test
	// rather than a wrong fixture.
	sim.present, sim.account = true, ""
	sim.payload = claudeOAuthPayload(token, expiresAt)
	code, out := captureStdout(t, func() int {
		return runCapture(context.Background(), app, commonOpts{Format: formatText},
			constants.ToolClaude, accountName)
	})
	mustExit(t, constants.ExitOK, code, out)
}

// Deleting a per-directory keychain item is unrecoverable — a re-pin rebuilds a
// store's credential from the account snapshot, so a copy that was never harvested
// into one is simply gone — and that item can hold the newest copy of the account's
// credential, the only one that can still refresh. So the sweep harvests before it
// deletes. An isolated store names its own account: kae composed the path from it.
func TestPruneDirCredentialsHarvestsBeforeDeleting(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		now := app.Now()
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, now.Add(time.Hour))

		pinID := paths.PinID(t.TempDir())
		stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
		mkdirs(t, stale)
		writeFile(t, filepath.Join(stale, ".claude.json"), claudeIdentityFile("main-uuid"))
		// The store as the tool left it: refreshed in place, ahead of the snapshot.
		const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
		sim.payload = claudeOAuthPayload(refreshed, now.Add(8*time.Hour))

		be := testBackend(t, app)
		var lines []string
		_, stderr := captureStderr(t, func() int {
			lines = app.pruneDirCredentials(ctx, be, pinID, "", nil, fragmentInfo{}, false)
			return 0
		})

		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
			t.Fatalf("the item was deleted without harvesting what it held: %s", got)
		}
		if !strings.Contains(stderr, "harvested") {
			t.Fatalf("the harvest must be reported: %q", stderr)
		}
		if !strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a harvested item must still be swept: %v", sim.ops)
		}
		if len(lines) != 1 || !strings.Contains(lines[0], stale) {
			t.Fatalf("the removal must be reported: %v", lines)
		}
	})
}

// A live copy kae cannot **date** is not one it has nothing to lose by deleting. This is
// the fourth consumer of the same predicate the recapture guards split on, and the only
// one where folding "nothing to lose" together with "kae cannot tell" licenses a
// **delete**: readLiveCredential classified `Known && !Revoked &&` undated as
// `liveNothing`, so harvestBeforeDelete removed the item without harvesting it and
// without a word, reporting it as a *superseded* credential.
//
// The fixture is TestPruneDirCredentialsHarvestsBeforeDeleting's, with one difference: the
// payload's `expiresAt` is a string. That is what an upstream type change produces, and
// what a comment here used to call unobservable.
func TestPruneDirCredentialsKeepsACopyItCannotDate(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		now := app.Now()
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, now.Add(time.Hour))

		pinID := paths.PinID(t.TempDir())
		stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
		mkdirs(t, stale)
		writeFile(t, filepath.Join(stale, ".claude.json"), claudeIdentityFile("main-uuid"))
		const live = "sk-ant-oat01-MAIN-UNDATED-dddd"
		sim.payload = `{"claudeAiOauth":{"accessToken":"` + live +
			`","refreshToken":"r","expiresAt":"1814400000000"}}`

		be := testBackend(t, app)
		var lines []string
		_, stderr := captureStderr(t, func() int {
			lines = app.pruneDirCredentials(ctx, be, pinID, "", nil, fragmentInfo{}, false)
			return 0
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("a live copy kae cannot date must be kept, not deleted: %v", sim.ops)
		}
		if len(lines) != 0 {
			t.Fatalf("nothing was removed, so nothing may be reported as removed: %v", lines)
		}
		// Kept for a reason the user can act on — this is the only offline signal of an
		// upstream format change on this path.
		if !strings.Contains(stderr, "cannot read or date the claude credential") {
			t.Fatalf("keeping it must say why: %q", stderr)
		}
		// And the snapshot is untouched: kae could not order the two, so it may not adopt
		// the copy either.
		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
			t.Fatalf("a copy kae cannot date must not be adopted either: %s", got)
		}
	})
}

// The delete half of the decodability gate, which is the outcome that matters most:
// an item that would previously have been harvested and swept is now **kept**.
//
// Two identity payloads that are byte-identical and neither an account record used to
// confirm the store (identityDiffers returned false, so nothing refused), which let the
// sweep harvest and then delete. They agree about nothing, so the attribution is not
// there, and an item kae cannot attribute is left in place — a leftover secret rather
// than a login destroyed by a cleanup, the rule this whole sweep is built on.
//
// Written because the review round that would have measured this seam did not finish:
// the write path's version of the same change is
// TestWriteDirCredentialRefusesTwoIdentitiesThatAreNotAccountRecords, and "an item that
// used to be deleted is now kept" is the other half, in the direction that cannot be
// undone.
func TestPruneDirCredentialsKeepsAnItemItCannotAttributeToARecord(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		now := app.Now()
		const nonRecord = `{"oauthAccount":null,"projects":{"/repo":{}}}`
		// Captured with a live identity cache that is well-formed JSON and not an account
		// record, so that is what the snapshot records. Written after the seed inside
		// captureClaudeFromKeychain would overwrite it, so the capture is done by hand.
		seedClaude(t, app, mainToken, "main-uuid")
		writeFile(t, filepath.Join(app.Env.Home, ".claude.json"), nonRecord)
		sim.present, sim.account = true, ""
		sim.payload = claudeOAuthPayload(mainToken, now.Add(time.Hour))
		code, out := captureStdout(t, func() int {
			return runCapture(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
		})
		mustExit(t, constants.ExitOK, code, out)

		pinID := paths.PinID(t.TempDir())
		stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
		mkdirs(t, stale)
		writeFile(t, filepath.Join(stale, ".claude.json"), nonRecord)
		// Newer than the snapshot, so the sweep reaches attribution at all.
		const refreshed = "sk-ant-oat01-MAIN-REFRESHED-eeee"
		sim.payload = claudeOAuthPayload(refreshed, now.Add(8*time.Hour))
		sim.ops = nil

		be := testBackend(t, app)
		var lines []string
		_, stderr := captureStderr(t, func() int {
			lines = app.pruneDirCredentials(ctx, be, pinID, "", nil, fragmentInfo{}, false)
			return 0
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("an item kae cannot attribute must not be deleted: %v", sim.ops)
		}
		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, refreshed) {
			t.Fatalf("it must not be harvested either, on two sides agreeing about nothing: %s", got)
		}
		if len(lines) != 0 {
			t.Fatalf("nothing was removed, so nothing may be reported: %v", lines)
		}
		if !strings.Contains(stderr, "kae cannot read the identity records it would compare") {
			t.Fatalf("keeping it must name the reason: %q", stderr)
		}
		if strings.Contains(stderr, refreshed) {
			t.Fatalf("a credential must never reach a message: %q", stderr)
		}
	})
}

// The shared mechanism's store records no account in its path — one directory
// serves every account this pin ever bound there — so the only thing that can name
// the account behind its credential is the binding being replaced. That is why the
// sweep is handed the previous fragment, and why the callers read it before
// overwriting or removing it.
func TestPruneDirCredentialsHarvestsSharedStoreFromPreviousBinding(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		now := app.Now()
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, now.Add(time.Hour))

		pinID := paths.PinID(t.TempDir())
		shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
		mkdirs(t, shared)
		writeFile(t, filepath.Join(shared, ".claude.json"), claudeIdentityFile("main-uuid"))
		const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
		sim.payload = claudeOAuthPayload(refreshed, now.Add(8*time.Hour))

		be := testBackend(t, app)
		prev := fragmentInfo{Mode: modeShared, Accounts: map[string]string{constants.ToolClaude: "main"}}
		lines := app.pruneDirCredentials(ctx, be, pinID, "", nil, prev, false)

		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
			t.Fatalf("the shared store's credential was not harvested: %s", got)
		}
		if len(lines) != 1 {
			t.Fatalf("the removal must be reported: %v", lines)
		}
	})
}

// And when nothing can name the account — no account in the path and no readable
// binding to ask — the item stays. A leftover secret is a smaller fault than a
// cleanup that destroys the last copy of a login, and kae must not adopt a payload
// it cannot attribute into some other account's snapshot to avoid that.
func TestPruneDirCredentialsKeepsUnattributableCredential(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		now := app.Now()
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, now.Add(time.Hour))

		pinID := paths.PinID(t.TempDir())
		shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
		mkdirs(t, shared)
		writeFile(t, filepath.Join(shared, ".claude.json"), claudeIdentityFile("main-uuid"))
		sim.payload = claudeOAuthPayload("sk-ant-oat01-UNKNOWN-eeee", now.Add(8*time.Hour))
		sim.ops = nil

		be := testBackend(t, app)
		var lines []string
		_, stderr := captureStderr(t, func() int {
			// A previous binding kae could not read: the zero fragment attributes nothing.
			lines = app.pruneDirCredentials(ctx, be, pinID, "", nil, fragmentInfo{}, false)
			return 0
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("an unattributable credential must not be deleted: %v", sim.ops)
		}
		if len(lines) != 0 {
			t.Fatalf("nothing was removed, so nothing may be reported: %v", lines)
		}
		if !strings.Contains(stderr, "cannot tell which account") {
			t.Fatalf("keeping it must be explained: %q", stderr)
		}
		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, "UNKNOWN") {
			t.Fatalf("an unattributable payload was adopted into a snapshot: %s", got)
		}
	})
}

// The mode gate on where a shared store's account comes from. A shared store can be
// left over from a binding *older* than the one being replaced — kae keeps a store
// so a re-pin restores its sessions — and taking the account from a fragment that
// was in isolated mode would attribute it to whichever account that binding named.
// Filing one account's token under another's is undetectable afterwards, so the
// answer here is "kae cannot tell", not a guess. Dropping the gate survived the
// whole suite (execution-type review, 2026-08-04).
func TestPruneDirCredentialsWillNotAttributeASharedStoreFromAnIsolatedBinding(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		now := app.Now()
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, now.Add(time.Hour))

		pinID := paths.PinID(t.TempDir())
		shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
		mkdirs(t, shared)
		writeFile(t, filepath.Join(shared, ".claude.json"), claudeIdentityFile("main-uuid"))
		sim.payload = claudeOAuthPayload("sk-ant-oat01-LEFTOVER-eeee", now.Add(8*time.Hour))
		sim.ops = nil

		be := testBackend(t, app)
		// The binding being replaced was isolated, so it says nothing about who owns a
		// leftover *shared* store.
		prev := fragmentInfo{Mode: modeIsolated, Accounts: map[string]string{constants.ToolClaude: "main"}}
		var lines []string
		_, stderr := captureStderr(t, func() int {
			lines = app.pruneDirCredentials(ctx, be, pinID, "", nil, prev, false)
			return 0
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("an unattributable credential must not be deleted: %v", sim.ops)
		}
		if len(lines) != 0 {
			t.Fatalf("nothing was removed, so nothing may be reported: %v", lines)
		}
		if !strings.Contains(stderr, "cannot tell which account") {
			t.Fatalf("the refusal must name its reason: %q", stderr)
		}
		if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, "LEFTOVER") {
			t.Fatalf("a leftover store's credential was filed under main: %s", got)
		}
	})
}

// The read side of the same gate, and the reason it has to be there. The doctor
// sweep resolves a bound directory's credential to judge its freshness; for a tool
// whose keychain item does *not* move with its isolation variable, the item it
// would read is the **global** login. Reporting that as this directory's credential
// blames a healthy global login on a directory it has nothing to do with — and a
// stale one on every bound directory at once.
//
// Asserted through the subprocess seam rather than through the absence of a check,
// because on a non-darwin host every keychain read fails anyway and a missing check
// would prove nothing.
func TestDirCredentialFreshnessRefusesGlobalKeychainStore(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	credDir := t.TempDir()
	seedKeyringCodex(t, credDir)

	fake := &runnertest.Fake{Code: 0}
	var ok bool
	runner.With(fake, func() {
		_, ok = app.dirCredentialFreshness(context.Background(),
			dirStore{Tool: constants.ToolCodex, Dir: credDir})
	})
	if ok {
		t.Fatal("a store kae cannot bind per directory must not be judged")
	}
	if fake.Name != "" {
		t.Fatalf("the refusal must happen before the keychain is touched, ran %q %v", fake.Name, fake.Args)
	}
}

// The permitted counterpart: claude's item is namespaced by the config dir, so the
// sweep reads the item that directory owns — proven by the per-directory hash in
// the service name, the same assertion the write side makes.
func TestDirCredentialFreshnessReadsTheDirScopedItem(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	credDir := t.TempDir()

	fake := &runnertest.Fake{
		Stdout: `{"claudeAiOauth":{"accessToken":"a","refreshToken":"","expiresAt":1577836800000}}`,
		Code:   0,
	}
	var info freshness.Info
	var ok bool
	runner.With(fake, func() {
		info, ok = app.dirCredentialFreshness(context.Background(),
			dirStore{Tool: constants.ToolClaude, Dir: credDir})
	})
	if !ok || !info.Known {
		t.Fatalf("a dir-scoped store must be judged, got %+v (ok=%v)", info, ok)
	}
	if !strings.Contains(strings.Join(fake.Args, " "), sha8Of(credDir)) {
		t.Fatalf("freshness read the wrong item (not this directory's): %v", fake.Args)
	}
	// And the payload it read is the one that decided the verdict.
	if !needsRelogin(info, app.Now()) {
		t.Fatalf("an expiry of 2020 with no refresh token must read as needing a re-login: %+v", info)
	}
}

// `--purge` is the way out for a copy kae cannot judge, and a bind's sweep still is not.
// Keeping it during housekeeping is right — it may be a working login in a shape kae has
// not been taught — but keeping it under `--purge` stranded a secret **nothing kae offers
// can remove**: a per-directory item is addressable only from the string kae hashes its
// service name from, and this sweep is the only path to it. Both arms say what they do.
func TestPurgeIsTheWayOutForACredentialKaeCannotJudge(t *testing.T) {
	for _, tc := range []struct {
		name        string
		purging     bool
		wantDeleted bool
		wantSays    string
	}{
		{"a bind's sweep keeps it and names the way out", false, false, "kae unpin --purge in that directory removes it"},
		{"--purge takes it and says what it destroys", true, true, "nor tell which account it belonged to"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, pinID := prunableApp(t)
			stale := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
			bound := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
			mkdirs(t, stale, bound)

			// Unreadable at all: not JSON kae's parser recognizes as a credential.
			fake := &runnertest.Fake{Stdout: `{"somethingElse":true}`, Code: 0}
			var lines []string
			var stderr string
			runner.With(fake, func() {
				_, stderr = captureStderr(t, func() int {
					lines = app.pruneDirCredentials(context.Background(), testBackend(t, app), pinID, "",
						map[string]bool{bound: true}, fragmentInfo{}, tc.purging)
					return 0
				})
			})
			deleted := strings.Contains(strings.Join(fake.Args, " "), "delete-generic-password")
			if deleted != tc.wantDeleted {
				t.Fatalf("deleted=%v want %v (args %v, stderr %q)", deleted, tc.wantDeleted, fake.Args, stderr)
			}
			if !strings.Contains(stderr, tc.wantSays) {
				t.Errorf("want stderr to contain %q: %q", tc.wantSays, stderr)
			}
			if tc.wantDeleted && (len(lines) != 1 || !strings.Contains(lines[0], stale)) {
				t.Errorf("a purge that removed it must report it: %v", lines)
			}
			if !tc.wantDeleted && len(lines) != 0 {
				t.Errorf("nothing was removed, so nothing may be reported: %v", lines)
			}
		})
	}
}
