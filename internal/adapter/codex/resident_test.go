package codex

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
)

// Fixture ids; not real accounts.
const (
	fixtureAccount = "acct-fixture-0001"
	otherAccount   = "acct-fixture-0002"
)

// The daemon is addressed by the canonical home, the same one the keyring store
// key hashes: a symlinked CODEX_HOME names the target's socket, and CODEX_HOME
// is set to that target explicitly.
func TestResidentDaemonCanonicalizesASymlinkedHome(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(real) // macOS: /var -> /private/var
	if err != nil {
		t.Fatal(err)
	}

	spec := Codex{}.ResidentDaemon(envWithCodexHome(root, link))

	if want := filepath.Join(canonical, "app-server-control", "app-server-control.sock"); spec.Socket != want {
		t.Errorf("Socket = %q, want %q", spec.Socket, want)
	}
	if want := []string{"CODEX_HOME=" + canonical}; !slices.Equal(spec.Env, want) {
		t.Errorf("Env = %q, want %q", spec.Env, want)
	}
	if want := []string{"codex", "app-server", "daemon", "restart"}; !slices.Equal(spec.Restart, want) {
		t.Errorf("Restart = %q, want %q", spec.Restart, want)
	}
	if want := []string{"codex", "app-server", "daemon", "version"}; !slices.Equal(spec.Status, want) {
		t.Errorf("Status = %q, want %q", spec.Status, want)
	}
}

// A bound directory exports its own CODEX_HOME into the process environment.
// The spec follows the Env it is given, not the process, and still sets
// CODEX_HOME explicitly for the restart — with no CODEX_HOME in Env at all, the
// default ~/.codex.
func TestResidentDaemonIgnoresAnInheritedCodexHome(t *testing.T) {
	home := t.TempDir()
	bound := t.TempDir()
	t.Setenv("CODEX_HOME", bound)
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	live, err := filepath.EvalSymlinks(filepath.Join(home, ".codex"))
	if err != nil {
		t.Fatal(err)
	}

	spec := Codex{}.ResidentDaemon(testEnv(home))

	if want := []string{"CODEX_HOME=" + live}; !slices.Equal(spec.Env, want) {
		t.Errorf("Env = %q, want %q", spec.Env, want)
	}
	if filepath.Dir(filepath.Dir(spec.Socket)) != live {
		t.Errorf("Socket = %q, want it under %q", spec.Socket, live)
	}
}

// auth.json and the keyring item hold the same JSON, so one reader serves both.
func TestCredentialAccountReadsBothStores(t *testing.T) {
	want, _ := adapter.NewResidentAccount(fixtureAccount)
	payloads := map[string]string{
		"file": `{"auth_mode":"chatgpt","OPENAI_API_KEY":null,"tokens":{"id_token":"x","access_token":"y",` +
			`"refresh_token":"z","account_id":"` + fixtureAccount + `"},"last_refresh":"2026-10-01T00:00:00Z"}`,
		"keyring": `{"tokens":{"access_token":"y","account_id":"` + fixtureAccount + `"}}`,
	}
	for name, payload := range payloads {
		got, ok := Codex{}.CredentialAccount([]byte(payload))
		if !ok || !got.Same(want) {
			t.Errorf("%s: CredentialAccount = %v, %v; want the fixture account", name, got, ok)
		}
	}
	other, _ := adapter.NewResidentAccount(otherAccount)
	if got, _ := (Codex{}).CredentialAccount([]byte(payloads["file"])); got.Same(other) {
		t.Error("a different account matched")
	}
}

func TestCredentialAccountRejectsUnreadablePayloads(t *testing.T) {
	for name, payload := range map[string]string{
		"api key only":     `{"OPENAI_API_KEY":"sk-fixture"}`,
		"tokens null":      `{"tokens":null}`,
		"no account_id":    `{"tokens":{"access_token":"y"}}`,
		"empty account_id": `{"tokens":{"account_id":""}}`,
		"number":           `{"tokens":{"account_id":1234}}`,
		"tokens string":    `{"tokens":"x"}`,
		"not JSON":         `tokens`,
		"empty":            ``,
	} {
		if _, ok := (Codex{}).CredentialAccount([]byte(payload)); ok {
			t.Errorf("%s: CredentialAccount ok", name)
		}
	}
}

