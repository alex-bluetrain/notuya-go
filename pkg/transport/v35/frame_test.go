package v35

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv := bytes.Repeat([]byte{0xAA}, IVLen)
	want := Frame{Seqno: 42, Cmd: 0x0d, Payload: []byte(`{"protocol":5,"t":1234,"data":{"dps":{"20":true}}}`)}

	raw, err := Encode(key, iv, want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(key, raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Seqno != want.Seqno || got.Cmd != want.Cmd || !bytes.Equal(got.Payload, want.Payload) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestFrameAgainstFixture decodes the DP_QUERY request and response from a
// real capture (encrypted under the session key) and checks the
// plaintexts tinytuya logged.
func TestFrameAgainstFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "handshake", "session1.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var f struct {
		SessionKeyHex             string `json:"session_key_hex"`
		FrameDPQueryReqHex        string `json:"frame_dpquery_req_hex"`
		FrameDPQueryRespHex       string `json:"frame_dpquery_resp_hex"`
		FrameDPQueryRespPlaintext string `json:"frame_dpquery_resp_plaintext"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	key := mustHex(t, f.SessionKeyHex)

	req, err := Decode(key, mustHex(t, f.FrameDPQueryReqHex))
	if err != nil {
		t.Fatalf("Decode request: %v", err)
	}
	if string(req.Payload) != "{}" {
		t.Errorf("request plaintext = %q, want {}", req.Payload)
	}

	resp, err := Decode(key, mustHex(t, f.FrameDPQueryRespHex))
	if err != nil {
		t.Fatalf("Decode response: %v", err)
	}
	body, err := StripRetcode(resp.Payload)
	if err != nil {
		t.Fatalf("StripRetcode: %v", err)
	}
	if string(body) != f.FrameDPQueryRespPlaintext {
		t.Errorf("response plaintext\n got: %s\nwant: %s", body, f.FrameDPQueryRespPlaintext)
	}
}

func TestDecodeRejectsCorruption(t *testing.T) {
	key := []byte("0123456789abcdef")
	raw, err := Encode(key, bytes.Repeat([]byte{0xBB}, IVLen), Frame{Seqno: 1, Cmd: 0x10, Payload: []byte("{}")})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	flip := func(i int) []byte {
		b := append([]byte(nil), raw...)
		b[i] ^= 0xFF
		return b
	}
	cases := map[string]struct {
		key, raw []byte
	}{
		"truncated":           {key, raw[:10]},
		"bad prefix":          {key, flip(0)},
		"bad suffix":          {key, flip(len(raw) - 1)},
		"tampered ciphertext": {key, flip(HeaderLen + IVLen + 2)},
		"wrong key":           {[]byte("fedcba9876543210"), raw},
	}
	for name, c := range cases {
		if _, err := Decode(c.key, c.raw); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestStripRetcode(t *testing.T) {
	if got, err := StripRetcode(WithRetcode([]byte(`{"dps":{}}`))); err != nil || string(got) != `{"dps":{}}` {
		t.Errorf("zero retcode: got %q, %v", got, err)
	}
	if _, err := StripRetcode([]byte{0, 0, 0, 1, '{', '}'}); err == nil {
		t.Error("nonzero retcode: expected an error")
	}
	if _, err := StripRetcode([]byte{0, 0}); err == nil {
		t.Error("short payload: expected an error")
	}
}

func TestBodyLenRejectsOversize(t *testing.T) {
	h := make([]byte, HeaderLen)
	h[14] = 0xFF
	if _, err := BodyLen(h); err == nil {
		t.Error("expected an error for an oversized length field")
	}
}

// TestConnConcurrentWrites: writes from many goroutines must arrive as
// whole frames with distinct, increasing sequence numbers.
func TestConnConcurrentWrites(t *testing.T) {
	a, b := net.Pipe()
	key := []byte("0123456789abcdef")
	client, device := NewConn(a), NewConn(b)
	client.SetKey(key)
	device.SetKey(key)
	defer client.Close()
	defer device.Close()

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.WriteFrame(context.Background(), 0x0d, []byte("{}")); err != nil {
				t.Errorf("WriteFrame: %v", err)
			}
		}()
	}

	seen := map[uint32]bool{}
	for i := 0; i < n; i++ {
		f, err := device.ReadFrame()
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if seen[f.Seqno] {
			t.Errorf("duplicate seqno %d", f.Seqno)
		}
		seen[f.Seqno] = true
	}
	wg.Wait()
	for i := uint32(1); i <= n; i++ {
		if !seen[i] {
			t.Errorf("seqno %d missing", i)
		}
	}
}

func TestConnRefusesWriteWithoutKey(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, err := NewConn(a).WriteFrame(context.Background(), 0x0d, nil); err == nil {
		t.Error("expected an error writing before SetKey")
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decoding hex: %v", err)
	}
	return b
}
