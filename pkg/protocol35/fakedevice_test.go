package protocol35

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/averstraeten/notuya-go/pkg/protocol"
)

// cmdStatus is the unsolicited status push a device sends on its own
// initiative (tinytuya's STATUS). Only the fake device emits it, so it
// lives here rather than in the protocol package.
const cmdStatus uint32 = 0x08

func hmacSHA256(key, message []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return mac.Sum(nil)
}

// fakeDevice is an in-process stand-in for a bulb: it speaks the real v3.5
// handshake over a real TCP socket and records the frames it receives.
//
// It exists because the streaming path cannot be exercised with a mock
// session — what it has to get right (fire-and-forget writes racing a
// concurrent reader on one connection, deadlines set per direction) only
// exists at the socket level.
type fakeDevice struct {
	t        *testing.T
	listener net.Listener
	localKey []byte

	mu       sync.Mutex
	received []decodedFrame
	sessKey  []byte
	conn     net.Conn

	// push carries frames for the device to send unsolicited, simulating
	// the status updates a real bulb emits during a stream.
	push chan []byte

	closed   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func newFakeDevice(t *testing.T, localKey []byte) *fakeDevice {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	d := &fakeDevice{
		t:        t,
		listener: ln,
		localKey: localKey,
		push:     make(chan []byte, 8),
		closed:   make(chan struct{}),
	}
	d.wg.Add(1)
	go d.serve()
	t.Cleanup(d.stop)
	return d
}

func (d *fakeDevice) addr() string { return d.listener.Addr().String() }

// stop is idempotent: tests call it explicitly to simulate a device going
// away, and t.Cleanup calls it again afterwards.
func (d *fakeDevice) stop() {
	d.stopOnce.Do(d.shutdown)
}

func (d *fakeDevice) shutdown() {
	close(d.closed)
	_ = d.listener.Close()
	// Close the accepted connection too, not just the listener: serve() is
	// parked in a blocking read and would never notice the shutdown
	// otherwise, hanging the Wait below.
	d.mu.Lock()
	conn := d.conn
	d.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	d.wg.Wait()
}

func (d *fakeDevice) serve() {
	defer d.wg.Done()
	conn, err := d.listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	d.mu.Lock()
	d.conn = conn
	d.mu.Unlock()

	sessKey, err := d.handshake(conn)
	if err != nil {
		select {
		case <-d.closed:
		default:
			d.t.Errorf("fake device handshake: %v", err)
		}
		return
	}
	d.mu.Lock()
	d.sessKey = sessKey
	d.mu.Unlock()

	// Push unsolicited frames on their own goroutine: a real device does
	// not wait for us to speak before sending status updates.
	var pushWG sync.WaitGroup
	pushWG.Add(1)
	go func() {
		defer pushWG.Done()
		var seqno uint32 = 1000
		for {
			select {
			case <-d.closed:
				return
			case payload := <-d.push:
				seqno++
				iv := make([]byte, ivLen)
				frame, err := encodeRequest(sessKey, iv, seqno, cmdStatus, payload)
				if err != nil {
					return
				}
				if _, err := conn.Write(frame); err != nil {
					return
				}
			}
		}
	}()
	defer pushWG.Wait()

	for {
		frame, err := readOneFrame(conn)
		if err != nil {
			return
		}
		df, err := decodeFrame(sessKey, frame)
		if err != nil {
			select {
			case <-d.closed:
			default:
				d.t.Errorf("fake device decoding client frame: %v", err)
			}
			return
		}
		d.mu.Lock()
		d.received = append(d.received, *df)
		d.mu.Unlock()

		// Only DP_QUERY_NEW gets a reply here: that is what the streaming
		// path uses as its blocking warm-up, and answering the
		// fire-and-forget colour writes too would mask a client that
		// wrongly waits for them.
		if df.Cmd == protocol.DPQueryNew {
			iv := make([]byte, ivLen)
			payload := append(make([]byte, retcodeLen), []byte(`{"dps":{"20":true}}`)...)
			resp, err := encodeRequest(sessKey, iv, df.Seqno, protocol.DPQueryNew, payload)
			if err != nil {
				return
			}
			if _, err := conn.Write(resp); err != nil {
				return
			}
		}
	}
}

// handshake plays the device side of the 3-message session negotiation and
// derives the same session key the client does.
func (d *fakeDevice) handshake(conn net.Conn) ([]byte, error) {
	frame, err := readOneFrame(conn)
	if err != nil {
		return nil, err
	}
	step1, err := decodeFrame(d.localKey, frame)
	if err != nil {
		return nil, err
	}
	if step1.Cmd != protocol.SessKeyNegStart {
		return nil, errors.New("expected SESS_KEY_NEG_START")
	}
	localNonce := step1.Payload

	remoteNonce := []byte("0123456789abcdef")
	mac := hmacSHA256(d.localKey, localNonce)
	payload := append(make([]byte, retcodeLen), remoteNonce...)
	payload = append(payload, mac...)

	iv := make([]byte, ivLen)
	resp, err := encodeRequest(d.localKey, iv, step1.Seqno, protocol.SessKeyNegResp, payload)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(resp); err != nil {
		return nil, err
	}

	if _, err := readOneFrame(conn); err != nil { // step3, not verified here
		return nil, err
	}
	return deriveSessionKey(d.localKey, localNonce, remoteNonce)
}

func (d *fakeDevice) frames() []decodedFrame {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]decodedFrame(nil), d.received...)
}

// waitForFrames polls until the device has received at least n frames, so
// tests do not depend on a fixed sleep.
func (d *fakeDevice) waitForFrames(n int, timeout time.Duration) []decodedFrame {
	d.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := d.frames()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("timed out waiting for %d frames, got %d", n, len(got))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readOneFrame reads a single 6699 frame from conn, mirroring
// Session.readFrame's header-then-body approach.
func readOneFrame(conn net.Conn) ([]byte, error) {
	header := make([]byte, headerLen)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[14:18])
	if length > maxFrameBody {
		return nil, errors.New("frame body too large")
	}
	rest := make([]byte, length+4)
	if _, err := io.ReadFull(conn, rest); err != nil {
		return nil, err
	}
	return append(header, rest...), nil
}
