package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// CmdRelogin drives a tool's own login flow into the store the current
// directory is bound to, then harvests the result back into that account's
// snapshot:
//
//	kae relogin [<tool>]
//
// It exists because the remedy it replaces is two facts a message could only
// ask the user to remember. `cd <dir> && claude /login` lands in the bound
// store **only** while the pin is active in that shell; with mise activation
// absent or the config untrusted the isolation variable is unset and the login
// refreshes the *real* home instead — the wrong account moves and the bound one
// is still stale. kae exports the variable itself here, so that hazard cannot
// happen rather than being warned about (docs/ROADMAP.md § Re-login UX). And a
// login inside a bound directory is only captured back the next time a bind or a
// sweep runs, so until then `kae use <tool> <account>` applies the older copy
// globally; this captures it back at the moment it is newest.
//
// The account is never an argument: which account a bound directory holds is the
// binding's answer, and a name typed here that disagreed with it would either be
// ignored or file one account's login under another's.
func CmdRelogin(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("relogin", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) > 1 {
		return usageError("usage: %s relogin [<tool>]", toolName)
	}
	explicitTool := ""
	if len(positionals) == 1 {
		tool, err := resolveToolArg(positionals[0])
		if err != nil {
			return finish(opts, err)
		}
		if err := validateTool(tool); err != nil {
			return finish(opts, err)
		}
		explicitTool = tool
	}
	return runRelogin(ctx, newApp(opts.ConfigPath), opts, explicitTool)
}

