package udp

import (
	"bytes"
	"encoding/hex"
	"net"
	"testing"
	"time"

	v35 "github.com/alex-bluetrain/notuya-go/pkg/transport/v35"
)

func TestKeyIsTuyaDiscoveryKey(t *testing.T) {
	// md5("yGAdlopoPVldABfn"), as used by tinytuya.
	if got := hex.EncodeToString(Key()); got != "6c1ec8e2bb9bb59ab50b0daf649b410a" {
		t.Errorf("Key() = %s", got)
	}
}

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

// TestListenerDecodesFramesAndPlainJSON sends an encrypted frame, a plain
// JSON announcement and junk to a listener; the junk must be skipped.
func TestListenerDecodesFramesAndPlainJSON(t *testing.T) {
	l, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	_ = l.SetDeadline(time.Now().Add(2 * time.Second))

	out, err := net.DialUDP("udp4", nil, l.c.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	body := []byte(`{"gwId":"x","ip":"1.2.3.4"}`)
	frame, err := v35.Encode(Key(), bytes.Repeat([]byte{1}, v35.IVLen), v35.Frame{Seqno: 1, Cmd: 0x13, Payload: body})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range [][]byte{frame, []byte("junk"), body} {
		if _, err := out.Write(d); err != nil {
			t.Fatal(err)
		}
	}

	p, err := l.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !p.Encrypted || p.Cmd != 0x13 || !bytes.Equal(p.Payload, body) {
		t.Errorf("encrypted packet = %+v", p)
	}
	p, err = l.Read()
	if err != nil {
		t.Fatal(err)
	}
	if p.Encrypted || !bytes.Equal(p.Payload, body) {
		t.Errorf("plain packet = %+v", p)
	}
}
