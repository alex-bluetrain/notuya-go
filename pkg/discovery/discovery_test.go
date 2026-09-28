package discovery

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/alex-bluetrain/notuya-go/pkg/protocol"
	"github.com/alex-bluetrain/notuya-go/pkg/protocol35"
)

// TestDiscoveryFrameRoundTrip is a network-independent sanity check for
// the framing this package relies on: encode a device announcement under
// the well-known discovery key exactly as a real device would, then
// confirm Scan's own decode path (protocol35.DecodeFrame + json.Unmarshal
// into Device) recovers it.
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

// TestSolicitedReplyIsDecoded covers the bug that made scanning look like
// a network limitation for far too long. Replies to a solicitation carry a
// 4-byte retcode ahead of the JSON, which Scan did not strip, so every
// reply failed to unmarshal and was dropped without a trace. Devices were
// answering the whole time.
func TestSolicitedReplyIsDecoded(t *testing.T) {
	want := Device{ID: "ebfake1111111111111111", IP: "192.168.1.6", Version: "3.5"}
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	// A reply as a real device sends it: retcode 0, then the JSON.
	withRetcode := append([]byte{0, 0, 0, 0}, body...)

	got, ok := parseAnnouncement(withRetcode)
	if !ok {
		t.Fatal("a reply carrying a retcode was rejected")
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestUnsolicitedAnnouncementIsDecoded guards the other half: periodic
// announcements on UDP/6667 have no retcode, so stripping four bytes
// unconditionally would corrupt them.
func TestUnsolicitedAnnouncementIsDecoded(t *testing.T) {
	want := Device{ID: "eb9194738bf46de9e7qxc5", IP: "192.168.1.5", Version: "3.5"}
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := parseAnnouncement(body)
	if !ok {
		t.Fatal("a bare announcement was rejected")
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestOwnSolicitationIsIgnored: the request is broadcast on the same port
// the replies arrive on, so it comes straight back. It has no gwId and
// must not be reported as a device.
func TestOwnSolicitationIsIgnored(t *testing.T) {
	own, err := json.Marshal(map[string]string{"from": "app", "ip": "192.168.1.3"})
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := parseAnnouncement(own); ok {
		t.Errorf("our own request was reported as device %+v", d)
	}
}

// TestBroadcastTargetsAreUnique: two interfaces on one subnet share a
// directed broadcast address, and sending it twice only duplicates the
// copies of our own request that come back.
func TestBroadcastTargetsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, ip := range broadcastTargets() {
		if seen[ip.String()] {
			t.Errorf("duplicate broadcast target %s", ip)
		}
		seen[ip.String()] = true
	}
	if !seen["255.255.255.255"] {
		t.Error("the limited broadcast address is missing")
	}
}
