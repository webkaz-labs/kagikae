package cmd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// autoStoreKeychain is a stateful `security` double: the Codex Auth item does not
// exist until the login flow creates it, which is the state change the whole case
// turns on. runnertest.Fake is canned and says so in its own doc comment ("tests
// needing stateful command simulation define their own fakes").
type autoStoreKeychain struct{ payload string }

func (f *autoStoreKeychain) Run(_ context.Context, name string, args ...string) (string, string, int) {
	if name != "security" || len(args) == 0 || args[0] != "find-generic-password" {
		return "", "", 0
	}
	if f.payload == "" {
		return "", "The specified item " + keychain.NotFoundMarker + " in the keychain.", 44
	}
	if slices.Contains(args, "-w") { // the read; without it, the attributes-only probe
		return f.payload + "\n", "", 0
	}
	return "attributes", "", 0
}

func (f *autoStoreKeychain) RunInput(ctx context.Context, _, name string, args ...string) (string, string, int) {
	return f.Run(ctx, name, args...)
}

// The login is the one event that can move where a store's credential lives, so the
// post-flow read must go through a spec resolved *after* it. codex under
// `cli_auth_credentials_store = "auto"` decides by probing the keychain, so a
// directory whose store has no item yet resolves to the file spec before the flow
// and to the item after it. Reading the post-login state through the pre-login spec
// then reads the store codex abandoned — absent before, absent after — and calls a
// successful login "unchanged" (exit 11).
//
// Two things to keep in view, because this fixture looks like it claims more than it
// does. `KeychainDirBindable` is **not** on this path: it gates dirCredentialSpec
// (the freshness, write and delete sides), while relogin's comparison reaches the
// spec through dirSpecs → Codex.Artifacts → usesKeyring and reads it with
// artifact.ReadLive. So this pins the *comparison*, and is not a claim that kae may
// write or harvest into that item — the harvest still refuses it
// (unbindableDirKeychain inside harvestDirCredential). And `auto` is a supported
// value, not a faked capability: configuredStore says so in as many words.
//
// It also documents why runRelogin must never wrap its context in
// keychain.WithReadCache the way `kae pin` and `kae use` do. A cached pre-login
// probe served to the post-login read reopens exactly this defect, silently.
func TestReloginResolvesTheStoreAgainAfterTheFlow(t *testing.T) {
	app := overlayTestApp(t)
	app.Env.GOOS = "darwin" // `auto` probes the keychain only there (usesKeyring)
	ctx := context.Background()
	app.Config.Profiles["main"].Accounts[constants.ToolCodex] = "main"
	seedCodex(t, app, "codex-token")
	// The store's config.toml is a symlink to this one, so `auto` is what the bound
	// store resolves too.
	writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"),
		"model = \"gpt-5.4\"\ncli_auth_credentials_store = \"auto\"\n")

	fake := &autoStoreKeychain{}
	var code int
	var stderr string
	runner.With(fake, func() {
		c, out := captureStdout(t, func() int {
			return runCapture(ctx, app, commonOpts{Format: formatText}, constants.ToolCodex, "main")
		})
		mustExit(t, constants.ExitOK, c, out)
		dir := pinHere(t, app, modeShared)
		storeDir := app.Paths.SharedDir(paths.PinID(dir), constants.ToolCodex)
		// No item and no file: the ordinary state of a bound directory codex has not run
		// in yet, and the one where the two specs disagree about what "absent" means.
		if err := os.Remove(filepath.Join(storeDir, "auth.json")); err != nil {
			t.Fatal(err)
		}
		// codex's first save creates the item and leaves no file, so nothing the
		// pre-login spec points at ever changes.
		withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
			fake.payload = `{"tokens":{"access_token":"codex-relogged"}}`
			return 0, nil
		})
		code, stderr = captureStderr(t, func() int {
			var inner int
			inner, _ = captureStdout(t, func() int {
				return runRelogin(ctx, app, commonOpts{Format: formatText}, constants.ToolCodex)
			})
			return inner
		})
	})
	if code == constants.ExitAuthUnchanged {
		t.Fatalf("the login landed in the store codex moved to; comparing through the abandoned one calls it unchanged: %s", stderr)
	}
	mustExit(t, constants.ExitOK, code, stderr)
	if strings.Contains(stderr, "found no codex credential") {
		t.Errorf("kae must resolve the store again after the flow: %q", stderr)
	}
}
