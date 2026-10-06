// Package wsrpc is a minimal WebSocket JSON-RPC client for a local IPC socket.
// It exists so kae can ask a resident upstream process (the codex managed
// app-server daemon) one read-only question without adding a WebSocket
// dependency: it opens one connection, performs the RFC 6455 opening handshake,
// sends a fixed list of JSON-RPC messages as masked text frames, and reads until
// the response with the wanted id arrives.
//
// The package is a leaf: it imports only the standard library and knows nothing
// about which methods are sent. Callers own the request allowlist.
//
// Messages from the peer may carry personal data (email, account id, plan), so
// no error this package returns contains frame payloads, response bodies or
// header values — only kinds, lengths, status codes and close codes.
package wsrpc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1" // RFC 6455 fixes SHA-1 for Sec-WebSocket-Accept.
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Dialer opens the transport connection. The production value is
// (&net.Dialer{}).DialContext; tests may pass the same against a fake server.
type Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

// Sentinel errors. Returned errors wrap one of these where it applies, so
// callers can classify a failure with errors.Is without parsing text.
var (
	// ErrHandshake: the peer did not complete a valid WebSocket upgrade.
	ErrHandshake = errors.New("wsrpc: websocket handshake failed")
	// ErrProtocol: the peer sent a frame or message this client does not accept
	// (binary data, masked server frame, reserved bits, bad fragmentation,
	// malformed JSON).
	ErrProtocol = errors.New("wsrpc: websocket protocol violation")
	// ErrMessageTooLarge: a message (or a declared frame length) exceeds maxMsg.
	ErrMessageTooLarge = errors.New("wsrpc: message exceeds size limit")
	// ErrClosed: the connection ended (close frame or EOF) before the wanted
	// response arrived.
	ErrClosed = errors.New("wsrpc: connection closed before response")
)

const (
	acceptGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

	// handshakeLimit bounds the bytes read for the HTTP 101 response so a peer
	// cannot make the client buffer unbounded header lines.
	handshakeLimit = 16 << 10

	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA

	maxControlPayload = 125
)

