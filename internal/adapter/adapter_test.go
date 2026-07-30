package adapter_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/adapter/agy"
	"github.com/webkaz-labs/kagikae/internal/adapter/claude"
	"github.com/webkaz-labs/kagikae/internal/adapter/codex"
	"github.com/webkaz-labs/kagikae/internal/adapter/copilot"
	"github.com/webkaz-labs/kagikae/internal/adapter/cursor"
	"github.com/webkaz-labs/kagikae/internal/adapter/opencode"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

var (
	claudeAdapter   = claude.Claude{}
	codexAdapter    = codex.Codex{}
	agyAdapter      = agy.Agy{}
	opencodeAdapter = opencode.Opencode{}
	cursorAdapter   = cursor.Cursor{}
	copilotAdapter  = copilot.Copilot{}
)

// testEnv injects LookupEnv as well as Getenv so tests exercise the same
// set-vs-empty predicate production does (Env.IsSet degrades to a non-empty test
// without it, which is a *different* predicate — a variable set to "" would read
// as absent).
func testEnv(t *testing.T, goos string, vars map[string]string) adapter.Env {
	t.Helper()
	home := t.TempDir()
	return adapter.Env{
		GOOS: goos,
		Home: home,
		Getenv: func(key string) string {
			return vars[key]
		},
		LookupEnv: func(key string) (string, bool) {
			value, ok := vars[key]
			return value, ok
		},
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
}

// wantEnvConflictWarning asserts that an environment condition is reported on
// **both** surfaces: Detect's Info.Warnings (which reaches the pre-write stderr
// warning) and Doctor's env_conflict check. Every adapter that warns about the
// environment owes both, and a warning present on one surface only is the bug
// this pins.
func wantEnvConflictWarning(t *testing.T, adp adapter.Adapter, vars map[string]string, want string) {
	t.Helper()
	env := testEnv(t, "darwin", vars)
	info, err := adp.Detect(context.Background(), env)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	found := false
	for _, warning := range info.Warnings {
		if strings.Contains(warning, want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a Detect warning containing %q: %+v", want, info.Warnings)
	}
	found = false
	for _, check := range adp.Doctor(context.Background(), env) {
		if check.Code == constants.CheckEnvConflict && check.Status == constants.StatusWarn &&
			strings.Contains(check.Message, want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a doctor env_conflict warning containing %q", want)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryHasAllTools(t *testing.T) {
	for _, tool := range constants.Tools {
		a, err := adapter.ForTool(tool)
		if err != nil || a.ID() != tool {
			t.Fatalf("adapter for %s: %v", tool, err)
		}
	}
	if _, err := adapter.ForTool("vscode"); err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

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

// TestKeychainDirBindableMatchesTheItemIdentity is the drift guard on the flag a
// seventh adapter would otherwise forget. KeychainDirBindable is a *claim* that the
// item's identity moves with the tool's isolation env var, and the per-directory
// materializer trusts it: claim it wrongly and kae writes a global login; omit it
// on a tool that does move and per-directory credentials silently degrade to a
// warning. So derive the truth — resolve each tool's specs with its isolation
// variable pointed at two different directories — and require the flag to agree.
//
// The truth is the whole item identity, service **and** account, because either
// half can be the one that moves: claude's service name is namespaced by
// `CLAUDE_CONFIG_DIR`, codex's account is a hash of `CODEX_HOME` under one fixed
// service. Deriving it from `Target` alone did worse than under-measure — it made
// the guard *contradict* its own consumer, so an adapter that binds by account and
// declared the flag honestly would fail here.
//
// The keyring `config.toml` matters as much as the derivation: without it codex
// resolves to `auth.json` (the store's default is `file`), its spec is not
// KindKeychain, and the tool the guard exists for is skipped entirely.
func TestKeychainDirBindableMatchesTheItemIdentity(t *testing.T) {
	// codex's identity does move, but the capability is not declared yet: each bound
	// directory would add a `Codex Auth` item and nothing tears it down on unpin or a
	// `pin -s` ↔ `pin -i` toggle (docs/ROADMAP.md). Carried by name so the gap stays a
	// gap, instead of being encoded here as "codex's identity is fixed" — which is
	// false, and is what a Target-only derivation quietly asserted.
	bindableNotYetDeclared := map[string]bool{constants.ToolCodex: true}
	isolationEnvVar := map[string]string{
		constants.ToolClaude: "CLAUDE_CONFIG_DIR",
		constants.ToolCodex:  "CODEX_HOME",
	}
	for _, tool := range constants.Tools {
		t.Run(tool, func(t *testing.T) {
			adp, err := adapter.ForTool(tool)
			if err != nil {
				t.Fatal(err)
			}
			specsFor := func(dir string) []artifact.Spec {
				vars := map[string]string{"USER": "alice"}
				if envVar := isolationEnvVar[tool]; envVar != "" {
					vars[envVar] = dir
				}
				// The keychain store, explicitly: `keyring` needs no live probe, so the
				// keychain spec resolves without a runner double.
				write(t, filepath.Join(dir, "config.toml"), "cli_auth_credentials_store = \"keyring\"\n")
				specs, err := adp.Artifacts(context.Background(), testEnv(t, "darwin", vars))
				if err != nil {
					t.Skipf("%s has no darwin artifacts: %v", tool, err)
				}
				return specs
			}
			// Two directories that exist, so only the identity is under test.
			first, second := specsFor(t.TempDir()), specsFor(t.TempDir())
			identity := func(sp artifact.Spec) string { return sp.Target + "\x00" + sp.KeychainAccount }
			keychainSpecs := 0
			for i := range first {
				if first[i].Kind != constants.KindKeychain {
					continue
				}
				keychainSpecs++
				moves := identity(first[i]) != identity(second[i])
				want := moves && !bindableNotYetDeclared[tool]
				if first[i].KeychainDirBindable != want {
					t.Errorf("%s/%s: KeychainDirBindable = %v but the item identity %s (%q vs %q)",
						tool, first[i].Name, first[i].KeychainDirBindable,
						map[bool]string{true: "moves with the isolation env var", false: "is fixed"}[moves],
						identity(first[i]), identity(second[i]))
				}
			}
			// A tool with an isolation variable and no keychain spec here means the
			// resolution went somewhere this guard cannot see, and the loop above
			// silently passed. That is how codex went unchecked.
			if isolationEnvVar[tool] != "" && keychainSpecs == 0 {
				t.Errorf("%s: no keychain spec resolved, so the flag was never checked", tool)
			}
		})
	}
}

// codex's `Codex Auth` item is scoped by its account rather than its service name,
// and that account *is* derived from `CODEX_HOME` — confirmed against a real item
// for a bond-dir-shaped path, symlink included (docs/VALIDATION.md). So the reason
// the flag stays false is no longer "unverified derivation": it is the item's
// lifecycle. Declaring it makes every pinned directory create a `Codex Auth` item,
// and nothing removes one on unpin or a `pin -s` ↔ `pin -i` toggle
// (docs/ROADMAP.md). Do not "fix" this by setting the flag; land the teardown.
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

// TestClaudeRefusesSecureStorageConfigDir covers the variable that moves both of
// claude's stores at once. The empty-value case is the dangerous one — it removes
// the per-directory suffix entirely, so every pin would collapse onto one shared
// item — and it is only visible through LookupEnv, which is why Env carries one.
func TestClaudeRefusesSecureStorageConfigDir(t *testing.T) {
	for _, value := range []string{"", "/somewhere/else"} {
		t.Run("value="+value, func(t *testing.T) {
			env := testEnv(t, "darwin", map[string]string{
				"USER":                     "alice",
				claude.EnvSecureStorageDir: value,
			})
			if _, err := claudeAdapter.Artifacts(context.Background(), env); !errors.Is(err, adapter.ErrUnsupported) {
				t.Fatalf("expected unsupported, got %v", err)
			}
		})
	}
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
	warned := strings.Join(info.Warnings, "\n")
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
	if len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0], "ANTHROPIC_API_KEY") {
		t.Fatalf("expected env conflict warning: %+v", info.Warnings)
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
	if len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0], "keyring") {
		t.Fatalf("expected keyring-possibility warning under auto: %+v", info.Warnings)
	}
}

func TestOpencodeArtifactsAndXDGDataHome(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := opencodeAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 {
		t.Fatalf("unexpected specs: %+v %v", specs, err)
	}
	if specs[0].Kind != constants.KindJSONPointer ||
		specs[0].Target != filepath.Join(env.Home, ".local", "share", "opencode", "auth.json") ||
		specs[0].Pointer != "/openai" {
		t.Fatalf("unexpected auth spec: %+v", specs[0])
	}

	dataHome := t.TempDir()
	env = testEnv(t, "darwin", map[string]string{"XDG_DATA_HOME": dataHome})
	specs, err = opencodeAdapter.Artifacts(context.Background(), env)
	if err != nil || specs[0].Target != filepath.Join(dataHome, "opencode", "auth.json") {
		t.Fatalf("XDG_DATA_HOME not honored: %+v %v", specs, err)
	}

	// A relative value is ignored per the XDG spec (paths.XDGDataHome).
	env = testEnv(t, "darwin", map[string]string{"XDG_DATA_HOME": "relative/data"})
	specs, err = opencodeAdapter.Artifacts(context.Background(), env)
	if err != nil || specs[0].Target != filepath.Join(env.Home, ".local", "share", "opencode", "auth.json") {
		t.Fatalf("relative XDG_DATA_HOME must fall back to the default: %+v %v", specs, err)
	}
}

func TestOpencodeDetect(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	info, err := opencodeAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("expected no auth without auth.json: %+v %v", info, err)
	}
	if len(info.Warnings) != 0 {
		t.Fatalf("missing auth.json must not warn: %+v", info.Warnings)
	}

	authPath := filepath.Join(env.Home, ".local", "share", "opencode", "auth.json")

	// API-key-only auth.json: no subscription login, explanatory warning.
	write(t, authPath, `{"openrouter":{"type":"api","key":"sk-x"}}`)
	info, err = opencodeAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("expected no auth without an openai entry: %+v %v", info, err)
	}
	if len(info.Warnings) != 1 || !strings.Contains(info.Warnings[0], "openai") {
		t.Fatalf("expected missing-openai warning: %+v", info.Warnings)
	}

	write(t, authPath, `{"openai":{"type":"oauth","refresh":"r","access":"a"},"openrouter":{"type":"api","key":"sk-x"}}`)
	info, err = opencodeAdapter.Detect(context.Background(), env)
	if err != nil || !info.AuthPresent || info.Driver != constants.DriverOpencodeFilePatch {
		t.Fatalf("unexpected: %+v %v", info, err)
	}
}

