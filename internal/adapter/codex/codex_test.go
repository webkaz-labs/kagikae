package codex

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

func testEnv(home string) adapter.Env {
	return adapter.Env{
		GOOS:     "linux",
		Home:     home,
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
}

// envWithCodexHome is testEnv with CODEX_HOME set, so the derived keychain
// account can be checked for a known path.
func envWithCodexHome(home, codexHomeDir string) adapter.Env {
	env := testEnv(home)
	env.Getenv = func(key string) string {
		if key == "CODEX_HOME" {
			return codexHomeDir
		}
		return ""
	}
	return env
}

// TestStoreKeyGolden pins codex's derivation against values computed *outside*
// kae, so the test cannot agree with a wrong formula here:
//
//	$ printf '%s' /kae-fixture/codexhome | shasum -a 256 | cut -c1-16
//
// It covers the prefix, the 16-hex truncation (claude's rule truncates to 8), and
// lowercase hex. The paths do not exist, which is also the documented fallback
// branch: codex hashes the unresolved path when canonicalize fails.
func TestStoreKeyGolden(t *testing.T) {
	for _, tc := range []struct{ codexHome, home, want string }{
		{codexHome: "/kae-fixture/codexhome", want: "cli|546dac80d1e8c587"},
		{home: "/kae-fixture/home", want: "cli|e4126022ee29b5b0"},
	} {
		if got := storeKey(envWithCodexHome(tc.home, tc.codexHome)); got != tc.want {
			t.Errorf("storeKey(CODEX_HOME=%q, HOME=%q) = %q, want %q",
				tc.codexHome, tc.home, got, tc.want)
		}
	}
}

// codex canonicalizes CODEX_HOME before hashing, so a symlink to a directory and
// the directory itself resolve to the same item — unlike claude, which hashes the
// raw env string. Getting this backwards writes an item codex never reads.
func TestStoreKeyResolvesSymlinksAndRelativePaths(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real-home")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link-home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	want := storeKey(envWithCodexHome("", real))
	if got := storeKey(envWithCodexHome("", link)); got != want {
		t.Errorf("symlinked CODEX_HOME = %q, want the resolved dir's %q", got, want)
	}
	t.Chdir(root)
	if got := storeKey(envWithCodexHome("", "real-home")); got != want {
		t.Errorf("relative CODEX_HOME = %q, want the absolute dir's %q", got, want)
	}
}

// makeJWT builds a minimal unsigned JWT carrying the given payload JSON.
func makeJWT(payloadJSON string) string {
	seg := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return seg(`{"alg":"none"}`) + "." + seg(payloadJSON) + "."
}

// writeCodexHome writes one file of the default codex home (created 0700).
func writeCodexHome(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeAuth(t *testing.T, home, body string) {
	t.Helper()
	writeCodexHome(t, home, "auth.json", body)
}

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	writeCodexHome(t, home, "config.toml", body)
}

// The store mapping is upstream's enum, not "keyring or else file": an absent key
// is `file`, `auto` is its own mode, and the two modes kae cannot switch are
// refused rather than silently treated as the file store.
func TestConfiguredStoreMapsUpstreamEnum(t *testing.T) {
	for _, tc := range []struct {
		name, config, want string
		wantUnsupported    bool
	}{
		{name: "absent config.toml is file", want: storeFile},
		{name: "empty key is file", config: "model = \"gpt-5\"\n", want: storeFile},
		{name: "keyring", config: "cli_auth_credentials_store = \"keyring\"\n", want: storeKeyring},
		{name: "auto", config: "cli_auth_credentials_store = \"auto\"\n", want: storeAuto},
		{name: "file", config: "cli_auth_credentials_store = \"file\"\n", want: storeFile},
		{
			name:            "ephemeral persists nothing",
			config:          "cli_auth_credentials_store = \"ephemeral\"\n",
			wantUnsupported: true,
		},
		{
			name:            "unknown value is not guessed at",
			config:          "cli_auth_credentials_store = \"vault\"\n",
			wantUnsupported: true,
		},
		{
			name:            "secret_auth_storage moves the credential out of the item",
			config:          "cli_auth_credentials_store = \"keyring\"\n[features]\nsecret_auth_storage = true\n",
			wantUnsupported: true,
		},
		{
			name:            "unparseable config.toml fails loud",
			config:          "cli_auth_credentials_store = \n",
			wantUnsupported: false, // a parse error, not an unsupported store
			want:            "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.config != "" {
				writeConfig(t, home, tc.config)
			}
			env := testEnv(home)
			env.GOOS = "darwin" // the keyring store is only readable there
			got, err := configuredStore(env)
			switch {
			case tc.wantUnsupported:
				if !errors.Is(err, adapter.ErrUnsupported) {
					t.Fatalf("store = %q, err = %v; want ErrUnsupported", got, err)
				}
			case tc.want == "":
				if err == nil {
					t.Fatalf("store = %q, want an error", got)
				}
			case err != nil || got != tc.want:
				t.Fatalf("store = %q, err = %v; want %q", got, err, tc.want)
			}
		})
	}
}

