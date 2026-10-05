package wsrpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// secret stands in for personal data a peer may send; no error may contain it.
const secret = "you@example.com"

var testReqs = [][]byte{
	[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
	[]byte(`{"jsonrpc":"2.0","method":"initialized"}`),
	[]byte(`{"jsonrpc":"2.0","id":2,"method":"account/read","params":{"refreshToken":false}}`),
}

const wantResp = `{"jsonrpc":"2.0","id":2,"result":{"account":{"email":"` + secret + `"}}}`

// srv is the server side of one fake connection.
type srv struct {
	conn net.Conn
	br   *bufio.Reader
}

// startServer listens on a real unix socket and runs handle for the first
// connection. macOS t.TempDir() paths can exceed the 104-byte sun_path limit,
// so the socket lives in a short directory under /tmp.
func startServer(t *testing.T, handle func(s *srv) error) (socket string, done <-chan error) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wsrpc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket = filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	ch := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			ch <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		ch <- handle(&srv{conn: conn, br: bufio.NewReader(conn)})
	}()
	return socket, ch
}

func call(t *testing.T, socket string, wantID, maxMsg int, timeout time.Duration) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return Call(ctx, (&net.Dialer{}).DialContext, socket, testReqs, wantID, maxMsg)
}

// serverErr waits for the handler's verdict.
func serverErr(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not finish")
	}
}

// readUpgrade reads and checks the client's upgrade request and returns the
// Sec-WebSocket-Accept value the server should send.
func (s *srv) readUpgrade() (string, error) {
	req, err := http.ReadRequest(s.br)
	if err != nil {
		return "", err
	}
	if req.Method != http.MethodGet || req.URL.Path != "/" {
		return "", fmt.Errorf("request line %s %s", req.Method, req.URL.Path)
	}
	if !strings.EqualFold(req.Header.Get("Upgrade"), "websocket") ||
		!headerHasToken(req.Header, "Connection", "upgrade") ||
		req.Header.Get("Sec-WebSocket-Version") != "13" {
		return "", fmt.Errorf("upgrade headers: %v", req.Header)
	}
	key := req.Header.Get("Sec-WebSocket-Key")
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 16 {
		return "", fmt.Errorf("Sec-WebSocket-Key is not 16 base64 bytes")
	}
	return acceptKey(key), nil
}

func response101(accept string) string {
	return "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
}

// upgrade completes a valid handshake.
func (s *srv) upgrade() error {
	accept, err := s.readUpgrade()
	if err != nil {
		return err
	}
	_, err = io.WriteString(s.conn, response101(accept))
	return err
}

type frame struct {
	fin     bool
	op      byte
	masked  bool
	mask    [4]byte
	payload []byte
}

// readFrame reads one client frame and rejects it unless masked (RFC 6455
// §5.1: a server must close the connection on an unmasked client frame).
func (s *srv) readFrame() (frame, error) {
	var h [2]byte
	if _, err := io.ReadFull(s.br, h[:]); err != nil {
		return frame{}, err
	}
	f := frame{fin: h[0]&0x80 != 0, op: h[0] & 0x0F, masked: h[1]&0x80 != 0}
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(s.br, ext[:]); err != nil {
			return frame{}, err
		}
		n = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(s.br, ext[:]); err != nil {
			return frame{}, err
		}
		n = binary.BigEndian.Uint64(ext[:])
	}
	if n > 1<<20 {
		return frame{}, fmt.Errorf("client frame declares %d bytes", n)
	}
	if !f.masked {
		_, _ = s.conn.Write(rawFrame(true, opClose, closePayload(1002)))
		return frame{}, errors.New("client frame not masked")
	}
	if _, err := io.ReadFull(s.br, f.mask[:]); err != nil {
		return frame{}, err
	}
	f.payload = make([]byte, n)
	if _, err := io.ReadFull(s.br, f.payload); err != nil {
		return frame{}, err
	}
	for i := range f.payload {
		f.payload[i] ^= f.mask[i%4]
	}
	return f, nil
}

// readRequests reads the client's request frames and checks them.
func (s *srv) readRequests() error { return s.expect(testReqs) }

// expect reads one client frame per element of reqs and checks it byte for byte.
func (s *srv) expect(reqs [][]byte) error {
	for i, want := range reqs {
		f, err := s.readFrame()
		if err != nil {
			return err
		}
		if !f.fin || f.op != opText || !bytes.Equal(f.payload, want) {
			return fmt.Errorf("request %d: fin=%v op=%d %d bytes; want %d", i, f.fin, f.op, len(f.payload), len(want))
		}
	}
	return nil
}