// The account/read result shape measured on 0.160.0 (docs/ADAPTERS.md
// § Resident processes), with fixture values.
func accountReadResult(accountID string) string {
	return `{"account":{"type":"chatgpt","email":"you@example.com","planType":"plus"},` +
		`"requiresOpenaiAuth":true,"workspaceRouting":{"chatgptAccountId":"` + accountID +
		`","backendOrigin":"https://chatgpt.com","accountRoutingOverride":"NO_CONSTRAINT"}}`
}

func TestParseDaemonAccountComparesWithTheCredential(t *testing.T) {
	cred, _ := Codex{}.CredentialAccount([]byte(`{"tokens":{"account_id":"` + fixtureAccount + `"}}`))

	same, ok := Codex{}.ParseDaemonAccount([]byte(accountReadResult(fixtureAccount)))
	if !ok || !same.Held || !same.Account.Same(cred) {
		t.Errorf("matching account: got %+v, ok %v", same, ok)
	}
	other, ok := Codex{}.ParseDaemonAccount([]byte(accountReadResult(otherAccount)))
	if !ok || !other.Held || other.Account.Same(cred) {
		t.Errorf("different account: got %+v, ok %v, want a readable key that differs", other, ok)
	}
}

// A daemon that answers `account` null with requiresOpenaiAuth true holds no
// account, as one on 0.160.1 did after a switch (docs/ADAPTERS.md § Resident
// processes); that is readable, and distinct from an answer kae cannot read.
func TestParseDaemonAccountReadsNoAccount(t *testing.T) {
	for name, result := range map[string]string{
		"measured on 0.160.1":     `{"account":null,"requiresOpenaiAuth":true,"workspaceRouting":null}`,
		"workspaceRouting absent": `{"account":null,"requiresOpenaiAuth":true}`,
	} {
		if got, ok := (Codex{}).ParseDaemonAccount([]byte(result)); !ok || got.Held {
			t.Errorf("%s: got %+v, ok %v, want no account (Held false, ok true)", name, got, ok)
		}
	}
}

func TestParseDaemonAccountRejectsUnreadableAnswers(t *testing.T) {
	for name, result := range map[string]string{
		"api key login":                `{"account":{"type":"apiKey"},"requiresOpenaiAuth":true,"workspaceRouting":null}`,
		"no workspaceRouting":          `{"account":{"type":"chatgpt","email":"you@example.com","planType":"plus"}}`,
		"no chatgptAccountId":          `{"workspaceRouting":{"backendOrigin":"https://chatgpt.com"}}`,
		"empty id":                     `{"workspaceRouting":{"chatgptAccountId":""}}`,
		"null id":                      `{"workspaceRouting":{"chatgptAccountId":null}}`,
		"number id":                    `{"workspaceRouting":{"chatgptAccountId":42}}`,
		"routing is a string":          `{"workspaceRouting":"` + fixtureAccount + `"}`,
		"result is an array":           `[]`,
		"not JSON":                     `{"workspaceRouting":`,
		"whole JSON-RPC reply":         `{"jsonrpc":"2.0","id":2,"result":` + accountReadResult(fixtureAccount) + `}`,
		"no account, routing a string": `{"account":null,"workspaceRouting":"x"}`,
		"empty result":                 `{}`,
		// A model provider that needs no OpenAI login answers these nulls whatever
		// the credential; a restart would not change it.
		"no OpenAI auth required":                    `{"account":null,"requiresOpenaiAuth":false,"workspaceRouting":null}`,
		"no workspaceRouting, no requiresOpenaiAuth": `{"account":null}`,
		"requiresOpenaiAuth a string":                `{"account":null,"requiresOpenaiAuth":"true","workspaceRouting":null}`,
		"no account, but routing":                    `{"account":null,"requiresOpenaiAuth":true,"workspaceRouting":{"chatgptAccountId":"` + fixtureAccount + `"}}`,
	} {
		if got, ok := (Codex{}).ParseDaemonAccount([]byte(result)); ok || got.Held {
			t.Errorf("%s: ParseDaemonAccount %+v, ok %v", name, got, ok)
		}
	}
}

