package protocol35

import (
	"context"
	"testing"
	"time"

	"github.com/averstraeten/notuya-go/internal/protocol"
)

var testLocalKey = []byte("0123456789abcdef")

func openTestSession(t *testing.T, d *fakeDevice) *Session {
	t.Helper()
	sess := NewSession(d.addr(), testLocalKey)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sess.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// TestCommandNoWaitReturnsWithoutResponse checks the fire-and-forget path
// against a device that deliberately never acks: with wait=true this would
// block until the deadline.
func TestCommandNoWaitReturnsWithoutResponse(t *testing.T) {
	d := newFakeDevice(t, testLocalKey)
	sess := openTestSession(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	payload, err := sess.Command(ctx, protocol.ControlNew, []byte(`{"dps":{"28":"x"}}`), false)
	if err != nil {
		t.Fatalf("Command(wait=false): %v", err)
	}
	if payload != nil {
		t.Errorf("payload = %v, want nil for a fire-and-forget send", payload)
	}

	frames := d.waitForFrames(1, 2*time.Second)
	if frames[0].Cmd != protocol.ControlNew {
		t.Errorf("device received cmd 0x%02x, want 0x%02x", frames[0].Cmd, protocol.ControlNew)
	}
}

// TestStreamingWritesWhileDraining is the concurrency contract the music
// mode depends on: a DrainInbound goroutine holds the read side while
// fire-and-forget writes go out on the same connection. Split deadlines
// (SetReadDeadline/SetWriteDeadline instead of SetDeadline) are what make
// this safe, and a regression would show up here as a timeout.
func TestStreamingWritesWhileDraining(t *testing.T) {
	d := newFakeDevice(t, testLocalKey)
	sess := openTestSession(t, d)

	warmCtx, cancelWarm := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelWarm()
	if _, err := sess.Command(warmCtx, protocol.DPQueryNew, []byte("{}"), true); err != nil {
		t.Fatalf("warm-up query: %v", err)
	}

	drainCtx, stopDrain := context.WithCancel(context.Background())
	drainDone := make(chan error, 1)
	go func() { drainDone <- sess.DrainInbound(drainCtx) }()

	// The device talks back while we write, so the drain goroutine is
	// actually consuming frames rather than just idling.
	for range 5 {
		d.push <- append(make([]byte, retcodeLen), []byte(`{"dps":{"20":true}}`)...)
	}

	const sends = 50
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := range sends {
		if _, err := sess.Command(ctx, protocol.ControlNew, []byte(`{"dps":{"28":"stream"}}`), false); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	frames := d.waitForFrames(1+sends, 5*time.Second)
	controls := 0
	for _, f := range frames {
		if f.Cmd == protocol.ControlNew {
			controls++
		}
	}
	if controls != sends {
		t.Errorf("device received %d CONTROL_NEW frames, want %d", controls, sends)
	}

	stopDrain()
	select {
	case err := <-drainDone:
		if err != nil {
			t.Errorf("DrainInbound: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DrainInbound did not return after its context was cancelled")
	}
}

// TestBlockingCommandWorksAfterDrainStops covers the hand-back: once the
// drain goroutine is stopped, the caller must be able to issue an ordinary
// blocking command (the final SetColour that ends a stream). If the drain
// had swallowed the response, this would time out.
func TestBlockingCommandWorksAfterDrainStops(t *testing.T) {
	d := newFakeDevice(t, testLocalKey)
	sess := openTestSession(t, d)

	drainCtx, stopDrain := context.WithCancel(context.Background())
	drainDone := make(chan error, 1)
	go func() { drainDone <- sess.DrainInbound(drainCtx) }()

	time.Sleep(50 * time.Millisecond)
	stopDrain()
	if err := <-drainDone; err != nil {
		t.Fatalf("DrainInbound: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	payload, err := sess.Command(ctx, protocol.DPQueryNew, []byte("{}"), true)
	if err != nil {
		t.Fatalf("blocking command after drain stopped: %v", err)
	}
	if len(payload) == 0 {
		t.Error("empty payload from blocking query")
	}
}

// TestDrainInboundStopsOnClosedConnection verifies a dropped connection
// surfaces as an error instead of spinning: StreamColours relies on this to
// notice a dead device.
func TestDrainInboundStopsOnClosedConnection(t *testing.T) {
	d := newFakeDevice(t, testLocalKey)
	sess := openTestSession(t, d)

	drainDone := make(chan error, 1)
	go func() { drainDone <- sess.DrainInbound(context.Background()) }()

	time.Sleep(50 * time.Millisecond)
	d.stop()

	select {
	case err := <-drainDone:
		if err == nil {
			t.Error("DrainInbound returned nil for a dropped connection, want an error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("DrainInbound did not return after the device went away")
	}
}