// rawFrame builds an unmasked server frame.
func rawFrame(fin bool, op byte, payload []byte) []byte {
	b0 := op
	if fin {
		b0 |= 0x80
	}
	out := []byte{b0}
	n := len(payload)
	switch {
	case n <= 125:
		out = append(out, byte(n))
	case n <= 0xFFFF:
		out = append(out, 126)
		out = binary.BigEndian.AppendUint16(out, uint16(n))
	default:
		out = append(out, 127)
		out = binary.BigEndian.AppendUint64(out, uint64(n))
	}
	return append(out, payload...)
}

func (s *srv) send(frames ...[]byte) error {
	for _, f := range frames {
		if _, err := s.conn.Write(f); err != nil {
			return err
		}
	}
	return nil
}

func text(payload string) []byte { return rawFrame(true, opText, []byte(payload)) }

func TestCallReturnsMatchingResponseAfterNotificationsAndServerRequests(t *testing.T) {
	socket, done := startServer(t, func(s *srv) error {
		if err := s.upgrade(); err != nil {
			return err
		}
		if err := s.readRequests(); err != nil {
			return err
		}
		return s.send(
			text(`{"jsonrpc":"2.0","method":"account/updated","params":{"email":"`+secret+`"}}`),
			text(`{"jsonrpc":"2.0","id":2,"method":"item/tool/requestUserInput","params":{}}`),
			text(`{"jsonrpc":"2.0","id":1,"result":{}}`),
			text(`{"jsonrpc":"2.0","id":"2","result":{}}`),
			text(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600}}`),
			text(wantResp),
		)
	})
	got, err := call(t, socket, 2, 1<<20, 2*time.Second)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(got) != wantResp {
		t.Fatalf("Call = %q; want %q", got, wantResp)
	}
	serverErr(t, done)
}

func TestCallDoesNotAnswerServerRequests(t *testing.T) {
	socket, done := startServer(t, func(s *srv) error {
		if err := s.upgrade(); err != nil {
			return err
		}
		if err := s.readRequests(); err != nil {
			return err
		}
		if err := s.send(text(`{"jsonrpc":"2.0","id":7,"method":"execCommandApproval","params":{}}`), text(wantResp)); err != nil {
			return err
		}
		// The next client frame must be the closing handshake, not a reply.
		f, err := s.readFrame()
		if err != nil {
			return err
		}
		if f.op != opClose {
			return fmt.Errorf("client sent opcode %d after server request; want close", f.op)
		}
		return nil
	})
	if _, err := call(t, socket, 2, 1<<20, 2*time.Second); err != nil {
		t.Fatalf("Call: %v", err)
	}
	serverErr(t, done)
}

func TestClientFramesAreMaskedWithFreshKeys(t *testing.T) {
	socket, done := startServer(t, func(s *srv) error {
		if err := s.upgrade(); err != nil {
			return err
		}
		keys := map[[4]byte]bool{}
		for range testReqs {
			f, err := s.readFrame() // rejects unmasked frames
			if err != nil {
				return err
			}
			keys[f.mask] = true
		}
		if len(keys) < 2 {
			return fmt.Errorf("mask keys repeat across %d frames", len(testReqs))
		}
		return s.send(text(wantResp))
	})
	if _, err := call(t, socket, 2, 1<<20, 2*time.Second); err != nil {
		t.Fatalf("Call: %v", err)
	}
	serverErr(t, done)
}

func TestHandshakeAndFirstFrameInOneWrite(t *testing.T) {
	socket, done := startServer(t, func(s *srv) error {
		accept, err := s.readUpgrade()
		if err != nil {
			return err
		}
		// 101 and the answer in one write: they reach the client in one read,
		// so the frame sits in the client's bufio buffer after ReadResponse.
		one := append([]byte(response101(accept)), text(wantResp)...)
		if _, err := s.conn.Write(one); err != nil {
			return err
		}
		return s.readRequests()
	})
	got, err := call(t, socket, 2, 1<<20, 2*time.Second)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(got) != wantResp {
		t.Fatalf("Call = %q; want %q", got, wantResp)
	}
	serverErr(t, done)
}

func TestHandshakeRejections(t *testing.T) {
	// ok101 is a valid 101 header block without the terminating blank line, so
	// a case can append one header and be rejected for that header alone.
	ok101 := func(accept string) string {
		return "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\n"
	}
	cases := []struct {
		name string
		resp func(accept string) string
		// want is a substring the error must contain, pinning which check fired.
		want string
	}{
		{"accept mismatch", func(string) string {
			return response101(acceptKey("not-the-client-key"))
		}, "Accept mismatch"},
		{"accept carries data", func(string) string {
			return response101(secret)
		}, "Accept mismatch"},
		{"status 200", func(accept string) string {
			return "HTTP/1.1 200 OK\r\nSec-WebSocket-Accept: " + accept + "\r\nContent-Length: 15\r\n\r\n" + secret
		}, "status 200"},
		{"status 400 with body", func(string) string {
			return "HTTP/1.1 400 Bad Request\r\nContent-Length: 15\r\n\r\n" + secret
		}, "status 400"},
		{"missing upgrade", func(accept string) string {
			return "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n"
		}, "missing Upgrade"},
		{"missing connection", func(accept string) string {
			return "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n"
		}, "missing Connection"},
		{"unrequested extension", func(accept string) string {
			return ok101(accept) + "Sec-WebSocket-Extensions: permessage-deflate\r\n\r\n"
		}, "unrequested"},
		{"unrequested subprotocol", func(accept string) string {
			return ok101(accept) + "Sec-WebSocket-Protocol: jsonrpc\r\n\r\n"
		}, "unrequested"},
		// Only the padding header is wrong, so only the budget can reject it.
		{"headers over budget", func(accept string) string {
			return ok101(accept) + "X-Pad: " + strings.Repeat("a", handshakeLimit) + "\r\n\r\n"
		}, "response headers exceed"},
		// net/http quotes the offending bytes into these errors; none may surface.
		{"malformed status code", func(string) string {
			return "HTTP/1.1 " + secret + " Switching Protocols\r\n\r\n"
		}, "malformed response"},
		{"malformed version", func(string) string {
			return secret + " 101 Switching Protocols\r\n\r\n"
		}, "malformed response"},
		{"duplicate content length", func(string) string {
			return "HTTP/1.1 200 OK\r\nContent-Length: 1\r\nContent-Length: 2" + secret + "\r\n\r\n"
		}, "malformed response"},
		{"unsupported transfer encoding", func(string) string {
			return "HTTP/1.1 200 OK\r\nTransfer-Encoding: " + secret + "\r\n\r\n"
		}, "malformed response"},
		{"header without colon", func(accept string) string {
			return ok101(accept) + "X-Bad " + secret + "\r\n\r\n"
		}, "malformed response"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			socket, done := startServer(t, func(s *srv) error {
				accept, err := s.readUpgrade()
				if err != nil {
					return err
				}
				_, _ = io.WriteString(s.conn, tc.resp(accept))
				return nil
			})
			_, err := call(t, socket, 2, 1<<20, 2*time.Second)
			if !errors.Is(err, ErrHandshake) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Call error = %v; want ErrHandshake containing %q", err, tc.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaks peer data: %v", err)
			}
			serverErr(t, done)
		})
	}
}

func TestFragmentedResponseWithInterleavedPing(t *testing.T) {
	a, b, c := wantResp[:10], wantResp[10:30], wantResp[30:]
	socket, done := startServer(t, func(s *srv) error {
		if err := s.upgrade(); err != nil {
			return err
		}
		if err := s.readRequests(); err != nil {
			return err
		}
		if err := s.send(
			rawFrame(false, opText, []byte(a)),
			rawFrame(false, opContinuation, []byte(b)),
			rawFrame(true, opPing, []byte("p")),
		); err != nil {
			return err
		}
		f, err := s.readFrame()
		if err != nil {
			return err
		}
		if f.op != opPong || string(f.payload) != "p" {
			return fmt.Errorf("want pong %q, got opcode %d payload %q", "p", f.op, f.payload)
		}
		return s.send(rawFrame(true, opContinuation, []byte(c)))
	})
	got, err := call(t, socket, 2, 1<<20, 2*time.Second)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(got) != wantResp {
		t.Fatalf("Call = %q; want %q", got, wantResp)
	}
	serverErr(t, done)
}

func TestPingIsAnsweredWithPong(t *testing.T) {
	socket, done := startServer(t, func(s *srv) error {
		if err := s.upgrade(); err != nil {
			return err
		}
		if err := s.readRequests(); err != nil {
			return err
		}
		if err := s.send(rawFrame(true, opPing, []byte("hello"))); err != nil {
			return err
		}
		f, err := s.readFrame()
		if err != nil {
			return err
		}
		if !f.fin || f.op != opPong || string(f.payload) != "hello" {
			return fmt.Errorf("want pong %q, got opcode %d payload %q", "hello", f.op, f.payload)
		}
		return s.send(text(wantResp))
	})
	if _, err := call(t, socket, 2, 1<<20, 2*time.Second); err != nil {
		t.Fatalf("Call: %v", err)
	}
	serverErr(t, done)
}

// failingServer upgrades, reads the requests and sends frames, then holds the
// connection open until the client goes away.
func failingServer(t *testing.T, frames ...[]byte) (string, <-chan error) {
	t.Helper()
	return startServer(t, func(s *srv) error {
		if err := s.upgrade(); err != nil {
			return err
		}
		if err := s.readRequests(); err != nil {
			return err
		}
		if err := s.send(frames...); err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, s.br)
		return nil
	})
}

func TestReadFailures(t *testing.T) {
	big := `{"jsonrpc":"2.0","id":2,"result":"` + strings.Repeat(secret, 20) + `"}`
	hugeLen := []byte{0x81, 127}
	hugeLen = binary.BigEndian.AppendUint64(hugeLen, 1<<40)
	msbLen := []byte{0x81, 127}
	msbLen = binary.BigEndian.AppendUint64(msbLen, 1<<63)
	maskedServer := append([]byte{0x81, 0x80 | 2, 0, 0, 0, 0}, "{}"...)
	cases := []struct {
		name   string
		frames [][]byte
		maxMsg int
		want   error
	}{
		{"message over maxMsg", [][]byte{text(big)}, 100, ErrMessageTooLarge},
		{"fragments over maxMsg", [][]byte{
			rawFrame(false, opText, []byte(big[:60])),
			rawFrame(true, opContinuation, []byte(big[60:])),
		}, 100, ErrMessageTooLarge},
		{"huge declared length", [][]byte{hugeLen}, 1 << 20, ErrMessageTooLarge},
		{"length with msb set", [][]byte{msbLen}, 1 << 20, ErrProtocol},
		{"binary frame", [][]byte{rawFrame(true, opBinary, []byte(secret))}, 1 << 20, ErrProtocol},
		{"masked server frame", [][]byte{maskedServer}, 1 << 20, ErrProtocol},
		{"reserved bits", [][]byte{append([]byte{0x80 | 0x40 | opText, 2}, "{}"...)}, 1 << 20, ErrProtocol},
		{"continuation without start", [][]byte{rawFrame(true, opContinuation, []byte("{}"))}, 1 << 20, ErrProtocol},
		{"text inside fragmented message", [][]byte{rawFrame(false, opText, []byte("{")), text("{}")}, 1 << 20, ErrProtocol},
		{"fragmented ping", [][]byte{rawFrame(false, opPing, nil)}, 1 << 20, ErrProtocol},
		{"malformed json", [][]byte{text(`{"email":"` + secret)}, 1 << 20, ErrProtocol},
		{"invalid utf-8", [][]byte{text(`{"jsonrpc":"2.0","id":2,"result":"` + secret + "\xff\"}")}, 1 << 20, ErrProtocol},
		{"control frame over 125 bytes", [][]byte{rawFrame(true, opPing, bytes.Repeat([]byte("p"), 126))}, 1 << 20, ErrProtocol},
		{"reserved data opcode", [][]byte{rawFrame(true, 0x3, []byte(`{"jsonrpc":"2.0","id":2,"result":{}}`))}, 1 << 20, ErrProtocol},
		{"close mid-stream", [][]byte{
			text(`{"jsonrpc":"2.0","method":"note"}`),
			rawFrame(true, opClose, append(closePayload(1001), secret...)),
		}, 1 << 20, ErrClosed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			socket, done := failingServer(t, tc.frames...)
			start := time.Now()
			_, err := call(t, socket, 2, tc.maxMsg, 2*time.Second)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Call error = %v; want %v", err, tc.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaks peer data: %v", err)
			}
			if time.Since(start) > time.Second {
				t.Fatalf("Call took %v; the failure should be immediate", time.Since(start))
			}
			serverErr(t, done)
		})
	}
}

