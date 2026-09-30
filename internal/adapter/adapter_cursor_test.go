// Cursor adapter tests: the opaque darwin keychain and other platforms.

package adapter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/adapter/cursor"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// cursor-agent writes access token, refresh token and API key as one unit, so all
// three are switched. The order is a contract: Detect reads specs[0] as the
// credential whose presence means "logged in", and it must be the access token —
// the API key is normally absent, and the refresh token alone is not a login.
func TestCursorArtifactsDarwinOpaqueKeychain(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := cursorAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []struct{ name, service string }{
		{"access_token", cursor.KeychainService},
		{"refresh_token", cursor.KeychainServiceRefresh},
		{"api_key", cursor.KeychainServiceAPIKey},
	}
	if len(specs) != len(want) {
		t.Fatalf("unexpected specs: %+v", specs)
	}
	for i, w := range want {
		sp := specs[i]
		if sp.Name != w.name || sp.Target != w.service || sp.Kind != constants.KindKeychain {
			t.Fatalf("spec %d: got %+v, want %s/%s", i, sp, w.name, w.service)
		}
		// An empty pointer marks the opaque (raw token) payload.
		if sp.Pointer != "" || sp.KeychainAccount != cursor.KeychainAccount {
			t.Fatalf("opaque spec %s must carry an empty pointer and the cursor-user account: %+v", w.name, sp)
		}
		// None of the three is IdentityOnly: an absent one must apply as absent so a
		// switch cannot leave the previous account's token behind.
		if sp.IdentityOnly {
			t.Fatalf("spec %s is a credential and must not be IdentityOnly", w.name)
		}
	}
}

func TestCursorUnsupportedOffDarwin(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		env := testEnv(t, goos, nil)
		if _, err := cursorAdapter.Artifacts(context.Background(), env); !errors.Is(err, adapter.ErrUnsupported) {
			t.Fatalf("%s: expected unsupported: %v", goos, err)
		}
		checks := cursorAdapter.Doctor(context.Background(), env)
		if len(checks) != 1 || checks[0].Code != constants.CheckUnsupported || checks[0].Status != constants.StatusError {
			t.Fatalf("%s: doctor must report a single unsupported error: %+v", goos, checks)
		}
	}
}
