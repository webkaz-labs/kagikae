// Claude adapter tests: artifacts, keychain addressing, secure storage and detection.

package adapter_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/adapter/claude"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

func TestClaudeArtifactsLinux(t *testing.T) {
	env := testEnv(t, "linux", nil)
	specs, err := claudeAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs: %+v", specs)
	}
	if specs[0].Kind != constants.KindJSONPointer ||
		specs[0].Target != filepath.Join(env.Home, ".claude", ".credentials.json") ||
		specs[0].Pointer != "/claudeAiOauth" {
		t.Fatalf("unexpected credentials spec: %+v", specs[0])
	}
}

// The identity cache lives in the mixed-state ~/.claude.json (never in the
// credential file), is patched by pointer only, and is optional so snapshots
// captured before it existed still apply.
func TestClaudeOAuthAccountSpec(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		env := testEnv(t, goos, map[string]string{"USER": "alice"})
		specs, err := claudeAdapter.Artifacts(context.Background(), env)
		if err != nil {
			t.Fatal(err)
		}
		sp := specs[len(specs)-1]
		if sp.Name != "oauth_account" || sp.Kind != constants.KindJSONPointer ||
			sp.Target != filepath.Join(env.Home, ".claude.json") ||
			sp.Pointer != "/oauthAccount" || !sp.IdentityOnly {
			t.Fatalf("%s: unexpected identity-cache spec: %+v", goos, sp)
		}
	}
}

// The identity cache carries no expiresAt. Callers walk every stored artifact
// and take the first datable one, so it must report Known=false: a zero expiry
// would read as long expired and warn on every switch.
func TestClaudeFreshnessSkipsIdentityCache(t *testing.T) {
	identity := []byte(`{"accountUuid":"u1","emailAddress":"you@example.com"}`)
	if info := claudeAdapter.Freshness(identity); info.Known {
		t.Fatalf("identity cache must not be datable: %+v", info)
	}
	cred := []byte(`{"claudeAiOauth":{"expiresAt":1785350072021,"refreshToken":"r"}}`)
	if info := claudeAdapter.Freshness(cred); !info.Known || !info.HasRefresh {
		t.Fatalf("credential must stay datable: %+v", info)
	}
}

// With CLAUDE_CONFIG_DIR set, Claude Code keeps .claude.json inside that
// directory, so the identity cache must follow it and not the real home.
func TestClaudeOAuthAccountHonorsConfigDir(t *testing.T) {
	configDir := t.TempDir()
	env := testEnv(t, "linux", map[string]string{"CLAUDE_CONFIG_DIR": configDir})
	specs, err := claudeAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if got := specs[len(specs)-1].Target; got != filepath.Join(configDir, ".claude.json") {
		t.Fatalf("CLAUDE_CONFIG_DIR not honored: %s", got)
	}
}

func TestClaudeArtifactsDarwin(t *testing.T) {
	env := testEnv(t, "darwin", map[string]string{"USER": "alice"})
	specs, err := claudeAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Kind != constants.KindKeychain || specs[0].Target != claude.KeychainService {
		t.Fatalf("unexpected keychain spec: %+v", specs[0])
	}
	if specs[0].KeychainAccount != "alice" {
		t.Fatalf("fallback account not propagated: %+v", specs[0])
	}
}

// pinConfigDir is the shape of a real `kae pin --isolated` config dir, and
// pinConfigDirSHA8 is the suffix Claude Code derives from it. The expected
// hashes are computed **outside** kae (`printf %s <path> | shasum -a 256`) on
// purpose: deriving them with kae's own hash would make this test agree with any
// formula, including a wrong one. These paths are pure ASCII, so NFC leaves them
// alone and `shasum` is exact; the normalization step has its own test below.
const (
	pinConfigDir     = "/home/u/.local/share/kagikae/isolation/deadbeefdeadbeef/claude/isolated/side/config"
	pinConfigDirSHA8 = "b43dacab"
	// The same path with a trailing slash is a *different* keychain item, because
	// claude hashes the environment string with no path cleaning at all.
	pinConfigDirTrailingSlashSHA8 = "430765be"
)