func TestCloseIsAnswered(t *testing.T) {
	socket, done := startServer(t, func(s *srv) error {
		if err := s.upgrade(); err != nil {
			return err
		}
		if err := s.readRequests(); err != nil {
			return err
		}
		if err := s.send(rawFrame(true, opClose, closePayload(1001))); err != nil {
			return err
		}
		f, err := s.readFrame()
		if err != nil {
			return err
		}
		if f.op != opClose {
			return fmt.Errorf("want close reply, got opcode %d", f.op)
		}
		return nil
	})
	_, err := call(t, socket, 2, 1<<20, 2*time.Second)
	if !errors.Is(err, ErrClosed) || !strings.Contains(err.Error(), "1001") {
		t.Fatalf("Call error = %v; want ErrClosed with code 1001", err)
	}
	serverErr(t, done)
}

func TestEOFWithoutMatchingID(t *testing.T) {
	socket, done := startServer(t, func(s *srv) error {
		if err := s.upgrade(); err != nil {
			return err
		}
		if err := s.readRequests(); err != nil {
			return err
		}
		return s.send(text(`{"jsonrpc":"2.0","id":3,"result":{}}`))
		// The deferred Close ends the stream.
	})
	_, err := call(t, socket, 2, 1<<20, 2*time.Second)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("Call error = %v; want ErrClosed", err)
	}
	serverErr(t, done)
}

