package protocol35

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/averstraeten/notuya-go/internal/protocol"
)

// TestSessionOpenAgainstRealDevice is the other half of M1's "done"
// criterion: a live handshake (random nonce/IV, not the fixture's frozen
// ones) must be accepted by real hardware end-to-end. Hermetic by default;
// opt in with NOTUYA_INTEGRATION=1 plus NOTUYA_TEST_IP/NOTUYA_TEST_KEY.
func TestSessionOpenAgainstRealDevice(t *testing.T) {
	if os.Getenv("NOTUYA_INTEGRATION") != "1" {
		t.Skip("set NOTUYA_INTEGRATION=1 (and NOTUYA_TEST_IP/NOTUYA_TEST_KEY) to run against real hardware")
	}
	ip := os.Getenv("NOTUYA_TEST_IP")
	key := os.Getenv("NOTUYA_TEST_KEY")
	if ip == "" || key == "" {
		t.Fatal("NOTUYA_TEST_IP and NOTUYA_TEST_KEY must be set for the integration test")
	}

	sess := NewSession(ip, []byte(key))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sess.Open(ctx); err != nil {
		t.Fatalf("Open (handshake) against real device failed: %v", err)
	}
	defer sess.Close()

	payload, err := sess.Command(ctx, protocol.DPQueryNew, []byte("{}"), true)
	if err != nil {
		t.Fatalf("DP_QUERY_NEW against real device failed: %v", err)
	}
	t.Logf("status response: %s", payload)
	if len(payload) == 0 {
		t.Error("expected a non-empty status payload")
	}
}
