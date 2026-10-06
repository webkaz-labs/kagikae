// Package wsrpctest is the server side of a WebSocket JSON-RPC peer on a real
// Unix socket, shared by the tests of internal/wsrpc and of cmd's daemon probe.
// It implements the RFC 6455 framing independently of internal/wsrpc, so a
// defect there is not mirrored here.
package wsrpctest

import (
	"bufio"
	"crypto/sha1" // RFC 6455 fixes SHA-1 for Sec-WebSocket-Accept.
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
	"sync/atomic"
	"testing"
	"time"
)

// Frame opcodes (RFC 6455 §5.2).
const (
	OpContinuation = 0x0
	OpText         = 0x1
	OpBinary       = 0x2
	OpClose        = 0x8
	OpPing         = 0x9
	OpPong         = 0xA
)

// maxClientFrame bounds the length a client frame may declare.
const maxClientFrame = 1 << 20

// Peer is the server side of one connection.
type Peer struct {
	Conn net.Conn
	// BR is the only reader of Conn: the upgrade request and the first frames
	// can arrive in one read.
	BR *bufio.Reader
}

// ShortDir returns a fresh directory under /tmp, removed at cleanup. macOS
// t.TempDir() paths can exceed the 104-byte sun_path limit, so sockets live here.
func ShortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wsrpc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func listen(t *testing.T) (string, net.Listener) {
	t.Helper()
	socket := filepath.Join(ShortDir(t), "s.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return socket, ln
}

func newPeer(conn net.Conn) *Peer {
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return &Peer{Conn: conn, BR: bufio.NewReader(conn)}
}

// Serve listens on a real Unix socket and runs handle for the first
// connection; done receives handle's verdict.
func Serve(t *testing.T, handle func(p *Peer) error) (socket string, done <-chan error) {
	t.Helper()
	socket, ln := listen(t)
	ch := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			ch <- err
			return
		}
		defer func() { _ = conn.Close() }()
		ch <- handle(newPeer(conn))
	}()
	return socket, ch
}

// ServeEach runs handle for every connection and counts them, for a caller
// that must show it did not connect at all.
func ServeEach(t *testing.T, handle func(p *Peer)) (socket string, accepted *atomic.Int32) {
	t.Helper()
	socket, ln := listen(t)
	accepted = new(atomic.Int32)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go func() {
				defer func() { _ = conn.Close() }()
				handle(newPeer(conn))
			}()
		}
	}()
	return socket, accepted
}

// AcceptKey is the Sec-WebSocket-Accept value for a client key.
func AcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
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

// ReadUpgrade reads and checks the client's upgrade request and returns the
// Sec-WebSocket-Accept value the server should send.
func (p *Peer) ReadUpgrade() (string, error) {
	req, err := http.ReadRequest(p.BR)
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
		return "", errors.New("Sec-WebSocket-Key is not 16 base64 bytes")
	}
	return AcceptKey(key), nil
}

// Response101 is a valid upgrade response carrying accept.
func Response101(accept string) string {
	return "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
}

// Upgrade completes a valid handshake.
func (p *Peer) Upgrade() error {
	accept, err := p.ReadUpgrade()
	if err != nil {
		return err
	}
	_, err = io.WriteString(p.Conn, Response101(accept))
	return err
}

// Frame is one client frame, unmasked.
type Frame struct {
	Fin     bool
	Op      byte
	Mask    [4]byte
	Payload []byte
}

// ReadFrame reads one client frame and rejects it unless masked (RFC 6455
// §5.1: a server must close the connection on an unmasked client frame).
func (p *Peer) ReadFrame() (Frame, error) {
	var h [2]byte
	if _, err := io.ReadFull(p.BR, h[:]); err != nil {
		return Frame{}, err
	}
	f := Frame{Fin: h[0]&0x80 != 0, Op: h[0] & 0x0F}
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(p.BR, ext[:]); err != nil {
			return Frame{}, err
		}
		n = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(p.BR, ext[:]); err != nil {
			return Frame{}, err
		}
		n = binary.BigEndian.Uint64(ext[:])
	}
	if n > maxClientFrame {
		return Frame{}, fmt.Errorf("client frame declares %d bytes", n)
	}
	if h[1]&0x80 == 0 {
		_, _ = p.Conn.Write(RawFrame(true, OpClose, ClosePayload(1002)))
		return Frame{}, errors.New("client frame not masked")
	}
	if _, err := io.ReadFull(p.BR, f.Mask[:]); err != nil {
		return Frame{}, err
	}
	f.Payload = make([]byte, n)
	if _, err := io.ReadFull(p.BR, f.Payload); err != nil {
		return Frame{}, err
	}
	for i := range f.Payload {
		f.Payload[i] ^= f.Mask[i%4]
	}
	return f, nil
}

// RawFrame builds an unmasked server frame.
func RawFrame(fin bool, op byte, payload []byte) []byte {
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

// Text is one final text frame.
func Text(payload string) []byte { return RawFrame(true, OpText, []byte(payload)) }

// ClosePayload is a close frame body carrying code.
func ClosePayload(code uint16) []byte { return binary.BigEndian.AppendUint16(nil, code) }

// Send writes frames in order.
func (p *Peer) Send(frames ...[]byte) error {
	for _, f := range frames {
		if _, err := p.Conn.Write(f); err != nil {
			return err
		}
	}
	return nil
}