// Call dials socket (a unix-domain socket path) with dial, upgrades the
// connection to WebSocket, sends each element of reqs as one text message in
// order, and returns the complete JSON-RPC response message (the raw object,
// including "jsonrpc", "id" and either "result" or "error") whose integer "id"
// equals wantID. Returning the whole message lets the caller tell a JSON-RPC
// error from a result without a second error channel.
//
// While waiting, notifications (no id) and server requests (an id plus a
// method) are read and discarded; server requests are not answered. Responses
// with another id are discarded too. Pings are answered with pongs. A close
// frame or EOF before the wanted response returns ErrClosed.
//
// maxMsg bounds one reassembled message in bytes and must be positive; a
// larger message, or a frame header declaring a larger length, returns
// ErrMessageTooLarge before the payload is read. A text message that is not
// valid UTF-8 returns ErrProtocol (RFC 6455 §8.1).
//
// The ctx deadline (and cancellation) applies to the whole exchange: dial,
// handshake, writes and reads. Callers must give ctx a deadline: Call does not
// impose one, so a ctx with neither deadline nor cancellation waits on a silent
// peer forever.
func Call(ctx context.Context, dial Dialer, socket string, reqs [][]byte, wantID int, maxMsg int) ([]byte, error) {
	if maxMsg <= 0 {
		return nil, fmt.Errorf("wsrpc: maxMsg must be positive, got %d", maxMsg)
	}
	conn, err := dial(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("wsrpc: dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("wsrpc: set deadline: %w", err)
		}
	}
	// Cancellation without a deadline still has to unblock a pending read.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	c := &client{conn: conn, lim: &limitReader{r: conn, n: handshakeLimit}}
	c.br = bufio.NewReader(c.lim)

	resp, err := c.exchange(reqs, wantID, maxMsg)
	if err != nil {
		// The only conn deadlines are the ctx deadline and the cancel hook, so a
		// conn timeout means ctx is done or about to be (its timer may fire a
		// moment after the conn's).
		// The wait is bounded in case a custom Dialer returned a conn with its
		// own deadline.
		if errors.Is(err, os.ErrDeadlineExceeded) {
			select {
			case <-ctx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("wsrpc: %w", ctxErr)
		}
		return nil, err
	}
	// Best-effort normal closure; the answer is already in hand.
	_ = c.writeFrame(opClose, closePayload(1000))
	return resp, nil
}

type client struct {
	conn net.Conn
	lim  *limitReader
	// br is the only reader of conn after the handshake: the 101 response and
	// the first frames can arrive in one read, and whatever bufio buffered past
	// the response headers belongs to the frame stream.
	br *bufio.Reader
}

func (c *client) exchange(reqs [][]byte, wantID int, maxMsg int) ([]byte, error) {
	if err := c.handshake(); err != nil {
		return nil, err
	}
	// Frames are bounded by maxMsg, not by the handshake budget.
	c.lim.n = math.MaxInt64
	for _, req := range reqs {
		if err := c.writeFrame(opText, req); err != nil {
			return nil, fmt.Errorf("wsrpc: write request: %w", err)
		}
	}
	for {
		msg, err := c.readMessage(maxMsg)
		if err != nil {
			return nil, err
		}
		matched, err := isResponseTo(msg, wantID)
		if err != nil {
			return nil, err
		}
		if matched {
			return msg, nil
		}
	}
}

func (c *client) handshake() error {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	key := base64.StdEncoding.EncodeToString(raw[:])

	req := "GET / HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	if _, err := io.WriteString(c.conn, req); err != nil {
		return fmt.Errorf("%w: write request: %w", ErrHandshake, err)
	}

	resp, err := http.ReadResponse(c.br, &http.Request{Method: http.MethodGet})
	if err != nil {
		return handshakeReadErr(err)
	}
	// A 101 has no body; a non-101 body is never read (it may echo data).
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("%w: status %d", ErrHandshake, resp.StatusCode)
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		return fmt.Errorf("%w: missing Upgrade: websocket", ErrHandshake)
	}
	if !headerHasToken(resp.Header, "Connection", "upgrade") {
		return fmt.Errorf("%w: missing Connection: Upgrade", ErrHandshake)
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != acceptKey(key) {
		return fmt.Errorf("%w: Sec-WebSocket-Accept mismatch", ErrHandshake)
	}
	// No extension or subprotocol was offered, so none may be selected.
	if resp.Header.Get("Sec-WebSocket-Extensions") != "" || resp.Header.Get("Sec-WebSocket-Protocol") != "" {
		return fmt.Errorf("%w: unrequested extension or subprotocol", ErrHandshake)
	}
	return nil
}

// handshakeReadErr classifies a failure to read the 101 response. net/http and
// textproto quote the offending status line or header into their error text,
// which may carry personal data, so only the class survives.
func handshakeReadErr(err error) error {
	switch {
	case errors.Is(err, ErrHandshake): // header budget exceeded (limitReader)
		return err
	case errors.Is(err, os.ErrDeadlineExceeded):
		return fmt.Errorf("%w: read response: %w", ErrHandshake, os.ErrDeadlineExceeded)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("%w: read response: %w", ErrHandshake, io.ErrUnexpectedEOF)
	default:
		return fmt.Errorf("%w: malformed response", ErrHandshake)
	}
}

