package bulb

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alex-bluetrain/notuya-go/pkg/dp"
	v35 "github.com/alex-bluetrain/notuya-go/pkg/session/v35"
)

// TestBulbAgainstRealDevice exercises the full business control surface
// (Status, TurnOn, SetColour, TurnOff) against real
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sess, err := v35.Open(ctx, ip, []byte(key), v35.Options{})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer sess.Close()
		b := New(sess, "integration-test-bulb")
		if err := fn(ctx, b); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("Status", func(t *testing.T) {
		run(t, func(ctx context.Context, b *Bulb) error { _, err := b.Status(ctx); return err })
	})
	t.Run("TurnOn", func(t *testing.T) {
		run(t, func(ctx context.Context, b *Bulb) error { return b.TurnOn(ctx) })
	})
	t.Run("SetColour red", func(t *testing.T) {
		run(t, func(ctx context.Context, b *Bulb) error { return b.SetColour(ctx, dp.HSV{H: 0, S: 1000, V: 1000}) })
	})
	t.Run("SetColour green", func(t *testing.T) {
		run(t, func(ctx context.Context, b *Bulb) error { return b.SetColour(ctx, dp.HSV{H: 120, S: 1000, V: 1000}) })
	})
	t.Run("TurnOff", func(t *testing.T) {
		run(t, func(ctx context.Context, b *Bulb) error { return b.TurnOff(ctx) })
	})
}