func TestDeadlineCoversSilentPeer(t *testing.T) {
	socket, done := failingServer(t)
	start := time.Now()
	_, err := call(t, socket, 2, 1<<20, 150*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Call error = %v; want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Call took %v; want it bounded by the 150ms deadline", elapsed)
	}
	serverErr(t, done)
}

func TestDeadlineCoversHandshake(t *testing.T) {
	socket, done := startServer(t, func(s *srv) error {
		if _, err := s.readUpgrade(); err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, s.br) // never answer the upgrade
		return nil
	})
	_, err := call(t, socket, 2, 1<<20, 150*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Call error = %v; want context.DeadlineExceeded", err)
	}
	serverErr(t, done)
}

func TestCancelWithoutDeadline(t *testing.T) {
	socket, done := failingServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, err := Call(ctx, (&net.Dialer{}).DialContext, socket, testReqs, 2, 1<<20)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Call error = %v; want context.Canceled", err)
	}
	serverErr(t, done)
}

func TestInvalidMaxMsg(t *testing.T) {
	dial := func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("dialed despite invalid maxMsg")
		return nil, nil
	}
	if _, err := Call(context.Background(), dial, "unused", nil, 1, 0); err == nil {
		t.Fatal("Call accepted maxMsg 0")
	}
}