// The keyring spec is scoped to this codex home's item: the account is derived
// (never captured from whichever item happens to be first) and match-account is
// set, which is what keeps a switch off another CODEX_HOME's login.
func TestCodexKeyringSpecIsAccountScoped(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "cli_auth_credentials_store = \"keyring\"\n")
	env := testEnv(home)
	env.GOOS = "darwin"
	specs, err := Codex{}.Artifacts(t.Context(), env)
	if err != nil || len(specs) != 1 {
		t.Fatalf("Artifacts = %+v, err = %v", specs, err)
	}
	sp := specs[0]
	if !sp.KeychainMatchAccount {
		t.Error("keyring spec must be account-scoped (a service-only delete removes another codex home's item)")
	}
	if want := storeKey(env); sp.KeychainAccount != want {
		t.Errorf("KeychainAccount = %q, want the derived %q", sp.KeychainAccount, want)
	}
	if sp.Target != KeychainService || sp.Pointer != "/tokens" {
		t.Errorf("unexpected keyring spec: %+v", sp)
	}
}

// Off macOS the two keyring-capable modes part ways, because kae reads a keyring
// only through the macOS `security` CLI: `auto` resolves to the file store (where
// codex falls back with no keyring, and no probe is issued), while keyring-*only*
// is refused at the declaration point rather than handed out as a keychain spec
// whose every operation would fail with a raw `security` error.
func TestCodexKeyringStoresOffDarwin(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "cli_auth_credentials_store = \"auto\"\n")
	specs, err := Codex{}.Artifacts(t.Context(), testEnv(home))
	if err != nil || len(specs) != 1 || specs[0].Kind != constants.KindFile {
		t.Fatalf("auto: Artifacts = %+v, err = %v; want the file spec", specs, err)
	}
	writeConfig(t, home, "cli_auth_credentials_store = \"keyring\"\n")
	specs, err = Codex{}.Artifacts(t.Context(), testEnv(home))
	if !errors.Is(err, adapter.ErrUnsupported) {
		t.Fatalf("keyring: Artifacts = %+v, err = %v; want ErrUnsupported", specs, err)
	}
}

func TestCodexIdentityFromIDTokenEmail(t *testing.T) {
	home := t.TempDir()
	writeAuth(t, home, `{"tokens":{"id_token":"`+makeJWT(`{"email":"bob@example.com"}`)+`","account_id":"acct-123"}}`)
	got, err := Codex{}.Identity(t.Context(), testEnv(home))
	if err != nil || got != "bob@example.com" {
		t.Fatalf("Identity = %q, err = %v; want bob@example.com", got, err)
	}
}

