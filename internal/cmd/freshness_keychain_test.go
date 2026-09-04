package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter/cursor"
	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/testutil/secrettest"
)

// keychainSim is a stateful security-CLI double holding one generic-password
// item, used to count how many payload reads (find-generic-password -w) a
// single switch issues for the claude keychain driver.
type keychainSim struct {
	payload string
	account string
	present bool
	readW   int      // payload reads (find-generic-password -w)
	ops     []string // mutation log: "add" / "delete", in order
}

func valueAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (k *keychainSim) Run(_ context.Context, _ string, args ...string) (string, string, int) {
	if len(args) == 0 {
		return "", "", 0
	}
	switch args[0] {
	case "find-generic-password":
		if !k.present {
			return "", "security: could not be found", 44
		}
		// An account-scoped read (-a) sees the item only when the accounts agree:
		// service+account is how a service holding more than one legitimate item is
		// addressed (codex's `Codex Auth`, one item per CODEX_HOME).
		if want := valueAfter(args, "-a"); want != "" && k.account != "" && want != k.account {
			return "", "security: could not be found", 44
		}
		hasW := false
		for _, a := range args {
			if a == "-w" {
				hasW = true
			}
		}
		if hasW {
			k.readW++
			return k.payload, "", 0
		}
		return fmt.Sprintf("keychain: \"login\"\nattributes:\n    \"acct\"<blob>=\"%s\"\n", k.account), "", 0
	case "add-generic-password":
		k.payload = valueAfter(args, "-w")
		k.account = valueAfter(args, "-a")
		k.present = true
		k.ops = append(k.ops, "add")
		return "", "", 0
	case "delete-generic-password":
		want := valueAfter(args, "-a")
		if want == "" {
			return "", "keychainSim: refusing delete-generic-password without -a", 2
		}
		if k.account != "" && want != k.account {
			return "", "security: could not be found", 44
		}
		k.present = false
		k.ops = append(k.ops, "delete")
		return "", "", 0
	}
	return "", "", 0
}

func (k *keychainSim) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
	return k.Run(ctx, name, args...)
}

func TestKeychainSimRejectsUnscopedDelete(t *testing.T) {
	sim := &keychainSim{payload: "fixture-payload", account: "main", present: true}
	runner.With(sim, func() {
		if err := keychain.DeleteItem(context.Background(), "service"); err == nil {
			t.Fatal("service-only delete succeeded against an account-scoped simulator")
		}
		if !sim.present || len(sim.ops) != 0 {
			t.Fatalf("refused delete changed the simulated item: present=%v ops=%v", sim.present, sim.ops)
		}
		if err := keychain.DeleteItemForAccount(context.Background(), "service", "main"); err != nil {
			t.Fatalf("account-scoped delete failed: %v", err)
		}
	})
	if sim.present || len(sim.ops) != 1 || sim.ops[0] != "delete" {
		t.Fatalf("scoped positive control did not delete exactly once: present=%v ops=%v", sim.present, sim.ops)
	}
}

// cursorKeychainSim holds cursor's service-keyed credential set. Its mutation
// log is the unit-test equivalent of the real keychain item's mdat: an upsert of
// identical bytes still changes mdat, so value equality alone cannot prove a
// refused switch left the live items unchanged.
type cursorKeychainSim struct {
	items     map[string]string
	mutations []string
}

func (k *cursorKeychainSim) Run(_ context.Context, name string, args ...string) (string, string, int) {
	if name == "cursor-agent" {
		return "Logged in as you@example.com\n", "", 0
	}
	if name != "security" || len(args) == 0 {
		return "", "", 0
	}
	service := valueAfter(args, "-s")
	switch args[0] {
	case "find-generic-password":
		payload, ok := k.items[service]
		if !ok {
			return "", "security: could not be found", 44
		}
		withPayload := false
		for _, arg := range args {
			if arg == "-w" {
				withPayload = true
				break
			}
		}
		if !withPayload {
			return "keychain: \"login\"\nattributes:\n    \"acct\"<blob>=\"cursor-user\"\n", "", 0
		}
		return payload, "", 0
	case "add-generic-password":
		k.items[service] = valueAfter(args, "-w")
		k.mutations = append(k.mutations, "add "+service)
		return "", "", 0
	case "delete-generic-password":
		delete(k.items, service)
		k.mutations = append(k.mutations, "delete "+service)
		return "", "", 0
	default:
		return "", "", 0
	}
}

