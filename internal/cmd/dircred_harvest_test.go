package cmd

// Tests of the harvest that writeDirCredential and the pin mode toggle run
// before they replace a directory's credential copy.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// The other side of the same seam, and the reason the guard is keyed on *why* the
// harvest refused rather than on the refusal alone: positive evidence that the copy is
// somebody else's means this account's credential is elsewhere and fine, so the bind
// must still take effect. Keeping here would silently leave the directory running an
// account the user just bound away from — which is what re-pinning it is *for*.
//
// Same fixture as TestWriteDirCredentialRefusesToHarvestAnotherAccountsCredential, and
// deliberately a different half of it: that one asserts nothing is filed under this
// account, this one asserts the write still happens. Each fails on its own mutation
// (Unattributed marking versus the harvest's attribution gate).
func TestWriteDirCredentialStillReplacesAConflictingCopy(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, storeDir := bindClaudeHere(t, app, "main")
	// Every reader of this store says the copy is side's, so the store really does hold
	// another account's credential and this bind may replace it.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir),
		claudeOAuthPayload(sideToken, now.Add(8*time.Hour)))
	writeFile(t, filepath.Join(storeDir, ".claude.json"), claudeIdentityFile("side-uuid"))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir)); !strings.Contains(got, mainToken) {
		t.Fatalf("a conflicting copy must still be replaced by the bound account: %s", got)
	}
	if strings.Contains(stderr, "kept it rather than replacing it") {
		t.Fatalf("positive evidence must not take the keep branch: %q", stderr)
	}
}

// The third refusal is deliberately NOT on the keep branch, and this pins that choice so
// nobody widens it by reading the comment as "kae keeps what it cannot judge". A payload
// kae can neither read nor date may be a working login in a shape kae has not been
// taught — but keeping it would make a corrupted account store unrepairable by `kae pin`,
// and manual deletion the only escape. The trade-off is docs/ROADMAP.md's to settle; the
// behaviour here is the one that shipped.
func TestWriteDirCredentialStillReplacesAnUnreadableCopy(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, credDir := bindClaudeHere(t, app, "main")
	// Structurally valid for the artifact layer, but carrying no field kae can date.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir),
		`{"claudeAiOauth":{"accessTokenRenamedUpstream":"live-and-working"}}`)

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, mainToken) {
		t.Fatalf("the unreadable arm still applies the snapshot: %s", got)
	}
	if strings.Contains(stderr, "kept it rather than replacing it") {
		t.Fatalf("only the attribution refusal keeps the copy: %q", stderr)
	}
	if !strings.Contains(stderr, "this write replaces it") {
		t.Fatalf("replacing a copy kae cannot judge must be said out loud: %q", stderr)
	}
}

// A tombstone — what claude writes after a refresh it could not complete — is a
// fully-formed payload with both tokens blanked, so presence cannot stand in for
// "there is a login here". Harvesting one would overwrite a working snapshot with a
// dead credential, which no later kae command could undo.
func TestWriteDirCredentialDoesNotHarvestTombstone(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, credDir := bindClaudeHere(t, app, "main")
	// Blank tokens with a deadline still in the future: only Revoked separates this
	// from a healthy copy.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir), fmt.Sprintf(
		`{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":%d,"refreshTokenExpiresAt":%d}}`,
		now.Add(8*time.Hour).UnixMilli(), now.Add(27*24*time.Hour).UnixMilli(),
	))
	writeFile(t, filepath.Join(credDir, ".claude.json"), claudeIdentityFile("main-uuid"))

	be := testBackend(t, app)
	if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir, false); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
		t.Fatalf("a tombstone was harvested over a usable snapshot: %s", got)
	}
}

