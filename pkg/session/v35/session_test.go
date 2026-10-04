package v35

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func open(t *testing.T, d *fakeDevice, opts Options) *Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, err := Open(ctx, d.addr(), testKey, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	<-d.ready
	return s
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestQueryReturnsStatusJSON(t *testing.T) {
	s := open(t, newFakeDevice(t), Options{})
	body, err := s.Query(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"dps":{"20":true,"22":500}}` {
		t.Errorf("body = %s", body)
	}
}

func TestControlCarriesVersionHeaderAndWaits(t *testing.T) {
	d := newFakeDevice(t)
	s := open(t, d, Options{})
	if err := s.Control(ctx(t), []byte(`{"data":{"dps":{"20":false}}}`), true); err != nil {
		t.Fatal(err)
	}
	for _, f := range d.frames() {
		if f.Cmd == cmdControl && !strings.HasPrefix(string(f.Payload), "3.5\x00") {
			t.Errorf("control payload lacks version header: %q", f.Payload)
		}
	}
	select {
	case p := <-s.Pushes():
		if !strings.Contains(string(p.Body), `"20":false`) {
			t.Errorf("push = %s", p.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("no status push after control")
	}
}

// The case the old read-ownership workaround existed for: many callers
// at once, replies and pushes interleaved on one connection.
func TestConcurrentQueryAndControl(t *testing.T) {
	s := open(t, newFakeDevice(t), Options{})
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			body, err := s.Query(ctx(t))
			if err == nil && !strings.Contains(string(body), `"22":500`) {
				err = errors.New("query got " + string(body))
			}
			errs <- err
		}()
		go func() {
			defer wg.Done()
			errs <- s.Control(ctx(t), []byte(`{"data":{"dps":{"22":100}}}`), true)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

// While streaming the bulb stops acknowledging, but it can still push.
func TestPushArrivesDuringStream(t *testing.T) {
	d := newFakeDevice(t)
	d.silentControl = true
	s := open(t, d, Options{})
	for i := 0; i < 50; i++ {
		if err := s.Control(ctx(t), []byte(`{"data":{"dps":{"28":"0000003e803e800000000"}}}`), false); err != nil {
			t.Fatal(err)
		}
	}
	d.waitFor(t, cmdControl, 50)
	d.sendPush(`{"dps":{"20":false}}`)
	select {
	case p := <-s.Pushes():
		if string(p.Body) != `{"dps":{"20":false}}` {
			t.Errorf("push = %s", p.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("push lost while streaming")
	}
}

func TestRefreshReportsOnPushes(t *testing.T) {
	d := newFakeDevice(t)
	s := open(t, d, Options{})
	if err := s.Refresh(ctx(t), []int{22}); err != nil {
		t.Fatal(err)
	}
	d.waitFor(t, cmdRefresh, 1)
	if got := string(d.frames()[0].Payload); got != `{"dpId":[22]}` {
		t.Errorf("refresh body = %s", got)
	}
	select {
	case p := <-s.Pushes():
		if string(p.Body) != `{"dps":{"22":500}}` {
			t.Errorf("push = %s", p.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("no push after refresh")
	}
}

func TestHeartbeat(t *testing.T) {
	d := newFakeDevice(t)
	s := open(t, d, Options{HeartbeatInterval: 40 * time.Millisecond})
	if err := s.Heartbeat(ctx(t), true); err != nil {
		t.Fatal(err)
	}
	d.waitFor(t, cmdHeartbeat, 3) // the explicit one plus idle ones
}

func TestPushesNeverStallTheConnection(t *testing.T) {
	d := newFakeDevice(t)
	s := open(t, d, Options{})
	for i := 0; i < pushBuffer*3; i++ {
		d.sendPush(`{"dps":{"20":true}}`)
	}
	if _, err := s.Query(ctx(t)); err != nil {
		t.Fatalf("query behind unread pushes: %v", err)
	}
}

func TestDeviceGoingAwayEndsTheSession(t *testing.T) {
	d := newFakeDevice(t)
	d.silentControl = true
	s := open(t, d, Options{})
	waiting := make(chan error, 1)
	go func() { waiting <- s.Control(context.Background(), []byte(`{}`), true) }()
	d.waitFor(t, cmdControl, 1)
	d.mu.Lock()
	d.conn.Close()
	d.mu.Unlock()

	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("session did not notice the device leaving")
	}
	if err := <-waiting; !errors.Is(err, ErrClosed) {
		t.Errorf("pending request got %v, want ErrClosed", err)
	}
	if s.Err() == nil {
		t.Error("Err() is nil after a broken link")
	}
	if _, ok := <-s.Pushes(); ok {
		t.Error("Pushes not closed")
	}
	if _, err := s.Query(ctx(t)); !errors.Is(err, ErrClosed) {
		t.Errorf("query after end: %v", err)
	}
}

func TestCloseIsCleanAndIdempotent(t *testing.T) {
	s := open(t, newFakeDevice(t), Options{})
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if s.Err() != nil {
		t.Errorf("Err after plain Close = %v", s.Err())
	}
}

func TestWrongKeyFailsHandshake(t *testing.T) {
	d := newFakeDevice(t)
	t.Cleanup(func() { d.ln.Close() })
	c, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Open(c, d.addr(), []byte("ffffffffffffffff"), Options{})
	if err == nil {
		t.Fatal("Open succeeded with the wrong key")
	}
}
