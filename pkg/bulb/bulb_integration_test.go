package bulb

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/averstraeten/notuya-go/pkg/protocol35"
)

// TestBulbAgainstRealDevice exercises the full business control surface
// (Status, TurnOn, SetColour, SetBrightnessPercent, TurnOff) against real
// hardware. Hermetic by default; opt in with NOTUYA_INTEGRATION=1 plus
// NOTUYA_TEST_IP/NOTUYA_TEST_KEY.
func TestBulbAgainstRealDevice(t *testing.T) {
	if os.Getenv("NOTUYA_INTEGRATION") != "1" {
		t.Skip("set NOTUYA_INTEGRATION=1 (and NOTUYA_TEST_IP/NOTUYA_TEST_KEY) to run against real hardware")
	}
	ip := os.Getenv("NOTUYA_TEST_IP")
	key := os.Getenv("NOTUYA_TEST_KEY")
	if ip == "" || key == "" {
		t.Fatal("NOTUYA_TEST_IP and NOTUYA_TEST_KEY must be set for the integration test")
	}

	run := func(t *testing.T, fn func(ctx context.Context, b *Bulb) error) {
		t.Helper()
		sess := protocol35.NewSession(ip, []byte(key))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sess.Open(ctx); err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer sess.Close()
		b := NewBulb(sess, "integration-test-bulb")
		if err := fn(ctx, b); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("Status", func(t *testing.T) {
		run(t, func(ctx context.Context, b *Bulb) error { return b.Status(ctx) })
	})
	t.Run("TurnOn", func(t *testing.T) {
		run(t, func(ctx context.Context, b *Bulb) error { return b.TurnOn(ctx) })
	})
	t.Run("SetColour red", func(t *testing.T) {
		run(t, func(ctx context.Context, b *Bulb) error { return b.SetColour(ctx, 255, 0, 0) })
	})
	t.Run("SetBrightnessPercent 30", func(t *testing.T) {
		run(t, func(ctx context.Context, b *Bulb) error { return b.SetBrightnessPercent(ctx, 30) })
	})
	t.Run("SetColour green", func(t *testing.T) {
		run(t, func(ctx context.Context, b *Bulb) error { return b.SetColour(ctx, 0, 255, 0) })
	})
	t.Run("TurnOff", func(t *testing.T) {
		run(t, func(ctx context.Context, b *Bulb) error { return b.TurnOff(ctx) })
	})
}