// The harvest is claude-only, and stays that way until another tool's rotation is
// measured: without that measurement "the newest copy" is a guess, and a wrong
// guess destroys the working credential (docs/ROADMAP.md § Rotation is measured for
// claude only). codex's file store is copied into bound directories exactly like
// claude's, so it is the one that would break first if this gate were dropped.
func TestWriteDirCredentialDoesNotHarvestUnmeasuredTool(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	seedCodex(t, app, "codex-main-token")
	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, constants.ToolCodex, "main")
	})
	mustExit(t, constants.ExitOK, code, out)
	credDir := t.TempDir()
	// A JWT-dated codex credential far ahead of the snapshot's: it would win any
	// expiry comparison, and must still not be harvested.
	writeFile(t, filepath.Join(credDir, "auth.json"),
		`{"tokens":{"access_token":"`+jwtWithExp(app.Now().Add(9*time.Hour))+`"}}`)

	be := testBackend(t, app)
	if err := app.writeDirCredential(ctx, be, constants.ToolCodex, "main", credDir, false); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolCodex, "main"); !strings.Contains(got, "codex-main-token") {
		t.Fatalf("an unmeasured tool's live copy was harvested: %s", got)
	}
}

// The harvest inside writeDirCredential can only see the store it is writing, and the
// operation that hurts most binds the directory's **credential** to a different store: a
// re-bind to another account. (A `-s` ↔ `-i` toggle moves only the config store since the
// per-account split — the credential stays put.) Without a pin-level pass, the new
// store is built from the account snapshot while the copy the tool refreshed sits in
// the old one — so the directory the user just bound holds the credential rotation
// has already invalidated, with every offline check green and no message. Found by
// review (execution-type, 2026-08-04) after the first version of this fix shipped
// only the chokepoint half.
//
// This also pins the wiring the pass depends on: the superseded store here is the
// *shared* one, whose account is recorded nowhere but the binding being replaced, so
// it fails if `prev` is ever read after the fragment is rewritten.
func TestRunPinModeToggleHarvestsTheSupersededStoreFirst(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))

	if code := runPin(ctx, app, opts, "main", modeShared, false); code != constants.ExitOK {
		t.Fatalf("pin --shared exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pinID := paths.PinID(cwd)
	shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
	// The tool refreshed the credential in the shared store, in place.
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	if code := runPin(ctx, app, opts, "main", modeIsolated, false); code != constants.ExitOK {
		t.Fatalf("pin --isolated exit %d", code)
	}

	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("the superseded store's newer credential was not harvested: %s", got)
	}
	isolated := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", isolated)); !strings.Contains(got, refreshed) {
		t.Fatalf("the newly bound store holds a credential that can no longer refresh: %s", got)
	}
}

// refusalLines counts the stderr lines that report a credential kae did not harvest.
func refusalLines(stderr string) int {
	n := 0
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "not harvesting") || strings.Contains(line, "could not preserve") {
			n++
		}
	}
	return n
}

