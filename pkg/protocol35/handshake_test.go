package protocol35

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alex-bluetrain/notuya-go/pkg/protocol"
)

// fixture mirrors testdata/handshake/session1.json: a real capture against
// actual hardware, used to validate this package byte-for-byte instead of
// only against itself.
type fixture struct {
	LocalKey                  string `json:"local_key"`
	LocalNonce                string `json:"local_nonce"`
	RemoteNonce               string `json:"remote_nonce"`
	SessionKeyHex             string `json:"session_key_hex"`
	FrameStep1Hex             string `json:"frame_step1_hex"`
	FrameStep2Hex             string `json:"frame_step2_hex"`
	FrameStep3Hex             string `json:"frame_step3_hex"`
	FrameDPQueryReqHex        string `json:"frame_dpquery_req_hex"`
	FrameDPQueryRespHex       string `json:"frame_dpquery_resp_hex"`
	FrameDPQueryRespPlaintext string `json:"frame_dpquery_resp_plaintext"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "handshake", "session1.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var f fixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	return f
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decoding hex: %v", err)
	}
	return b
}

// TestHandshakeAgainstRealCapture is the M1 "done" criterion: our encode of
// step1/step3 must be byte-identical to what tinytuya produced on the wire
// against real hardware (same local_key/nonce/IV), and our decode of the
// device's real step2 response must authenticate and yield the exact same
// remote_nonce that a from-scratch reference implementation (and tinytuya's
// own debug log) derived — proving the frame/AAD/IV layout is correct, not
// just self-consistent.
func TestHandshakeAgainstRealCapture(t *testing.T) {
	f := loadFixture(t)
	localKey := []byte(f.LocalKey)
	localNonce := []byte(f.LocalNonce)
	fixedIV := localNonce[:ivLen] // this capture ran with tinytuya's debug-mode fixed IV

	t.Run("step1 matches captured wire bytes", func(t *testing.T) {
		got, err := buildStep1(localKey, localNonce, fixedIV, 1)
		if err != nil {
			t.Fatalf("buildStep1: %v", err)
		}
		want := mustHex(t, f.FrameStep1Hex)
		if !bytes.Equal(got, want) {
			t.Errorf("step1 mismatch\n got: %x\nwant: %x", got, want)
		}
	})

	remoteNonce, err := parseStep2(localKey, localNonce, mustHex(t, f.FrameStep2Hex))
	t.Run("step2 decrypts and authenticates against the real device response", func(t *testing.T) {
		if err != nil {
			t.Fatalf("parseStep2: %v", err)
		}
		want := []byte(f.RemoteNonce)
		if !bytes.Equal(remoteNonce, want) {
			t.Errorf("remote_nonce mismatch\n got: %s\nwant: %s", remoteNonce, want)
		}
	})

	t.Run("step2 rejects a tampered HMAC", func(t *testing.T) {
		tampered := append([]byte(nil), localKey...)
		tampered[0] ^= 0xff // flip a byte -> HMAC expectation no longer matches
		if _, err := parseStep2(tampered, localNonce, mustHex(t, f.FrameStep2Hex)); err == nil {
			t.Error("expected HMAC verification to fail with the wrong local_key, got nil error")
		}
	})

	t.Run("step3 matches captured wire bytes", func(t *testing.T) {
		got, err := buildStep3(localKey, remoteNonce, fixedIV, 2)
		if err != nil {
			t.Fatalf("buildStep3: %v", err)
		}
		want := mustHex(t, f.FrameStep3Hex)
		if !bytes.Equal(got, want) {
			t.Errorf("step3 mismatch\n got: %x\nwant: %x", got, want)
		}
	})

	t.Run("derived session key matches tinytuya's own logged value", func(t *testing.T) {
		got, err := deriveSessionKey(localKey, localNonce, remoteNonce)
		if err != nil {
			t.Fatalf("deriveSessionKey: %v", err)
		}
		want := mustHex(t, f.SessionKeyHex)
		if !bytes.Equal(got, want) {
			t.Errorf("session key mismatch\n got: %x\nwant: %x", got, want)
		}
	})

	t.Run("session key decrypts a real post-handshake device response", func(t *testing.T) {
		sessionKey := mustHex(t, f.SessionKeyHex)
		df, err := decodeFrame(sessionKey, mustHex(t, f.FrameDPQueryRespHex))
		if err != nil {
			t.Fatalf("decodeFrame: %v", err)
		}
		if df.Cmd != protocol.DPQueryNew {
			t.Errorf("cmd = %d, want %d (DP_QUERY_NEW)", df.Cmd, protocol.DPQueryNew)
		}
		payload, err := StripRetcode(df.Payload)
		if err != nil {
			t.Fatalf("StripRetcode: %v", err)
		}
		if string(payload) != f.FrameDPQueryRespPlaintext {
			t.Errorf("payload mismatch\n got: %s\nwant: %s", payload, f.FrameDPQueryRespPlaintext)
		}
	})
}

func TestGenerateNonceIsRandomAndCorrectLength(t *testing.T) {
	a, err := generateNonce()
	if err != nil {
		t.Fatalf("generateNonce: %v", err)
	}
	b, err := generateNonce()
	if err != nil {
		t.Fatalf("generateNonce: %v", err)
	}
	if len(a) != nonceLen || len(b) != nonceLen {
		t.Fatalf("nonce length = %d/%d, want %d", len(a), len(b), nonceLen)
	}
	if bytes.Equal(a, b) {
		t.Error("two generated nonces were identical — rand.Read is not producing entropy")
	}
}
