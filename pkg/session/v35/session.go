// Package v35 implements session.Session for Tuya local protocol 3.5: the
// key-negotiation handshake, the message codes, matching replies to
// requests, routing status pushes, and keeping an idle link alive.
//
// One goroutine owns the read side of the connection for the session's
// whole life. Requests that expect a reply register before writing and
// are woken by that reader, so any number of goroutines can issue requests
// concurrently, and pushes are never lost to a caller that happened to be
// waiting for something else.
package v35

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/alex-bluetrain/notuya-go/pkg/session"
	transport "github.com/alex-bluetrain/notuya-go/pkg/transport/v35"
)

// Message codes. Only this package knows them.
const (
	cmdSessKeyNegStart  uint32 = 0x03
	cmdSessKeyNegResp   uint32 = 0x04
	cmdSessKeyNegFinish uint32 = 0x05
	cmdStatus           uint32 = 0x08 // FRM_TP_STAT_REPORT, device → client
	cmdHeartbeat        uint32 = 0x09
	cmdControl          uint32 = 0x0d // CONTROL_NEW
	cmdQuery            uint32 = 0x10 // DP_QUERY_NEW
	cmdRefresh          uint32 = 0x12 // UPDATEDPS / FRM_LAN_QUERY_DP
)

// DefaultHeartbeatInterval is how long the link may stay silent before
// the session sends a heartbeat. Tuya devices drop connections idle for
// roughly 30 s.
const DefaultHeartbeatInterval = 10 * time.Second

const pushBuffer = 16

// Options tunes a session. The zero value selects the defaults.
type Options struct {
	HeartbeatInterval time.Duration
}

// Session is a live protocol 3.5 session. See session.Session.
type Session struct {
	conn      *transport.Conn
	heartbeat time.Duration

	mu        sync.Mutex
	waiters   []*waiter
	lastWrite time.Time
	err       error

	pushes  chan session.Push
	done    chan struct{}
	closing chan struct{}
	once    sync.Once
	wg      sync.WaitGroup
}

var _ session.Session = (*Session)(nil)

// waiter is a request waiting for its reply. Replies carry the device's
// own sequence number rather than the request's (confirmed against a real
// capture), so they are matched by message code, oldest waiter first.
type waiter struct {
	cmds  []uint32
	reply chan result
}

type result struct {
	payload []byte
	err     error
}

// Open dials addr (host, or host:port), negotiates a session key with the
// device's local_key and starts the session. ctx bounds the dial and the
// handshake only; the session outlives it.
func Open(ctx context.Context, addr string, localKey []byte, opts Options) (*Session, error) {
	if len(localKey) != 16 {
		return nil, fmt.Errorf("v35: local key must be 16 bytes, got %d", len(localKey))
	}
	conn, err := transport.Dial(ctx, addr)
	if err != nil {
		return nil, err
	}
	sessionKey, err := handshake(ctx, conn, localKey)
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetKey(sessionKey)

	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = DefaultHeartbeatInterval
	}
	s := &Session{
		conn:      conn,
		heartbeat: opts.HeartbeatInterval,
		lastWrite: time.Now(),
		pushes:    make(chan session.Push, pushBuffer),
		done:      make(chan struct{}),
		closing:   make(chan struct{}),
	}
	s.wg.Add(2)
	go s.readLoop()
	go s.heartbeatLoop()
	return s, nil
}

// handshake runs the three-message key negotiation on a connection no
// other goroutine is using yet, and returns the session key.
func handshake(ctx context.Context, conn *transport.Conn, localKey []byte) ([]byte, error) {
	// The deadline covers this exchange only and is cleared after: a
	// leftover deadline would later expire under a live session.
	deadline, _ := ctx.Deadline()
	_ = conn.SetReadDeadline(deadline)
	defer conn.SetReadDeadline(time.Time{})

	conn.SetKey(localKey)
	localNonce, err := newNonce()
	if err != nil {
		return nil, err
	}
	if _, err := conn.WriteFrame(ctx, cmdSessKeyNegStart, localNonce); err != nil {
		return nil, fmt.Errorf("v35: handshake step 1: %w", err)
	}
	f, err := conn.ReadFrame()
	if err != nil {
		return nil, fmt.Errorf("v35: handshake step 2: %w", err)
	}
	remoteNonce, err := parseStep2(localKey, localNonce, f)
	if err != nil {
		return nil, err
	}
	if _, err := conn.WriteFrame(ctx, cmdSessKeyNegFinish, step3Payload(localKey, remoteNonce)); err != nil {
		return nil, fmt.Errorf("v35: handshake step 3: %w", err)
	}
	return deriveSessionKey(localKey, localNonce, remoteNonce)
}

