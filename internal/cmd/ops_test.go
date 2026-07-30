package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter/cursor"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
	"github.com/webkaz-labs/kagikae/internal/testutil/secrettest"
)

// TestPlansFromBackupMetaPreservesKeychainAccount guards the rollback path:
// a keychain artifact's account must survive the metadata round-trip so a
// recreated item is restored under the tool's own account (e.g. cursor-user),
// not the generic fallback.
func TestPlansFromBackupMetaPreservesKeychainAccount(t *testing.T) {
	meta := backup.Meta{Artifacts: []backup.ArtifactRecord{{
		Tool: constants.ToolCursor, Name: "access_token", Kind: constants.KindKeychain,
		Target: "cursor-access-token", KeychainAccount: "cursor-user",
		SecretRef: "backup/x/cursor/access_token", Present: true,
	}}}
	plans := plansFromBackupMeta(meta, nil)
	if len(plans) != 1 || len(plans[0].Specs) != 1 {
		t.Fatalf("unexpected plans: %+v", plans)
	}
	if got := plans[0].Specs[0].KeychainAccount; got != "cursor-user" {
		t.Fatalf("keychain account lost in metadata round-trip: %q", got)
	}
}

// A legacy codex backup record — `keychain_replace` with **no** recorded account,
// which is what the old capture wrote when no `Codex Auth` item was live — must not
// restore as a delete of the whole service. That was still destructive after the
// account-scoping fix: `specFromRecord` marked it account-scoped, but with an empty
// account the primitive fell back to a service-only delete and removed another
// CODEX_HOME's login. It is refused instead, and the refusal is visible here rather
// than only in the artifact primitive because this record shape can only arrive
// through a backup.
func TestSpecFromRecordLegacyReplaceWithoutAccountRefused(t *testing.T) {
	rec := backup.ArtifactRecord{
		Tool: constants.ToolCodex, Name: "auth", Kind: constants.KindKeychain,
		Target: "Codex Auth", Pointer: "/tokens", KeychainReplace: true,
		SecretRef: "backup/x/codex/auth", Present: false,
	}
	sp := specFromRecord(rec)
	if !sp.KeychainMatchAccount {
		t.Fatal("a legacy keychain_replace record must restore account-scoped")
	}
	fake := &runnertest.Fake{Code: 0}
	var err error
	runner.With(fake, func() {
		err = artifact.ApplyLive(context.Background(), sp, artifact.Value{Present: false})
	})
	if !errors.Is(err, artifact.ErrUnsafe) {
		t.Fatalf("err = %v, want ErrUnsafe", err)
	}
	if fake.Name != "" {
		t.Fatalf("the refusal must not touch the keychain, ran %q %v", fake.Name, fake.Args)
	}
}

// A snapshot captured by an older kae lacks an artifact today's adapter declares
// — every pre-existing cursor snapshot lacks refresh_token and api_key. The
// refusal must land before the *first* live write: applying access_token and then
// refusing would leave cursor-agent holding one account's access token beside
// another account's refresh token until the caller's restore undid it.
func TestApplySnapshotRefusesMissingArtifactBeforeAnyWrite(t *testing.T) {
	ctx := context.Background()
	be := secrettest.NewMem()
	ref := account.SecretRef(constants.ToolCursor, "main", "access_token")
	if err := be.Set(ctx, ref, []byte("raw-jwt")); err != nil {
		t.Fatal(err)
	}
	spec := func(name, service string) artifact.Spec {
		return artifact.Spec{
			Name: name, Kind: constants.KindKeychain,
			Target: service, KeychainAccount: cursor.KeychainAccount,
		}
	}
	plan := toolPlan{
		Tool: constants.ToolCursor, Account: "main",
		Specs: []artifact.Spec{
			spec("access_token", cursor.KeychainService),
			spec("refresh_token", cursor.KeychainServiceRefresh),
		},
		Meta: account.Account{
			Tool: constants.ToolCursor, Name: "main",
			Artifacts: map[string]account.Artifact{"access_token": {
				Kind: constants.KindKeychain, Target: cursor.KeychainService,
				SecretRef: ref, Present: true,
			}},
		},
	}
	fake := &runnertest.Fake{Code: 0}
	var err error
	runner.With(fake, func() { err = applySnapshot(ctx, be, plan) })
	if err == nil || !strings.Contains(err.Error(), "refresh_token") {
		t.Fatalf("err = %v, want a refusal naming the missing artifact", err)
	}
	if fake.Name != "" {
		t.Fatalf("the refusal must precede every write, ran %q %v", fake.Name, fake.Args)
	}
}