// What a refusal *says*, which is a contract of its own: nine mutations to the
// reporting survived the suite before this test existed (execution-type review, round
// 2), including "print the login remedy for every refusal" — the one case where kae's
// own comment says a login would mint a chain invalidating what it just harvested.
//
// Three rules, all measured through `kae pin` rather than through the helpers:
// exactly one message per refused store (the store being written is looked at by both
// the pin-level pass and the write path, and one refusal read as two problems); the
// remedy appears only when the refusal is *missing evidence*; and a leftover store the
// command does not touch is not mentioned at all.
func TestRunPinReportsOneRefusalPerStoreWithTheRightRemedy(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))

	if code := runPin(ctx, app, opts, "main", modeShared, false); code != constants.ExitOK {
		t.Fatalf("pin --shared exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pinID := paths.PinID(cwd)
	shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
	// A leftover isolated store from no binding this pin still has: its account cannot
	// be attributed from the replaced (shared) fragment, and the command does not touch
	// it either way.
	leftover := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "side")
	mkdirs(t, leftover)
	writeFile(t, filepath.Join(leftover, ".credentials.json"),
		claudeOAuthPayload("sk-ant-oat01-LEFTOVER-eeee", now.Add(9*time.Hour)))

	// Missing evidence: the store holds a newer copy and no identity cache to compare.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared),
		claudeOAuthPayload("sk-ant-oat01-MAIN-REFRESHED-cccc", now.Add(8*time.Hour)))
	if err := os.Remove(filepath.Join(shared, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	_, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeShared, false) })

	// Counted per line, not per phrase: one message carries several of these markers, and
	// counting phrases made this assertion fail on correct output.
	if n := refusalLines(stderr); n != 1 {
		t.Fatalf("one refused store must produce one message, got %d:\n%s", n, stderr)
	}
	if !strings.Contains(stderr, "log in inside that directory") || !strings.Contains(stderr, cwd) {
		t.Fatalf("a missing-evidence refusal must name the bound directory to log in to:\n%s", stderr)
	}
	if strings.Contains(stderr, leftover) {
		t.Fatalf("a store this command does not touch must not be reported:\n%s", stderr)
	}
	// The consequence this message states has to be the one the write applies. Both halves
	// are asserted because each was unguarded and each failed on its own: the state, which
	// is how a re-pin came to destroy the copy with every test green, and the wording,
	// measured 2026-08-08 — leaving the old "and this bind replaces it" clause in place
	// while the write kept the copy survived the entire suite.
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", shared)); !strings.Contains(got, "sk-ant-oat01-MAIN-REFRESHED-cccc") {
		t.Fatalf("a copy kae could not attribute must be kept, not overwritten: %s", got)
	}
	// "Leaving it where it is" is the one clause true in every shape this arm reaches — a
	// refusal defers the delete, and for a pre-split binding the write does not touch this
	// store at all. The wording it replaced ("and this bind replaces it") was false about a
	// copy kae kept, and survived the whole suite until this assertion existed.
	if !strings.Contains(stderr, "so kae is leaving it where it is") {
		t.Fatalf("the primary voice must state what actually happens to the copy:\n%s", stderr)
	}
	if strings.Contains(stderr, "this bind replaces it") {
		t.Fatalf("the replace wording must not survive where nothing replaced it:\n%s", stderr)
	}

	// Positive evidence: the copy belongs to another account. Same store, so still one
	// message — and no remedy, because this account's own credential is fine.
	writeFile(t, filepath.Join(shared, ".claude.json"), claudeIdentityFile("side-uuid"))
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared),
		claudeOAuthPayload(sideToken, now.Add(10*time.Hour)))
	_, stderr = captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeShared, false) })

	if n := refusalLines(stderr); n != 1 {
		t.Fatalf("one refused store must produce one message, got %d:\n%s", n, stderr)
	}
	if strings.Contains(stderr, "log in inside") {
		t.Fatalf("a copy that belongs to another account must not come with a login remedy:\n%s", stderr)
	}
	// The positive half of the consequence clause, and the only one in the repo: every other
	// assertion on this string fails if it is *present*, so swapping which half of the pair
	// `replacedNow` compares made the clause disappear everywhere and survived the suite
	// (execution-type review, 2026-08-08). Here the write really does replace — same
	// account, same store — so it has to say so.
	if !strings.Contains(stderr, "and this bind replaces it") {
		t.Fatalf("a store the write really does overwrite must be reported as replaced:\n%s", stderr)
	}
}

// The harvest reads a store twice in one command by design — the pin-level pass
// classifies it, the chokepoint reads it again before writing — and on darwin each read
// is a `security` invocation, so a re-pin would double the keychain accesses (and the
// prompts) for every bound claude directory. `kae pin` therefore opts into the same
// per-command read cache the switch path uses, and this counts it the same way
// `TestSwitchCoalescesKeychainReads` does (efficiency lens, 2026-08-04).
func TestRunPinCoalescesTheHarvestKeychainReads(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := overlayTestApp(t)
		app.Env.GOOS = "darwin"
		chdirTemp(t)
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))
		if code := runPin(ctx, app, opts, "main", modeIsolated, false); code != constants.ExitOK {
			t.Fatalf("pin --isolated exit %d", code)
		}

		sim.readW = 0
		if code := runPin(ctx, app, opts, "main", modeIsolated, false); code != constants.ExitOK {
			t.Fatalf("re-pin exit %d", code)
		}

		if sim.readW != 1 {
			t.Fatalf("a re-pin must read the bound store's item once, got %d", sim.readW)
		}
	})
}

