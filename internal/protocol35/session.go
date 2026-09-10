package protocol35

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/averstraeten/notuya-go/internal/protocol"
)

var _ protocol.Session = (*Session)(nil)

// DefaultPort is the TCP port Tuya local-protocol devices listen on.
const DefaultPort = 6668

// Session is a protocol.Session implementation for Tuya local protocol
// v3.5: TCP transport, "6699" AES-GCM framing, and the 3-message
// session-key handshake (see handshake.go, frame.go).
type Session struct {
	addr         string
	realLocalKey []byte

	conn       net.Conn
	sessionKey []byte
	seqno      uint32
}

// NewSession constructs a v3.5 session for a device at addr (host, or
// host:port to override the default Tuya local port) authenticated with
// its local_key.
func NewSession(addr string, localKey []byte) *Session {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, strconv.Itoa(DefaultPort))
	}
	return &Session{addr: addr, realLocalKey: localKey}
}

// Open dials the device and negotiates the session key. See
// deriveSessionKey's doc comment for the derivation itself.
func (s *Session) Open(ctx context.Context) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("protocol35: dial %s: %w", s.addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	s.conn = conn
	s.seqno = 0

	if err := s.negotiateSessionKey(); err != nil {
		_ = conn.Close()
		s.conn = nil
		return err
	}
	return nil
}

func (s *Session) negotiateSessionKey() error {
	localNonce, err := generateNonce()
	if err != nil {
		return err
	}

	iv1, err := randomIV()
	if err != nil {
		return err
	}
	s.seqno++
	step1, err := buildStep1(s.realLocalKey, localNonce, iv1, s.seqno)
	if err != nil {
		return err
	}
	if err := s.writeFrame(step1); err != nil {
		return fmt.Errorf("protocol35: sending handshake step1: %w", err)
	}

	resp, err := s.readFrame()
	if err != nil {
		return fmt.Errorf("protocol35: reading handshake step2: %w", err)
	}
	remoteNonce, err := parseStep2(s.realLocalKey, localNonce, resp)
	if err != nil {
		return err
	}

	iv3, err := randomIV()
	if err != nil {
		return err
	}
	s.seqno++
	step3, err := buildStep3(s.realLocalKey, remoteNonce, iv3, s.seqno)
	if err != nil {
		return err
	}
	// The device does not reply to step3 — confirmed against a real capture
	// (the next frame on the wire is always our own next request).
	if err := s.writeFrame(step3); err != nil {
		return fmt.Errorf("protocol35: sending handshake step3: %w", err)
	}

	sessionKey, err := deriveSessionKey(s.realLocalKey, localNonce, remoteNonce)
	if err != nil {
		return err
	}
	s.sessionKey = sessionKey
	return nil
}

// Command sends a framed request under the negotiated session key. If wait
// is false it returns immediately after the write, with a nil payload —
// reserved for a future persistent/fire-and-forget mode; Phase A (this
// milestone) always calls with wait=true.
func (s *Session) Command(ctx context.Context, cmd uint32, payload []byte, wait bool) ([]byte, error) {
	if s.conn == nil || s.sessionKey == nil {
		return nil, fmt.Errorf("protocol35: session is not open")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = s.conn.SetDeadline(deadline)
	}

	iv, err := randomIV()
	if err != nil {
		return nil, err
	}
	s.seqno++
	frame, err := encodeRequest(s.sessionKey, iv, s.seqno, cmd, payload)
	if err != nil {
		return nil, err
	}
	if err := s.writeFrame(frame); err != nil {
		return nil, fmt.Errorf("protocol35: sending command %d: %w", cmd, err)
	}
	if !wait {
		return nil, nil
	}

	respFrame, err := s.readFrame()
	if err != nil {
		return nil, fmt.Errorf("protocol35: reading response to command %d: %w", cmd, err)
	}
	df, err := decodeFrame(s.sessionKey, respFrame)
	if err != nil {
		return nil, err
	}
	return stripRetcode(df.Payload)
}

// Close releases the underlying connection. Safe to call multiple times.
func (s *Session) Close() error {
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

func (s *Session) writeFrame(frame []byte) error {
	_, err := s.conn.Write(frame)
	return err
}

// readFrame reads exactly one 6699 frame off the wire: the fixed-size
// header first (to learn the body length), then body+suffix.
func (s *Session) readFrame() ([]byte, error) {
	header := make([]byte, headerLen)
	if _, err := io.ReadFull(s.conn, header); err != nil {
		return nil, fmt.Errorf("reading frame header: %w", err)
	}
	// header layout: prefix(4) + unknown(2) + seqno(4) + cmd(4) + length(4).
	length := binary.BigEndian.Uint32(header[14:18])
	rest := make([]byte, length+4)
	if _, err := io.ReadFull(s.conn, rest); err != nil {
		return nil, fmt.Errorf("reading frame body (length=%d): %w", length, err)
	}
	return append(header, rest...), nil
}

func randomIV() ([]byte, error) {
	iv := make([]byte, ivLen)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	return iv, nil
}