func TestCodexIdentityFallsBackToAccountID(t *testing.T) {
	home := t.TempDir()
	writeAuth(t, home, `{"tokens":{"id_token":"not-a-jwt","account_id":"acct-123"}}`)
	got, err := Codex{}.Identity(t.Context(), testEnv(home))
	if err != nil || got != "acct-123" {
		t.Fatalf("Identity = %q, err = %v; want acct-123", got, err)
	}
}

func TestCodexIdentityMissing(t *testing.T) {
	home := t.TempDir()
	writeAuth(t, home, `{"tokens":{}}`)
	var c Codex
	if _, err := c.Identity(t.Context(), testEnv(home)); err == nil {
		t.Fatal("expected an error when no email claim or account_id is present")
	}
}

// makeJWTExp builds an unsigned JWT carrying an exp claim (seconds).
func makeJWTExp(exp int64) string { return makeJWT(fmt.Sprintf(`{"exp":%d}`, exp)) }

func TestCodexFreshnessJWTExpiryAndRefresh(t *testing.T) {
	exp := time.Date(2031, 6, 1, 0, 0, 0, 0, time.UTC)
	payload := fmt.Appendf(nil, `{"tokens":{"access_token":%q,"refresh_token":"r"}}`, makeJWTExp(exp.Unix()))
	info := Codex{}.Freshness(payload)
	if !info.Known || !info.HasRefresh || !info.ExpiresAt.Equal(exp) {
		t.Fatalf("Freshness = %+v (want exp %v, refresh true)", info, exp)
	}
}

func TestCodexFreshnessAPIKeyOnly(t *testing.T) {
	info := Codex{}.Freshness([]byte(`{"OPENAI_API_KEY":"sk-x"}`))
	if !info.Known || info.HasRefresh || !info.ExpiresAt.IsZero() {
		t.Fatalf("Freshness = %+v (want Known, no refresh, no expiry)", info)
	}
}

func TestCodexFreshnessUnparseable(t *testing.T) {
	if info := (Codex{}).Freshness([]byte("not json")); info.Known {
		t.Fatalf("Freshness on garbage = %+v (want Known=false)", info)
	}
}

// TestCodexDoctorWarnsRelativeHomeEvenWhenTheStoreIsUnreadable pins that the
// environment warning survives a store kae refuses. A `config.toml` it cannot
// parse is exactly when a relative CODEX_HOME matters most — kae is about to
// report the tool unsupported, and the relative path may be why it read the
// wrong file — but Doctor returned only the unsupported check, disagreeing with
// Detect, which appends the warning unconditionally.
func TestCodexDoctorWarnsRelativeHomeEvenWhenTheStoreIsUnreadable(t *testing.T) {
	home := t.TempDir()
	// A relative CODEX_HOME resolves against the *process* cwd, which is the
	// whole point of the warning, so the fixture has to live there too.
	t.Chdir(home)
	if err := os.MkdirAll(filepath.Join(home, "relcfg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "relcfg", "config.toml"),
		[]byte("this is not = valid toml [[[\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := adapter.Env{
		GOOS: "darwin", Home: home,
		Getenv:    func(k string) string { return map[string]string{"CODEX_HOME": "relcfg"}[k] },
		LookupEnv: func(k string) (string, bool) { v, ok := map[string]string{"CODEX_HOME": "relcfg"}[k]; return v, ok },
		LookPath:  func(string) (string, error) { return "", errors.New("not found") },
	}
	var unsupported, relative bool
	for _, c := range (Codex{}).Doctor(context.Background(), env) {
		if c.Code == constants.CheckUnsupported {
			unsupported = true
		}
		if c.Code == constants.CheckEnvConflict && strings.Contains(c.Message, "CODEX_HOME is relative") {
			relative = true
		}
	}
	if !unsupported {
		t.Error("an unparseable config.toml must still be reported")
	}
	if !relative {
		t.Error("the relative CODEX_HOME warning must survive an unreadable store")
	}
}