func (k *cursorKeychainSim) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
	return k.Run(ctx, name, args...)
}

// Acceptance: a single switch performs at most one keychain payload read for
// the recapture decision (Detect, backup, and recapture share the coalesced
// read via keychain.WithReadCache).
func TestSwitchCoalescesKeychainReads(t *testing.T) {
	sim := &keychainSim{}
	runner.With(sim, func() {
		app := testApp(t, map[string]string{"USER": "me"})
		app.Env.GOOS = "darwin" // claude keychain driver
		ctx := context.Background()
		opts := commonOpts{Format: formatText}

		sim.present = true
		sim.account = "me"
		sim.payload = `{"claudeAiOauth":{"accessToken":"` + mainToken + `"}}`
		if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
			t.Fatalf("capture main: %s", out)
		}
		sim.payload = `{"claudeAiOauth":{"accessToken":"` + sideToken + `"}}`
		if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "side") }); code != constants.ExitOK {
			t.Fatalf("capture side: %s", out)
		}
		if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
			t.Fatalf("switch to main: %s", out)
		}

		// Measure the keychain payload reads of one switch.
		sim.readW = 0
		if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "side") }); code != constants.ExitOK {
			t.Fatalf("switch to side: %s", out)
		}
		if sim.readW != 1 {
			t.Fatalf("expected 1 coalesced keychain payload read in a switch, got %d", sim.readW)
		}
		// Sanity: the switch actually applied the side token.
		if !strings.Contains(sim.payload, sideToken) {
			t.Fatalf("switch did not apply side token: %s", sim.payload)
		}
	})
}

// A corrupt snapshot must be rejected before switch-away recapture. active and
// target deliberately name the same account: that is the real-machine path that
// used to rewrite account.toml from the live set, then apply the stale in-memory
// metadata, fail, and restore identical keychain bytes with a newer mdat.
func TestSwitchRefusesMissingCursorArtifactBeforeAnyMutation(t *testing.T) {
	access := jwtWithExp(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	sim := &cursorKeychainSim{items: map[string]string{
		cursor.KeychainService:        access,
		cursor.KeychainServiceRefresh: "refresh-token",
	}}
	runner.With(sim, func() {
		app := testApp(t, nil)
		app.Env.GOOS = "darwin"
		ctx := context.Background()
		opts := commonOpts{Format: formatText}

		if code, out := captureStdout(t, func() int {
			return runCapture(ctx, app, opts, constants.ToolCursor, "main")
		}); code != constants.ExitOK {
			t.Fatalf("capture cursor/main: exit %d: %s", code, out)
		}

		accountDir := app.Paths.AccountDir(constants.ToolCursor, "main")
		acc, found, err := account.Load(accountDir)
		if err != nil || !found {
			t.Fatalf("load cursor/main: found=%v err=%v", found, err)
		}
		delete(acc.Artifacts, "refresh_token")
		if err := account.Save(accountDir, acc); err != nil {
			t.Fatal(err)
		}

		accountFile := filepath.Join(accountDir, "account.toml")
		accountBefore, err := os.ReadFile(accountFile)
		if err != nil {
			t.Fatal(err)
		}
		stateBefore, err := os.ReadFile(app.Paths.StateFile())
		if err != nil {
			t.Fatal(err)
		}
		sim.mutations = nil

		dryOpts := opts
		dryOpts.DryRun = true
		dryCode, _, dryStderr := captureBoth(t, func() int {
			return runSwitch(ctx, app, dryOpts, constants.ToolCursor, "main")
		})
		if dryCode != constants.ExitUnsafeRefused {
			t.Fatalf("dry-run exit code = %d, want %d: %s", dryCode, constants.ExitUnsafeRefused, dryStderr)
		}
		if len(sim.mutations) != 0 {
			t.Fatalf("refused dry-run mutated the keychain: %v", sim.mutations)
		}
		assertSwitchFilesUnchanged(t, app, accountFile, accountBefore, stateBefore)
		assertNoSwitchBackups(t, app)

		// Backend resolution failure retains the diagnostic-preview exception: dry-run
		// can still show the plan because it has no backend with which to validate it.
		app.Config.Security.SecretBackend = "unavailable-for-test"
		previewCode, previewStdout, _ := captureBoth(t, func() int {
			return runSwitch(ctx, app, dryOpts, constants.ToolCursor, "main")
		})
		if previewCode != constants.ExitOK || !strings.Contains(previewStdout, "cursor -> main") {
			t.Fatalf("backend-unavailable dry-run lost its preview: exit %d: %q", previewCode, previewStdout)
		}
		if len(sim.mutations) != 0 {
			t.Fatalf("backend-unavailable dry-run mutated the keychain: %v", sim.mutations)
		}
		assertSwitchFilesUnchanged(t, app, accountFile, accountBefore, stateBefore)
		assertNoSwitchBackups(t, app)
		app.Config.Security.SecretBackend = secret.BackendFile

		code, _, stderr := captureBoth(t, func() int {
			return runSwitch(ctx, app, opts, constants.ToolCursor, "main")
		})
		if code != constants.ExitUnsafeRefused {
			t.Fatalf("exit code = %d, want %d: %s", code, constants.ExitUnsafeRefused, stderr)
		}
		for _, want := range []string{"refresh_token", "kae add --no-login cursor main"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("refusal missing %q: %q", want, stderr)
			}
		}
		if strings.Contains(stderr, "refreshed cursor/main snapshot") {
			t.Errorf("refusal ran switch-away recapture: %q", stderr)
		}
		if len(sim.mutations) != 0 {
			t.Fatalf("refused switch mutated the keychain (and would change mdat): %v", sim.mutations)
		}

		assertSwitchFilesUnchanged(t, app, accountFile, accountBefore, stateBefore)
		assertNoSwitchBackups(t, app)
	})
}