// restoreSpec covers a credential that moved stores between the backup and the
// restore — codex under `auto` creates its keychain item and deletes auth.json on
// its first save, which a `kae run -s` child or a login flow is enough to trigger.
// The payload must follow the tool (the same bytes live in either store), because
// writing the recorded store puts the credential where nothing reads it while kae
// reports success. A move between shapes that are *not* interchangeable cannot be
// redirected and is refused instead.
func TestRestoreSpecFollowsAMovedStore(t *testing.T) {
	fileRec := backup.ArtifactRecord{
		Tool: constants.ToolCodex, Name: "auth", Kind: constants.KindFile,
		Target: "/home/u/.codex/auth.json", SecretRef: "backup/x/codex/auth", Present: true,
	}
	itemNow := artifact.Spec{
		Name: "auth", Kind: constants.KindKeychain, Target: "Codex Auth",
		Pointer: "/tokens", KeychainAccount: "cli|1111111111111111", KeychainMatchAccount: true,
	}
	sp, _, err := restoreSpec(map[string][]artifact.Spec{constants.ToolCodex: {itemNow}}, fileRec)
	if err != nil {
		t.Fatalf("a whole-document payload must follow the tool: %v", err)
	}
	if sp.Kind != constants.KindKeychain || sp.KeychainAccount != itemNow.KeychainAccount {
		t.Fatalf("restore spec = %+v, want today's keychain item", sp)
	}

	// Same kind, and no declaration at all: the record stands.
	fileNow := map[string][]artifact.Spec{constants.ToolCodex: {{
		Name: "auth", Kind: constants.KindFile, Target: "/home/u/.codex/auth.json",
	}}}
	for _, current := range []map[string][]artifact.Spec{fileNow, nil} {
		sp, _, err := restoreSpec(current, fileRec)
		if err != nil || sp.Kind != constants.KindFile || sp.Target != fileRec.Target {
			t.Fatalf("an unmoved store must restore from the record: %+v %v", sp, err)
		}
	}

	// A whole document cannot be restored through a pointer spec (claude's two
	// drivers): that nests it under its own key and the tool reads it as malformed,
	// so it is refused rather than redirected.
	pointerNow := map[string][]artifact.Spec{constants.ToolClaude: {{
		Name: "claude_ai_oauth", Kind: constants.KindJSONPointer,
		Target: "/home/u/.claude/.credentials.json", Pointer: "/claudeAiOauth",
	}}}
	keychainRec := backup.ArtifactRecord{
		Tool: constants.ToolClaude, Name: "claude_ai_oauth", Kind: constants.KindKeychain,
		Target: "Claude Code-credentials", Pointer: "/claudeAiOauth",
		SecretRef: "backup/x/claude/claude_ai_oauth", Present: true,
	}
	_, _, err = restoreSpec(pointerNow, keychainRec)
	if exitOf(err) != constants.ExitUnsafeRefused {
		t.Fatalf("err = %v (exit %d), want a refusal with exit %d",
			err, exitOf(err), constants.ExitUnsafeRefused)
	}
	if msg := err.Error(); !strings.Contains(msg, "kae use") {
		t.Fatalf("the refusal must name the recovery: %s", msg)
	}
}

