package v35

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	transport "github.com/alex-bluetrain/notuya-go/pkg/transport/v35"
)

// fixture mirrors testdata/handshake/session1.json, produced by an
// independent implementation, so these tests check against more than
// this package's own output.
type fixture struct {
	LocalKey      string `json:"local_key"`
	LocalNonce    string `json:"local_nonce"`
	RemoteNonce   string `json:"remote_nonce"`
	SessionKeyHex string `json:"session_key_hex"`
	FrameStep1Hex string `json:"frame_step1_hex"`
	FrameStep2Hex string `json:"frame_step2_hex"`
	FrameStep3Hex string `json:"frame_step3_hex"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "handshake", "session1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHandshakeAgainstFixture(t *testing.T) {
	f := loadFixture(t)
	localKey := []byte(f.LocalKey)
	localNonce := []byte(f.LocalNonce)
	iv := localNonce[:transport.IVLen] // the capture used tinytuya's debug-mode fixed IV

	encode := func(seqno, cmd uint32, payload []byte) []byte {
		raw, err := transport.Encode(localKey, iv, transport.Frame{Seqno: seqno, Cmd: cmd, Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	if got := encode(1, cmdSessKeyNegStart, localNonce); !bytes.Equal(got, mustHex(t, f.FrameStep1Hex)) {
		t.Errorf("step 1 differs from capture\n got %x\nwant %x", got, mustHex(t, f.FrameStep1Hex))
	}

	step2, err := transport.Decode(localKey, mustHex(t, f.FrameStep2Hex))
	if err != nil {
		t.Fatal(err)
	}
	remote, err := parseStep2(localKey, localNonce, step2)
	if err != nil {
		t.Fatalf("parseStep2: %v", err)
	}
	if string(remote) != f.RemoteNonce {
		t.Errorf("remote nonce = %q, want %q", remote, f.RemoteNonce)
	}

	wrong := append([]byte(nil), localKey...)
	wrong[0] ^= 0xff
	if _, err := parseStep2(wrong, localNonce, step2); err == nil {
		t.Error("parseStep2 accepted an HMAC made with another key")
	}

	if got := encode(2, cmdSessKeyNegFinish, step3Payload(localKey, remote)); !bytes.Equal(got, mustHex(t, f.FrameStep3Hex)) {
		t.Errorf("step 3 differs from capture")
	}

	key, err := deriveSessionKey(localKey, localNonce, remote)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, mustHex(t, f.SessionKeyHex)) {
		t.Errorf("session key = %x, want %s", key, f.SessionKeyHex)
	}
}

func TestNonceIsRandom(t *testing.T) {
	a, _ := newNonce()
	b, _ := newNonce()
	if len(a) != nonceLen || bytes.Equal(a, b) {
		t.Errorf("nonces %x / %x", a, b)
	}
}

func TestJSONObject(t *testing.T) {
	for in, want := range map[string]string{
		`{"dps":{"20":true}}`:              `{"dps":{"20":true}}`,
		"3.5\x00\x00\x00{\"a\":1}\x00\x00": `{"a":1}`,
		`{"s":"}{"}trailing`:               `{"s":"}{"}`,
		`{"s":"a\"}"}`:                     `{"s":"a\"}"}`,
		"no object":                        ``,
	} {
		if got := string(jsonObject([]byte(in))); got != want {
			t.Errorf("jsonObject(%q) = %q, want %q", in, got, want)
		}
	}
}
