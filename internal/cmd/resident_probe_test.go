package cmd

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/wsrpctest"
)

// Fixture personal data a daemon answer carries; none of it may leave the probe.
const (
	probeEmail     = "you@example.com"
	probeAccount   = "acct-fixture-0001"
	probeOtherAcct = "acct-fixture-0002"
)

func accountReadReply(accountID string) string {
	return `{"jsonrpc":"2.0","id":2,"result":{"account":{"type":"chatgpt","email":"` + probeEmail +
		`","planType":"plus"},"requiresOpenaiAuth":true,"workspaceRouting":{"chatgptAccountId":"` +
		accountID + `","backendOrigin":"https://chatgpt.com","accountRoutingOverride":"NO_CONSTRAINT"}}}`
}

// fakeDaemon is a codex managed daemon on a real Unix socket (wsrpctest): it
// completes the WebSocket upgrade, records every client message, and answers
// the third one with its current answer, after a notification as the real
// daemon sends. The answer can change while it runs, as a restart changes the
// account a daemon holds; an empty answer keeps it silent until the client
// hangs up.
type fakeDaemon struct {
	socket   string // the short path it listens on
	accepted *atomic.Int32
	answer   atomic.Pointer[string]
	mu       sync.Mutex
	messages []string
}

func (d *fakeDaemon) received() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.messages...)
}

// answers sets the reply to account/read from now on.
func (d *fakeDaemon) answers(reply string) { d.answer.Store(&reply) }

// holds makes the daemon answer that it holds accountID.
func (d *fakeDaemon) holds(accountID string) { d.answers(accountReadReply(accountID)) }

// startFakeDaemon listens in a short directory under /tmp, beyond the sun_path
// limit's reach; the declared socket under the codex home links there the way
// codex's own does.
func startFakeDaemon(t *testing.T, answer string) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{}
	d.answers(answer)
	d.socket, d.accepted = wsrpctest.ServeEach(t, func(p *wsrpctest.Peer) {
		if p.Upgrade() != nil {
			return
		}
		for i := 0; ; i++ {
			f, err := p.ReadFrame()
			if err != nil || f.Op == wsrpctest.OpClose {
				return
			}
			d.mu.Lock()
			d.messages = append(d.messages, string(f.Payload))
			d.mu.Unlock()
			if answer := *d.answer.Load(); i == 2 && answer != "" {
				_ = p.Send(wsrpctest.Text(`{"jsonrpc":"2.0","method":"account/updated","params":{"authMode":"chatgpt","planType":"plus"}}`),
					wsrpctest.Text(answer))
			}
		}
	})
	return d
}

// probeFixture is an App whose real codex home holds a file-store credential
// for credentialAccount ("" writes none).
func probeFixture(t *testing.T, credentialAccount string) (*App, adapter.Adapter, adapter.ResidentHolder) {
	t.Helper()
	app := testApp(t, nil)
	if credentialAccount != "" {
		writeFile(t, filepath.Join(app.Env.Home, ".codex", "auth.json"),
			`{"auth_mode":"chatgpt","tokens":{"access_token":"fixture","account_id":"`+credentialAccount+`"}}`)
	}
	ad, err := adapter.ForTool(constants.ToolCodex)
	if err != nil {
		t.Fatal(err)
	}
	return app, ad, ad.(adapter.ResidentHolder)
}

// linkSocket points the declared socket of app's codex home at target.
func linkSocket(t *testing.T, app *App, h adapter.ResidentHolder, target string) string {
	t.Helper()
	declared := h.ResidentDaemon(app.Env).Socket
	if err := os.MkdirAll(filepath.Dir(declared), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, declared); err != nil {
		t.Fatal(err)
	}
	return declared
}

func probe(t *testing.T, app *App, ad adapter.Adapter, h adapter.ResidentHolder) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return app.probeResidentDaemon(ctx, h, h.ResidentDaemon(app.Env), liveCredential(ad, app.Env))
}