func assertSwitchFilesUnchanged(t *testing.T, app *App, accountFile string, accountBefore, stateBefore []byte) {
	t.Helper()
	accountAfter, err := os.ReadFile(accountFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(accountAfter) != string(accountBefore) {
		t.Fatal("refused switch changed the corrupt snapshot")
	}
	stateAfter, err := os.ReadFile(app.Paths.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	if string(stateAfter) != string(stateBefore) {
		t.Fatal("refused switch changed state.json")
	}
}

func assertNoSwitchBackups(t *testing.T, app *App) {
	t.Helper()
	backups, err := backup.List(app.Paths.BackupsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("refused switch created backups before preflight: %+v", backups)
	}
}

// cursor's three artifacts are all opaque tokens, and Freshness cannot tell an
// access token from any other JWT — it dates whatever parses. accountFreshness
// takes the first artifact that answers in sorted-name order, so which of the
// three dates the account is decided by the sort ("access_token" < "api_key" <
// "refresh_token"). Pin that: if the artifacts are ever renamed, or the iteration
// stops being sorted, the account would be dated from a refresh token that
// outlives the access token and a stale credential would read as fresh.
func TestCursorFreshnessComesFromTheAccessToken(t *testing.T) {
	ctx := context.Background()
	app := testApp(t, nil)
	be := secrettest.NewMem()

	accessExp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	refreshExp := time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC)
	acc := account.Account{
		Version: 1, Tool: constants.ToolCursor, Name: "main",
		Artifacts: map[string]account.Artifact{},
	}
	for name, exp := range map[string]time.Time{
		"access_token":  accessExp,
		"refresh_token": refreshExp, // a JWT too, so both would answer
	} {
		ref := account.SecretRef(constants.ToolCursor, "main", name)
		if err := be.Set(ctx, ref, []byte(jwtWithExp(exp))); err != nil {
			t.Fatal(err)
		}
		acc.Artifacts[name] = account.Artifact{
			Kind: constants.KindKeychain, Target: name, SecretRef: ref, Present: true,
		}
	}

	info, err := app.accountFreshness(ctx, be, acc)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Known || !info.ExpiresAt.Equal(accessExp) {
		t.Fatalf("accountFreshness = %+v, want the access token's expiry %v", info, accessExp)
	}
}

// jwtWithExp builds a minimal unsigned JWT carrying only exp.
func jwtWithExp(exp time.Time) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"exp":%d}`, exp.Unix()))
	return header + "." + payload + ".sig"
}