func TestClaudeKeychainServiceIsPerConfigDir(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configDir string
		want      string
	}{
		{"unset keeps the shared item", "", claude.KeychainService},
		{"set namespaces by config dir", pinConfigDir, claude.KeychainService + "-" + pinConfigDirSHA8},
		{"trailing slash is a different item", pinConfigDir + "/", claude.KeychainService + "-" + pinConfigDirTrailingSlashSHA8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t, "darwin", map[string]string{
				"USER":              "alice",
				"CLAUDE_CONFIG_DIR": tc.configDir,
			})
			specs, err := claudeAdapter.Artifacts(context.Background(), env)
			if err != nil {
				t.Fatal(err)
			}
			if specs[0].Kind != constants.KindKeychain || specs[0].Target != tc.want {
				t.Fatalf("keychain target = %q, want %q", specs[0].Target, tc.want)
			}
			// KeychainDirBindable is what tells the per-directory materializer the
			// item is safe to write for one bound directory; it must be true
			// exactly when the name is namespaced.
			if wantScoped := tc.configDir != ""; specs[0].KeychainDirBindable != wantScoped {
				t.Fatalf("KeychainDirBindable = %v, want %v", specs[0].KeychainDirBindable, wantScoped)
			}
		})
	}
}

// TestClaudeKeychainServiceNormalizesToNFC pins the normalization step. claude
// hashes the config dir after NFC-normalizing it, so a decomposed path — which
// macOS can hand back for any non-ASCII component of a home or XDG_DATA_HOME —
// must resolve to the *same* item as its composed form. Hashing the bytes as
// given would make kae write an item claude never reads, which is the failure
// this whole area exists to prevent.
//
// Both expected hashes are computed outside kae (python3 hashlib over the NFC
// form), so the composed spelling is pinned rather than merely self-consistent.
func TestClaudeKeychainServiceNormalizesToNFC(t *testing.T) {
	// Escapes, not literal accents: the two forms must stay distinguishable in
	// the source, where they would render identically.
	const (
		nfc  = "/home/u/caf\u00e9/config"  // \u00e9 as a single code point
		nfd  = "/home/u/cafe\u0301/config" // e + combining acute accent
		want = claude.KeychainService + "-42ef30c3"
	)
	for name, dir := range map[string]string{"composed": nfc, "decomposed": nfd} {
		t.Run(name, func(t *testing.T) {
			env := testEnv(t, "darwin", map[string]string{
				"USER":              "alice",
				"CLAUDE_CONFIG_DIR": dir,
			})
			specs, err := claudeAdapter.Artifacts(context.Background(), env)
			if err != nil {
				t.Fatal(err)
			}
			if specs[0].Target != want {
				t.Fatalf("keychain target = %q, want %q", specs[0].Target, want)
			}
		})
	}
}

// TestClaudeKeychainAccountMirrorsUpstream pins claude's own rule
// (`$USER || os.userInfo().username`, then validate, else the literal). The
// account attribute is load-bearing: claude's reads are account-scoped, so an
// item written under the wrong one is invisible to it even with the right
// service name.
func TestClaudeKeychainAccountMirrorsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name     string
		user     string
		username string
		want     string
	}{
		{"USER wins", "alice", "beta", "alice"},
		{"OS username fills in for an unset USER", "", "beta", "beta"},
		{"an invalid USER does not fall through to the OS name", "not a name", "beta", "claude-code-user"},
		{"neither usable", "", "", "claude-code-user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t, "darwin", map[string]string{"USER": tc.user})
			env.Username = tc.username
			specs, err := claudeAdapter.Artifacts(context.Background(), env)
			if err != nil {
				t.Fatal(err)
			}
			if specs[0].KeychainAccount != tc.want {
				t.Fatalf("KeychainAccount = %q, want %q", specs[0].KeychainAccount, tc.want)
			}
		})
	}
}

