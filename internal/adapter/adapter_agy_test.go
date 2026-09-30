// Agy adapter tests: keychain driver, file snapshot and identity.

package adapter_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter/agy"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// On macOS agy resolves the keychain driver: one opaque gemini/antigravity item
// matched by service AND account (the gemini service is shared, so a sibling
// item must never be touched). The keychain probe is stubbed so the test never
// reaches the real keychain.
func TestAgyDarwinKeychainDriver(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := agyAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 {
		t.Fatalf("unexpected specs: %+v %v", specs, err)
	}
	if specs[0].Kind != constants.KindKeychain || specs[0].Target != agy.KeychainService ||
		specs[0].Pointer != "" || specs[0].KeychainAccount != agy.KeychainAccount || !specs[0].KeychainMatchAccount {
		t.Fatalf("expected an opaque, account-matched gemini/antigravity spec: %+v", specs[0])
	}

	// Logged out: the keychain item is absent, Detect reports no auth with the
	// keychain driver and no "cannot switch" warning.
	notFound := &runnertest.Fake{Stderr: "could not be found", Code: 44}
	runner.With(notFound, func() {
		info, err := agyAdapter.Detect(context.Background(), env)
		if err != nil || info.AuthPresent || info.Driver != constants.DriverAgyKeychain {
			t.Fatalf("logged-out keychain detect: %+v %v", info, err)
		}
		for _, warning := range info.Warnings {
			if strings.Contains(warning, "cannot switch") {
				t.Fatalf("macOS keychain driver must not warn that kae cannot switch agy: %+v", info.Warnings)
			}
		}
	})

	// Logged in: the item is present, Detect reports auth with an OK doctor check.
	present := &runnertest.Fake{Stdout: "opaque-antigravity-token\n"}
	runner.With(present, func() {
		info, err := agyAdapter.Detect(context.Background(), env)
		if err != nil || !info.AuthPresent || info.Driver != constants.DriverAgyKeychain {
			t.Fatalf("logged-in keychain detect: %+v %v", info, err)
		}
		var authOK, driverOK bool
		for _, check := range agyAdapter.Doctor(context.Background(), env) {
			if check.Code == constants.CheckAuthPresent && check.Status == constants.StatusOK {
				authOK = true
			}
			if check.Code == constants.CheckDriver && check.Status == constants.StatusOK {
				driverOK = true
			}
		}
		if !authOK || !driverOK {
			t.Fatalf("keychain doctor: authOK=%v driverOK=%v", authOK, driverOK)
		}
	})
}

// agy's keychain is not unconditional on macOS: its auth package chooses between a
// keyring store and a file store, and an ssh/wsl/container detector can skip the
// keyring outright. Only the detectors' inputs are visible offline, and the
// fallback file's path is not derivable from the binary — so kae warns instead of
// switching a store it cannot name.
func TestAgyWarnsWhenTheKeychainMayBeBypassed(t *testing.T) {
	// The keychain probe is stubbed as logged-in, so the only warning left to see
	// is the bypass one.
	present := &runnertest.Fake{Stdout: "opaque-antigravity-token\n"}
	runner.With(present, func() {
		wantEnvConflictWarning(t, agyAdapter, map[string]string{"SSH_TTY": "/dev/ttys001"},
			"SSH_TTY is set: agy may bypass the keychain")
	})

	// A local session must stay silent.
	clean := testEnv(t, "darwin", nil)
	runner.With(present, func() {
		info, err := agyAdapter.Detect(context.Background(), clean)
		if err != nil || len(info.Warnings) != 0 {
			t.Fatalf("a local darwin session must not warn: %+v %v", info.Warnings, err)
		}
	})
}

// Off macOS agy keeps the file-based snapshot driver (Linux/WSL headless), with
// the keyring-likely warning when the CLI dir exists without a credential file.
func TestAgyFileSnapshotOffDarwin(t *testing.T) {
	env := testEnv(t, "linux", nil)
	specs, err := agyAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 3 {
		t.Fatalf("unexpected specs: %+v %v", specs, err)
	}
	if specs[0].Kind != constants.KindFile ||
		specs[0].Target != filepath.Join(env.Home, ".gemini", "antigravity-cli", "credentials.enc") {
		t.Fatalf("unexpected spec: %+v", specs[0])
	}

	info, err := agyAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("expected no auth: %+v %v", info, err)
	}

	// keyring-likely warning when the CLI dir exists without credential files
	write(t, filepath.Join(env.Home, ".gemini", "antigravity-cli", "settings.json"), `{}`)
	info, _ = agyAdapter.Detect(context.Background(), env)
	keyringWarned := false
	for _, warning := range info.Warnings {
		if strings.Contains(warning, "keyring") {
			keyringWarned = true
		}
	}
	if !keyringWarned {
		t.Fatalf("expected keyring warning: %+v", info.Warnings)
	}

	write(t, filepath.Join(env.Home, ".gemini", "antigravity-cli", "credentials.enc"), "opaque")
	info, _ = agyAdapter.Detect(context.Background(), env)
	if !info.AuthPresent || info.Driver != constants.DriverAgyFileSnapshot {
		t.Fatalf("unexpected: %+v", info)
	}
}

// TestAgyIdentityFromGoogleAccounts: agy reads the active Google account email
// from ~/.gemini/google_accounts.json so `kae add agy` can auto-detect a name
// and the snapshot records the identity.
func TestAgyIdentityFromGoogleAccounts(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	write(t, filepath.Join(env.Home, ".gemini", "google_accounts.json"),
		`{"active":"you@example.com","old":[]}`)
	got, err := agyAdapter.Identity(context.Background(), env)
	if err != nil || got != "you@example.com" {
		t.Fatalf("Identity = %q, err = %v; want you@example.com", got, err)
	}
}

func TestAgyIdentityMissingOrEmpty(t *testing.T) {
	// no file at all
	if _, err := agyAdapter.Identity(context.Background(), testEnv(t, "darwin", nil)); err == nil {
		t.Fatal("expected an error when google_accounts.json is absent")
	}
	// present but no active account
	env := testEnv(t, "darwin", nil)
	write(t, filepath.Join(env.Home, ".gemini", "google_accounts.json"), `{"active":"","old":[]}`)
	if _, err := agyAdapter.Identity(context.Background(), env); err == nil {
		t.Fatal("expected an error when no active Google account is recorded")
	}
}
