package adapter_test

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/adapter/agy"
	"github.com/webkaz-labs/kagikae/internal/adapter/claude"
	"github.com/webkaz-labs/kagikae/internal/adapter/codex"
	"github.com/webkaz-labs/kagikae/internal/adapter/copilot"
	"github.com/webkaz-labs/kagikae/internal/adapter/cursor"
	"github.com/webkaz-labs/kagikae/internal/adapter/opencode"
	"github.com/webkaz-labs/kagikae/internal/constants"
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
	wantEnvConflictWarningOn(t, "darwin", adp, vars, want)
}

// wantEnvConflictWarningOn is wantEnvConflictWarning for a chosen platform. An
// environment warning is platform-independent by construction, but Detect is
// not: claude's darwin driver reads the keychain, which needs a `security` this
// repository's CI does not have. Assert those on linux rather than making the
// test depend on the host.
func wantEnvConflictWarningOn(t *testing.T, goos string, adp adapter.Adapter, vars map[string]string, want string) {
	t.Helper()
	env := testEnv(t, goos, vars)
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
// freshness: claude/codex/opencode/cursor are datable; copilot's pointer
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
	// A guard that silently checks nothing is worse than none — codex's keychain
	// spec only appears under a keyring config.toml, and this test skipped it
	// entirely until that was noticed. So assert *which* adapters it reached.
	wantChecked := map[string]bool{
		constants.ToolClaude: true, constants.ToolCodex: true,
		constants.ToolCursor: true, constants.ToolAgy: true,
	}
	checked := map[string]bool{}
	for _, tool := range constants.Tools {
		ad, err := adapter.ForTool(tool)
		if err != nil {
			t.Fatalf("adapter for %s: %v", tool, err)
		}
		for _, goos := range []string{"linux", "darwin"} {
			env := testEnv(t, goos, nil)
			// codex is the one tool whose keychain spec is *conditional*: with no
			// config.toml the store resolves to the default `file`, so a plain
			// environment produces no KindKeychain spec and this guard would skip
			// the very adapter the v0.12.0 regression came from.
			if tool == constants.ToolCodex {
				write(t, filepath.Join(env.Home, ".codex", "config.toml"),
					"cli_auth_credentials_store = \"keyring\"\n")
			}
			specs, err := ad.Artifacts(context.Background(), env)
			if err != nil {
				continue // unsupported platform: nothing to check
			}
			anyKeychain := false
			for _, sp := range specs {
				if sp.Kind != constants.KindKeychain {
					continue
				}
				anyKeychain = true
				if !sp.KeychainMatchAccount || sp.KeychainAccount == "" {
					t.Errorf("%s/%s artifact %q: keychain specs must set KeychainMatchAccount "+
						"and a non-empty KeychainAccount (got %v / %q); service-only IO reads the "+
						"first item, writes under the live item's account and deletes every sibling",
						tool, goos, sp.Name, sp.KeychainMatchAccount, sp.KeychainAccount)
				}
			}
			if goos == "darwin" && anyKeychain {
				checked[tool] = true
			}
		}
	}
	if !maps.Equal(checked, wantChecked) {
		t.Errorf("this guard reached %v on darwin, want %v; a tool that stopped producing a "+
			"keychain spec is either a real change or a test environment that no longer resolves it",
			checked, wantChecked)
	}
}

// No identity-only artifact may be a keychain item, because the per-directory
// identity paths have no gate for one and would read a **global** login as a bound
// directory's.
//
// The credential paths all pass `unbindableDirKeychain` first — a keychain item is
// only a bound directory's when the adapter declares that it moves with the isolation
// variable — and the identity paths (`writeDirIdentity`, `dirIdentityConfirms`, and
// through it `doctor`'s bound-directory `identity_drift`) deliberately do not,
// because today the only identity artifact anywhere is claude's `KindJSONPointer`
// `/oauthAccount`. A tool that gained a keychain identity item would silently make
// those three read the global item and label a bound directory from it — the same
// defect class the write gate exists for. So the assumption is asserted here rather
// than left in a comment: if this fails, add the gate, do not relax the guard.
func TestNoIdentityArtifactIsAKeychainItem(t *testing.T) {
	seen := 0
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
				if !sp.IdentityOnly {
					continue
				}
				seen++
				if sp.Kind == constants.KindKeychain {
					t.Errorf("%s/%s artifact %q is IdentityOnly and a keychain item; the "+
						"per-directory identity paths have no KeychainDirBindable gate, so they "+
						"would read the global item as a bound directory's identity",
						tool, goos, sp.Name)
				}
			}
		}
	}
	// A guard that reached no artifact checks nothing, which is how the sibling
	// keychain guard came to skip codex entirely.
	if seen == 0 {
		t.Error("this guard saw no IdentityOnly artifact at all; either one stopped being " +
			"declared or the test environment no longer resolves it")
	}
}