func TestProbeResidentDaemonMatchesAndDiffers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		daemonAccount, want string
	}{
		{probeAccount, constants.ResidentObservedMatches},
		{probeOtherAcct, constants.ResidentObservedDiffers},
	} {
		app, ad, h := probeFixture(t, probeAccount)
		d := startFakeDaemon(t, accountReadReply(tc.daemonAccount))
		linkSocket(t, app, h, d.socket)
		if got := probe(t, app, ad, h); got != tc.want {
			t.Errorf("daemon on %s: probe = %q, want %q", tc.daemonAccount, got, tc.want)
		}
	}
}

// What reaches the daemon is the fixed read-only sequence, as sent on the wire.
func TestProbeResidentDaemonSendsOnlyTheReadOnlySequence(t *testing.T) {
	t.Parallel()
	app, ad, h := probeFixture(t, probeAccount)
	d := startFakeDaemon(t, accountReadReply(probeAccount))
	linkSocket(t, app, h, d.socket)
	probe(t, app, ad, h)

	var methods []string
	for _, raw := range d.received() {
		var m struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("client sent non-JSON %q", raw)
		}
		methods = append(methods, m.Method)
		if m.Method == "account/read" {
			if v, ok := m.Params["refreshToken"].(bool); !ok || v {
				t.Errorf("account/read refreshToken = %#v, want false", m.Params["refreshToken"])
			}
		}
		lower := strings.ToLower(raw)
		for _, banned := range []string{"getauthstatus", "account/logout", "account/login"} {
			if strings.Contains(lower, banned) {
				t.Errorf("client sent %s: %q", banned, raw)
			}
		}
	}
	if got := strings.Join(methods, ","); got != "initialize,initialized,account/read" {
		t.Errorf("methods on the wire = %s", got)
	}
}

func TestProbeResidentDaemonAbsent(t *testing.T) {
	t.Parallel()
	app, ad, h := probeFixture(t, probeAccount)
	if got := probe(t, app, ad, h); got != constants.ResidentObservedAbsent {
		t.Errorf("no socket: probe = %q", got)
	}
	// codex's socket is a symlink into a temporary directory; a daemon that is
	// gone leaves a link to nothing.
	linkSocket(t, app, h, filepath.Join(t.TempDir(), "gone.sock"))
	if got := probe(t, app, ad, h); got != constants.ResidentObservedAbsent {
		t.Errorf("dangling link: probe = %q", got)
	}
	// No daemon is absent whatever the credential: there is nothing to compare.
	app, ad, h = probeFixture(t, "")
	if got := probe(t, app, ad, h); got != constants.ResidentObservedAbsent {
		t.Errorf("no socket, no credential: probe = %q", got)
	}
}

// The declared path under a long home exceeds sun_path; the probe connects to
// the short target the link names.
func TestProbeResidentDaemonFollowsTheLinkPastTheSunPathLimit(t *testing.T) {
	t.Parallel()
	app, ad, h := probeFixture(t, "")
	app.Env.Home = filepath.Join(app.Env.Home, strings.Repeat("h", 60))
	writeFile(t, filepath.Join(app.Env.Home, ".codex", "auth.json"),
		`{"tokens":{"account_id":"`+probeAccount+`"}}`)
	d := startFakeDaemon(t, accountReadReply(probeAccount))
	declared := linkSocket(t, app, h, d.socket)
	if len(declared) <= 104 {
		t.Fatalf("declared socket is only %d bytes; the case needs more than 104", len(declared))
	}
	if got := probe(t, app, ad, h); got != constants.ResidentObservedMatches {
		t.Errorf("probe = %q, want matches", got)
	}
}