// Two ways the switched auth.json stops being what opencode reads, both visible
// offline: OPENCODE_AUTH_CONTENT carries the whole body inline and is consulted
// before the file, and a relative XDG_DATA_HOME (which opencode uses verbatim,
// against its own working directory, while kae ignores it per the XDG spec) puts
// the two on different files. Neither can be fixed by writing somewhere else, so
// both warn on Detect and in doctor.
func TestOpencodeWarnsWhenAuthJSONIsNotWhatItReads(t *testing.T) {
	wantEnvConflictWarning(t, opencodeAdapter,
		map[string]string{opencode.EnvAuthContent: `{"openai":{}}`}, opencode.EnvAuthContent)
	wantEnvConflictWarning(t, opencodeAdapter,
		map[string]string{"XDG_DATA_HOME": "relative/data"}, "XDG_DATA_HOME is relative")

	// An absolute value is the normal case and must stay silent.
	env := testEnv(t, "darwin", map[string]string{"XDG_DATA_HOME": t.TempDir()})
	info, err := opencodeAdapter.Detect(context.Background(), env)
	if err != nil || len(info.Warnings) != 0 {
		t.Fatalf("an absolute XDG_DATA_HOME must not warn: %+v %v", info.Warnings, err)
	}
}

func TestOpencodeRefusesUnrecognizedAuthJSON(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := opencodeAdapter.Artifacts(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	authPath := specs[0].Target

	// Malformed auth.json: reading refuses instead of misparsing.
	write(t, authPath, `not json`)
	if _, err := artifact.ReadLive(context.Background(), specs[0]); !errors.Is(err, artifact.ErrUnsafe) {
		t.Fatalf("expected structure-guard refusal: %v", err)
	}
	checks := opencodeAdapter.Doctor(context.Background(), env)
	foundError := false
	for _, check := range checks {
		if check.Code == constants.CheckAuthPresent && check.Status == constants.StatusError {
			foundError = true
		}
	}
	if !foundError {
		t.Fatalf("doctor should flag the unrecognized auth.json: %+v", checks)
	}

	// Non-object root: applying refuses instead of replacing the file.
	write(t, authPath, `["not","an","object"]`)
	err = artifact.ApplyLive(context.Background(), specs[0],
		artifact.Value{Data: []byte(`{"type":"oauth"}`), Present: true})
	if !errors.Is(err, artifact.ErrUnsafe) {
		t.Fatalf("expected apply refusal on non-object root: %v", err)
	}
}

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

const copilotConfigFixture = `// User settings belong in settings.json.
// This file is managed automatically.
{
  "trustedFolders": ["/workspaces"],
  "lastLoggedInUser": {"host":"https://github.com","login":"main"},
  "loggedInUsers": [{"host":"https://github.com","login":"main"}]
}
`

func TestCopilotArtifactsJSONCPointer(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := copilotAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 {
		t.Fatalf("unexpected specs: %+v %v", specs, err)
	}
	if specs[0].Kind != constants.KindJSONPointer || specs[0].Pointer != "/lastLoggedInUser" || !specs[0].JSONC {
		t.Fatalf("expected a JSONC json-pointer spec: %+v", specs[0])
	}
	if specs[0].Target != filepath.Join(env.Home, ".copilot", "config.json") {
		t.Fatalf("unexpected target: %+v", specs[0])
	}
}

// COPILOT_HOME replaces ~/.copilot outright — it is the config directory itself,
// not a parent. Measured at 1.0.61, where it is also the mechanism copilot's own
// (deprecated) --config-dir flag points users at.
func TestCopilotHonorsCopilotHome(t *testing.T) {
	copilotHome := t.TempDir()
	env := testEnv(t, "darwin", map[string]string{copilot.EnvHome: copilotHome})
	specs, err := copilotAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 {
		t.Fatalf("unexpected specs: %+v %v", specs, err)
	}
	if specs[0].Target != filepath.Join(copilotHome, "config.json") {
		t.Fatalf("COPILOT_HOME not honored: %+v", specs[0])
	}
	// The default location must not be consulted at all: a config.json there is
	// a different account's pointer, so reading it would report the wrong login.
	write(t, filepath.Join(env.Home, ".copilot", "config.json"), copilotConfigFixture)
	info, err := copilotAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("$HOME/.copilot must be ignored while COPILOT_HOME is set: %+v %v", info, err)
	}
	write(t, filepath.Join(copilotHome, "config.json"), copilotConfigFixture)
	info, err = copilotAdapter.Detect(context.Background(), env)
	if err != nil || !info.AuthPresent {
		t.Fatalf("COPILOT_HOME config.json not detected: %+v %v", info, err)
	}
	if len(info.Warnings) != 0 {
		t.Fatalf("an absolute COPILOT_HOME must not warn: %+v", info.Warnings)
	}
}