// runRelogin is CmdRelogin with the App injected, the split CmdPin/runPin use so
// tests drive it against a temp HOME.
func runRelogin(ctx context.Context, app *App, opts commonOpts, explicitTool string) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	// Unlike unpin, every step here needs the absolute path: the pin id names the
	// store, and the lock is taken on it. A cwd kae cannot resolve is fatal rather
	// than a warning, because guessing would log in somewhere else.
	absDir, err := cwdAbs()
	if err != nil {
		return finish(opts, fmt.Errorf("resolve the current directory: %w", err))
	}
	fragment, exists, err := readDirFragment()
	if err != nil {
		return finish(opts, err)
	}
	if !exists {
		return finish(opts, errf(constants.ExitNotFound,
			"this directory is not pinned, so there is no bound store to log in to; "+
				"to refresh a global account run: %s add --restore <tool> <account>", toolName))
	}
	tool, err := reloginTool(app, paths.PinID(absDir), fragment, explicitTool)
	if err != nil {
		return finish(opts, err)
	}
	// bound is true by construction (reloginTool resolved this tool through the same
	// call), and the value is what the fragment exports for it.
	storeDir, _ := app.boundStoreDir(paths.PinID(absDir), tool, fragment)
	accountName := fragment.Accounts[tool]
	// The credential half of the binding, read from the fragment rather than derived
	// from the account: a directory bound before the credential split keeps its
	// credential in the store, and exporting a per-account variable it was never
	// bound with would send the login to a store the directory does not read.
	dirs := bindDirs{Config: storeDir, Cred: fragment.CredDirs[tool]}
	command := loginCommand(tool)
	// The store path is **recomputed** from a hash of this directory's current path,
	// while what the tool will actually read there is the literal value in the
	// fragment's `[env]` block — which kae does not parse. The two agree for a
	// directory that has not moved, and a directory that has (renamed, or reached
	// through a symlinked parent, since os.Getwd answers logically) keeps its fragment
	// pointing at the old pin-id's store while this computes a new one. Logging in
	// there would create a store nothing reads and report success, which is the exact
	// failure this command exists to prevent. `kae pin` always materializes the store,
	// so its absence is the reliable signal that the two have diverged — the same
	// dirExists gate boundDirStores applies before naming a store in a report.
	if !dirExists(storeDir) {
		return finish(opts, errf(constants.ExitNotFound,
			"this directory's %s store is not there (%s), so kae cannot tell where its login would land; "+
				"re-bind it at its current path: %s pin %s %s",
			tool, app.displayPath(storeDir), toolName, tool, accountName))
	}

	be, err := app.secretBackend()
	if err != nil {
		return finish(opts, err)
	}
	// The pin lock, and not the tool lock: what must not happen underneath an
	// interactive login is a re-bind of *this* directory overwriting the store the
	// login is writing to. The account snapshot the harvest writes afterwards is
	// covered no more tightly than every other harvest site, which docs/ROADMAP.md
	// records as accepted (it self-heals on the next bind of the directory that
	// still holds the live copy). Held across the flow, as `kae add` holds the tool
	// lock across the same kind of wait.
	pinLock, err := app.acquirePinLock(absDir)
	if err != nil {
		return finish(opts, err)
	}
	defer pinLock.Release()

	// Resolved before the flow, because the credential in the store has to be read
	// on both sides of it. A store kae cannot resolve is not fatal: the login is
	// still the useful half, and everything that needs the spec degrades to saying
	// so (reloginCredentialSpec). Quiet here — the post-flow resolution is the one
	// that decides whether the capture back can happen, so warning now would both
	// repeat itself and claim something about a step that has not run.
	_, sp, haveSpec := app.reloginCredentialSpec(ctx, tool, dirs, quiet)
	before, comparable := storeCredential(ctx, sp, haveSpec)

	// Every variable the binding sets, not just the home one. The login writes the
	// credential where the *credential* variable points, so exporting one half sends
	// the new token to a store kae does not read back — reported as a login that
	// changed nothing, with the directory still stale.
	loginEnv := []string{isolationEnvVar(tool) + "=" + storeDir}
	shown := []string{isolationEnvVar(tool) + "=" + app.displayPath(storeDir)}
	if credVar := credentialEnvVar(tool); credVar != "" && dirs.Cred != "" {
		loginEnv = append(loginEnv, credVar+"="+dirs.Cred)
		shown = append(shown, credVar+"="+app.displayPath(dirs.Cred))
	}
	fmt.Fprintf(os.Stderr,
		"kae: complete the %s login flow; kae is running it against this directory's own store (%s), "+
			"so it refreshes %s/%s and not the real home\n",
		tool, strings.Join(shown, " "), tool, accountName)
	code, err := runner.RunInteractive(ctx, loginEnv, command[0], command[1:]...)
	if err != nil {
		return finish(opts, fmt.Errorf("launch %s login: %w", tool, err))
	}
	if code != 0 {
		// Same reading as `kae add`: a non-zero exit does not prove nothing was
		// written, so go on and let what is in the store decide.
		fmt.Fprintf(os.Stderr, "kae: %s exited with %d; kae is checking what is in the store now\n", command[0], code)
	}
	// Resolved **again**, because the login is precisely the event that can move the
	// answer: codex under `cli_auth_credentials_store = "auto"` probes the keychain to
	// decide which store it is on, so a directory whose store had no item resolved to
	// the file spec before the flow and to the item after it. Comparing the post-login
	// state through the pre-login spec then reads the store the tool abandoned — empty
	// before, empty after — and calls a successful login "unchanged". `kae add` re-plans
	// after its login flow for the same reason (refreshPlan).
	//
	// Covered by TestReloginResolvesTheStoreAgainAfterTheFlow, which is worth naming
	// here because this was first written down as untestable and that was wrong:
	// `KeychainDirBindable` — the reason the argument went "reaching codex's probe needs
	// a capability kae does not declare" — gates `dirCredentialSpec`, not this path.
	// Relogin reaches the spec through `dirSpecs` → `Codex.Artifacts` → `usesKeyring`
	// and reads it with `artifact.ReadLive`, neither of which asks. And the probe is a
	// `security` subprocess through `internal/runner`, so it needs no real keychain.
	// For claude both resolutions are identical (its kind follows an env var, not the
	// store), so this is one extra `dirSpecs` per relogin there.
	//
	// Never wrap this function's context in `keychain.WithReadCache` the way `kae pin`
	// and `kae use` do: a cached pre-login probe served to the post-login read reopens
	// this defect with every test still green.
	specs, sp, haveSpec := app.reloginCredentialSpec(ctx, tool, dirs, speak)
	after, comparedAfter := storeCredential(ctx, sp, haveSpec)
	switch {
	// A flow the user aborted, or one that failed, leaves the store exactly as it
	// was — and reporting a login that did not happen is the claim this command must
	// never make, because the whole point of running it is that the directory was
	// already stale. `kae add` refuses the same case with the same exit code.
	case comparable && comparedAfter && bytes.Equal(before, after):
		return finish(opts, errf(constants.ExitAuthUnchanged,
			"the %s login flow left this directory's credential unchanged, so there is nothing to capture back",
			tool))
	// Both reads have to have succeeded for the comparison to mean anything: two
	// failed reads are also "equal", and that is kae not knowing rather than nothing
	// having changed. Saying so is not optional — falling through to the success line
	// would claim a login on the strength of a comparison that never happened.
	//
	// Of the pair, only the **before** half is observable through the wording, measured
	// rather than argued: dropping `comparedAfter` from the gate below survives every
	// fixture, including the readable-before/unreadable-after one written to kill it.
	// Keep both halves anyway — what makes the after-half redundant is an invariant in
	// two other packages, and it is the kind that breaks quietly.
	//
	// `comparedAfter == false` has two routes and they close differently. A failed
	// `ReadLive` maps to `liveUnreadable` in the harvest, which reads the same spec
	// through the same call, so it refuses and `attributed` is false. But `!haveSpec`
	// does **not** go that way: harvestDirCredential would return an empty refusal from
	// its own `!ok` arm, i.e. `attributed` true. What actually closes that route is that
	// every adapter with a `credentialArtifactName` returns a spec under that name
	// whenever `Artifacts` succeeds, so `!haveSpec` implies `specs == nil` and
	// captureBackAfterRelogin refuses at its own gate. Nothing guarded that until
	// TestCredentialArtifactNameMatchesEveryAdapter; an adapter that grows a spec set
	// without its credential, or a name that drifts from the adapter's, reopens this.
	case !comparable || !comparedAfter:
		fmt.Fprintf(os.Stderr,
			"kae: warning: kae could not read this directory's %s credential, so it cannot tell whether the "+
				"login flow changed anything\n", tool)
	}
	// "A login happened, for this account" is three separate observations, and the
	// strong wording may only be printed when kae made all three. They fail
	// independently, which is why one gate cannot stand for the others. The wording and
	// the rule that picks between the two lines are a user-visible contract, and
	// docs/CLI.md § kae relogin Semantics is normative for them; what is here is why
	// each gate exists, which is the half a contract does not carry:
	//
	//   - **changed**: kae read the store on both sides and the bytes differ. Without
	//     it a flow kae could not compare at all still printed a login (the warning
	//     above says the opposite two lines earlier).
	//   - **nothing usable there**: what the store holds now is not something kae read
	//     as a login. Two states, and they are the same state to the classifier that
	//     already owns this question (readLiveCredential's liveNothing is documented as
	//     "absent, **or** present with nothing left to authenticate or refresh with"),
	//     so splitting them here is how the first version of this gate caught the
	//     blanked credential and missed the removed one. A payload kae simply cannot
	//     parse is *not* in this set: it may be a login in a shape kae has not been
	//     taught, and the harvest refuses that on its own evidence.
	//   - **attributed**: the harvest confirmed the login is this account's. A login as
	//     somebody else leaves a store that is legitimately theirs, and printing
	//     "Logged claude in for claude/main" over the warning that says otherwise hands
	//     the reader the wrong one of two contradicting lines.
	changed := comparable && comparedAfter && !bytes.Equal(before, after)
	// Each arm says what kae **read**, names causes without picking one, and issues no
	// instruction. `Revoked` means "no usable token in this payload", derived from
	// fields that are empty *or absent* — so an upstream rename of the token keys
	// reads identically to a tombstone (AGENTS.md; docs/VALIDATION.md). The demotion
	// is the right weak consequence for that; "the login failed, run it again" is not,
	// and it would loop forever against a working login on the day kae's parser is the
	// stale thing.
	switch {
	case comparedAfter && len(after) == 0:
		fmt.Fprintf(os.Stderr,
			"kae: warning: kae found no %s credential where it resolves this directory's store, so it is not "+
				"reporting a login — the flow may have left nothing there, or it may have moved the credential "+
				"to a store kae does not resolve for this directory\n", tool)
		changed = false
	case comparedAfter && freshnessOf(tool, after).Revoked:
		fmt.Fprintf(os.Stderr,
			"kae: warning: kae read no usable %s token in the payload now in this directory's store, so it is "+
				"not reporting a login — blank tokens are what a failed refresh leaves behind, and a payload "+
				"whose token keys changed upstream reads the same way\n", tool)
		changed = false
	}
	// Called unconditionally, and *before* the wording is decided: a flow kae could not
	// compare may still have left a copy worth harvesting, and the harvest's own guards
	// are the ones that decide that. Only the wording depends on both answers.
	attributed := app.captureBackAfterRelogin(ctx, be, specs, tool, accountName, dirs)
	if changed && attributed {
		fmt.Printf("Logged %s in for %s/%s in this directory\n", tool, tool, accountName)
	} else {
		fmt.Printf("Ran the %s login flow in this directory\n", tool)
	}
	return constants.ExitOK
}

