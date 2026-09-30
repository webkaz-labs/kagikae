package cmd

// Per-tool capture and switch round trips (codex, agy, gemini, opencode) and
// the small command-level checks that follow them.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// With the keyring store, capture/use round-trip through the single
// `Codex Auth` keychain item. The per-login opaque account is captured verbatim
// and apply deletes the prior item before writing the target's, so exactly one
// item — with the target account's id and payload — remains. The keychainSim
// (a stateful `security` double) keeps the test off the real keychain.
func TestCodexKeyringRoundTrip(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, nil)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		opts := commonOpts{Format: formatText}
		writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"),
			"cli_auth_credentials_store = \"keyring\"\n")

		// codex logged in as main in this codex home: one keychain item, under the
		// account codex derives from CODEX_HOME. The sim answers any account here so
		// the derivation stays pinned in the codex package's golden test; what this
		// test pins is that capture and apply agree on one item.
		sim.present = true
		sim.payload = `{"tokens":{"access_token":"main-access","refresh_token":"main-refresh"}}`
		captureCode, captureOut := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "main") })
		if captureCode != constants.ExitOK {
			t.Fatalf("capture main: %s", captureOut)
		}
		// Redaction: the keyring payload is a credential and must never reach
		// stdout or the snapshot metadata (only the opaque account id is stored).
		if strings.Contains(captureOut, "main-access") {
			t.Fatalf("keyring token leaked to capture output: %s", captureOut)
		}
		meta := readFile(t, filepath.Join(app.Paths.AccountDir("codex", "main"), "account.toml"))
		if strings.Contains(meta, "main-access") {
			t.Fatalf("keyring token leaked into account.toml: %s", meta)
		}
		// The account the apply must use is the one the adapter derives for *this*
		// codex home, resolved the same way production resolves it (the derivation
		// itself is pinned in the codex package's golden test).
		derived := codexItemAccount(t, ctx, app)
		if !strings.HasPrefix(derived, "cli|") {
			t.Fatalf("adapter derived no keychain account: %q", derived)
		}
		// A re-login as side rewrote the item's payload. Its account did not change:
		// one codex home has one item, whatever account is logged in.
		sim.payload = `{"tokens":{"access_token":"side-access","refresh_token":"side-refresh"}}`
		if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "side") }); code != constants.ExitOK {
			t.Fatalf("capture side: %s", out)
		}
		// Switch back to main: upsert this home's item, and nothing else.
		sim.ops = nil // isolate the apply's keychain mutations
		if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "codex", "main") }); code != constants.ExitOK {
			t.Fatalf("switch to main: %s", out)
		}
		if !sim.present {
			t.Fatal("no Codex Auth item after switch")
		}
		// A single add, no delete: `Codex Auth` holds one item per CODEX_HOME, so a
		// delete on this path removed another codex home's login (shipped in v0.12.0).
		if len(sim.ops) != 1 || sim.ops[0] != "add" {
			t.Fatalf("expected a single add on apply, got %v", sim.ops)
		}
		if sim.account != derived {
			t.Fatalf("item account = %q, want the derived %q", sim.account, derived)
		}
		if !strings.Contains(sim.payload, "main-access") || strings.Contains(sim.payload, "side-access") {
			t.Fatalf("item payload not restored to main verbatim: %s", sim.payload)
		}
	})
}

// codexItemAccount returns the account attribute codex's adapter derives for the
// codex home app's environment names — the value an apply must address the
// `Codex Auth` item by.
func codexItemAccount(t *testing.T, ctx context.Context, app *App) string {
	t.Helper()
	adp, err := adapter.ForTool(constants.ToolCodex)
	if err != nil {
		t.Fatal(err)
	}
	specs, err := adp.Artifacts(ctx, app.Env)
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range specs {
		if sp.Kind == constants.KindKeychain {
			return sp.KeychainAccount
		}
	}
	t.Fatal("codex declared no keychain artifact")
	return ""
}