// A relative COPILOT_HOME resolves against the *tool's* working directory, and
// kae is invoked from anywhere in a project — so the file kae writes is the file
// copilot reads only while both run from the same directory. kae keeps following
// the value (there is no default to fall back to while it is set) and warns,
// because the alternative is a silently wrong write with every guard green.
func TestCopilotWarnsOnRelativeCopilotHome(t *testing.T) {
	wantEnvConflictWarning(t, copilotAdapter,
		map[string]string{copilot.EnvHome: ".copilot-local"}, copilot.EnvHome+" is relative")
}

func TestCopilotDetect(t *testing.T) {
	env := testEnv(t, "linux", nil)
	info, err := copilotAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("no config.json should mean no auth: %+v %v", info, err)
	}

	cfg := filepath.Join(env.Home, ".copilot", "config.json")
	write(t, cfg, copilotConfigFixture)
	info, err = copilotAdapter.Detect(context.Background(), env)
	if err != nil || !info.AuthPresent || info.Driver != constants.DriverCopilotConfigPointer {
		t.Fatalf("unexpected: %+v %v", info, err)
	}

	// env override is warned about.
	env = testEnv(t, "linux", map[string]string{"GH_TOKEN": "x"})
	write(t, filepath.Join(env.Home, ".copilot", "config.json"), copilotConfigFixture)
	info, _ = copilotAdapter.Detect(context.Background(), env)
	warned := false
	for _, w := range info.Warnings {
		if strings.Contains(w, "GH_TOKEN") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("expected GH_TOKEN warning: %+v", info.Warnings)
	}
}

