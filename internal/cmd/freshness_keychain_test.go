package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
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
		k.present = false
		k.ops = append(k.ops, "delete")
		return "", "", 0
	}
	return "", "", 0
}

func (k *keychainSim) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
	return k.Run(ctx, name, args...)
}

// §C acceptance: a single switch performs at most one keychain payload read for
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
		sim.payload = `{"claudeAiOauth":{"accessToken":"` + workToken + `"}}`
		if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "work") }); code != constants.ExitOK {
			t.Fatalf("capture work: %s", out)
		}
		sim.payload = `{"claudeAiOauth":{"accessToken":"` + personalToken + `"}}`
		if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "personal") }); code != constants.ExitOK {
			t.Fatalf("capture personal: %s", out)
		}
		if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "work") }); code != constants.ExitOK {
			t.Fatalf("switch to work: %s", out)
		}

		// Measure the keychain payload reads of one switch.
		sim.readW = 0
		if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "personal") }); code != constants.ExitOK {
			t.Fatalf("switch to personal: %s", out)
		}
		if sim.readW != 1 {
			t.Fatalf("expected 1 coalesced keychain payload read in a switch, got %d", sim.readW)
		}
		// Sanity: the switch actually applied the personal token.
		if !strings.Contains(sim.payload, personalToken) {
			t.Fatalf("switch did not apply personal token: %s", sim.payload)
		}
	})
}
