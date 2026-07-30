package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
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