// TestClaudeRefusesEmptySecureStorageConfigDir pins the one value of that
// variable kae still refuses. An empty value removes the per-config-dir suffix
// entirely, so every bound directory collapses onto claude's one global item and
// `kae use` silently changes what a bound directory runs. It is visible only
// through LookupEnv, which is why Env carries one — a non-empty value is a
// different mechanism and is honored (the test below).
func TestClaudeRefusesEmptySecureStorageConfigDir(t *testing.T) {
	env := testEnv(t, "darwin", map[string]string{
		"USER":                     "alice",
		claude.EnvSecureStorageDir: "",
	})
	if _, err := claudeAdapter.Artifacts(context.Background(), env); !errors.Is(err, adapter.ErrUnsupported) {
		t.Fatalf("expected unsupported, got %v", err)
	}
}

// TestClaudeSecureStorageConfigDirSplitsTheStores is the mechanism the
// per-account credential store rests on: with both variables set, the credential
// follows CLAUDE_SECURESTORAGE_CONFIG_DIR and everything else follows
// CLAUDE_CONFIG_DIR. Asserting the two *differ* is the point — a single-variable
// model passes every "the item is namespaced" check while writing the item claude
// does not read.
//
// The expected suffix is computed outside kae (python3 hashlib over the NFC form,
// as in TestClaudeKeychainServiceNormalizesToNFC), and the config dir's own hash
// is asserted absent so a fallback to it cannot pass.
func TestClaudeSecureStorageConfigDirSplitsTheStores(t *testing.T) {
	const (
		credDir      = "/data/credstore/claude/main"
		configDir    = "/data/pin/claude/config"
		wantService  = claude.KeychainService + "-1b3c6671" // sha8(credDir)
		configSuffix = "7ab99601"                           // sha8(configDir), must not appear
	)
	envVars := map[string]string{
		"USER":                     "alice",
		"CLAUDE_CONFIG_DIR":        configDir,
		claude.EnvSecureStorageDir: credDir,
	}

	t.Run("darwin keychain item follows the credential dir", func(t *testing.T) {
		specs, err := claudeAdapter.Artifacts(context.Background(), testEnv(t, "darwin", envVars))
		if err != nil {
			t.Fatal(err)
		}
		if specs[0].Target != wantService {
			t.Fatalf("keychain target = %q, want %q", specs[0].Target, wantService)
		}
		if strings.Contains(specs[0].Target, configSuffix) {
			t.Fatalf("keychain target %q is namespaced by the config dir, not the credential dir", specs[0].Target)
		}
		if !specs[0].KeychainDirBindable {
			t.Fatal("a namespaced item must stay bindable, or no bound directory may write it")
		}
	})

	t.Run("linux credential file follows the credential dir", func(t *testing.T) {
		specs, err := claudeAdapter.Artifacts(context.Background(), testEnv(t, "linux", envVars))
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(credDir, ".credentials.json"); specs[0].Target != want {
			t.Fatalf("credential target = %q, want %q", specs[0].Target, want)
		}
	})

	t.Run("the identity cache stays with the config dir", func(t *testing.T) {
		specs, err := claudeAdapter.Artifacts(context.Background(), testEnv(t, "darwin", envVars))
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(configDir, ".claude.json"); specs[1].Target != want {
			t.Fatalf("identity target = %q, want %q", specs[1].Target, want)
		}
	})
}

// The refusal must not fire on an absent variable — that is every normal run.
func TestClaudeAllowsAbsentSecureStorageConfigDir(t *testing.T) {
	env := testEnv(t, "darwin", map[string]string{"USER": "alice"})
	if _, err := claudeAdapter.Artifacts(context.Background(), env); err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
}

