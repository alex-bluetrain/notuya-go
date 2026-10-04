package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/alex-bluetrain/notuya-go/pkg/transport/udp"
	v35 "github.com/alex-bluetrain/notuya-go/pkg/transport/v35"
)

// TestListenDecodesAnnouncements sends a device announcement to a
// listener exactly as a real device would — a 6699 frame under the
// well-known discovery key — and checks Listen's path recovers it.
func TestListenDecodesAnnouncements(t *testing.T) {
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	probe.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	devices, err := listen(ctx, port)
	if err != nil {
		t.Fatal(err)
	}

	want := Device{ID: "ebfake1111111111111111", IP: "192.168.1.6", Version: "3.5"}
	body, _ := json.Marshal(want)
	frame, err := v35.Encode(udp.Key(), bytes.Repeat([]byte{0x42}, v35.IVLen), v35.Frame{Seqno: 1, Cmd: 0x13, Payload: body})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write(frame); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-devices:
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	case <-ctx.Done():
		t.Fatal("no announcement received")
	}
	cancel()
	for range devices {
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