func TestDialError(t *testing.T) {
	missing := filepath.Join("/tmp", "wsrpc-missing-"+fmt.Sprint(time.Now().UnixNano())+".sock")
	if _, err := call(t, missing, 2, 1<<20, time.Second); err == nil {
		t.Fatal("Call succeeded without a listener")
	}
}

// sized returns a JSON-RPC message with the given id that is exactly n bytes.
func sized(id, n int) []byte {
	head := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":"`, id)
	return []byte(head + strings.Repeat("x", n-len(head)-2) + `"}`)
}

// TestExtendedLengthsRoundTrip covers the 16-bit (126..65535) and 64-bit
// length encodings in both directions. The 70000-byte message also exceeds
// the handshake budget, so it fails unless the budget is lifted after the 101.
func TestExtendedLengthsRoundTrip(t *testing.T) {
	for _, n := range []int{126, 200, 65535, 65536, 70000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			reqs := [][]byte{sized(1, n), sized(2, n)}
			want := sized(2, n)
			socket, done := startServer(t, func(s *srv) error {
				if err := s.upgrade(); err != nil {
					return err
				}
				if err := s.expect(reqs); err != nil {
					return err
				}
				return s.send(rawFrame(true, opText, sized(1, n)), rawFrame(true, opText, want))
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			got, err := Call(ctx, (&net.Dialer{}).DialContext, socket, reqs, 2, 1<<20)
			if err != nil {
				t.Fatalf("Call: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("Call returned %d bytes; want the %d-byte message", len(got), len(want))
			}
			serverErr(t, done)
		})
	}
}

func TestUTF8SplitAcrossFragments(t *testing.T) {
	msg := []byte(`{"jsonrpc":"2.0","id":2,"result":"鍵"}`)
	cut := bytes.IndexRune(msg, '鍵') + 1 // inside the 3-byte sequence
	socket, done := failingServer(t, rawFrame(false, opText, msg[:cut]), rawFrame(true, opContinuation, msg[cut:]))
	got, err := call(t, socket, 2, 1<<20, 2*time.Second)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("Call = %q; want %q", got, msg)
	}
	serverErr(t, done)
}