// `codex app-server daemon version` names the daemon's status and socket path
// (docs/ADAPTERS.md § Resident processes); only those two members are read.
func daemonRunningAt(sock string) string {
	return `{"status":"running","socketPath":"` + sock + `"}` + "\n"
}

func TestParseDaemonStatus(t *testing.T) {
	const sock = "/home/you/.codex/app-server-control/app-server-control.sock"
	for _, tc := range []struct {
		name, output string
		running, ok  bool
		socket       string
	}{
		{"running", `{"cliVersion":"0.160.0","status":"running","socketPath":"` + sock + `"}` + "\n", true, true, sock},
		{"not running", `{"status":"stopped"}`, false, true, ""},
		{"not running with a path", `{"status":"stopped","socketPath":"relative.sock"}`, false, true, ""},
		{"running without a path", `{"status":"running"}`, false, false, ""},
		{"running with a relative path", `{"status":"running","socketPath":"app-server-control.sock"}`, false, false, ""},
		{"running with an empty path", `{"status":"running","socketPath":""}`, false, false, ""},
		{"path is a number", `{"status":"running","socketPath":1}`, false, false, ""},
		{"no status", `{"socketPath":"` + sock + `"}`, false, false, ""},
		{"status is a bool", `{"status":true,"socketPath":"` + sock + `"}`, false, false, ""},
		{"not JSON", "codex-cli 0.160.0\n", false, false, ""},
		{"followed by a log line", daemonRunningAt(sock) + "daemon: listening\n{", true, true, sock},
		{"array", `[{"status":"running"}]`, false, false, ""},
		{"JSON after a banner", "note\n" + `{"status":"running","socketPath":"` + sock + `"}`, false, false, ""},
		{"empty", ``, false, false, ""},
	} {
		running, socket, ok := Codex{}.ParseDaemonStatus([]byte(tc.output))
		if running != tc.running || socket != tc.socket || ok != tc.ok {
			t.Errorf("%s: got (%v, %q, %v), want (%v, %q, %v)", tc.name, running, socket, ok, tc.running, tc.socket, tc.ok)
		}
	}
}

// The restart's report is read from its first JSON object: `status` restarted
// with an absolute socket path, nothing else.
func TestParseDaemonRestart(t *testing.T) {
	const sock = "/home/you/.codex/app-server-control/app-server-control.sock"
	for _, tc := range []struct {
		name, output string
		ok           bool
		socket       string
	}{
		{"restarted", `{"status":"restarted","backend":"pid","pid":42,"socketPath":"` + sock + `","cliVersion":"0.160.1"}` + "\n", true, sock},
		{"followed by a log line", `{"status":"restarted","socketPath":"` + sock + `"}` + "\ndaemon: listening\n{", true, sock},
		{"running is not restarted", `{"status":"running","socketPath":"` + sock + `"}`, false, ""},
		{"started is not restarted", `{"status":"started","socketPath":"` + sock + `"}`, false, ""},
		{"no socket path", `{"status":"restarted"}`, false, ""},
		{"relative socket path", `{"status":"restarted","socketPath":"app-server-control.sock"}`, false, ""},
		{"socket path is a number", `{"status":"restarted","socketPath":1}`, false, ""},
		{"status is a bool", `{"status":true,"socketPath":"` + sock + `"}`, false, ""},
		{"not JSON", "restarted\n", false, ""},
		{"empty", ``, false, ""},
	} {
		socket, ok := Codex{}.ParseDaemonRestart([]byte(tc.output))
		if socket != tc.socket || ok != tc.ok {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", tc.name, socket, ok, tc.socket, tc.ok)
		}
	}
}