// The pre-rollback backup must capture **exactly** the store the rollback then
// writes, or rolling back the rollback cannot put it back. Resolving the two
// independently is what breaks it: preferring today's spec by name alone once made
// this capture a different codex home's item from the one the restore deleted, so a
// rollback destroyed a login it had not backed up. The property is checked as an
// identity, per record, across the cases that differ (moved store, absent record,
// same store, and a record today's adapter no longer declares).
func TestPlansFromBackupMetaCapturesWhatTheRollbackWrites(t *testing.T) {
	itemA := artifact.Spec{
		Name: "auth", Kind: constants.KindKeychain, Target: "Codex Auth",
		Pointer: "/tokens", KeychainAccount: "cli|aaaaaaaaaaaaaaaa", KeychainMatchAccount: true,
	}
	itemB := itemA
	itemB.KeychainAccount = "cli|bbbbbbbbbbbbbbbb" // another CODEX_HOME's item
	fileRec := func(present bool) backup.ArtifactRecord {
		return backup.ArtifactRecord{
			Tool: constants.ToolCodex, Name: "auth", Kind: constants.KindFile,
			Target: "/home/u/.codex/auth.json", SecretRef: "backup/x/codex/auth", Present: present,
		}
	}
	itemRec := backup.ArtifactRecord{
		Tool: constants.ToolCodex, Name: "auth", Kind: constants.KindKeychain,
		Target: "Codex Auth", Pointer: "/tokens", KeychainAccount: itemA.KeychainAccount,
		KeychainMatchAccount: true, SecretRef: "backup/x/codex/auth", Present: false,
	}
	for _, tc := range []struct {
		name    string
		rec     backup.ArtifactRecord
		current map[string][]artifact.Spec
	}{
		{
			name: "store moved, payload to write", rec: fileRec(true),
			current: map[string][]artifact.Spec{constants.ToolCodex: {itemA}},
		},
		{
			name: "store moved, nothing to write", rec: fileRec(false),
			current: map[string][]artifact.Spec{constants.ToolCodex: {itemA}},
		},
		{
			name: "same kind, another codex home", rec: itemRec,
			current: map[string][]artifact.Spec{constants.ToolCodex: {itemB}},
		},
		{name: "artifact no longer declared", rec: fileRec(true), current: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plans := plansFromBackupMeta(backup.Meta{Artifacts: []backup.ArtifactRecord{tc.rec}}, tc.current)
			if len(plans) != 1 || len(plans[0].Specs) != 1 {
				t.Fatalf("unexpected plans: %+v", plans)
			}
			captured := plans[0].Specs[0]
			written, _, err := restoreSpec(tc.current, tc.rec)
			if err != nil {
				t.Fatalf("restoreSpec: %v", err)
			}
			if !reflect.DeepEqual(captured, written) {
				t.Fatalf("the pre-rollback backup captures %+v but the rollback writes %+v", captured, written)
			}
		})
	}
}

// An **absent** record must never redirect. Redirecting it would delete the store
// the tool moved to, and the paths that need the redirect reach this case with a
// credential nothing has a copy of: `kae add codex --restore` under `auto` whose
// login succeeded (creating the keychain item) and which then failed before the
// capture — deleting there destroys the login the user just performed.
func TestRestoreSpecNeverRedirectsADelete(t *testing.T) {
	absentFileRec := backup.ArtifactRecord{
		Tool: constants.ToolCodex, Name: "auth", Kind: constants.KindFile,
		Target: "/home/u/.codex/auth.json", SecretRef: "backup/x/codex/auth", Present: false,
	}
	itemNow := artifact.Spec{
		Name: "auth", Kind: constants.KindKeychain, Target: "Codex Auth",
		Pointer: "/tokens", KeychainAccount: "cli|1111111111111111", KeychainMatchAccount: true,
	}
	sp, _, err := restoreSpec(map[string][]artifact.Spec{constants.ToolCodex: {itemNow}}, absentFileRec)
	if err != nil {
		t.Fatalf("an absent record must restore, not fail: %v", err)
	}
	if sp.Kind != constants.KindFile || sp.Target != absentFileRec.Target {
		t.Fatalf("restore spec = %+v, want the recorded store (deleting the live one is unrecoverable)", sp)
	}
}