// TestRelativeConfigVariablesWarnPerTool covers 1.7: the divergence copilot and
// opencode already warn about reaches claude and codex too, and it is *not* the
// same divergence, which is why each tool measures its own before warning.
//
// claude uses CLAUDE_CONFIG_DIR verbatim (2.1.220 applies Unicode NFC and no path
// resolution), so a relative value moves its file artifacts but leaves the
// keychain item alone — the service name hashes the raw string. codex
// canonicalizes CODEX_HOME against its own working directory before hashing the
// keyring account, so a relative value moves both stores. Copying one warning to
// the other tool would have shipped the wrong model for one of them.
func TestRelativeConfigVariablesWarnPerTool(t *testing.T) {
	// linux: the warning does not depend on the platform, but claude's darwin
	// driver reads the keychain, and Detect fails without a `security` binary.
	wantEnvConflictWarningOn(t, "linux", claudeAdapter,
		map[string]string{"CLAUDE_CONFIG_DIR": "relcfg"}, "CLAUDE_CONFIG_DIR is relative")
	wantEnvConflictWarningOn(t, "linux", codexAdapter,
		map[string]string{"CODEX_HOME": "relcfg"}, "CODEX_HOME is relative")

	// The distinction is the point: only codex's says the keyring account moves,
	// and only claude's says the keychain item does not.
	claudeWarn := warningsOf(t, "linux", claudeAdapter, map[string]string{"CLAUDE_CONFIG_DIR": "relcfg"})
	if !strings.Contains(claudeWarn, "keychain item is unaffected") {
		t.Errorf("claude's warning must say the item is unaffected: %s", claudeWarn)
	}
	// The credential variable has the same hazard for the file half and needs its own
	// warning: once it is set, the credential no longer follows CLAUDE_CONFIG_DIR at
	// all, so the warning above is not saying anything about it.
	wantEnvConflictWarningOn(t, "linux", claudeAdapter,
		map[string]string{claude.EnvSecureStorageDir: "relcred"},
		claude.EnvSecureStorageDir+" is relative")

	codexWarn := warningsOf(t, "linux", codexAdapter, map[string]string{"CODEX_HOME": "relcfg"})
	if !strings.Contains(codexWarn, "keyring account") {
		t.Errorf("codex's warning must say the keyring account moves too: %s", codexWarn)
	}

	// kae itself only ever sets these to absolute kae-owned paths, so the normal
	// case must stay silent or every pinned shell warns.
	for _, tc := range []struct {
		adp  adapter.Adapter
		vars map[string]string
	}{
		{claudeAdapter, map[string]string{"CLAUDE_CONFIG_DIR": t.TempDir()}},
		{codexAdapter, map[string]string{"CODEX_HOME": t.TempDir()}},
	} {
		if got := warningsOf(t, "linux", tc.adp, tc.vars); strings.Contains(got, "is relative") {
			t.Errorf("%s: an absolute value must not warn: %s", tc.adp.ID(), got)
		}
	}
}

// warningsOf joins one adapter's Detect warnings for an environment, so a test
// can assert on their content without pinning their order.
func warningsOf(t *testing.T, goos string, adp adapter.Adapter, vars map[string]string) string {
	t.Helper()
	info, err := adp.Detect(context.Background(), testEnv(t, goos, vars))
	if err != nil {
		t.Fatalf("detect %s: %v", adp.ID(), err)
	}
	return strings.Join(info.Warnings, "\n")
}

// verifiedRow matches one row of the "Verified Upstream Versions" table in
// docs/ADAPTERS.md: | <tool> | `<version>` | `<date>` | ... |
var verifiedRow = regexp.MustCompile(`(?m)^\| (\w+) \| ` + "`" + `([^` + "`" + `]*)` + "`" + `[^|]*\| ` + "`" + `([0-9-]+)` + "`" + ` \|`)

// TestVerifiedVersionsMatchTheDocs closes the one gap in the re-verification
// discipline that nothing else can: the lockstep is "bump VerifiedVersion() and
// the recorded version in the same commit", and until now only a human enforced
// it. A doc that still names the old release is worse than no doc — the next
// session reads it, believes the assumptions were checked there, and skips the
// re-verification the code is actually asking for.
//
// It parses only this one table, deliberately. The per-row "Verified on" cells
// in docs/VALIDATION.md are prose with the procedure and the evidence in them,
// at a finer grain than one date per tool; parsing those would make every
// wording change a test failure, which is how a guard gets deleted.
func TestVerifiedVersionsMatchTheDocs(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "ADAPTERS.md"))
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string][2]string{}
	for _, m := range verifiedRow.FindAllStringSubmatch(string(data), -1) {
		documented[m[1]] = [2]string{strings.Trim(m[2], `"`), m[3]}
	}
	if len(documented) != len(constants.Tools) {
		t.Fatalf("parsed %d rows from the Verified Upstream Versions table, want %d "+
			"(the table's shape changed; fix verifiedRow or the table): %v",
			len(documented), len(constants.Tools), documented)
	}
	for _, tool := range constants.Tools {
		ad, err := adapter.ForTool(tool)
		if err != nil {
			t.Fatalf("adapter for %s: %v", tool, err)
		}
		row, ok := documented[tool]
		if !ok {
			t.Errorf("%s has no row in docs/ADAPTERS.md's Verified Upstream Versions table", tool)
			continue
		}
		if row[0] != ad.VerifiedVersion() {
			t.Errorf("%s: docs/ADAPTERS.md says version %q, the adapter says %q — bump both in one commit",
				tool, row[0], ad.VerifiedVersion())
		}
		if row[1] != ad.VerifiedOn() {
			t.Errorf("%s: docs/ADAPTERS.md says verified on %q, the adapter says %q",
				tool, row[1], ad.VerifiedOn())
		}
		if _, err := time.Parse(time.DateOnly, ad.VerifiedOn()); err != nil {
			t.Errorf("%s: VerifiedOn() %q is not YYYY-MM-DD; the age check parses it",
				tool, ad.VerifiedOn())
		}
	}
}