func TestCopilotRefusesBrokenConfig(t *testing.T) {
	env := testEnv(t, "linux", nil)
	write(t, filepath.Join(env.Home, ".copilot", "config.json"), `// c`+"\n"+`{not json`)
	checks := copilotAdapter.Doctor(context.Background(), env)
	foundError := false
	for _, check := range checks {
		if check.Code == constants.CheckAuthPresent && check.Status == constants.StatusError {
			foundError = true
		}
	}
	if !foundError {
		t.Fatalf("doctor should flag the unparseable config: %+v", checks)
	}
}

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

// TestIdentifierConformance pins that every tool adapter exposes a readable
// login identity (adapter.Identifier) so `kae add <tool>` can auto-detect a name
// and `kae ls`/`accounts`/`status` can show it. agy was the last gap (v0.8.7).
func TestIdentifierConformance(t *testing.T) {
	all := map[string]adapter.Adapter{
		"claude": claudeAdapter, "codex": codexAdapter, "agy": agyAdapter,
		"opencode": opencodeAdapter, "cursor": cursorAdapter, "copilot": copilotAdapter,
	}
	for name, ad := range all {
		if _, ok := ad.(adapter.Identifier); !ok {
			t.Errorf("%s adapter does not implement adapter.Identifier", name)
		}
	}
}

