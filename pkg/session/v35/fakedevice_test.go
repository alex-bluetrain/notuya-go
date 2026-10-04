package v35

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	transport "github.com/alex-bluetrain/notuya-go/pkg/transport/v35"
)

var testKey = []byte("0123456789abcdef")

// fakeDevice is an in-process bulb: the device side of the real handshake
// over a real socket. It answers Query, acknowledges Control with a status
// push the way real bulbs do (unless silent is set, as a streaming bulb
// stops answering), answers Heartbeat, and turns Refresh into a push.
type fakeDevice struct {
	t  *testing.T
	ln net.Listener

	silentControl bool

	mu       sync.Mutex
	received []transport.Frame
	conn     *transport.Conn

	ready chan struct{}
	wg    sync.WaitGroup
}

func newFakeDevice(t *testing.T) *fakeDevice {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDevice{t: t, ln: ln, ready: make(chan struct{})}
	d.wg.Add(1)
	go d.serve()
	t.Cleanup(func() {
		ln.Close()
		d.mu.Lock()
		if d.conn != nil {
			d.conn.Close()
		}
		d.mu.Unlock()
		d.wg.Wait()
	})
	return d
}

func (d *fakeDevice) addr() string { return d.ln.Addr().String() }

func (d *fakeDevice) serve() {
	defer d.wg.Done()
	nc, err := d.ln.Accept()
	if err != nil {
		return
	}
	c := transport.NewConn(nc)
	d.mu.Lock()
	d.conn = c
	d.mu.Unlock()
	if err := d.handshake(c); err != nil {
		d.t.Logf("fake handshake: %v", err)
		return
	}
	close(d.ready)
	for {
		f, err := c.ReadFrame()
		if err != nil {
			return
		}
		d.mu.Lock()
		d.received = append(d.received, f)
		d.mu.Unlock()
		ctx := context.Background()
		switch f.Cmd {
		case cmdQuery:
			_, _ = c.WriteFrame(ctx, cmdQuery, transport.WithRetcode([]byte(`{"dps":{"20":true,"22":500}}`)))
		case cmdControl:
			if d.silentControl {
				continue
			}
			var env struct {
				Data struct {
					DPS json.RawMessage `json:"dps"`
				} `json:"data"`
			}
			_ = json.Unmarshal(jsonObject(f.Payload), &env)
			_, _ = c.WriteFrame(ctx, cmdControl, transport.WithRetcode(nil))
			d.sendPush(`{"protocol":4,"data":{"dps":` + string(env.Data.DPS) + `}}`)
		case cmdHeartbeat:
			_, _ = c.WriteFrame(ctx, cmdHeartbeat, transport.WithRetcode(nil))
		case cmdRefresh:
			d.sendPush(`{"dps":{"22":500}}`)
		}
	}
}

func (d *fakeDevice) handshake(c *transport.Conn) error {
	c.SetKey(testKey)
	f, err := c.ReadFrame()
	if err != nil {
		return err
	}
	remote := []byte("fedcba9876543210")
	reply := append(append([]byte(nil), remote...), hmacSHA256(testKey, f.Payload)...)
	if _, err := c.WriteFrame(context.Background(), cmdSessKeyNegResp, transport.WithRetcode(reply)); err != nil {
		return err
	}
	if _, err := c.ReadFrame(); err != nil {
		return err
	}
	key, err := deriveSessionKey(testKey, f.Payload, remote)
	if err != nil {
		return err
	}
	c.SetKey(key)
	return nil
}

// sendPush emits an unsolicited status report: 3.5 version header, then
// the JSON body, as real bulbs send them.
func (d *fakeDevice) sendPush(body string) {
	d.mu.Lock()
	c := d.conn
	d.mu.Unlock()
	_, _ = c.WriteFrame(context.Background(), cmdStatus, append(versionHeader(), body...))
}

func (d *fakeDevice) frames() []transport.Frame {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]transport.Frame(nil), d.received...)
}

func (d *fakeDevice) waitFor(t *testing.T, cmd uint32, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := 0
		for _, f := range d.frames() {
			if f.Cmd == cmd {
				got++
			}
		}
		if got >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("device never received %d frames with code %d", n, cmd)
}