// The case a suppression keyed on the store's *kind* silenced completely: the pass ran
// and had nothing to say, so nobody said anything while a live login was overwritten.
// Reached without any exotic state — `kae unpin` keeps the store on purpose, so a re-pin
// has no previous binding to attribute it from (reading-type review, round 3).
func TestRunPinReportsARefusalThePinLevelPassCannotAttribute(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))

	if code := runPin(ctx, app, opts, "main", modeShared, false); code != constants.ExitOK {
		t.Fatalf("pin --shared exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	shared := app.Paths.SharedDir(paths.PinID(cwd), constants.ToolClaude)
	// A bind that *does* report a refusal for this store first, so its record exists
	// before the one under test. That record is scoped to one bind and cleared when the
	// next pass starts; left to live as long as the App it silences the report below,
	// which is how it made a two-phase test pass for the wrong reason.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared),
		claudeOAuthPayload("sk-ant-oat01-NO-IDENTITY-ffff", now.Add(6*time.Hour)))
	if err := os.Remove(filepath.Join(shared, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if _, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeShared, false) }); refusalLines(stderr) != 1 {
		t.Fatalf("setup expected one reported refusal:\n%s", stderr)
	}

	// Someone else's newer copy in the store, and no binding left to attribute it from.
	writeFile(t, filepath.Join(shared, ".claude.json"), claudeIdentityFile("side-uuid"))
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", shared),
		claudeOAuthPayload(sideToken, now.Add(8*time.Hour)))
	if code := runUnpin(ctx, app, opts, false); code != constants.ExitOK {
		t.Fatalf("unpin exit %d", code)
	}

	_, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeShared, false) })

	if n := refusalLines(stderr); n != 1 {
		t.Fatalf("overwriting a copy nobody could attribute must be reported exactly once, got %d:\n%s",
			n, stderr)
	}
	be := testBackend(t, app)
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, sideToken) {
		t.Fatalf("an unattributable copy must not be filed under main: %s", got)
	}
}

// The same hole one store shape over. An **isolated** store is attributable from its
// own path, so the pass reaches its report switch and leaves by the "this operation
// does not touch it" arm rather than the unattributable gate the test above covers —
// and marking the store there would silence the write path exactly as the kind-based
// suppression did (execution-type review, round 3: that mutation survived the suite
// while a re-pin overwrote the only refreshable copy in silence).
func TestRunPinReportsARefusalForAnIsolatedStoreThePassSkips(t *testing.T) {
	app := overlayTestApp(t)
	chdirTemp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	captureClaudeAt(t, app, "side", sideToken, now.Add(time.Hour))

	if code := runPin(ctx, app, opts, "main", modeIsolated, false); code != constants.ExitOK {
		t.Fatalf("pin --isolated exit %d", code)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	store := app.Paths.IsolatedConfigDir(paths.PinID(cwd), constants.ToolClaude, "main")
	writeFile(t, filepath.Join(store, ".claude.json"), claudeIdentityFile("side-uuid"))
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", store),
		claudeOAuthPayload(sideToken, now.Add(8*time.Hour)))
	// After unpin there is no binding to replace, so the pass skips this store rather
	// than reporting it — which is exactly when the write path has to speak.
	if code := runUnpin(ctx, app, opts, false); code != constants.ExitOK {
		t.Fatalf("unpin exit %d", code)
	}

	_, stderr := captureStderr(t, func() int { return runPin(ctx, app, opts, "main", modeIsolated, false) })

	if n := refusalLines(stderr); n != 1 {
		t.Fatalf("overwriting a copy the pass skipped must be reported exactly once, got %d:\n%s", n, stderr)
	}
}