// TestVerifiedVersionFormat pins that every registered adapter's declared
// version parses as a triple, since doctor skips an unparseable one silently and
// a typo would look like "nothing to report". VerifiedVersion is a method of
// adapter.Adapter, so the compiler already enforces that every tool declares one;
// this only guards the *value*. Driven off constants.Tools so a seventh tool is
// covered without editing the test.
//
// "" is allowed and means "no usable signal, skip me": cursor is date-versioned,
// so the comparison reads a new build month as a minor bump and would warn every
// month (see cursor.VerifiedVersion).
func TestVerifiedVersionFormat(t *testing.T) {
	triple := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	for _, tool := range constants.Tools {
		ad, err := adapter.ForTool(tool)
		if err != nil {
			t.Fatalf("adapter for %s: %v", tool, err)
		}
		if got := ad.VerifiedVersion(); got != "" && !triple.MatchString(got) {
			t.Errorf("%s VerifiedVersion() = %q, want a major.minor.patch triple or \"\"", tool, got)
		}
	}
}

// TestIdentityKeysConformance pins the IdentityOnly ⇔ IdentityKeys invariant for
// every adapter on both platforms. An IdentityOnly spec that forgets
// IdentityKeys degrades *silently* to a byte comparison, which is the false-drift
// bug identityDiffers was written to fix (the tool renews a timestamp inside the
// payload and doctor accuses a correctly switched account). The reverse — keys
// without IdentityOnly — is dead declaration: both consumers filter on
// IdentityOnly first, so it would never be read.
func TestIdentityKeysConformance(t *testing.T) {
	for _, tool := range constants.Tools {
		ad, err := adapter.ForTool(tool)
		if err != nil {
			t.Fatalf("adapter for %s: %v", tool, err)
		}
		for _, goos := range []string{"linux", "darwin"} {
			// Artifacts is pure path/config resolution (no subprocess), so this
			// never reaches a real keychain.
			specs, err := ad.Artifacts(context.Background(), testEnv(t, goos, nil))
			if err != nil {
				continue // unsupported platform: nothing to check
			}
			for _, sp := range specs {
				if sp.IdentityOnly == (len(sp.IdentityKeys) > 0) {
					continue
				}
				t.Errorf("%s/%s artifact %q: IdentityOnly=%v but IdentityKeys=%v; "+
					"an identity spec without keys silently degrades to a byte comparison, "+
					"and keys without IdentityOnly are never read",
					tool, goos, sp.Name, sp.IdentityOnly, sp.IdentityKeys)
			}
		}
	}
}

