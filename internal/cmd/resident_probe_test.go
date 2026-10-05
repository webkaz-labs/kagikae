package cmd

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
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

// fakeDaemon is a codex managed daemon on a real Unix socket: it completes the
// WebSocket upgrade, records every client message, and answers the third one
// with answer (after a notification, as the real daemon sends). An empty answer
// keeps it silent until the client hangs up.
type fakeDaemon struct {
	socket   string // the short path it listens on
	accepted atomic.Int32
	mu       sync.Mutex
	messages []string
}

func (d *fakeDaemon) received() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.messages...)
}

// startFakeDaemon listens in a short directory under /tmp: macOS t.TempDir()
// paths can exceed the 104-byte sun_path limit, and the declared socket under
// the codex home links here the way codex's own does.
func startFakeDaemon(t *testing.T, answer string) *fakeDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "kaed")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	d := &fakeDaemon{socket: filepath.Join(dir, "d.sock")}
	ln, err := net.Listen("unix", d.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			d.accepted.Add(1)
			go d.serve(conn, answer)
		}
	}()
	return d
}

func (d *fakeDaemon) serve(conn net.Conn, answer string) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	sum := sha1.Sum([]byte(req.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n"+
		"Connection: Upgrade\r\nSec-WebSocket-Accept: "+base64.StdEncoding.EncodeToString(sum[:])+"\r\n\r\n")
	for i := 0; ; i++ {
		op, payload, err := readClientFrame(br)
		if err != nil || op == 0x8 {
			return
		}
		d.mu.Lock()
		d.messages = append(d.messages, string(payload))
		d.mu.Unlock()
		if i == 2 && answer != "" {
			_, _ = conn.Write(serverTextFrame(`{"jsonrpc":"2.0","method":"account/updated","params":{"authMode":"chatgpt","planType":"plus"}}`))
			_, _ = conn.Write(serverTextFrame(answer))
		}
	}
}

func readClientFrame(br *bufio.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(br, h[:]); err != nil {
		return 0, nil, err
	}
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(ext[:])
	}
	var mask [4]byte
	if _, err := io.ReadFull(br, mask[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(br, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return h[0] & 0x0F, payload, nil
}

func serverTextFrame(msg string) []byte {
	frame := []byte{0x81}
	switch n := len(msg); {
	case n <= 125:
		frame = append(frame, byte(n))
	default:
		frame = append(frame, 126)
		frame = binary.BigEndian.AppendUint16(frame, uint16(n))
	}
	return append(frame, msg...)
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
	return app.probeResidentDaemon(ctx, h, app.Env, liveCredential(ad, app.Env))
}

func TestProbeResidentDaemonMatchesAndDiffers(t *testing.T) {
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
	app, ad, h := probeFixture(t, probeAccount)
	dir, err := os.MkdirTemp("/tmp", "kaed")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "d.sock")
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

func TestProbeResidentDaemonUnreadableAnswersAreUnknown(t *testing.T) {
	for name, answer := range map[string]string{
		"not JSON":       `account`,
		"JSON-RPC error": `{"jsonrpc":"2.0","id":2,"error":{"code":-32600,"message":"` + probeEmail + `"}}`,
		"no result":      `{"jsonrpc":"2.0","id":2}`,
		"error beside result": `{"jsonrpc":"2.0","id":2,"error":{"code":-1,"message":"x"},"result":` +
			`{"workspaceRouting":{"chatgptAccountId":"` + probeAccount + `"}}}`,
		"no workspaceRouting": `{"jsonrpc":"2.0","id":2,"result":{"account":{"type":"chatgpt","email":"` + probeEmail + `"}}}`,
		"routing null":        `{"jsonrpc":"2.0","id":2,"result":{"account":{"type":"apiKey"},"workspaceRouting":null}}`,
		"id is a number":      `{"jsonrpc":"2.0","id":2,"result":{"workspaceRouting":{"chatgptAccountId":7}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			app, ad, h := probeFixture(t, probeAccount)
			d := startFakeDaemon(t, answer)
			linkSocket(t, app, h, d.socket)
			if got := probe(t, app, ad, h); got != constants.ResidentObservedUnknown {
				t.Errorf("probe = %q, want unknown", got)
			}
		})
	}
}

// A daemon that never answers is unknown at the caller's deadline.
func TestProbeResidentDaemonDeadlineIsUnknown(t *testing.T) {
	app, ad, h := probeFixture(t, probeAccount)
	d := startFakeDaemon(t, "")
	linkSocket(t, app, h, d.socket)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if got := app.probeResidentDaemon(ctx, h, app.Env, liveCredential(ad, app.Env)); got != constants.ResidentObservedUnknown {
		t.Errorf("probe = %q, want unknown", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("probe took %v past a 200ms deadline", elapsed)
	}
}

// With no deadline from the caller, the probe's own bounds the wait.
func TestProbeResidentDaemonBoundsASilentDaemon(t *testing.T) {
	t.Parallel()
	app, ad, h := probeFixture(t, probeAccount)
	d := startFakeDaemon(t, "")
	linkSocket(t, app, h, d.socket)
	start := time.Now()
	got := app.probeResidentDaemon(context.Background(), h, app.Env, liveCredential(ad, app.Env))
	elapsed := time.Since(start)
	if got != constants.ResidentObservedUnknown {
		t.Errorf("probe = %q, want unknown", got)
	}
	if elapsed < residentProbeTimeout || elapsed > residentProbeTimeout+time.Second {
		t.Errorf("probe returned after %v, want about %v", elapsed, residentProbeTimeout)
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
