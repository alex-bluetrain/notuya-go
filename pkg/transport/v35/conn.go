package v35

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// DefaultPort is the TCP port Tuya local-protocol devices listen on.
const DefaultPort = 6668

// Conn is a 6699 frame stream over one TCP connection. It owns the
// encryption key in use and the outgoing sequence number; it holds no
// other state and attaches no meaning to command codes.
//
// WriteFrame is safe for concurrent use. ReadFrame is not: exactly one
// goroutine may read, which is how pkg/session/v35 uses it.
type Conn struct {
	nc net.Conn

	mu    sync.Mutex // guards key, seqno and writes
	key   []byte
	seqno uint32
}

// Dial connects to addr (host, or host:port to override DefaultPort). The
// context bounds the dial only.
func Dial(ctx context.Context, addr string) (*Conn, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, fmt.Sprint(DefaultPort))
	}
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("v35: dial %s: %w", addr, err)
	}
	return NewConn(nc), nil
}

// NewConn wraps an established connection.
func NewConn(nc net.Conn) *Conn { return &Conn{nc: nc} }

// SetKey switches the key used for every subsequent frame in both
// directions: the device's local_key during the handshake, the negotiated
// session key afterwards.
func (c *Conn) SetKey(key []byte) {
	c.mu.Lock()
	c.key = key
	c.mu.Unlock()
}

func (c *Conn) currentKey() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.key
}

// WriteFrame encrypts payload under the current key with a fresh IV and
// the next sequence number, and writes the frame. ctx's deadline, if any,
// bounds the write. It returns the sequence number used.
func (c *Conn) WriteFrame(ctx context.Context, cmd uint32, payload []byte) (uint32, error) {
	iv, err := NewIV()
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.key == nil {
		return 0, fmt.Errorf("v35: no key set")
	}
	c.seqno++
	raw, err := Encode(c.key, iv, Frame{Seqno: c.seqno, Cmd: cmd, Payload: payload})
	if err != nil {
		return 0, err
	}
	// Always set the deadline this call implies, clearing it when ctx has
	// none: a stale deadline from an earlier call would otherwise expire
	// under a long-lived stream.
	deadline, _ := ctx.Deadline()
	_ = c.nc.SetWriteDeadline(deadline)
	if _, err := c.nc.Write(raw); err != nil {
		return 0, fmt.Errorf("v35: writing frame: %w", err)
	}
	return c.seqno, nil
}

// ReadFrame reads and decrypts exactly one frame: the fixed-size header
// first, to learn the body length, then body and suffix. It blocks until a
// frame arrives, the read deadline passes, or the connection is closed.
func (c *Conn) ReadFrame() (Frame, error) {
	header := make([]byte, HeaderLen)
	if _, err := io.ReadFull(c.nc, header); err != nil {
		return Frame{}, fmt.Errorf("v35: reading frame header: %w", err)
	}
	n, err := BodyLen(header)
	if err != nil {
		return Frame{}, err
	}
	rest := make([]byte, n+4)
	if _, err := io.ReadFull(c.nc, rest); err != nil {
		return Frame{}, fmt.Errorf("v35: reading frame body (length=%d): %w", n, err)
	}
	return Decode(c.currentKey(), append(header, rest...))
}

// SetReadDeadline bounds ReadFrame. A zero time clears it.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.nc.SetReadDeadline(t) }

// Close closes the connection, unblocking any pending ReadFrame.
func (c *Conn) Close() error { return c.nc.Close() }