// A snapshot that is itself a tombstone must lose to any usable live copy, whatever
// their deadlines say: a tombstone carries whatever `expiresAt` the tool left in it, so
// comparing timestamps alone would let a dead snapshot outrank a working credential and
// the bind would write the tombstone over it. Dropping the `!stored.Revoked` half of the
// cutoff survived the suite (execution-type review, round 3).
func TestWriteDirCredentialPrefersAUsableCopyOverATombstonedSnapshot(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	// The snapshot is a tombstone dated *later* than the live copy.
	seedClaude(t, app, mainToken, "main-uuid")
	writeFile(t, filepath.Join(app.Env.Home, ".claude", ".credentials.json"), fmt.Sprintf(
		`{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":%d}}`,
		now.Add(20*time.Hour).UnixMilli(),
	))
	code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, constants.ToolClaude, "main")
	})
	mustExit(t, constants.ExitOK, code, out)

	// A reader is needed only so attribution has something to confirm from; what this
	// test observes is the comparison, which happens before the store is written.
	_, storeDir := bindClaudeHere(t, app, "main")
	const alive = "sk-ant-oat01-MAIN-ALIVE-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir), claudeOAuthPayload(alive, now.Add(2*time.Hour)))

	be := testBackend(t, app)
	if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeDir, false); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, alive) {
		t.Fatalf("a usable copy must beat a tombstoned snapshot whatever the dates say: %s", got)
	}
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir)); !strings.Contains(got, alive) {
		t.Fatalf("the tombstone must not be written over a working login: %s", got)
	}
}

// Equal deadlines are **not** harvested, and two of the smoke block's cases rest on
// that: reusing one `expiresAt` on both sides is what made them prove nothing, which
// only holds as an argument while the comparison stays strict. Loosening it to
// `>=` survived the suite (execution-type review, round 3).
func TestWriteDirCredentialDoesNotHarvestAnEqualDeadline(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	deadline := app.Now().Add(time.Hour)
	captureClaudeAt(t, app, "main", mainToken, deadline)
	_, credDir := bindClaudeHere(t, app, "main")
	const other = "sk-ant-oat01-SAME-DEADLINE-dddd"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir), claudeOAuthPayload(other, deadline))

	be := testBackend(t, app)
	if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir, false); err != nil {
		t.Fatalf("writeDirCredential: %v", err)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, other) {
		t.Fatalf("an equal deadline is not newer, so nothing may be harvested: %s", got)
	}
}

// The defect this whole harvest exists for: the tool refreshes the copy *inside* a
// bound directory, in place, and claude's refresh token is single-use — so writing
// the account snapshot over that copy does not regress the directory to an older
// login, it logs it out, hours later, with every offline check green
// (docs/VALIDATION.md). The bind must take the newer copy into the snapshot first
// and then write *that*.
//
// The evidence that the copy is this account's comes from the directories that read the
// store, so the fixture binds one: the identity cache the bind leaves in that directory
// is what confirms the harvest. Its opposite number is
// TestWriteDirCredentialKeepsANewerCopyItCannotAttribute, which is the same store with no
// reader at all — the pair is what separates "the reader gate" from "the harvest".
func TestWriteDirCredentialHarvestsNewerLiveCredential(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, storeDir := bindClaudeHere(t, app, "main")
	// The tool refreshed the account's copy in place, in the store every directory bound
	// to claude/main reads.
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	// A capture time that has moved on, so the recorded one is proof this snapshot
	// was rewritten rather than merely unchanged.
	later := now.Add(2 * time.Hour)
	app.Now = func() time.Time { return later }

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir)); !strings.Contains(got, refreshed) {
		t.Fatalf("the bind overwrote the newer live credential: %s", got)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, refreshed) {
		t.Fatalf("the newer credential was not harvested into the snapshot: %s", got)
	}
	if !strings.Contains(stderr, "harvested") {
		t.Fatalf("a harvest must be reported: %q", stderr)
	}
	if strings.Contains(stderr, refreshed) {
		t.Fatalf("a credential must never reach a message: %q", stderr)
	}
	acc, _, err := account.Load(app.Paths.AccountDir(constants.ToolClaude, "main"))
	if err != nil || !acc.CapturedAt.Equal(later) {
		t.Fatalf("captured_at must follow the harvested payload: %v (err %v)", acc.CapturedAt, err)
	}
}

