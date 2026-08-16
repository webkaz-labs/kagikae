package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
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
		if want := valueAfter(args, "-a"); want != "" && k.account != "" && want != k.account {
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