// A `Codex Auth` item belonging to *another* CODEX_HOME is not this home's
// credential: capture must report none rather than storing a stranger's login
// under this home's account. kae used to read the service's first item, so a
// second codex home's credential was capturable — and then written back here.
func TestCodexKeyringForeignHomeItemNotCaptured(t *testing.T) {
	sim := &keychainSim{
		present: true,
		account: "cli|0000000000000000", // some other CODEX_HOME's item
		payload: `{"tokens":{"access_token":"other-home-access"}}`,
	}
	runner.With(sim, func() {
		app := testApp(t, nil)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"),
			"cli_auth_credentials_store = \"keyring\"\n")
		code, out := captureStdout(t, func() int { return runCapture(ctx, app, commonOpts{Format: formatText}, "codex", "main") })
		if code == constants.ExitOK {
			t.Fatalf("expected capture to find no credential for this codex home: %s", out)
		}
		if strings.Contains(out, "other-home-access") {
			t.Fatalf("another home's payload reached the output: %s", out)
		}
	})
}

// geminiSim is a stateful security double keyed by service+account, so the agy
// keychain driver's service+account matching can be exercised without touching
// the real keychain and a sibling gemini item (a different account) is visible.
type geminiSim struct {
	items map[string]string // key: service "\x00" account
}

func (k *geminiSim) key(service, account string) string { return service + "\x00" + account }

func (k *geminiSim) Run(_ context.Context, _ string, args ...string) (string, string, int) {
	if len(args) == 0 {
		return "", "", 0
	}
	service, account := valueAfter(args, "-s"), valueAfter(args, "-a")
	switch args[0] {
	case "find-generic-password":
		if account == "" {
			return "", "test: agy read must be account-scoped (-a)", 1
		}
		payload, ok := k.items[k.key(service, account)]
		if !ok {
			return "", "security: could not be found", 44
		}
		if slices.Contains(args, "-w") {
			return payload, "", 0
		}
		return fmt.Sprintf("    \"acct\"<blob>=\"%s\"\n", account), "", 0
	case "add-generic-password":
		k.items[k.key(service, account)] = valueAfter(args, "-w")
		return "", "", 0
	case "delete-generic-password":
		delete(k.items, k.key(service, account))
		return "", "", 0
	}
	return "", "", 0
}

func (k *geminiSim) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
	return k.Run(ctx, name, args...)
}

// On macOS, agy capture/use round-trip through the gemini/antigravity
// keychain item, matched by service AND account so a sibling gemini item (the
// Gemini ecosystem's, under a different account) is never read or written. The
// opaque token is stored verbatim and never leaks to stdout or the snapshot.
func TestAgyKeychainRoundTrip(t *testing.T) {
	const sibling = "gemini\x00gemini-cli-user"
	sim := &geminiSim{items: map[string]string{
		"gemini\x00antigravity": "agy-main-token",
		sibling:                 "gemini-cli-secret",
	}}
	runner.With(sim, func() {
		app := testApp(t, nil)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		opts := commonOpts{Format: formatText}

		// agy logged in as main: capture stores the token verbatim, never leaking it.
		captureCode, captureOut := captureStdout(t, func() int { return runCapture(ctx, app, opts, "agy", "main") })
		if captureCode != constants.ExitOK {
			t.Fatalf("capture main: %s", captureOut)
		}
		if strings.Contains(captureOut, "agy-main-token") {
			t.Fatalf("agy token leaked to capture output: %s", captureOut)
		}
		meta := readFile(t, filepath.Join(app.Paths.AccountDir("agy", "main"), "account.toml"))
		if strings.Contains(meta, "agy-main-token") {
			t.Fatalf("agy token leaked into account.toml: %s", meta)
		}

		// A re-login as side replaced the live antigravity item.
		sim.items["gemini\x00antigravity"] = "agy-side-token"
		if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "agy", "side") }); code != constants.ExitOK {
			t.Fatalf("capture side: %s", out)
		}

		// Switch back to main: the antigravity item is restored verbatim.
		if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "agy", "main") }); code != constants.ExitOK {
			t.Fatalf("switch to main: %s", out)
		}
		if got := sim.items["gemini\x00antigravity"]; got != "agy-main-token" {
			t.Fatalf("antigravity item not switched to main verbatim: %q", got)
		}
		// The sibling gemini item must never be touched by any agy operation.
		if sim.items[sibling] != "gemini-cli-secret" {
			t.Fatalf("sibling gemini item was modified: %q", sim.items[sibling])
		}
	})
}

// An empty keychain payload is refused at capture (structure guard:
// non-empty, single-line), not stored as an unusable snapshot.
func TestAgyKeychainEmptyPayloadRefused(t *testing.T) {
	sim := &geminiSim{items: map[string]string{"gemini\x00antigravity": ""}}
	runner.With(sim, func() {
		app := testApp(t, nil)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		code, out := captureStdout(t, func() int { return runCapture(ctx, app, commonOpts{Format: formatText}, "agy", "main") })
		if code == constants.ExitOK {
			t.Fatalf("expected capture to refuse an empty keychain payload: %s", out)
		}
	})
}