func TestDesktopAppsDarwinOnly(t *testing.T) {
	got := Codex{}.DesktopApps()
	if runtime.GOOS == "darwin" {
		if !slices.Equal(got, []string{"com.openai.codex"}) {
			t.Errorf("DesktopApps = %q on darwin", got)
		}
		return
	}
	if len(got) != 0 {
		t.Errorf("DesktopApps = %q on %s, want none", got, runtime.GOOS)
	}
}

// workspaceJWT is a JWT whose "https://api.openai.com/auth" claim names
// account as the workspace; "" leaves the claim out.
func workspaceJWT(account string) string {
	if account == "" {
		return makeJWT(`{"email":"you@example.com"}`)
	}
	return makeJWT(`{"email":"you@example.com","https://api.openai.com/auth":{"chatgpt_account_id":"` + account + `"}}`)
}

// CredentialConflict compares three values and decides only for a ChatGPT login.
// The two Conflict shapes are the race's (docs/ACCEPTANCE.md § Fifth part): a
// refresh that rewrote every token, and one that rewrote only the access token.
func TestCredentialConflictVerdicts(t *testing.T) {
	login := func(mode, idToken, access, accountID string) []byte {
		modeField := ""
		if mode != "" {
			modeField = `"auth_mode":"` + mode + `",`
		}
		return []byte(`{` + modeField + `"tokens":{"id_token":"` + idToken + `","access_token":"` + access +
			`","refresh_token":"r","account_id":"` + accountID + `"}}`)
	}
	main, side := fixtureAccount, otherAccount
	for name, tc := range map[string]struct {
		payload []byte
		want    adapter.ConflictVerdict
	}{
		"every token is main's, account id side": {
			login("chatgpt", workspaceJWT(main), workspaceJWT(main), side), adapter.ConflictDetected,
		},
		"only the access token is main's": {
			login("chatgpt", workspaceJWT(side), workspaceJWT(main), side), adapter.ConflictDetected,
		},
		"tokens disagree, account id absent": {
			login("chatgpt", workspaceJWT(main), workspaceJWT(side), ""), adapter.ConflictDetected,
		},
		"auth_mode absent still decides": {
			login("", workspaceJWT(main), workspaceJWT(main), side), adapter.ConflictDetected,
		},
		"all three equal": {
			login("chatgpt", workspaceJWT(side), workspaceJWT(side), side), adapter.ConflictConsistent,
		},
		"two equal, id_token claim absent": {
			login("chatgpt", workspaceJWT(""), workspaceJWT(side), side), adapter.ConflictConsistent,
		},
		"claims absent from both tokens": {
			login("chatgpt", workspaceJWT(""), workspaceJWT(""), side), adapter.ConflictUnknown,
		},
		"broken JWTs": {
			login("chatgpt", "not-a-jwt", "a.!!.c", side), adapter.ConflictUnknown,
		},
		"no account id and one claim": {
			login("chatgpt", workspaceJWT(main), "opaque", ""), adapter.ConflictUnknown,
		},
		"external token mode": {
			login("chatgptAuthTokens", workspaceJWT(main), workspaceJWT(main), side), adapter.ConflictUnknown,
		},
		"api key login": {
			[]byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-x"}`), adapter.ConflictUnknown,
		},
		"no tokens object": {[]byte(`{"OPENAI_API_KEY":"sk-x"}`), adapter.ConflictUnknown},
		"not JSON":         {[]byte(`not json`), adapter.ConflictUnknown},
		"claim of the wrong type counts as absent": {
			login("chatgpt", makeJWT(`{"https://api.openai.com/auth":{"chatgpt_account_id":7}}`), workspaceJWT(main), side),
			adapter.ConflictDetected,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := (Codex{}).CredentialConflict(tc.payload); got != tc.want {
				t.Fatalf("CredentialConflict = %d, want %d", got, tc.want)
			}
		})
	}
}
