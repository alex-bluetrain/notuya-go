package bulb

import (
	"context"
	"testing"

	"github.com/alex-bluetrain/notuya-go/pkg/protocol"
)

// TestRawExposesUnderlyingDevice verifies Raw() returns the Bulb's own
// low-level Device, wired to the same session, so a caller can drive a DP
// the business layer does not cover. Raw() has no CLI consumer yet; this
// test is what keeps it exercised as intentional public surface.
func TestRawExposesUnderlyingDevice(t *testing.T) {
	sess := newMockStreamSession()
	b := NewBulb(sess, "test")

	dev := b.Raw()
	if dev == nil {
		t.Fatal("Raw() returned nil")
	}
	if dev.Session() != sess {
		t.Fatal("Raw().Session() is not the session passed to NewBulb")
	}

	// A raw DP write must reach the underlying session as a CONTROL_NEW.
	if err := dev.SetValue(context.Background(), "23", 500, true); err != nil {
		t.Fatalf("Raw().SetValue: %v", err)
	}

	var control *streamCall
	for _, c := range sess.recorded() {
		if c.cmd == protocol.ControlNew {
			control = &c
			break
		}
	}
	if control == nil {
		t.Fatal("Raw().SetValue did not emit a CONTROL_NEW command")
	}

	dps := musicDPSFromPayload(t, control.payload)
	if _, ok := dps["23"]; !ok {
		t.Fatalf("CONTROL_NEW payload missing DP 23: %v", dps)
	}
}