// The other direction, which must stay cheap and silent: the snapshot is the newer
// copy, so the bind writes it as it always did. Without this the harvest would be
// free to run backwards and overwrite a good snapshot from a directory nobody has
// opened in weeks.
func TestWriteDirCredentialKeepsSnapshotWhenLiveIsOlder(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(8*time.Hour))
	_, credDir := bindClaudeHere(t, app, "main")
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir),
		claudeOAuthPayload("sk-ant-oat01-MAIN-OLD-dddd", now.Add(time.Hour)))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, mainToken) {
		t.Fatalf("the snapshot must be applied when it is the newer copy: %s", got)
	}
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
		t.Fatalf("the older live copy must not reach the snapshot: %s", got)
	}
	if strings.Contains(stderr, "harvested") {
		t.Fatalf("nothing was harvested, so nothing may be reported: %q", stderr)
	}
}

// Attribution is the guard that makes the harvest safe, because a store can hold a
// credential that is not the account's at all. The reachable shape is a **login as
// somebody else inside a bound directory**: the directory binds claude/main, the user runs
// `/login` there as side, and the account's store now holds side's credential — usually
// the newer one, since it is the one in daily use — while the directory's identity cache
// says side too. Harvesting that would file side's token under main's name, after which
// nothing offline can tell: the token is opaque, so live, snapshot and doctor all agree on
// a label that is simply wrong.
//
// Note which state this is *not*, since it used to be: a re-bind of this directory from
// one account to another. That one is now the case the model deliberately declines to
// judge (TestRunPinRebindBetweenAccountsPreservesTheTargetsLiveCredential) — the directory
// being re-bound is not yet a reader of the new account's store, so its stale label says
// nothing about it.
func TestWriteDirCredentialRefusesToHarvestAnotherAccountsCredential(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, storeDir := bindClaudeHere(t, app, "main")
	// Logged in as side inside the bound directory: the store's copy and the reader's
	// label both name side.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir), claudeOAuthPayload(sideToken, now.Add(8*time.Hour)))
	writeFile(t, filepath.Join(storeDir, ".claude.json"), claudeIdentityFile("side-uuid"))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, sideToken) {
		t.Fatalf("another account's token was filed under this one: %s", got)
	}
	if !strings.Contains(stderr, "not harvesting") {
		t.Fatalf("declining to harvest must be said out loud: %q", stderr)
	}
	// The bind still does its job: the directory ends up on the account it names.
	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir)); !strings.Contains(got, mainToken) {
		t.Fatalf("the bind must still apply the bound account: %s", got)
	}
}