// TestClaudeRefusesCustomOAuthURL covers the variable that renames both stores
// through the build's OAuth suffix. The empty value must *not* refuse: claude
// tests it for truthiness, so an empty one changes nothing — the opposite of
// EnvSecureStorageDir, and the reason this cannot reuse IsSet.
func TestClaudeRefusesCustomOAuthURL(t *testing.T) {
	for _, tc := range []struct {
		value  string
		refuse bool
	}{
		{value: "https://oauth.example.com", refuse: true},
		{value: "", refuse: false},
	} {
		t.Run("value="+tc.value, func(t *testing.T) {
			env := testEnv(t, "darwin", map[string]string{
				"USER":                       "alice",
				claude.EnvCustomOAuthURL:     tc.value,
				constants.EnvKaeClaudeDriver: constants.DriverValueFile,
			})
			_, err := claudeAdapter.Artifacts(context.Background(), env)
			if tc.refuse != errors.Is(err, adapter.ErrUnsupported) {
				t.Fatalf("refuse=%v, got err %v", tc.refuse, err)
			}
		})
	}
}

// The host-managed provider supplies the login from outside kae's stores, and the
// variable holding the token is whatever CLAUDE_CODE_HOST_AUTH_ENV_VAR names — so
// the warning has to fire on the mechanism.
func TestClaudeWarnsOnHostManagedProvider(t *testing.T) {
	env := testEnv(t, "linux", map[string]string{
		"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST": "1",
		"CLAUDE_CODE_HOST_CREDS_FILE":          "/run/host-creds.json",
	})
	write(t, filepath.Join(env.Home, ".claude", ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok"}}`)
	info, err := claudeAdapter.Detect(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	warned := strings.Join(l10ntest.English(info.Warnings), "\n")
	if len(info.Warnings) != 2 ||
		!strings.Contains(warned, "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST") ||
		!strings.Contains(warned, "CLAUDE_CODE_HOST_CREDS_FILE") {
		t.Fatalf("expected both host-managed warnings: %+v", info.Warnings)
	}
}

func TestClaudeDriverOverrideForcesFileOnDarwin(t *testing.T) {
	configDir := t.TempDir()
	env := testEnv(t, "darwin", map[string]string{
		constants.EnvKaeClaudeDriver: constants.DriverValueFile,
		"CLAUDE_CONFIG_DIR":          configDir,
	})
	specs, err := claudeAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Kind != constants.KindJSONPointer ||
		specs[0].Target != filepath.Join(configDir, ".credentials.json") {
		t.Fatalf("override did not force the file driver: %+v", specs[0])
	}
}

func TestClaudeDriverOverrideRejectsUnknownValue(t *testing.T) {
	env := testEnv(t, "darwin", map[string]string{constants.EnvKaeClaudeDriver: "keychain"})
	if _, err := claudeAdapter.Artifacts(context.Background(), env); !errors.Is(err, adapter.ErrUnsupported) {
		t.Fatalf("expected unsupported for invalid override value: %v", err)
	}
}

func TestClaudeArtifactsWindowsUnsupported(t *testing.T) {
	env := testEnv(t, "windows", nil)
	if _, err := claudeAdapter.Artifacts(context.Background(), env); !errors.Is(err, adapter.ErrUnsupported) {
		t.Fatalf("expected unsupported: %v", err)
	}
}

func TestClaudeHonorsConfigDir(t *testing.T) {
	configDir := t.TempDir()
	env := testEnv(t, "linux", map[string]string{"CLAUDE_CONFIG_DIR": configDir})
	specs, err := claudeAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Target != filepath.Join(configDir, ".credentials.json") {
		t.Fatalf("CLAUDE_CONFIG_DIR not honored: %+v", specs[0])
	}
}

func TestClaudeDetectLinux(t *testing.T) {
	env := testEnv(t, "linux", map[string]string{"ANTHROPIC_API_KEY": "sk-x"})
	write(t, filepath.Join(env.Home, ".claude", ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok"}}`)
	info, err := claudeAdapter.Detect(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if !info.AuthPresent || info.Driver != constants.DriverClaudeFilePatch {
		t.Fatalf("unexpected info: %+v", info)
	}
	if len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0].Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("expected env conflict warning: %+v", info.Warnings)
	}
}