// Query implements session.Session.
func (s *Session) Query(ctx context.Context) ([]byte, error) {
	payload, err := s.request(ctx, cmdQuery, []byte("{}"), cmdQuery)
	if err != nil {
		return nil, fmt.Errorf("v35: query: %w", err)
	}
	return jsonObject(payload), nil
}

// Control implements session.Session. Control bodies carry the 15-byte
// "3.5" version header, as tinytuya sends them.
//
// The wait completes on the device's acknowledgement or on a status push,
// whichever arrives first: either shows the command was received.
func (s *Session) Control(ctx context.Context, body []byte, wait bool) error {
	payload := append(versionHeader(), body...)
	var err error
	if wait {
		_, err = s.request(ctx, cmdControl, payload, cmdControl, cmdStatus)
	} else {
		err = s.send(ctx, cmdControl, payload)
	}
	if err != nil {
		return fmt.Errorf("v35: control: %w", err)
	}
	return nil
}

// Refresh implements session.Session.
func (s *Session) Refresh(ctx context.Context, dpIDs []int) error {
	body, err := json.Marshal(map[string][]int{"dpId": dpIDs})
	if err != nil {
		return err
	}
	if err := s.send(ctx, cmdRefresh, body); err != nil {
		return fmt.Errorf("v35: refresh: %w", err)
	}
	return nil
}

// Heartbeat implements session.Session.
func (s *Session) Heartbeat(ctx context.Context, wait bool) error {
	var err error
	if wait {
		_, err = s.request(ctx, cmdHeartbeat, nil, cmdHeartbeat)
	} else {
		err = s.send(ctx, cmdHeartbeat, nil)
	}
	if err != nil {
		return fmt.Errorf("v35: heartbeat: %w", err)
	}
	return nil
}

// Pushes implements session.Session.
func (s *Session) Pushes() <-chan session.Push { return s.pushes }

// Done implements session.Session.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err implements session.Session.
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close implements session.Session.
func (s *Session) Close() error {
	s.once.Do(func() { close(s.closing) })
	err := s.conn.Close()
	s.wg.Wait()
	return ignoreClosed(err)
}

// ErrClosed is returned by requests on a session that has ended.
var ErrClosed = errors.New("v35: session closed")

// send writes one frame without waiting for anything back.
func (s *Session) send(ctx context.Context, cmd uint32, payload []byte) error {
	select {
	case <-s.done:
		return s.endErr()
	default:
	}
	if _, err := s.conn.WriteFrame(ctx, cmd, payload); err != nil {
		s.fail(err)
		return err
	}
	s.mu.Lock()
	s.lastWrite = time.Now()
	s.mu.Unlock()
	return nil
}

// request writes one frame and waits for a reply with one of the given
// codes. The waiter is registered before the write so a fast reply cannot
// slip past it.
func (s *Session) request(ctx context.Context, cmd uint32, payload []byte, replyCmds ...uint32) ([]byte, error) {
	w := &waiter{cmds: replyCmds, reply: make(chan result, 1)}
	s.mu.Lock()
	if s.isDone() {
		s.mu.Unlock()
		return nil, s.endErr()
	}
	s.waiters = append(s.waiters, w)
	s.mu.Unlock()

	if err := s.send(ctx, cmd, payload); err != nil {
		s.dropWaiter(w)
		return nil, err
	}
	select {
	case r := <-w.reply:
		return r.payload, r.err
	case <-ctx.Done():
		s.dropWaiter(w)
		return nil, ctx.Err()
	}
}