// TestFresherConformance pins which adapters expose readable credential
// freshness (§A): claude/codex/opencode/cursor are datable; copilot's pointer
// and agy's opaque blob are not, so they must not implement adapter.Fresher
// (cmd.freshnessOf then treats them as Known=false).
func TestFresherConformance(t *testing.T) {
	datable := map[adapter.Adapter]bool{
		claudeAdapter: true, codexAdapter: true,
		opencodeAdapter: true, cursorAdapter: true,
		copilotAdapter: false, agyAdapter: false,
	}
	for ad, want := range datable {
		if _, ok := ad.(adapter.Fresher); ok != want {
			t.Fatalf("%s Fresher=%v, want %v", ad.ID(), ok, want)
		}
	}
}

// TestKeychainSpecsAreAccountScoped pins that every keychain artifact kae ships
// is identified by service **and** account.
//
// A service-only spec reads the service's *first* item, writes with whatever
// account the live item happens to carry, and deletes every item of the service.
// All three are wrong the moment a service holds an item kae did not put there:
// codex shipped a switch that deleted another CODEX_HOME's login that way, and
// claude's reads are account-scoped, so an item left under a former $USER is one
// the tool cannot see while kae keeps writing to it. Every adapter's account is
// derivable — a constant (cursor, agy), $USER (claude) or the tool home (codex) —
// so there is no case left for guessing it from the live item.
func TestKeychainSpecsAreAccountScoped(t *testing.T) {
	for _, tool := range constants.Tools {
		ad, err := adapter.ForTool(tool)
		if err != nil {
			t.Fatalf("adapter for %s: %v", tool, err)
		}
		for _, goos := range []string{"linux", "darwin"} {
			specs, err := ad.Artifacts(context.Background(), testEnv(t, goos, nil))
			if err != nil {
				continue // unsupported platform: nothing to check
			}
			for _, sp := range specs {
				if sp.Kind != constants.KindKeychain {
					continue
				}
				if !sp.KeychainMatchAccount || sp.KeychainAccount == "" {
					t.Errorf("%s/%s artifact %q: keychain specs must set KeychainMatchAccount "+
						"and a non-empty KeychainAccount (got %v / %q); service-only IO reads the "+
						"first item, writes under the live item's account and deletes every sibling",
						tool, goos, sp.Name, sp.KeychainMatchAccount, sp.KeychainAccount)
				}
			}
		}
	}
}