// **The first bind of a directory has no evidence to attribute from, and the store it
// would overwrite belongs to the account, not to the directory.** writeDirCredential's
// comment carries why attribution refuses there; what this test adds is the reason it went
// unnoticed — every test above binds a directory first, so none of them is a first bind.
// Measured end to end 2026-08-08: use claude in one worktree, bind a second, and both are
// dead up to 8h later with nothing left for doctor to compare.
//
// Deliberately **not** bound, which is what makes it the opposite number of
// TestWriteDirCredentialHarvestsNewerLiveCredential: no directory reads this account's
// credential store yet, so nothing can say whose login the copy is. Missing evidence here
// is the absence of a *reader*, not the absence of a file in this directory — seeding a
// `.claude.json` beside the store would change nothing, because a directory no binding
// points at is evidence about nothing.
func TestWriteDirCredentialKeepsANewerCopyItCannotAttribute(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	credDir := t.TempDir() // an unbound config dir: nothing reads the account's store
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir),
		claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})

	if got := readFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir)); !strings.Contains(got, refreshed) {
		t.Fatalf("the only copy that can still refresh was destroyed: %s", got)
	}
	// Kept is not harvested: the copy stays where it is and is *not* filed under this
	// account, because the reason kae kept it is that it could not tell whose it is.
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, refreshed) {
		t.Fatalf("an unattributable copy must not be filed under this account: %s", got)
	}
	if !strings.Contains(stderr, "kept it rather than replacing it") {
		t.Fatalf("keeping the copy must be said out loud: %q", stderr)
	}
	if strings.Contains(stderr, "this write replaces it") {
		t.Fatalf("the overwrite wording must not survive here: %q", stderr)
	}
	// No remedy at this site by design: it holds a store path, not the bound directory a
	// login would have to happen in. The pin-level pass carries the remedy, and when it
	// speaks this message is suppressed — asserted by the pin-level tests.
	if strings.Contains(stderr, "kae relogin") || strings.Contains(stderr, "kae add --no-login") {
		t.Fatalf("the chokepoint must not name a remedy for a store path: %q", stderr)
	}
	if strings.Contains(stderr, refreshed) {
		t.Fatalf("a credential must never reach a message: %q", stderr)
	}
	// No label either, and this is the load-bearing half. kae's own label is exactly the
	// evidence the next bind's attribution reads, so writing it here let `kae pin` again
	// confirm against a cache kae had planted and harvest the copy this bind refused —
	// measured 2026-08-08, filing another account's token under this one's name. Absence is
	// the honest record; the next cache here is the tool's own.
	if _, err := os.Stat(filepath.Join(credDir, ".claude.json")); !os.IsNotExist(err) {
		t.Fatalf("kae must not plant the label it would later read as attribution (err %v)", err)
	}
}

// The harvest is claude-only by declaration, not by accident, and the declaration is
// what a new tool has to earn: without a measured rotation "the newest copy" is a
// guess, and a wrong guess destroys the working credential. Enumerated rather than
// spot-checked, so adding a tool to that predicate cannot pass unnoticed.
//
// The second half is why a tool needs more than the measurement: attribution reads
// an identity-only artifact, and a tool that declares none can never satisfy it — so
// the harvest would be dead code for it. codex is in that state today, which is why
// the end-to-end codex test below cannot prove this gate on its own.
func TestHarvestIsDeclaredForMeasuredToolsOnly(t *testing.T) {
	ctx := context.Background()
	app := testApp(t, nil)
	for _, tool := range constants.Tools {
		if got := rotatesSingleUse(tool); got != (tool == constants.ToolClaude) {
			t.Fatalf("rotatesSingleUse(%s) = %v; only claude's rotation is measured "+
				"(docs/VALIDATION.md, docs/ROADMAP.md)", tool, got)
		}
		if !rotatesSingleUse(tool) {
			continue
		}
		ad, err := adapter.ForTool(tool)
		if err != nil {
			t.Fatalf("adapter for %s: %v", tool, err)
		}
		specs, err := ad.Artifacts(ctx, app.Env)
		if err != nil {
			t.Fatalf("artifacts for %s: %v", tool, err)
		}
		identities := 0
		for _, sp := range specs {
			if sp.IdentityOnly {
				identities++
			}
		}
		if identities == 0 {
			t.Fatalf("%s harvests but declares no identity-only artifact, so dirIdentityConfirms "+
				"can never confirm and the harvest would never fire", tool)
		}
	}
}

