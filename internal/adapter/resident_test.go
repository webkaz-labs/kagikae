package adapter_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// fakeAccountID stands in for an account id; it is not a real one.
const fakeAccountID = "acct-fixture-0001"

// An account key is personal data, so no way of printing it may show the id.
func TestResidentAccountNeverPrintsTheID(t *testing.T) {
	key, ok := adapter.NewResidentAccount(fakeAccountID)
	if !ok {
		t.Fatal("NewResidentAccount refused a non-empty id")
	}
	wrapped := struct{ Key adapter.ResidentAccount }{key}
	jsonOut, err := json.Marshal(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]string{
		"String": key.String(),
		"%v":     fmt.Sprintf("%v", key),
		"%+v":    fmt.Sprintf("%+v", key),
		"%#v":    fmt.Sprintf("%#v", key),
		"%s":     fmt.Sprintf("%s", key),
		"%q":     fmt.Sprintf("%q", key),
		"%x":     fmt.Sprintf("%x", key),
		"%X":     fmt.Sprintf("%X", key),
		"%d":     fmt.Sprintf("%d", key),
		"struct": fmt.Sprintf("%+v %#v", wrapped, wrapped),
		"ptr":    fmt.Sprintf("%v", &key),
		"error":  fmt.Errorf("probe: %v", key).Error(),
		"wrap":   fmt.Errorf("probe: %w", errors.New(fmt.Sprint(key))).Error(),
		"json":   string(jsonOut),
	}
	for name, out := range outputs {
		if strings.Contains(out, fakeAccountID) || strings.Contains(out, "acct-fixture") {
			t.Errorf("%s printed the account id: %q", name, out)
		}
	}
}

func TestResidentAccountSame(t *testing.T) {
	a, _ := adapter.NewResidentAccount(fakeAccountID)
	b, _ := adapter.NewResidentAccount(fakeAccountID)
	c, _ := adapter.NewResidentAccount("acct-fixture-0002")
	if !a.Same(b) {
		t.Error("equal ids did not match")
	}
	if a.Same(c) {
		t.Error("different ids matched")
	}
	if _, ok := adapter.NewResidentAccount(""); ok {
		t.Error("NewResidentAccount accepted an empty id")
	}
	var zero adapter.ResidentAccount
	if zero.Same(zero) || zero.Same(a) || a.Same(zero) {
		t.Error("a zero key matched")
	}
}

// The probe sequence is the security contract: exactly these three messages,
// account/read with refreshToken false, and nothing that changes auth state or
// can return a token (docs/SECURITY.md § Resident processes).
func TestDaemonProbeRequestsAreTheFixedReadOnlySequence(t *testing.T) {
	reqs := adapter.DaemonProbeRequests()
	type msg struct {
		ID     *int            `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	var methods []string
	for i, raw := range reqs {
		var m msg
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("request %d is not JSON: %v", i, err)
		}
		methods = append(methods, m.Method)
		lower := strings.ToLower(string(raw))
		for _, banned := range []string{"getauthstatus", "account/logout", "account/login", "chatgptauthtokens"} {
			if strings.Contains(lower, banned) {
				t.Errorf("request %d mentions %s", i, banned)
			}
		}
		switch m.Method {
		case "initialize":
			if m.ID == nil || *m.ID == adapter.DaemonProbeResponseID {
				t.Errorf("initialize id = %v, want a request id other than the account/read one", m.ID)
			}
		case "initialized":
			if m.ID != nil {
				t.Error("initialized carries an id; it must be a notification")
			}
		case "account/read":
			if m.ID == nil || *m.ID != adapter.DaemonProbeResponseID {
				t.Errorf("account/read id = %v, want %d", m.ID, adapter.DaemonProbeResponseID)
			}
			var params map[string]any
			if err := json.Unmarshal(m.Params, &params); err != nil {
				t.Fatalf("account/read params: %v", err)
			}
			if v, ok := params["refreshToken"].(bool); !ok || v {
				t.Errorf("account/read refreshToken = %#v, want false", params["refreshToken"])
			}
			if len(params) != 1 {
				t.Errorf("account/read params = %v, want only refreshToken", params)
			}
		}
	}
	if got, want := strings.Join(methods, ","), "initialize,initialized,account/read"; got != want {
		t.Fatalf("methods = %s, want %s", got, want)
	}

	// Each call is a fresh copy: altering one changes no later call.
	reqs[2][0] = 'X'
	if again := adapter.DaemonProbeRequests(); again[2][0] != '{' {
		t.Error("DaemonProbeRequests returned shared storage")
	}
}

// Only codex has resident processes; a tool that implemented the capability by
// accident would have its daemon probed and restarted.
func TestOnlyCodexIsAResidentHolder(t *testing.T) {
	for _, tool := range constants.Tools {
		a, err := adapter.ForTool(tool)
		if err != nil {
			t.Fatal(err)
		}
		_, holds := a.(adapter.ResidentHolder)
		if holds != (tool == constants.ToolCodex) {
			t.Errorf("%s implements ResidentHolder = %v", tool, holds)
		}
	}
}
