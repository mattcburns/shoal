package redfish

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // required by RFC 6455 handshake, not used for security
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Minimal hand-rolled RFC 6455 WebSocket client covering exactly what
// tryWebSocketSOL needs: a client-side handshake (with Basic-Auth header
// support), reading text/binary data frames (aggregating continuation
// frames), and sending a masked close frame. It intentionally does not
// implement compression, subprotocol negotiation, or ping/pong keepalive,
// since tryWebSocketSOL never exercises any of those.

// wsGUID is the well-known handshake magic value from RFC 6455 §1.3.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WebSocket opcodes (RFC 6455 §5.2).
const (
	wsOpContinuation = 0x0
	wsOpText         = 0x1
	wsOpBinary       = 0x2
	wsOpClose        = 0x8
	wsOpPing         = 0x9
	wsOpPong         = 0xa
)

// wsStatusNormalClosure is the RFC 6455 §7.4.1 "normal closure" status code.
const wsStatusNormalClosure = 1000

// wsMaxFramePayload caps a single frame's declared payload length as a
// sanity check against malformed/malicious length fields; SOL sniffing only
// ever deals with small frames.
const wsMaxFramePayload = 16 << 20 // 16 MiB

// wsAcceptKey computes the Sec-WebSocket-Accept value for a given
// Sec-WebSocket-Key per RFC 6455 §1.3.
func wsAcceptKey(key string) string {
	h := sha1.New() //nolint:gosec // RFC 6455 mandates SHA-1 for this handshake check
	h.Write([]byte(key))
	h.Write([]byte(wsGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func headerContainsToken(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// wsFramer implements RFC 6455 frame reading/writing over an underlying
// byte stream. mask controls whether outgoing frames are masked (true for
// the client role, false for the server role); incoming frames are
// unmasked based on their own mask bit, regardless of role, so the same
// type can be used to read frames sent by either side.
type wsFramer struct {
	w    io.Writer
	br   *bufio.Reader
	mask bool
}

// readFrame reads exactly one physical frame.
func (f *wsFramer) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var hdr [2]byte
	if _, err = io.ReadFull(f.br, hdr[:]); err != nil {
		return false, 0, nil, err
	}
	fin = hdr[0]&0x80 != 0
	opcode = hdr[0] & 0x0f
	masked := hdr[1]&0x80 != 0
	length := uint64(hdr[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(f.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(f.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
		if length > math.MaxInt64 {
			return false, 0, nil, fmt.Errorf("websocket: invalid frame length")
		}
	}
	if length > wsMaxFramePayload {
		return false, 0, nil, fmt.Errorf("websocket: frame payload too large (%d bytes)", length)
	}
	var maskKey [4]byte
	if masked {
		if _, err = io.ReadFull(f.br, maskKey[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload = make([]byte, length)
	if length > 0 {
		if _, err = io.ReadFull(f.br, payload); err != nil {
			return false, 0, nil, err
		}
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	return fin, opcode, payload, nil
}

// ReadMessage reads one complete message, transparently reassembling any
// continuation frames. Control frames (close/ping/pong) are never
// fragmented per RFC 6455 and are returned as soon as they're read.
func (f *wsFramer) ReadMessage() (opcode byte, payload []byte, err error) {
	var data []byte
	first := true
	var msgType byte
	for {
		fin, op, p, err := f.readFrame()
		if err != nil {
			return 0, nil, err
		}
		if op == wsOpPing || op == wsOpPong {
			// Not used by tryWebSocketSOL, but RFC 6455 §5.4 allows a
			// control frame to legally arrive interleaved between the
			// fragments of a data message; skip it without disturbing
			// any in-progress fragmentation state (which lives in this
			// same call's loop, not across calls).
			continue
		}
		if op == wsOpClose {
			// Close is also a control frame, complete in a single,
			// unfragmented frame; unlike ping/pong, callers need to see it.
			return op, p, nil
		}
		if first {
			if op == wsOpContinuation {
				return 0, nil, fmt.Errorf("websocket: unexpected continuation frame")
			}
			msgType = op
			first = false
		} else if op != wsOpContinuation {
			return 0, nil, fmt.Errorf("websocket: expected continuation frame, got opcode %d", op)
		}
		data = append(data, p...)
		if fin {
			return msgType, data, nil
		}
	}
}

// writeFrame writes a single, final (fin=1) frame with the given opcode
// and payload, masking it if f.mask is set (required for client->server
// frames, forbidden for server->client frames per RFC 6455 §5.1).
func (f *wsFramer) writeFrame(opcode byte, payload []byte) error {
	n := len(payload)
	b0 := byte(0x80) | (opcode & 0x0f)
	var hdr []byte
	switch {
	case n <= 125:
		hdr = []byte{b0, byte(n)}
	case n <= math.MaxUint16:
		hdr = []byte{b0, 126, 0, 0}
		binary.BigEndian.PutUint16(hdr[2:], uint16(n))
	default:
		hdr = make([]byte, 10)
		hdr[0] = b0
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:], uint64(n))
	}
	buf := make([]byte, 0, len(hdr)+4+n)
	if f.mask {
		hdr[1] |= 0x80
		buf = append(buf, hdr...)
		var maskKey [4]byte
		if _, err := rand.Read(maskKey[:]); err != nil {
			return fmt.Errorf("websocket: generate mask key: %w", err)
		}
		buf = append(buf, maskKey[:]...)
		masked := make([]byte, n)
		for i := 0; i < n; i++ {
			masked[i] = payload[i] ^ maskKey[i%4]
		}
		buf = append(buf, masked...)
	} else {
		buf = append(buf, hdr...)
		buf = append(buf, payload...)
	}
	_, err := f.w.Write(buf)
	return err
}

// wsConn is a client-side WebSocket connection, established by wsDial.
type wsConn struct {
	conn net.Conn
	fr   *wsFramer
}

// wsDial performs a client-side RFC 6455 handshake to rawURL ("ws://" or
// "wss://") over a freshly dialed net.Conn, honoring ctx for both the TCP
// dial and the TLS handshake. httpClient's Transport, if it carries a
// custom *tls.Config (e.g. InsecureSkipVerify or a custom CA pool, as
// configured by (*client).httpClient), is reused for wss:// connections.
// header carries any extra request headers (e.g. Authorization) to send
// with the Upgrade request. It returns the established connection, and
// separately the HTTP status code observed during the handshake (0 if the
// connection never got as far as a response) for error-path logging.
func wsDial(ctx context.Context, rawURL string, httpClient *http.Client, header http.Header) (*wsConn, int, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, 0, fmt.Errorf("websocket: parse url: %w", err)
	}
	var useTLS bool
	switch strings.ToLower(u.Scheme) {
	case "ws":
	case "wss":
		useTLS = true
	default:
		return nil, 0, fmt.Errorf("websocket: unsupported scheme %q", u.Scheme)
	}

	host := u.Host
	if _, _, splitErr := net.SplitHostPort(host); splitErr != nil {
		if useTLS {
			host = net.JoinHostPort(host, "443")
		} else {
			host = net.JoinHostPort(host, "80")
		}
	}

	var d net.Dialer
	rawConn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, 0, fmt.Errorf("websocket: dial: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = rawConn.SetDeadline(deadline)
	}

	var conn net.Conn = rawConn
	if useTLS {
		var tlsConfig *tls.Config
		if httpClient != nil {
			if t, ok := httpClient.Transport.(*http.Transport); ok && t.TLSClientConfig != nil {
				tlsConfig = t.TLSClientConfig.Clone()
			}
		}
		if tlsConfig == nil {
			tlsConfig = &tls.Config{} //nolint:gosec // default verification; InsecureSkipVerify only set explicitly above
		}
		if tlsConfig.ServerName == "" {
			tlsConfig = tlsConfig.Clone()
			tlsConfig.ServerName = u.Hostname()
		}
		tlsC := tls.Client(rawConn, tlsConfig)
		if hsErr := tlsC.HandshakeContext(ctx); hsErr != nil {
			_ = rawConn.Close()
			return nil, 0, fmt.Errorf("websocket: tls handshake: %w", hsErr)
		}
		conn = tlsC
	}

	keyRaw := make([]byte, 16)
	if _, err := rand.Read(keyRaw); err != nil {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("websocket: generate key: %w", err)
	}
	key := base64.StdEncoding.EncodeToString(keyRaw)

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("websocket: build request: %w", err)
	}
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", key)
	req.Header.Set("Sec-WebSocket-Version", "13")
	for k, vv := range header {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}

	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("websocket: write handshake request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("websocket: read handshake response: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	status := resp.StatusCode
	if status != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, status, fmt.Errorf("websocket: unexpected handshake status %s", resp.Status)
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") ||
		!headerContainsToken(resp.Header.Get("Connection"), "upgrade") {
		_ = conn.Close()
		return nil, status, fmt.Errorf("websocket: handshake response missing Upgrade/Connection headers")
	}
	if got, want := resp.Header.Get("Sec-WebSocket-Accept"), wsAcceptKey(key); got != want {
		_ = conn.Close()
		return nil, status, fmt.Errorf("websocket: Sec-WebSocket-Accept mismatch")
	}

	// Clear the dial/handshake deadline; the caller manages read deadlines
	// explicitly for the post-handshake sniff and streaming reads.
	_ = conn.SetDeadline(time.Time{})

	return &wsConn{conn: conn, fr: &wsFramer{w: conn, br: br, mask: true}}, status, nil
}

// SetReadDeadline sets the deadline for the next ReadMessage call.
func (c *wsConn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

// ReadMessage reads one complete WebSocket message (see wsFramer.ReadMessage).
func (c *wsConn) ReadMessage() (opcode byte, payload []byte, err error) { return c.fr.ReadMessage() }

// Close sends a masked close frame (normal closure) and closes the
// underlying connection.
func (c *wsConn) Close() error {
	payload := make([]byte, 2)
	binary.BigEndian.PutUint16(payload, wsStatusNormalClosure)
	_ = c.fr.writeFrame(wsOpClose, payload)
	return c.conn.Close()
}

// Reader returns an io.Reader that transparently reassembles subsequent
// text/binary data frames into one continuous byte stream, mirroring the
// behavior callers previously got from coder/websocket's NetConn helper.
// It has no read deadline of its own; callers relying on a timeout should
// call SetReadDeadline before reading.
func (c *wsConn) Reader() io.Reader { return &wsStreamReader{fr: c.fr} }

type wsStreamReader struct {
	fr  *wsFramer
	buf []byte
}

func (r *wsStreamReader) Read(p []byte) (int, error) {
	for len(r.buf) == 0 {
		opcode, payload, err := r.fr.ReadMessage()
		if err != nil {
			return 0, err
		}
		switch opcode {
		case wsOpClose:
			return 0, io.EOF
		case wsOpText, wsOpBinary:
			if len(payload) == 0 {
				continue
			}
			r.buf = payload
		default:
			// Ignore anything else (e.g. ping/pong, which this client never
			// sends and doesn't expect, but tolerate defensively).
			continue
		}
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}