func TestProbeResidentDaemonUnknownWithoutConnecting(t *testing.T) {
	t.Parallel()
	for name, setup := range map[string]func(t *testing.T, app *App, h adapter.ResidentHolder, d *fakeDaemon){
		"socket owned by another user": func(t *testing.T, app *App, h adapter.ResidentHolder, d *fakeDaemon) {
			app.euidForTest = func() int { return os.Geteuid() + 1 }
			linkSocket(t, app, h, d.socket)
		},
		"link to a regular file": func(t *testing.T, app *App, h adapter.ResidentHolder, _ *fakeDaemon) {
			file := filepath.Join(t.TempDir(), "plain")
			writeFile(t, file, "")
			linkSocket(t, app, h, file)
		},
		"no live credential": func(t *testing.T, app *App, h adapter.ResidentHolder, d *fakeDaemon) {
			if err := os.Remove(filepath.Join(app.Env.Home, ".codex", "auth.json")); err != nil {
				t.Fatal(err)
			}
			linkSocket(t, app, h, d.socket)
		},
		"credential without an account": func(t *testing.T, app *App, h adapter.ResidentHolder, d *fakeDaemon) {
			writeFile(t, filepath.Join(app.Env.Home, ".codex", "auth.json"), `{"OPENAI_API_KEY":"sk-fixture"}`)
			linkSocket(t, app, h, d.socket)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			app, ad, h := probeFixture(t, probeAccount)
			d := startFakeDaemon(t, accountReadReply(probeAccount))
			setup(t, app, h, d)
			if got := probe(t, app, ad, h); got != constants.ResidentObservedUnknown {
				t.Errorf("probe = %q, want unknown", got)
			}
			if n := d.accepted.Load(); n != 0 {
				t.Errorf("the probe connected %d times", n)
			}
		})
	}
}

