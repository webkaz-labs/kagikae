// Codex adapter tests: file and keyring artifacts, detection and doctor.

package adapter_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// codex's `Codex Auth` item is scoped by its account rather than its service name,
// and that account *is* derived from `CODEX_HOME` — confirmed against a real item
// for a bond-dir-shaped path, symlink included (docs/VALIDATION.md). So the reason
// the flag stays false is not "unverified derivation", and it is not the item's
// lifecycle either — **this comment told the next reader to "land the teardown"
// until 2026-08-10, and that teardown had shipped on 2026-07-30**:
// `removeDirCredential` sweeps a keychain item exactly when the adapter declares it
// bindable, so setting this flag is what makes the sweep cover codex, on unpin
// --purge, on a `pin -s` ↔ `pin -i` toggle and on a re-bind alike.
//
// What has never run is the round-trip itself, on a real keychain with two codex
// homes. docs/ACCEPTANCE.md § Optional account-combination checks is that check,
// says outright that everything else is in place including the teardown, and names
// the exception map in
// TestKeychainDirBindableMatchesTheItemIdentity as what its result unblocks.
//
// So no *mechanism* is missing — but declaring the capability is not a one-line
// change either, and saying "not waiting on code" without this paragraph is the same
// over-claim in the other direction. Declaring it is a pair — `KeychainDirBindable` on
// codex's `keyringSpec` **and** dropping codex from that map, since either alone
// fails the parity guard by construction — and the pair turns four guards that encode
// codex as the unbindable example red. Each is a deliberate statement of the current
// state rather than an obstacle: this test,
// TestWriteDirCredentialRefusesGlobalKeychainStore,
// TestDirCredentialFreshnessRefusesGlobalKeychainStore and
// TestPruneDirCredentialsSkipsBoundAndUnbindableStores — the last of which fails by
// observing the sweep issue the per-directory delete, which is the mechanism working.
// Measured 2026-08-10 by setting the flag in a throwaway extract, not inferred.
// Do the gate first; then those four, and nothing else.
func TestCodexKeyringIsNotDirBindable(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "config.toml"), "cli_auth_credentials_store = \"keyring\"\n")
	env := testEnv(t, "darwin", map[string]string{"CODEX_HOME": home})
	specs, err := codexAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Kind != constants.KindKeychain {
		t.Fatalf("expected the keyring spec, got %+v", specs[0])
	}
	if specs[0].KeychainDirBindable {
		t.Fatal("an item whose per-directory lifecycle is unowned must not claim to be bindable")
	}
}

func TestCodexArtifactsFileAndKeyring(t *testing.T) {
	// darwin: the keyring store is refused off macOS, where kae cannot read a
	// keyring at all (TestCodexKeyringStoresOffDarwin covers that half).
	env := testEnv(t, "darwin", nil)
	specs, err := codexAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 || specs[0].Kind != constants.KindFile {
		t.Fatalf("file store: %+v %v", specs, err)
	}
	write(t, filepath.Join(env.Home, ".codex", "config.toml"),
		"cli_auth_credentials_store = \"keyring\"\n")
	specs, err = codexAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 {
		t.Fatalf("keyring store: %+v %v", specs, err)
	}
	// Only the selection is this test's business: the config value is what picks the
	// keychain spec over the file one. The spec's shape and its derived account are
	// pinned in the codex package's own tests (TestCodexKeyringSpecIsAccountScoped,
	// TestStoreKeyGolden).
	if specs[0].Kind != constants.KindKeychain {
		t.Fatalf("the keyring store must select the keychain spec: %+v", specs[0])
	}
}

// With the keyring store and no live Codex Auth item, Detect reports absent and
// Doctor reports the keyring store ok with a logged-out auth warning (the
// detect-only error is gone). The keychain probe is stubbed so the test never
// touches the real keychain.
func TestCodexKeyringDetectAndDoctor(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	write(t, filepath.Join(env.Home, ".codex", "config.toml"),
		"cli_auth_credentials_store = \"keyring\"\n")
	notFound := &runnertest.Fake{Stderr: "could not be found", Code: 44}
	runner.With(notFound, func() {
		info, err := codexAdapter.Detect(context.Background(), env)
		if err != nil || info.AuthPresent || info.Driver != constants.DriverCodexKeyring {
			t.Fatalf("keyring detect: %+v %v", info, err)
		}
		var storeOK, authWarn bool
		for _, check := range codexAdapter.Doctor(context.Background(), env) {
			if check.Code == constants.CheckCredentialStore && check.Status == constants.StatusOK {
				storeOK = true
			}
			if check.Code == constants.CheckAuthPresent && check.Status == constants.StatusWarn {
				authWarn = true
			}
		}
		if !storeOK || !authWarn {
			t.Fatalf("keyring doctor: storeOK=%v authWarn=%v", storeOK, authWarn)
		}
	})
}

func TestCodexHonorsCodexHome(t *testing.T) {
	codexHome := t.TempDir()
	env := testEnv(t, "linux", map[string]string{"CODEX_HOME": codexHome})
	write(t, filepath.Join(codexHome, "auth.json"), `{"tokens":{}}`)
	info, err := codexAdapter.Detect(context.Background(), env)
	if err != nil || !info.AuthPresent {
		t.Fatalf("CODEX_HOME not honored: %+v %v", info, err)
	}
}

// Only `auto` leaves the store ambiguous, so only `auto` speculates about the
// keyring. An absent config.toml is upstream's `file` default, where a missing
// auth.json means exactly one thing — codex is logged out — and inventing a
// keyring possibility there is what let kae describe the wrong store.
func TestCodexDetectMissingAuthWarnings(t *testing.T) {
	env := testEnv(t, "linux", nil)
	info, err := codexAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("unexpected: %+v %v", info, err)
	}
	if len(info.Warnings) != 0 {
		t.Fatalf("the file store must not speculate about a keyring: %+v", info.Warnings)
	}
	write(t, filepath.Join(env.Home, ".codex", "config.toml"),
		"cli_auth_credentials_store = \"auto\"\n")
	info, err = codexAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("unexpected: %+v %v", info, err)
	}
	if len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0].Error(), "keyring") {
		t.Fatalf("expected keyring-possibility warning under auto: %+v", info.Warnings)
	}
}
