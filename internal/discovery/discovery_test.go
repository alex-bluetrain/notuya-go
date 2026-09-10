package discovery

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/averstraeten/notuya-go/internal/protocol"
	"github.com/averstraeten/notuya-go/internal/protocol35"
)

// TestDiscoveryFrameRoundTrip is a network-independent sanity check for
// the framing this package relies on: encode a device announcement under
// the well-known discovery key exactly as a real device would, then
// confirm Scan's own decode path (protocol35.DecodeFrame + json.Unmarshal
// into Device) recovers it. This does not require live broadcast traffic
// — useful because, in this development environment, broadcast discovery
// does not work for ANY implementation (confirmed: tinytuya's own
// `python -m tinytuya scan` also finds 0 devices here, almost certainly
// due to broadcast isolation on the LAN/AP rather than a protocol bug).
func TestDiscoveryFrameRoundTrip(t *testing.T) {
	key := discoveryKey()
	want := Device{ID: "ebfake1111111111111111", IP: "192.168.1.6", Version: "3.5"}
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	iv := bytes.Repeat([]byte{0x42}, ivLen)
	frame, err := protocol35.EncodeFrame(key, iv, 1, protocol.ReqDevInfo, body)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}

	cmd, payload, err := protocol35.DecodeFrame(key, frame)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if cmd != protocol.ReqDevInfo {
		t.Errorf("cmd = %d, want %d", cmd, protocol.ReqDevInfo)
	}

	var got Device
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshalling payload: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
