package bulb

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/averstraeten/notuya-go/internal/device"
	"github.com/averstraeten/notuya-go/internal/protocol"
)

// streamCall is one recorded Command invocation.
type streamCall struct {
	cmd     uint32
	payload []byte
	wait    bool
}

// mockStreamSession is a protocol.StreamSession that records every Command
// and blocks in DrainInbound until its context is cancelled, the way a real
// session does on an idle connection.
type mockStreamSession struct {
	mu    sync.Mutex
	calls []streamCall

	drainStarted  chan struct{}
	drainReturned chan struct{}
	queryResp     []byte
}

func newMockStreamSession() *mockStreamSession {
	return &mockStreamSession{
		drainStarted:  make(chan struct{}, 1),
		drainReturned: make(chan struct{}, 1),
		queryResp:     []byte(`{"dps":{"20":true}}`),
	}
}

func (m *mockStreamSession) Open(context.Context) error { return nil }
func (m *mockStreamSession) Close() error               { return nil }

func (m *mockStreamSession) Command(_ context.Context, cmd uint32, payload []byte, wait bool) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, streamCall{cmd: cmd, payload: append([]byte(nil), payload...), wait: wait})
	if cmd == protocol.DPQueryNew {
		return m.queryResp, nil
	}
	return nil, nil
}

func (m *mockStreamSession) DrainInbound(ctx context.Context) error {
	select {
	case m.drainStarted <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case m.drainReturned <- struct{}{}:
	default:
	}
	return nil
}

func (m *mockStreamSession) recorded() []streamCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]streamCall(nil), m.calls...)
}

// nonStreamSession is a protocol.Session with no streaming support.
type nonStreamSession struct{}

func (nonStreamSession) Open(context.Context) error { return nil }
func (nonStreamSession) Close() error               { return nil }
func (nonStreamSession) Command(context.Context, uint32, []byte, bool) ([]byte, error) {
	return []byte(`{"dps":{"20":true}}`), nil
}