// A payload that parses but carries no deadline cannot be ordered against anything,
// so it is never harvested — the guard the selection rule rests on. A mutation that
// dropped the zero check survived the whole suite (execution-type review).
//
// Two shapes, and they reach the classifier by different routes, which is why the second
// row exists: with `expiresAt` **absent** claude never sets `Known`, while a **non-numeric**
// one sets `Known` and parses to the zero time. The second used to be classified
// "nothing to lose" — see TestPruneDirCredentialsKeepsACopyItCannotDate for what that
// licensed — so an older comment here claiming the delete path "protected this state from
// the start" was true only of the first row.
func TestWriteDirCredentialDoesNotHarvestUndatedCredential(t *testing.T) {
	for _, tc := range []struct{ name, oauth string }{
		{"no expiresAt at all", `{"accessToken":"sk-ant-oat01-UNDATED-ffff","refreshToken":"r"}`},
		{"an expiresAt of the wrong type", `{"accessToken":"sk-ant-oat01-UNDATED-ffff","refreshToken":"r","expiresAt":"1814400000000"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := overlayTestApp(t)
			ctx := context.Background()
			captureClaudeAt(t, app, "main", mainToken, app.Now().Add(time.Hour))
			_, credDir := bindClaudeHere(t, app, "main")
			writeFile(t, dirCredFile(app, constants.ToolClaude, "main", credDir),
				`{"claudeAiOauth":`+tc.oauth+`}`)

			be := testBackend(t, app)
			_, stderr := captureStderr(t, func() int {
				if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", credDir, false); err != nil {
					t.Fatalf("writeDirCredential: %v", err)
				}
				return 0
			})
			if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); !strings.Contains(got, mainToken) {
				t.Fatalf("an undated credential must not be harvested: %s", got)
			}
			// And the overwrite is not silent. A payload kae cannot judge may still be a
			// login, and on this path it is the only early signal of an upstream format
			// change — `upstream_version` skips a version string it cannot parse.
			if !strings.Contains(stderr, "cannot read or date the copy already there") {
				t.Fatalf("overwriting a copy kae cannot judge must be reported: %q", stderr)
			}
		})
	}
}

// An identity cache that resolves *outside* the store labels the real home, not this
// directory (a pre-v0.16.0 bind linked it there), so it cannot attribute this
// store's credential — even when it happens to name the same account. Dropping that
// branch survived the whole suite (execution-type review).
func TestWriteDirCredentialDoesNotHarvestThroughSharedIdentity(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	now := app.Now()
	captureClaudeAt(t, app, "main", mainToken, now.Add(time.Hour))
	_, storeDir := bindClaudeHere(t, app, "main")
	const refreshed = "sk-ant-oat01-MAIN-REFRESHED-cccc"
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", storeDir), claudeOAuthPayload(refreshed, now.Add(8*time.Hour)))
	// The reader's own cache replaced by a link back out to the real home — the shape a
	// pre-v0.16.0 shared bind left — still naming the account being bound.
	identityFile := filepath.Join(storeDir, ".claude.json")
	if err := os.Remove(identityFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(app.Env.Home, ".claude.json"), identityFile); err != nil {
		t.Fatal(err)
	}
	// Positive first: the link resolves to a payload that names the *same* account, so the
	// refusal below is the escape guard rather than a disagreement or an unreadable file.
	if got := readFile(t, identityFile); !strings.Contains(got, "main-uuid") {
		t.Fatalf("the link must resolve to the real home's payload naming main, got %q", got)
	}

	be := testBackend(t, app)
	_, stderr := captureStderr(t, func() int {
		if err := app.writeDirCredential(ctx, be, constants.ToolClaude, "main", storeDir, false); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
		return 0
	})
	if got := snapshotPayload(t, app, be, constants.ToolClaude, "main"); strings.Contains(got, refreshed) {
		t.Fatalf("a credential attributed by the real home's label was harvested: %s", got)
	}
	if !strings.Contains(stderr, "shared with the real tool home") {
		t.Fatalf("the reason must be the shared label, not something else: %q", stderr)
	}
}