// reloginCredentialSpec resolves the store's artifact specs and the credential one
// among them. It reports haveSpec=false — rather than failing — for a tool kae
// materializes no per-directory credential for, and for a store it cannot resolve:
// the login is worth running either way, and the two things that need the spec (the
// did-anything-change comparison and the capture back) each say what their absence
// costs instead of blocking it.
//
// report exists because runRelogin resolves twice, before and after the flow, and a
// failure that does not clear would otherwise print the identical line at both.
const (
	quiet = false
	speak = true
)

func (app *App) reloginCredentialSpec(ctx context.Context, tool string, dirs bindDirs, report bool) ([]artifact.Spec, artifact.Spec, bool) {
	artName := credentialArtifactName(tool)
	if artName == "" {
		return nil, artifact.Spec{}, false
	}
	specs, err := app.dirSpecs(ctx, tool, dirs)
	if err != nil {
		if report {
			fmt.Fprintf(os.Stderr,
				"kae: warning: kae could not resolve where %s keeps this directory's credential (%v), "+
					"so it cannot capture the login back into the account snapshot\n", tool, err)
		}
		return nil, artifact.Spec{}, false
	}
	sp, ok := specByName(specs, artName)
	return specs, sp, ok
}

// storeCredential reads the raw credential in a bound store, for the one question a
// before/after pair answers: did the flow change anything at all. ok is false where
// kae could not read it, which is not the same as there being nothing there.
//
// Deliberately artifact.ReadLive and not readLiveCredential: that one classifies for
// an *ordering* and returns nil for everything it declines to order, which would
// make two different payloads kae cannot judge compare equal.
func storeCredential(ctx context.Context, sp artifact.Spec, haveSpec bool) ([]byte, bool) {
	if !haveSpec {
		return nil, false
	}
	live, err := artifact.ReadLive(ctx, sp)
	if err != nil {
		return nil, false
	}
	if !live.Present {
		return nil, true // nothing there is a state, and a login changes it
	}
	return live.Data, true
}