// applyBackup must leave the moved-to store alone for an absent record, and say so:
// the restore is partial, and silence there reads as a full one. The live store is
// resolved by the real codex adapter here (config.toml + GOOS), not injected, so
// this also covers applyBackup deriving today's specs for itself.
func TestApplyBackupKeepsAnUnaccountedCredential(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	ctx := context.Background()
	be := testBackend(t, app)
	authPath := filepath.Join(app.Env.Home, ".codex", "auth.json")
	writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"),
		"cli_auth_credentials_store = \"keyring\"\n")
	meta := backup.Meta{
		Tools: []string{constants.ToolCodex},
		Artifacts: []backup.ArtifactRecord{{
			Tool: constants.ToolCodex, Name: "auth", Kind: constants.KindFile,
			Target: authPath, SecretRef: backup.SecretRef("x", constants.ToolCodex, "auth"), Present: false,
		}},
	}
	const justLoggedIn = `{"tokens":{"access_token":"just-logged-in"}}`
	sim := &keychainSim{present: true, payload: justLoggedIn}
	var err error
	_, stderr := captureStderr(t, func() int {
		runner.With(sim, func() { err = app.applyBackup(ctx, be, meta, nil, false) })
		return 0
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !sim.present || sim.payload != justLoggedIn {
		t.Fatal("the credential the backup has no copy of must survive the restore")
	}
	for _, op := range sim.ops {
		if op == "delete" {
			t.Fatalf("no delete may reach the moved-to store: %v", sim.ops)
		}
	}
	if !strings.Contains(stderr, "moved its credential") {
		t.Fatalf("a partial restore must warn: %q", stderr)
	}
}

// When today's declaration cannot be resolved at all, the moved-store check does
// not run and the restore falls back to the record — correct, but it must not be
// silent: "previous state restored" would then be a claim kae did not check. A
// child rewriting config.toml to a store kae refuses is a way to cause exactly this.
func TestApplyBackupWarnsWhenTheStoreCannotBeResolved(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	be := testBackend(t, app)
	authPath := filepath.Join(app.Env.Home, ".codex", "auth.json")
	writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"),
		"cli_auth_credentials_store = \"ephemeral\"\n")
	ref := backup.SecretRef("x", constants.ToolCodex, "auth")
	const backedUp = `{"tokens":{"access_token":"backed-up"}}`
	if err := be.Set(ctx, ref, []byte(backedUp)); err != nil {
		t.Fatal(err)
	}
	meta := backup.Meta{
		Tools: []string{constants.ToolCodex},
		Artifacts: []backup.ArtifactRecord{{
			Tool: constants.ToolCodex, Name: "auth", Kind: constants.KindFile,
			Target: authPath, SecretRef: ref, Present: true,
		}},
	}
	var err error
	_, stderr := captureStderr(t, func() int {
		err = app.applyBackup(ctx, be, meta, nil, false)
		return 0
	})
	if err != nil {
		t.Fatalf("the restore must still put the recorded store back: %v", err)
	}
	if got := readFile(t, authPath); got != backedUp {
		t.Fatalf("recorded store = %q, want the backed-up credential", got)
	}
	if !strings.Contains(stderr, "could not resolve where codex keeps its credential") {
		t.Fatalf("an unchecked restore must warn: %q", stderr)
	}
}

// The redirect is only worth anything if the restore path consults it, so this
// covers the wiring end to end: applyBackup resolves the live store itself (the
// real codex adapter, from config.toml) and writes the backed-up credential there,
// leaving the store the tool abandoned alone.
func TestApplyBackupFollowsAMovedStore(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	ctx := context.Background()
	be := testBackend(t, app)
	authPath := filepath.Join(app.Env.Home, ".codex", "auth.json")
	writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"),
		"cli_auth_credentials_store = \"keyring\"\n")
	ref := backup.SecretRef("x", constants.ToolCodex, "auth")
	const backedUp = `{"tokens":{"access_token":"backed-up"}}`
	if err := be.Set(ctx, ref, []byte(backedUp)); err != nil {
		t.Fatal(err)
	}
	meta := backup.Meta{
		Tools: []string{constants.ToolCodex},
		Artifacts: []backup.ArtifactRecord{{
			Tool: constants.ToolCodex, Name: "auth", Kind: constants.KindFile,
			Target: authPath, SecretRef: ref, Present: true,
		}},
	}
	sim := &keychainSim{}
	var err error
	runner.With(sim, func() { err = app.applyBackup(ctx, be, meta, nil, false) })
	if err != nil {
		t.Fatalf("restore into the live store: %v", err)
	}
	if sim.payload != backedUp {
		t.Fatalf("the backed-up credential did not land in this home's item: %+v", sim)
	}
	if !strings.HasPrefix(sim.account, "cli|") {
		t.Fatalf("item account = %q, want codex's derived cli|<hash>", sim.account)
	}
	if _, statErr := os.Stat(authPath); statErr == nil {
		t.Fatal("the restore must not write the store the tool abandoned")
	}
}

