package protocol35

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/averstraeten/notuya-go/internal/bulb"
	"github.com/averstraeten/notuya-go/internal/device"
	"github.com/averstraeten/notuya-go/internal/protocol"
)

// TestStreamColoursEndToEnd runs the real device.StreamColours loop over a
// real TCP session against the fake device. The unit tests in the device
// package drive a mock, so this is what proves the two halves fit: a real
// handshake, real 6699 frames, and the drain goroutine racing the sends on
// one socket.
//
// It lives here rather than in the device package because the fake device
// is a protocol35 test fixture; the import direction is safe, since the
// device package does not depend on protocol35.
func TestStreamColoursEndToEnd(t *testing.T) {
	d := newFakeDevice(t, testLocalKey)
	sess := openTestSession(t, d)

	b := bulb.NewBulb(sess, "e2e-test-bulb")

	colours := make(chan device.RGB)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		done <- b.StreamColours(ctx, colours, bulb.StreamOptions{
			Interval:   5 * time.Millisecond,
			Transition: bulb.Transition(1),
		})
	}()

	// Feed colours slower than the interval so each one gets its own send,
	// making the assertion on the final colour deterministic.
	sequence := []device.RGB{{R: 255}, {G: 255}, {B: 255}}
	for _, c := range sequence {
		colours <- c
		time.Sleep(20 * time.Millisecond)
	}
	close(colours)

	if err := <-done; err != nil {
		t.Fatalf("StreamColours: %v", err)
	}

	frames := d.frames()
	if len(frames) < 2 {
		t.Fatalf("device received %d frames, want the warm-up query plus colours", len(frames))
	}
	if frames[0].Cmd != protocol.DPQueryNew {
		t.Errorf("first frame cmd = 0x%02x, want 0x%02x (warm-up)", frames[0].Cmd, protocol.DPQueryNew)
	}

	var colourValues []string
	for _, f := range frames[1:] {
		if f.Cmd != protocol.ControlNew {
			continue
		}
		// CONTROL_NEW payloads carry the 15-byte "3.5" version header.
		if len(f.Payload) < 15 {
			t.Fatalf("CONTROL_NEW payload too short (%d bytes)", len(f.Payload))
		}
		var body struct {
			Data struct {
				DPS map[string]string `json:"dps"`
			} `json:"data"`
		}
		if err := json.Unmarshal(f.Payload[15:], &body); err != nil {
			t.Fatalf("parsing streamed payload: %v", err)
		}
		v, ok := body.Data.DPS["28"]
		if !ok {
			t.Fatalf("streamed frame has dps %v, want a DP 28 entry", body.Data.DPS)
		}
		colourValues = append(colourValues, v)
	}

	if len(colourValues) == 0 {
		t.Fatal("no colour reached the device")
	}
	// Blue is the last colour fed in, so it must be the last one on the
	// wire regardless of how the throttle coalesced the earlier ones.
	const wantBlue = "1" + "00f003e803e8" + "00000000"
	if got := colourValues[len(colourValues)-1]; got != wantBlue {
		t.Errorf("last streamed DP 28 = %q, want %q (blue)", got, wantBlue)
	}
}
