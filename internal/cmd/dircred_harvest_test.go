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

// An **isolated** re-bind is the one case where the store the binding leaves is not the
// shared one, so the set of stores this operation moves off has to come from the
// replaced fragment's *own* mode. Forcing that lookup to shared mode survived the suite:
// the genuine refusal went unreported and a leftover shared store's was reported instead
// (execution-type review, round 3).
func TestRunRebindIsolatedReportsTheStoreItLeaves(t *testing.T) {
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
	pinID := paths.PinID(cwd)
	leaving := app.Paths.IsolatedConfigDir(pinID, constants.ToolClaude, "main")
	// A newer copy with nothing to attribute it by, so the harvest refuses and the pass
	// has something to report about the store the re-bind moves off.
	writeFile(t, dirCredFile(app, constants.ToolClaude, "main", leaving),
		claudeOAuthPayload("sk-ant-oat01-MAIN-REFRESHED-cccc", now.Add(8*time.Hour)))
	if err := os.Remove(filepath.Join(leaving, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	// A leftover shared store from no binding at all, which must not be what gets named.
	shared := app.Paths.SharedDir(pinID, constants.ToolClaude)
	mkdirs(t, shared)
	writeFile(t, filepath.Join(shared, ".credentials.json"),
		claudeOAuthPayload("sk-ant-oat01-LEFTOVER-eeee", now.Add(9*time.Hour)))

	_, stderr := captureStderr(t, func() int { return runRebind(ctx, app, opts, constants.ToolClaude, "side", false) })

	// The message names the **account** whose copy could not be kept and the bound
	// directory to log in to — not the store path, deliberately, since a store path is
	// not somewhere a login works. What the mutation removes is the message itself: with
	// `replaced` computed as if the previous binding were shared, the store being left is
	// no longer in it and nothing is reported.
	if !strings.Contains(stderr, "held for claude/main") ||
		!strings.Contains(stderr, "log in inside that directory") {
		t.Fatalf("the store the binding leaves must be reported, with the remedy:\n%s", stderr)
	}
	if strings.Contains(stderr, shared) || strings.Contains(stderr, "LEFTOVER") {
		t.Fatalf("a leftover store this re-bind does not touch must not be named:\n%s", stderr)
	}
	_ = leaving
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

// `kae account rename` warns and tells the user to re-bind with `kae pin <tool> <new>`,
// which is **runRebind** — so that is the sweep the renamed account's newest copy meets,
// and its `purging=false` needs pinning separately from `runPin`'s (reading-type review,
// round 3: mutating this one to `true` passed the whole suite).
//
// That first sentence describes a rename that no longer reaches this sweep: the rename
// harvests before its first write now, so a renamed account's copy is already in the
// snapshot (TestAccountRenameHarvestsWhatTheBoundDirectoryIsReading). What still arrives
// here is an account gone by **removal**, and only for a store holding its own credential
// — the shape makePreSplit builds below. Measured 2026-08-16; a sweep for the unscoped
// version of that sentence reached the docs and both dircred.go comments and missed this
// one, because it grepped prose files.
func TestRunRebindSweepKeepsALostAccountsCredential(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := overlayTestApp(t)
		app.Env.GOOS = "darwin"
		chdirTemp(t)
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		captureClaudeFromKeychain(t, app, sim, "main", mainToken, app.Now().Add(time.Hour))
		captureClaudeFromKeychain(t, app, sim, "side", sideToken, app.Now().Add(time.Hour))
		if code := runPin(ctx, app, opts, "main", modeIsolated, false); code != constants.ExitOK {
			t.Fatalf("pin --isolated exit %d", code)
		}
		// Pre-split: since the credential became the account's rather than the
		// directory's, a bind sweep does not consider it at all — it is kept the way an
		// account snapshot is kept, with nothing to say. The branch under test is the
		// one that still runs, for the per-directory item an older kae left behind.
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		makePreSplit(t, app, constants.ToolClaude, "main", cwd,
			app.Paths.IsolatedConfigDir(paths.PinID(cwd), constants.ToolClaude, "main"))
		if err := os.RemoveAll(app.Paths.AccountDir(constants.ToolClaude, "main")); err != nil {
			t.Fatal(err)
		}
		sim.payload = claudeOAuthPayload("sk-ant-oat01-MAIN-REFRESHED-cccc", app.Now().Add(8*time.Hour))
		sim.ops = nil

		_, stderr := captureStderr(t, func() int {
			return runRebind(ctx, app, opts, constants.ToolClaude, "side", false)
		})

		if strings.Contains(strings.Join(sim.ops, ","), "delete") {
			t.Fatalf("the re-bind kae itself recommends after a rename must not delete that copy: %v", sim.ops)
		}
		if !strings.Contains(stderr, "no account named claude/main exists any more") {
			t.Fatalf("keeping it must name the branch that kept it: %q", stderr)
		}
	})
}
