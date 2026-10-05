package wsrpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/testutil/wsrpctest"
)

// secret stands in for personal data a peer may send; no error may contain it.
const secret = "you@example.com"

var testReqs = [][]byte{
	[]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
	[]byte(`{"jsonrpc":"2.0","method":"initialized"}`),
	[]byte(`{"jsonrpc":"2.0","id":2,"method":"account/read","params":{"refreshToken":false}}`),
}

const wantResp = `{"jsonrpc":"2.0","id":2,"result":{"account":{"email":"` + secret + `"}}}`

// The fake peer is wsrpctest's, which implements the framing independently of
// this package.
type srv = wsrpctest.Peer

var (
	startServer = wsrpctest.Serve
	rawFrame    = wsrpctest.RawFrame
	text        = wsrpctest.Text
	response101 = wsrpctest.Response101
)

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

// readRequests reads the client's request frames and checks them.
func readRequests(s *srv) error { return expect(s, testReqs) }

// expect reads one client frame per element of reqs and checks it byte for byte.
func expect(s *srv, reqs [][]byte) error {
	for i, want := range reqs {
		f, err := s.ReadFrame()
		if err != nil {
			return err
		}
		if !f.Fin || f.Op != opText || !bytes.Equal(f.Payload, want) {
			return fmt.Errorf("request %d: fin=%v op=%d %d bytes; want %d", i, f.Fin, f.Op, len(f.Payload), len(want))
		}
	}
	return nil
}

func TestCallReturnsMatchingResponseAfterNotificationsAndServerRequests(t *testing.T) {
	socket, done := startServer(t, func(s *srv) error {
		if err := s.Upgrade(); err != nil {
			return err
		}
		if err := readRequests(s); err != nil {
			return err
		}
		return s.Send(
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
		if err := s.Upgrade(); err != nil {
			return err
		}
		if err := readRequests(s); err != nil {
			return err
		}
		if err := s.Send(text(`{"jsonrpc":"2.0","id":7,"method":"execCommandApproval","params":{}}`), text(wantResp)); err != nil {
			return err
		}
		// The next client frame must be the closing handshake, not a reply.
		f, err := s.ReadFrame()
		if err != nil {
			return err
		}
		if f.Op != opClose {
			return fmt.Errorf("client sent opcode %d after server request; want close", f.Op)
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
		if err := s.Upgrade(); err != nil {
			return err
		}
		keys := map[[4]byte]bool{}
		for range testReqs {
			f, err := s.ReadFrame() // rejects unmasked frames
			if err != nil {
				return err
			}
			keys[f.Mask] = true
		}
		if len(keys) < 2 {
			return fmt.Errorf("mask keys repeat across %d frames", len(testReqs))
		}
		return s.Send(text(wantResp))
	})
	if _, err := call(t, socket, 2, 1<<20, 2*time.Second); err != nil {
		t.Fatalf("Call: %v", err)
	}
	serverErr(t, done)
}

func TestHandshakeAndFirstFrameInOneWrite(t *testing.T) {
	socket, done := startServer(t, func(s *srv) error {
		accept, err := s.ReadUpgrade()
		if err != nil {
			return err
		}
		// 101 and the answer in one write: they reach the client in one read,
		// so the frame sits in the client's bufio buffer after ReadResponse.
		one := append([]byte(response101(accept)), text(wantResp)...)
		if _, err := s.Conn.Write(one); err != nil {
			return err
		}
		return readRequests(s)
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
			return response101(wsrpctest.AcceptKey("not-the-client-key"))
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
				accept, err := s.ReadUpgrade()
				if err != nil {
					return err
				}
				_, _ = io.WriteString(s.Conn, tc.resp(accept))
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
		if err := s.Upgrade(); err != nil {
			return err
		}
		if err := readRequests(s); err != nil {
			return err
		}
		if err := s.Send(
			rawFrame(false, opText, []byte(a)),
			rawFrame(false, opContinuation, []byte(b)),
			rawFrame(true, opPing, []byte("p")),
		); err != nil {
			return err
		}
		f, err := s.ReadFrame()
		if err != nil {
			return err
		}
		if f.Op != opPong || string(f.Payload) != "p" {
			return fmt.Errorf("want pong %q, got opcode %d payload %q", "p", f.Op, f.Payload)
		}
		return s.Send(rawFrame(true, opContinuation, []byte(c)))
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
		if err := s.Upgrade(); err != nil {
			return err
		}
		if err := readRequests(s); err != nil {
			return err
		}
		if err := s.Send(rawFrame(true, opPing, []byte("hello"))); err != nil {
			return err
		}
		f, err := s.ReadFrame()
		if err != nil {
			return err
		}
		if !f.Fin || f.Op != opPong || string(f.Payload) != "hello" {
			return fmt.Errorf("want pong %q, got opcode %d payload %q", "hello", f.Op, f.Payload)
		}
		return s.Send(text(wantResp))
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
		if err := s.Upgrade(); err != nil {
			return err
		}
		if err := readRequests(s); err != nil {
			return err
		}
		if err := s.Send(frames...); err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, s.BR)
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
		if err := s.Upgrade(); err != nil {
			return err
		}
		if err := readRequests(s); err != nil {
			return err
		}
		if err := s.Send(rawFrame(true, opClose, closePayload(1001))); err != nil {
			return err
		}
		f, err := s.ReadFrame()
		if err != nil {
			return err
		}
		if f.Op != opClose {
			return fmt.Errorf("want close reply, got opcode %d", f.Op)
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
		if err := s.Upgrade(); err != nil {
			return err
		}
		if err := readRequests(s); err != nil {
			return err
		}
		return s.Send(text(`{"jsonrpc":"2.0","id":3,"result":{}}`))
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
		if _, err := s.ReadUpgrade(); err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, s.BR) // never answer the upgrade
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
				if err := s.Upgrade(); err != nil {
					return err
				}
				if err := expect(s, reqs); err != nil {
					return err
				}
				return s.Send(rawFrame(true, opText, sized(1, n)), rawFrame(true, opText, want))
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
