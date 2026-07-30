package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// seedKeyringCodex configures codex's keyring store inside a bound directory, the
// shape that makes its credential a single global keychain item.
func seedKeyringCodex(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "config.toml"), "cli_auth_credentials_store = \"keyring\"\n")
}

// TestWriteDirCredentialRefusesGlobalKeychainStore is the guard on the most
// destructive thing this code could do. codex's keyring item is one global
// `Codex Auth` whatever CODEX_HOME says, and its spec carries KeychainReplace —
// which deletes the existing item before writing. Writing it for a bound
// directory would therefore destroy the user's global codex login while
// isolating nothing.
func TestWriteDirCredentialRefusesGlobalKeychainStore(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	credDir := t.TempDir()
	seedKeyringCodex(t, credDir)

	fake := &runnertest.Fake{Code: 0}
	var err error
	runner.With(fake, func() {
		err = app.writeDirCredential(context.Background(), testBackend(t, app),
			constants.ToolCodex, "main", credDir)
	})

	if !errors.Is(err, errGlobalCredentialStore) {
		t.Fatalf("a global credential store must be refused, got %v", err)
	}
	// Refused before anything ran: no read, no write, and above all no delete of
	// the global item.
	if fake.Name != "" {
		t.Fatalf("refusal must not touch the keychain, ran %q %v", fake.Name, fake.Args)
	}
}

// The per-directory case is the opposite: claude's item is namespaced by the
// config dir, so it is written.
func TestWriteDirCredentialWritesDirScopedKeychainStore(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	payload := `{"claudeAiOauth":{"accessToken":"` + mainToken + `","subscriptionType":"max"}}`
	runner.With(&runnertest.Fake{Stdout: payload, Code: 0}, func() {
		captureClaude(t, app, "main", mainToken)
	})
	credDir := t.TempDir()

	fake := &runnertest.Fake{Code: 0}
	runner.With(fake, func() {
		if err := app.writeDirCredential(context.Background(), testBackend(t, app),
			constants.ToolClaude, "main", credDir); err != nil {
			t.Fatalf("writeDirCredential: %v", err)
		}
	})
	if !strings.Contains(strings.Join(fake.Args, " "), sha8Of(credDir)) {
		t.Fatalf("credential not written to the per-directory item: %v", fake.Args)
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

// A whole-profile bind must not fail over one tool whose credential store cannot
// be scoped to a directory: the others still bind, and that tool's settings and
// sessions are still isolated. Only the credential is shared, and the warning
// says so.
func TestPrepareBondWarnsOnGlobalStoreAndKeepsBinding(t *testing.T) {
	app := testApp(t, nil)
	app.Env.GOOS = "darwin"
	cwd := t.TempDir()
	pinID := paths.PinID(cwd)
	// codex's real home carries the keyring setting; prepareBond symlinks
	// config.toml into the bond dir before the credential step, which is how the
	// bound directory ends up resolving the global store.
	seedKeyringCodex(t, filepath.Join(app.Env.Home, ".codex"))

	// The whole-profile path is prepareIsolationDirs; prepareBond itself reports
	// the limitation and the policy of tolerating it lives one level up.
	ctx := context.Background()
	be := testBackend(t, app)
	entries := app.bondIsolationEntries([]runTarget{{Tool: constants.ToolCodex, Account: "main"}}, pinID)
	bondDir := app.Paths.SharedDir(pinID, constants.ToolCodex)

	fake := &runnertest.Fake{Code: 0}
	var err error
	runner.With(fake, func() {
		err = app.prepareIsolationDirs(modeShared, entries, func(tool, account string) (string, error) {
			return app.prepareBond(ctx, be, tool, account, pinID)
		})
	})
	if err != nil {
		t.Fatalf("a global credential store must warn, not fail the bind: %v", err)
	}
	if fake.Name != "" {
		t.Fatalf("the global keychain item must be left alone, ran %q %v", fake.Name, fake.Args)
	}
	// The bond dir is still built, so the tool's non-auth state is isolated.
	if _, statErr := os.Lstat(filepath.Join(bondDir, "config.toml")); statErr != nil {
		t.Fatalf("bond dir must still be materialized: %v", statErr)
	}
	// And no credential file was left as a consolation prize: codex reads the
	// keyring, so a file here would be a plaintext secret nothing reads.
	if _, statErr := os.Stat(filepath.Join(bondDir, "auth.json")); !os.IsNotExist(statErr) {
		t.Error("no credential file may be written for a tool that reads a global keyring")
	}
}