func (s *Session) dropWaiter(w *waiter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.waiters {
		if x == w {
			s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
			return
		}
	}
}

// readLoop is the connection's only reader.
func (s *Session) readLoop() {
	defer s.wg.Done()
	defer close(s.pushes)
	for {
		f, err := s.conn.ReadFrame()
		if err != nil {
			s.fail(err)
			return
		}
		s.dispatch(f)
	}
}

func (s *Session) dispatch(f transport.Frame) {
	if f.Cmd == cmdStatus {
		s.push(session.Push{Body: jsonObject(f.Payload)})
	}

	s.mu.Lock()
	var w *waiter
	for i, x := range s.waiters {
		if x.accepts(f.Cmd) {
			w = x
			s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	if w == nil {
		return // an acknowledgement nobody waits for, e.g. of a streamed write
	}
	var r result
	if f.Cmd == cmdStatus {
		r.payload = jsonObject(f.Payload)
	} else {
		r.payload, r.err = stripRetcode(f.Payload)
	}
	w.reply <- r
}

func (w *waiter) accepts(cmd uint32) bool {
	for _, c := range w.cmds {
		if c == cmd {
			return true
		}
	}
	return false
}

// push delivers p, dropping the oldest buffered push when the reader of
// Pushes has fallen behind: a stale state report is worth less than a
// connection that keeps moving.
func (s *Session) push(p session.Push) {
	for {
		select {
		case s.pushes <- p:
			return
		default:
		}
		select {
		case <-s.pushes:
		default:
		}
	}
}

// heartbeatLoop keeps an idle link alive.
func (s *Session) heartbeatLoop() {
	defer s.wg.Done()
	t := time.NewTicker(s.heartbeat / 2)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.mu.Lock()
			idle := time.Since(s.lastWrite) >= s.heartbeat
			s.mu.Unlock()
			if idle {
				ctx, cancel := context.WithTimeout(context.Background(), s.heartbeat)
				_ = s.send(ctx, cmdHeartbeat, nil)
				cancel()
			}
		}
	}
}

// fail ends the session: the first error wins, every pending request is
// woken with it, and Done is closed.
func (s *Session) fail(err error) {
	s.mu.Lock()
	if s.isDone() {
		s.mu.Unlock()
		return
	}
	select {
	case <-s.closing:
		// A read or write failing because Close closed the connection is
		// the expected way down, not an error.
	default:
		s.err = err
	}
	waiters := s.waiters
	s.waiters = nil
	close(s.done)
	s.mu.Unlock()

	_ = s.conn.Close() // unblocks the reader if a write failed first
	for _, w := range waiters {
		w.reply <- result{err: s.endErr()}
	}
}

// isDone must be called with mu held, or racily as a fast path.
func (s *Session) isDone() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *Session) endErr() error {
	if err := s.Err(); err != nil {
		return fmt.Errorf("%w: %v", ErrClosed, err)
	}
	return ErrClosed
}

// versionHeader is the 15-byte prefix on 3.5 control payloads: "3.5"
// followed by 12 zero bytes.
func versionHeader() []byte {
	h := make([]byte, 15)
	copy(h, "3.5")
	return h
}

// stripRetcode removes the 4-byte status code devices put ahead of reply
// payloads, but leaves payloads that start straight with JSON alone: not
// every reply carries one.
func stripRetcode(p []byte) ([]byte, error) {
	if len(p) > 0 && p[0] == '{' {
		return p, nil
	}
	if len(p) < 4 {
		return p, nil
	}
	return transport.StripRetcode(p)
}

// jsonObject isolates the top-level JSON object in a device payload. Some
// wrap it in a retcode, a version header or trailing NUL padding, all of
// which json.Unmarshal rejects. It honours quoted strings and escapes. A
// payload with no object at all comes back empty.
func jsonObject(p []byte) []byte {
	start := -1
	for i, b := range p {
		if b == '{' {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(p); i++ {
		c := p[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return p[start : i+1]
			}
		}
	}
	return p[start:]
}

func ignoreClosed(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