func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + acceptGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// writeFrame sends one final, masked frame (RFC 6455 §5.3: every client frame
// is masked with a fresh random key).
func (c *client) writeFrame(op byte, payload []byte) error {
	n := len(payload)
	hdr := make([]byte, 0, 14)
	hdr = append(hdr, 0x80|op)
	switch {
	case n <= 125:
		hdr = append(hdr, 0x80|byte(n))
	case n <= math.MaxUint16:
		hdr = append(hdr, 0x80|126)
		hdr = binary.BigEndian.AppendUint16(hdr, uint16(n))
	default:
		hdr = append(hdr, 0x80|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	hdr = append(hdr, mask[:]...)

	buf := make([]byte, len(hdr)+n)
	copy(buf, hdr)
	for i, b := range payload {
		buf[len(hdr)+i] = b ^ mask[i%4]
	}
	_, err := c.conn.Write(buf)
	return err
}

// readMessage returns the next complete text message, answering pings and
// skipping pongs on the way.
func (c *client) readMessage(maxMsg int) ([]byte, error) {
	var msg []byte
	inMessage := false
	for {
		fin, op, n, err := c.readHeader()
		if err != nil {
			return nil, err
		}
		if op >= opClose {
			if !fin || n > maxControlPayload {
				return nil, fmt.Errorf("%w: invalid control frame (opcode %d, %d bytes)", ErrProtocol, op, n)
			}
			payload, err := c.readPayload(n)
			if err != nil {
				return nil, err
			}
			switch op {
			case opPing:
				if err := c.writeFrame(opPong, payload); err != nil {
					return nil, fmt.Errorf("wsrpc: write pong: %w", err)
				}
			case opPong:
			case opClose:
				code := 1005 // no status code present
				if len(payload) >= 2 {
					code = int(binary.BigEndian.Uint16(payload))
				}
				_ = c.writeFrame(opClose, closePayload(1000))
				return nil, fmt.Errorf("%w: peer sent close (code %d)", ErrClosed, code)
			default:
				return nil, fmt.Errorf("%w: unknown control opcode %d", ErrProtocol, op)
			}
			continue
		}

		switch op {
		case opText:
			if inMessage {
				return nil, fmt.Errorf("%w: new message inside a fragmented message", ErrProtocol)
			}
			inMessage = true
		case opContinuation:
			if !inMessage {
				return nil, fmt.Errorf("%w: continuation without a message", ErrProtocol)
			}
		case opBinary:
			return nil, fmt.Errorf("%w: binary frame (%d bytes)", ErrProtocol, n)
		default:
			return nil, fmt.Errorf("%w: unknown data opcode %d", ErrProtocol, op)
		}
		if n > uint64(maxMsg-len(msg)) {
			return nil, fmt.Errorf("%w: limit %d bytes", ErrMessageTooLarge, maxMsg)
		}
		payload, err := c.readPayload(n)
		if err != nil {
			return nil, err
		}
		msg = append(msg, payload...)
		if fin {
			if !utf8.Valid(msg) {
				return nil, fmt.Errorf("%w: text message is not valid UTF-8 (%d bytes)", ErrProtocol, len(msg))
			}
			return msg, nil
		}
	}
}

// readHeader reads a frame header. Server frames must not be masked and must
// not set reserved bits (no extension was negotiated).
func (c *client) readHeader() (fin bool, op byte, n uint64, err error) {
	var h [2]byte
	if _, err := io.ReadFull(c.br, h[:]); err != nil {
		return false, 0, 0, readErr(err)
	}
	fin = h[0]&0x80 != 0
	if h[0]&0x70 != 0 {
		return false, 0, 0, fmt.Errorf("%w: reserved bits set", ErrProtocol)
	}
	op = h[0] & 0x0F
	if h[1]&0x80 != 0 {
		return false, 0, 0, fmt.Errorf("%w: masked server frame", ErrProtocol)
	}
	n = uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, 0, readErr(err)
		}
		n = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, 0, readErr(err)
		}
		n = binary.BigEndian.Uint64(ext[:])
		if n > math.MaxInt64 {
			return false, 0, 0, fmt.Errorf("%w: frame length has the most significant bit set", ErrProtocol)
		}
	}
	return fin, op, n, nil
}

// readPayload reads n bytes; callers have already bounded n.
func (c *client) readPayload(n uint64) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.br, buf); err != nil {
		return nil, readErr(err)
	}
	return buf, nil
}

func readErr(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%w: end of stream", ErrClosed)
	}
	return fmt.Errorf("wsrpc: read: %w", err)
}

func closePayload(code uint16) []byte {
	return binary.BigEndian.AppendUint16(nil, code)
}

// isResponseTo reports whether msg is a JSON-RPC response (an id and no
// method) whose id is the number wantID.
func isResponseTo(msg []byte, wantID int) (bool, error) {
	var env struct {
		ID     json.RawMessage `json:"id"`
		Method *string         `json:"method"`
	}
	if err := json.Unmarshal(msg, &env); err != nil {
		return false, fmt.Errorf("%w: malformed JSON message (%d bytes)", ErrProtocol, len(msg))
	}
	if env.Method != nil || len(env.ID) == 0 {
		return false, nil // notification or server request
	}
	id, err := strconv.ParseInt(string(bytes.TrimSpace(env.ID)), 10, 64)
	if err != nil {
		return false, nil // null or string id: not ours
	}
	return id == int64(wantID), nil
}

// limitReader caps how many bytes may still be read; the handshake sets a small
// budget and frame reading lifts it.
type limitReader struct {
	r io.Reader
	n int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, fmt.Errorf("%w: response headers exceed %d bytes", ErrHandshake, handshakeLimit)
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}