func TestAgyCaptureSwitchFileSnapshot(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// without a credential file, capture reports missing auth
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "agy", "main") })
	mustExit(t, constants.ExitAuthMissing, code, out)

	credPath := filepath.Join(app.Env.Home, ".gemini", "antigravity-cli", "credentials.enc")
	writeFile(t, credPath, "opaque-main-blob")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "agy", "main") })
	mustExit(t, constants.ExitOK, code, out)

	writeFile(t, credPath, "opaque-side-blob")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "agy", "side") })
	mustExit(t, constants.ExitOK, code, out)

	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "agy", "main") })
	mustExit(t, constants.ExitOK, code, out)
	if got := readFile(t, credPath); got != "opaque-main-blob" {
		t.Fatalf("agy credential not switched: %s", got)
	}
}

func TestOpencodeCaptureSwitchPreservesSiblingProviders(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	authPath := filepath.Join(app.Env.Home, ".local", "share", "opencode", "auth.json")

	// without an openai entry, capture reports missing auth (sibling
	// API-key providers do not count as a subscription login)
	writeFile(t, authPath, `{"openrouter":{"type":"api","key":"sk-other"}}`)
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "opencode", "main") })
	mustExit(t, constants.ExitAuthMissing, code, out)

	writeFile(t, authPath,
		`{"openai":{"type":"oauth","refresh":"r-main","access":"a-main"},"openrouter":{"type":"api","key":"sk-other"}}`)
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "opencode", "main") })
	mustExit(t, constants.ExitOK, code, out)

	writeFile(t, authPath,
		`{"openai":{"type":"oauth","refresh":"r-side","access":"a-side"},"openrouter":{"type":"api","key":"sk-other"}}`)
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "opencode", "side") })
	mustExit(t, constants.ExitOK, code, out)

	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "opencode", "main") })
	mustExit(t, constants.ExitOK, code, out)
	got := readFile(t, authPath)
	if !strings.Contains(got, `"r-main"`) || strings.Contains(got, `"r-side"`) {
		t.Fatalf("openai entry not switched: %s", got)
	}
	if !strings.Contains(got, `"sk-other"`) {
		t.Fatalf("sibling provider key must survive the switch: %s", got)
	}
}

func TestCaptureWithoutLiveAuth(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitAuthMissing, code, out)
}

func TestSwitchLockBusy(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	seedClaude(t, app, mainToken, "main-uuid")
	code, _ := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, "")

	held, err := lock.Acquire(app.Paths.LocksDir(), "claude")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitLockBusy, code, out)
}

func TestJSONErrorReport(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	jsonOpts := commonOpts{Format: formatJSON}
	code, out := captureStdout(t, func() int { return runSwitch(ctx, app, jsonOpts, "claude", "nope") })
	mustExit(t, constants.ExitNotFound, code, out)
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("error report must be json: %v\n%s", err, out)
	}
	if report["ok"] != false || report["error_code"] != "not_found" {
		t.Fatalf("unexpected error report: %s", out)
	}
}

func TestInitCreatesConfigIdempotently(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int { return runInit(ctx, app, opts) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "Created") {
		t.Fatalf("unexpected: %s", out)
	}
	marker := "# user marker"
	writeFile(t, app.ConfigPath, "version = 1\n"+marker+"\n")
	code, out = captureStdout(t, func() int { return runInit(ctx, app, opts) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "already exists") {
		t.Fatalf("unexpected: %s", out)
	}
	if !strings.Contains(readFile(t, app.ConfigPath), marker) {
		t.Fatal("init must not overwrite an existing config")
	}
}

func TestRollbackUnknownID(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	code, out := captureStdout(t, func() int { return runRollback(ctx, app, opts, "20000101T000000Z") })
	mustExit(t, constants.ExitNotFound, code, out)
}

func TestBackupPruneRetention(t *testing.T) {
	app := testApp(t, nil)
	app.Config.Security.BackupKeep = 1
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	seedClaude(t, app, mainToken, "main-uuid")
	code, _ := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitOK, code, "")
	for i := 0; i < 3; i++ {
		code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
		mustExit(t, constants.ExitOK, code, out)
	}
	entries, err := os.ReadDir(app.Paths.BackupsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected retention to keep 1 backup, got %d", len(entries))
	}
}