// A daemon that exited can leave its socket file behind; connecting to it is
// refused, and that is unknown, not absent: the socket exists.
func TestProbeResidentDaemonStaleSocketIsUnknown(t *testing.T) {
	t.Parallel()
	app, ad, h := probeFixture(t, probeAccount)
	socket := filepath.Join(wsrpctest.ShortDir(t), "d.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	linkSocket(t, app, h, socket)
	if got := probe(t, app, ad, h); got != constants.ResidentObservedUnknown {
		t.Errorf("probe = %q, want unknown", got)
	}
}

// A JSON-RPC response may carry "error": null beside its result; that is no error.
func TestProbeResidentDaemonNullErrorIsNoError(t *testing.T) {
	t.Parallel()
	app, ad, h := probeFixture(t, probeAccount)
	answer := `{"jsonrpc":"2.0","id":2,"error":null,"result":{"workspaceRouting":{"chatgptAccountId":"` + probeOtherAcct + `"}}}`
	d := startFakeDaemon(t, answer)
	linkSocket(t, app, h, d.socket)
	if got := probe(t, app, ad, h); got != constants.ResidentObservedDiffers {
		t.Errorf("probe = %q, want differs", got)
	}
}

func TestProbeResidentDaemonUnreadableAnswersAreUnknown(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]string{
		"not JSON":       `account`,
		"JSON-RPC error": `{"jsonrpc":"2.0","id":2,"error":{"code":-32600,"message":"` + probeEmail + `"}}`,
		"no result":      `{"jsonrpc":"2.0","id":2}`,
		"null result":    `{"jsonrpc":"2.0","id":2,"result":null}`,
		"error beside result": `{"jsonrpc":"2.0","id":2,"error":{"code":-1,"message":"x"},"result":` +
			`{"workspaceRouting":{"chatgptAccountId":"` + probeAccount + `"}}}`,
		"no workspaceRouting": `{"jsonrpc":"2.0","id":2,"result":{"account":{"type":"chatgpt","email":"` + probeEmail + `"}}}`,
		"routing null":        `{"jsonrpc":"2.0","id":2,"result":{"account":{"type":"apiKey"},"workspaceRouting":null}}`,
		"id is a number":      `{"jsonrpc":"2.0","id":2,"result":{"workspaceRouting":{"chatgptAccountId":7}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			app, ad, h := probeFixture(t, probeAccount)
			d := startFakeDaemon(t, answer)
			linkSocket(t, app, h, d.socket)
			if got := probe(t, app, ad, h); got != constants.ResidentObservedUnknown {
				t.Errorf("probe = %q, want unknown", got)
			}
		})
	}
}

// A daemon that never answers is unknown at the caller's deadline, when that
// comes before the probe's own.
func TestProbeResidentDaemonDeadlineIsUnknown(t *testing.T) {
	t.Parallel()
	app, ad, h := probeFixture(t, probeAccount)
	d := startFakeDaemon(t, "")
	linkSocket(t, app, h, d.socket)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if got := app.probeResidentDaemon(ctx, h, h.ResidentDaemon(app.Env), liveCredential(ad, app.Env)); got != constants.ResidentObservedUnknown {
		t.Errorf("probe = %q, want unknown", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("probe took %v past a 100ms deadline", elapsed)
	}
}

// With no deadline from the caller, the probe's own bounds the wait.
func TestProbeResidentDaemonBoundsASilentDaemon(t *testing.T) {
	t.Parallel()
	app, ad, h := probeFixture(t, probeAccount)
	limit := 200 * time.Millisecond
	app.residentProbeTimeout = limit
	d := startFakeDaemon(t, "")
	linkSocket(t, app, h, d.socket)
	start := time.Now()
	got := app.probeResidentDaemon(context.Background(), h, h.ResidentDaemon(app.Env), liveCredential(ad, app.Env))
	elapsed := time.Since(start)
	if got != constants.ResidentObservedUnknown {
		t.Errorf("probe = %q, want unknown", got)
	}
	if elapsed < limit || elapsed > limit+time.Second {
		t.Errorf("probe returned after %v, want about %v", elapsed, limit)
	}
}

// The probe's own limit is 2 seconds unless a test shortens it
// (docs/SECURITY.md § Resident processes).
func TestResidentProbeLimitDefaultsToTwoSeconds(t *testing.T) {
	t.Parallel()
	if got := (&App{}).residentProbeLimit(); got != 2*time.Second {
		t.Errorf("residentProbeLimit = %v, want 2s", got)
	}
}

// The keyring store's payload is the same JSON; the live read goes through the
// adapter's resolved spec.
func TestProbeResidentDaemonReadsTheKeyringCredential(t *testing.T) {
	app, ad, h := probeFixture(t, "")
	app.Env.GOOS = "darwin"
	writeFile(t, filepath.Join(app.Env.Home, ".codex", "config.toml"), "cli_auth_credentials_store = \"keyring\"\n")
	d := startFakeDaemon(t, accountReadReply(probeOtherAcct))
	linkSocket(t, app, h, d.socket)
	var got string
	runner.With(&autoStoreKeychain{payload: `{"tokens":{"account_id":"` + probeAccount + `"}}`}, func() {
		got = probe(t, app, ad, h)
	})
	if got != constants.ResidentObservedDiffers {
		t.Errorf("probe = %q, want differs", got)
	}
}

// No email, account id or plan leaves the probe: not in its result, not on
// stdout or stderr, whatever the daemon answers.
func TestProbeResidentDaemonPrintsNoPersonalData(t *testing.T) {
	for _, answer := range []string{
		accountReadReply(probeOtherAcct),
		`{"jsonrpc":"2.0","id":2,"error":{"code":-1,"message":"` + probeEmail + " " + probeOtherAcct + `"}}`,
		`{"jsonrpc":"2.0","id":2,"result":{"workspaceRouting":{"chatgptAccountId":["` + probeOtherAcct + `"]}}}`,
	} {
		app, ad, h := probeFixture(t, probeAccount)
		d := startFakeDaemon(t, answer)
		linkSocket(t, app, h, d.socket)
		var result string
		_, stdout, stderr := captureBoth(t, func() int {
			result = probe(t, app, ad, h)
			return 0
		})
		for _, out := range []string{result, stdout, stderr} {
			for _, pii := range []string{probeEmail, probeAccount, probeOtherAcct, "plus"} {
				if strings.Contains(out, pii) {
					t.Errorf("probe output %q contains %q", out, pii)
				}
			}
		}
		switch result {
		case constants.ResidentObservedDiffers, constants.ResidentObservedUnknown:
		default:
			t.Errorf("probe = %q", result)
		}
	}
}