// reloginTool picks the tool to log in: the one named, or the directory's single
// candidate. A candidate is a tool the fragment binds *and* kae can drive a login
// for; a bound tool with no login command in the table is not one, because kae
// would have nothing to run.
//
// With several candidates and no argument this refuses rather than picking. Two
// interactive logins from one word is not a thing to do by default, and the
// alternative — taking the first — would silently log in the tool the user did
// not mean.
func reloginTool(app *App, pinID string, fragment fragmentInfo, explicitTool string) (string, error) {
	candidates := []string{}
	for _, tool := range constants.Tools {
		if _, bound := app.boundStoreDir(pinID, tool, fragment); !bound {
			continue
		}
		if loginCommand(tool) == nil {
			// **Unobservable today**, like the refusal below it and for the same reason:
			// every tool `kae pin` can bind is tier 1, and both tier-1 tools have a login
			// command. Dropping this filter survives the suite. It is the gate that keeps
			// a bound tool kae cannot drive out of the candidate set, so the multi-candidate
			// refusal counts only tools it could actually log in.
			continue
		}
		candidates = append(candidates, tool)
	}
	if explicitTool != "" {
		for _, tool := range candidates {
			if tool == explicitTool {
				return tool, nil
			}
		}
		if _, bound := app.boundStoreDir(pinID, explicitTool, fragment); bound {
			// **Unobservable today**, written down so nobody removes it as dead or writes a
			// test that cannot fail: every tool `kae pin` can bind is tier 1, and both tier-1
			// tools have a login command. It stops being unobservable the moment a tool
			// without one becomes bindable — and without it that tool would get "this
			// directory does not bind X" about a directory that does.
			return "", errf(constants.ExitUnsupported,
				"kae has no login command for %s, so it cannot log this directory in (see docs/ADAPTERS.md)", explicitTool)
		}
		return "", errf(constants.ExitNotFound,
			"this directory does not bind %s; it binds %s", explicitTool, boundToolList(fragment))
	}
	switch len(candidates) {
	case 0:
		return "", errf(constants.ExitNotFound,
			"this directory binds no tool kae can drive a login for (it binds %s)", boundToolList(fragment))
	case 1:
		return candidates[0], nil
	default:
		return "", errf(constants.ExitUsage, "this directory binds %s; name the one to log in: %s relogin <tool>",
			strings.Join(candidates, ", "), toolName)
	}
}

// boundToolList names what a fragment binds, for a refusal that has to say what
// the directory *does* hold. The order comes from boundTools, which is also what
// `kae ls --pins` renders through — canonical order first, then any tool kae has
// retired since the fragment was written, rather than dropping it. A dropped name
// is the one that would have explained why the directory needs re-pinning.
func boundToolList(fragment fragmentInfo) string {
	bound := boundTools(fragment.Accounts)
	if len(bound) == 0 {
		return "no tools"
	}
	return strings.Join(bound, ", ")
}