// musicDPSFromPayload extracts data.dps from a recorded CONTROL_NEW payload,
// skipping the 15-byte version header.
func musicDPSFromPayload(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	if len(payload) < 15 {
		t.Fatalf("payload too short (%d bytes) for version header", len(payload))
	}
	var body struct {
		Data struct {
			DPS map[string]any `json:"dps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload[15:], &body); err != nil {
		t.Fatalf("parsing control payload: %v", err)
	}
	return body.Data.DPS
}

// musicColourHex reconstructs the expected DP 28 value for assertions,
// mirroring device.SetMusicColour's encoding.
func musicColourHexRef(t *testing.T, transition int, r, g, b uint8) string {
	t.Helper()
	mock := newMockStreamSession()
	d := device.NewDevice(mock, "ref")
	if err := d.SetMusicColour(context.Background(), transition, r, g, b, false); err != nil {
		t.Fatalf("SetMusicColour: %v", err)
	}
	dps := musicDPSFromPayload(t, mock.recorded()[0].payload)
	s, ok := dps[device.DPMusic].(string)
	if !ok {
		t.Fatalf("reference DP %s missing or not a string: %v", device.DPMusic, dps)
	}
	return s
}

// TestStreamColoursHonoursZeroTransition guards the reason Transition is a
// pointer: zero is a meaningful setting (instant, no fade), so it must reach
// the wire instead of being read as "unset" and replaced by the default.
func TestStreamColoursHonoursZeroTransition(t *testing.T) {
	mock := newMockStreamSession()
	b := NewBulb(mock, "test")

	colours := make(chan device.RGB, 1)
	colours <- device.RGB{R: 255}
	close(colours)

	err := b.StreamColours(context.Background(), colours, StreamOptions{
		Interval:   time.Millisecond,
		Transition: Transition(0),
	})
	if err != nil {
		t.Fatalf("StreamColours: %v", err)
	}

	calls := mock.recorded()
	last := calls[len(calls)-1]
	dps := musicDPSFromPayload(t, last.payload)
	got := dps[device.DPMusic]
	want := musicColourHexRef(t, 0, 255, 0, 0)
	if got != want {
		t.Errorf("DP %s = %q, want %q (transition 0 must survive)", device.DPMusic, got, want)
	}
}

// TestStreamColoursPayloadShape checks the on-the-wire shape of a stream:
// a blocking warm-up query first, then DP 28 writes that must not wait.
func TestStreamColoursPayloadShape(t *testing.T) {
	mock := newMockStreamSession()
	b := NewBulb(mock, "test")

	colours := make(chan device.RGB, 1)
	colours <- device.RGB{R: 255}
	close(colours)

	err := b.StreamColours(context.Background(), colours, StreamOptions{
		Interval:   time.Millisecond,
		Transition: Transition(1),
	})
	if err != nil {
		t.Fatalf("StreamColours: %v", err)
	}

	calls := mock.recorded()
	if len(calls) < 2 {
		t.Fatalf("recorded %d calls, want at least 2 (warm-up + colour)", len(calls))
	}

	if calls[0].cmd != protocol.DPQueryNew {
		t.Errorf("first call cmd = 0x%02x, want 0x%02x (DP_QUERY_NEW warm-up)", calls[0].cmd, protocol.DPQueryNew)
	}
	if !calls[0].wait {
		t.Error("warm-up query wait = false, want true")
	}

	// No DP 21 write: entering music mode via DP 28 is what avoids the
	// start-of-drag flicker.
	for i, c := range calls[1:] {
		if c.cmd != protocol.ControlNew {
			continue
		}
		dps := musicDPSFromPayload(t, c.payload)
		if _, ok := dps[device.DPMode]; ok {
			t.Errorf("call %d writes DP %s (mode); music mode must be entered via DP %s only", i+1, device.DPMode, device.DPMusic)
		}
	}

	colourCall := calls[len(calls)-1]
	if colourCall.cmd != protocol.ControlNew {
		t.Fatalf("colour call cmd = 0x%02x, want 0x%02x (CONTROL_NEW)", colourCall.cmd, protocol.ControlNew)
	}
	if colourCall.wait {
		t.Error("colour send wait = true, want false (the bulb stops acking mid-stream)")
	}

	dps := musicDPSFromPayload(t, colourCall.payload)
	got, ok := dps[device.DPMusic]
	if !ok {
		t.Fatalf("payload dps = %v, want a DP %s entry", dps, device.DPMusic)
	}
	want := musicColourHexRef(t, 1, 255, 0, 0)
	if got != want {
		t.Errorf("DP %s = %v, want %q", device.DPMusic, got, want)
	}
}

// TestStreamColoursCoalesces verifies the newest-wins throttle: colours
// arriving faster than the interval must not queue up behind each other.
func TestStreamColoursCoalesces(t *testing.T) {
	mock := newMockStreamSession()
	b := NewBulb(mock, "test")

	colours := make(chan device.RGB)
	done := make(chan error, 1)
	go func() {
		done <- b.StreamColours(context.Background(), colours, StreamOptions{
			Interval:   50 * time.Millisecond,
			Transition: Transition(1),
		})
	}()

	// Push a burst well inside one interval; only the last should survive.
	for i := range 10 {
		colours <- device.RGB{R: uint8(i)}
	}
	close(colours)

	if err := <-done; err != nil {
		t.Fatalf("StreamColours: %v", err)
	}

	var colourCalls []streamCall
	for _, c := range mock.recorded() {
		if c.cmd == protocol.ControlNew {
			colourCalls = append(colourCalls, c)
		}
	}
	if len(colourCalls) == 0 {
		t.Fatal("no colour was sent")
	}
	if len(colourCalls) >= 10 {
		t.Errorf("sent %d colours for a 10-colour burst inside one interval; expected coalescing", len(colourCalls))
	}

	// Whatever the throttle dropped, the final colour must be the newest.
	dps := musicDPSFromPayload(t, colourCalls[len(colourCalls)-1].payload)
	want := musicColourHexRef(t, 1, 9, 0, 0)
	if got := dps[device.DPMusic]; got != want {
		t.Errorf("last colour = %v, want %q (the newest colour of the burst)", got, want)
	}
}

// TestStreamColoursStopsDrainOnReturn guards the session hand-back: if the
// drain goroutine outlived the stream it would steal responses from the
// blocking commands a caller makes next (e.g. SetColour to persist).
func TestStreamColoursStopsDrainOnReturn(t *testing.T) {
	mock := newMockStreamSession()
	b := NewBulb(mock, "test")

	colours := make(chan device.RGB)
	close(colours)

	if err := b.StreamColours(context.Background(), colours, StreamOptions{}); err != nil {
		t.Fatalf("StreamColours: %v", err)
	}

	select {
	case <-mock.drainStarted:
	default:
		t.Fatal("drain goroutine never started")
	}
	select {
	case <-mock.drainReturned:
	case <-time.After(time.Second):
		t.Fatal("drain goroutine still running after StreamColours returned")
	}
}

// TestStreamColoursRequiresStreamSession checks the fallback path for a
// protocol version that has no streaming support.
func TestStreamColoursRequiresStreamSession(t *testing.T) {
	b := NewBulb(nonStreamSession{}, "test")
	colours := make(chan device.RGB)
	close(colours)

	err := b.StreamColours(context.Background(), colours, StreamOptions{})
	if err == nil {
		t.Fatal("expected an error for a session without streaming support, got nil")
	}
}