// TestSpecFromRecordRestoresJSONC guards the restore path for JSONC targets
// (GitHub Copilot's commented config.json): if specFromRecord drops the JSONC
// bit, applyBackup falls through to the plain-JSON patch, which rejects the
// leading // comments and fails the rollback/restore. The reconstructed spec
// must patch through the comment-preserving path instead.
func TestSpecFromRecordRestoresJSONC(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	doc := "// managed automatically\n{\n  \"trustedFolders\": [\"/w\"],\n  \"lastLoggedInUser\": {\"host\":\"h\",\"login\":\"a\"}\n}\n"
	if err := os.WriteFile(cfg, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := backup.ArtifactRecord{
		Tool: constants.ToolCopilot, Name: "last_logged_in_user",
		Kind: constants.KindJSONPointer, Target: cfg, Pointer: "/lastLoggedInUser",
		JSONC: true, SecretRef: "backup/x/copilot/last_logged_in_user", Present: true,
	}
	if got := specFromRecord(rec); !got.JSONC {
		t.Fatalf("JSONC bit lost in metadata round-trip: %+v", got)
	}
	value := artifact.Value{Data: []byte(`{"host":"h","login":"b"}`), Present: true}
	if err := artifact.ApplyLive(context.Background(), specFromRecord(rec), value); err != nil {
		t.Fatalf("restore of a JSONC config must not fail on comments: %v", err)
	}
	out, _ := os.ReadFile(cfg)
	if s := string(out); !strings.Contains(s, "// managed automatically") || !strings.Contains(s, `"login":"b"`) {
		t.Fatalf("restore lost the comment or did not switch the value:\n%s", s)
	}
}

// TestCheckPayloadShapeRejectsIncompatibleTransitions covers the transition that
// would corrupt rather than fail: a keychain snapshot holds the whole
// `{"claudeAiOauth":…}` document, so applying it through a pointer spec nests it
// under its own key and claude reads a malformed credential. Reachable by
// capturing under one driver and applying under the KAE_CLAUDE_DRIVER override.
func TestCheckPayloadShapeRejectsIncompatibleTransitions(t *testing.T) {
	for _, tc := range []struct {
		name         string
		stored, dest string
		wantRefused  bool
	}{
		{"keychain snapshot into a pointer spec", constants.KindKeychain, constants.KindJSONPointer, true},
		{"pointer snapshot into a keychain spec", constants.KindJSONPointer, constants.KindKeychain, true},
		{"pointer snapshot into a pointer spec", constants.KindJSONPointer, constants.KindJSONPointer, false},
		{"keychain snapshot into a keychain spec", constants.KindKeychain, constants.KindKeychain, false},
		// Both are whole documents, which is what makes codex's auth.json and its
		// keyring item the same bytes.
		{"file snapshot into a keychain spec", constants.KindFile, constants.KindKeychain, false},
		// A snapshot predating the recorded kind must not be refused on a guess.
		{"unrecorded kind", "", constants.KindJSONPointer, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPayloadShape(constants.ToolClaude, "main", "claude_ai_oauth", tc.stored, tc.dest)
			if tc.wantRefused {
				if err == nil {
					t.Fatal("incompatible payload shapes must be refused, not applied")
				}
				if code := exitOf(err); code != constants.ExitUnsafeRefused {
					t.Fatalf("exit code = %d, want %d", code, constants.ExitUnsafeRefused)
				}
				return
			}
			if err != nil {
				t.Fatalf("compatible shapes must apply: %v", err)
			}
		})
	}
}

// TestSwitchRefusesIncompatibleSnapshotShape is the same guard on the global
// path. Capturing under the keychain driver stores the whole
// `{"claudeAiOauth":…}` document; switching afterwards under the forced file
// driver resolves a pointer spec, and applying one to the other would nest the
// document under its own key and report success. The live credential must be
// untouched.
func TestSwitchRefusesIncompatibleSnapshotShape(t *testing.T) {
	envVars := map[string]string{}
	app := testApp(t, envVars)
	app.Env.GOOS = "darwin"
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	payload := `{"claudeAiOauth":{"accessToken":"` + mainToken + `","subscriptionType":"max"}}`
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
	})

	// The live file the forced file driver will resolve, with a value of its own so
	// "untouched" is testable.
	live := filepath.Join(app.Env.Home, ".claude", ".credentials.json")
	writeFile(t, live, `{"claudeAiOauth":{"accessToken":"`+sideToken+`"}}`)

	// Force the file driver for the apply, the way [tools.claude] driver does.
	envVars[constants.EnvKaeClaudeDriver] = constants.DriverValueFile

	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	if code != constants.ExitUnsafeRefused {
		t.Fatalf("expected exit %d, got %d (%s)", constants.ExitUnsafeRefused, code, out)
	}
	got := readFile(t, live)
	if strings.Contains(got, `"claudeAiOauth":{"claudeAiOauth"`) {
		t.Fatalf("the credential was nested under its own key: %s", got)
	}
	if !strings.Contains(got, sideToken) {
		t.Fatalf("a refused switch must leave the live credential alone: %s", got)
	}
}