// captureBackAfterRelogin harvests the copy the login just wrote into the
// account's snapshot, so the account's own record is the one that can still
// refresh. Every guard is harvestDirCredential's, unchanged: it declines a copy
// that does not supersede the snapshot, and one it cannot attribute to this
// account.
//
// Nothing here is fatal. The login is already in the store and the directory
// works either way; what a failure costs is that `kae use <tool> <account>` would
// apply the older copy globally, which is what the warnings say.
//
// It returns whether the harvest **confirmed** the login is that account's, which is
// the account half of what the caller's success line claims. Every path where kae did
// not establish that answers false, including the ones where it never asked: a tool
// whose rotation is unmeasured harvests nothing, so nothing checked the identity
// there either.
//
// One route is the exception, and the comment used to deny it: harvestDirCredential
// short-circuits on `!supersedes(live, snapshot)` **before** dirIdentityConfirms, so a
// post-flow copy that does not order ahead of the snapshot returns an empty refusal
// with no identity comparison made, and this answers true. Unreachable in practice
// while claude's access token is ~8h — a real login sets `expiresAt` to now+8h, which
// no snapshot captured within that token's life can beat — and it stops being
// unreachable the moment a tool with a short-lived token is measured. Recorded rather
// than closed because closing it means the harvest distinguishing "not newer" from
// "confirmed", which its three other callers do not need.
func (app *App) captureBackAfterRelogin(ctx context.Context, be secret.Backend,
	specs []artifact.Spec, tool, accountName string, dirs bindDirs,
) bool {
	// Claude-only today, through the one predicate that owns the question. A tool
	// whose rotation is not measured has nothing to harvest *from* — its older
	// copies stay usable — so saying anything here would describe a problem it does
	// not have (docs/ROADMAP.md § Rotation is measured for claude only).
	artName := credentialArtifactName(tool)
	if !rotatesSingleUse(tool) || artName == "" || specs == nil {
		return false
	}
	acc, snapshot, _, err := app.snapshotCredential(ctx, be, tool, accountName, artName)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"kae: warning: logged in, but kae could not read snapshot %s/%s to capture it back (%v)\n",
			tool, accountName, err)
		return false
	}
	_, _, refused := app.harvestDirCredential(ctx, be, specs, tool, accountName, acc, dirs, snapshot)
	switch {
	// Either harvested — harvestDirCredential says so itself — or the snapshot already
	// holds a copy at least as new, which is the ordinary outcome of re-running this
	// command and is not worth a line. Nothing kae printed contradicts the account.
	case refused.Why == "":
		return true
	case refused.Conflicting:
		// Positive evidence that the login is somebody else's: the store now names an
		// account other than the one this directory binds. Filing it under this name is
		// the one thing that would be undetectable afterwards, so kae did not — and the
		// remedy is to fix the binding, never to log in again (that would mint a fresh
		// chain and invalidate the copy just left in place).
		fmt.Fprintf(os.Stderr,
			"kae: warning: the %s login now in this directory belongs to an account other than %s/%s (%s), "+
				"so kae did not capture it into that snapshot; re-bind with: %s pin %s <account>\n",
			tool, tool, accountName, refused.Why, toolName, tool)
	default:
		// The frame may claim no more than the reason it interpolates. It used to say kae
		// "cannot attribute" the login and that the snapshot holds "the **older** copy" —
		// an ordering claim, next to reasons that include "kae cannot read or date the copy
		// already there", which is kae saying it cannot order them. Corrected 2026-08-07.
		//
		// Two routes reach it, and the second is the one worth knowing: a live copy kae
		// cannot read or date (harvestDirCredential returns that refusal *before* its
		// "snapshot at least as new" arm, so it is not swallowed), and any missing-evidence
		// refusal from dirIdentityConfirms — a bound store with no identity cache is the
		// ordinary one. An earlier version of this comment claimed no fixture could reach
		// the arm; that was wrong, and it was wrong for a reason worth repeating: the probe
		// wrote its payload where relogin does not resolve the store, so the read came back
		// absent and landed on the silent-success arm above. A read that finds nothing and
		// a read that finds something unjudgeable are different findings.
		fmt.Fprintf(os.Stderr,
			"kae: warning: kae cannot confirm the %s login now in this directory is %s/%s's (%s), "+
				"so it did not capture it back and that snapshot still holds its own copy; "+
				"%s use %s %s would apply that one\n",
			tool, tool, accountName, refused.Why, toolName, tool, accountName)
	}
	return false
}
