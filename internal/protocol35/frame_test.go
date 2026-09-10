package protocol35

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestFrameRoundTrip encodes a frame with a known key/IV/seqno/cmd/plaintext,
// then decodes it and verifies every field survives the trip unchanged.
func TestFrameRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef") // 16 bytes
	iv := bytes.Repeat([]byte{0xAA}, ivLen)
	var seqno uint32 = 42
	var cmd uint32 = 0x0d
	plaintext := []byte(`{"protocol":5,"t":1234,"data":{"dps":{"20":true}}}`)

	frame, err := encodeRequest(key, iv, seqno, cmd, plaintext)
	if err != nil {
		t.Fatalf("encodeRequest: %v", err)
	}

	df, err := decodeFrame(key, frame)
	if err != nil {
		t.Fatalf("decodeFrame: %v", err)
	}
	if df.Seqno != seqno {
		t.Errorf("seqno = %d, want %d", df.Seqno, seqno)
	}
	if df.Cmd != cmd {
		t.Errorf("cmd = %d, want %d", df.Cmd, cmd)
	}
	if !bytes.Equal(df.Payload, plaintext) {
		t.Errorf("payload mismatch\n got: %s\nwant: %s", df.Payload, plaintext)
	}
}

// TestFrameAgainstFixture decodes the DP_QUERY request from the real capture
// fixture (encrypted under local_key during the handshake phase, seqno=3)
// and verifies that its plaintext is the expected empty JSON query.
func TestFrameAgainstFixture(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "handshake", "session1.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var f struct {
		LocalKey           string `json:"local_key"`
		SessionKeyHex      string `json:"session_key_hex"`
		FrameDPQueryReqHex string `json:"frame_dpquery_req_hex"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}

	// The DP query request is encrypted under the session key (post-handshake).
	sessionKey, err := hex.DecodeString(f.SessionKeyHex)
	if err != nil {
		t.Fatalf("decoding session key hex: %v", err)
	}
	frame, err := hex.DecodeString(f.FrameDPQueryReqHex)
	if err != nil {
		t.Fatalf("decoding frame hex: %v", err)
	}

	df, err := decodeFrame(sessionKey, frame)
	if err != nil {
		t.Fatalf("decodeFrame: %v", err)
	}
	if string(df.Payload) != "{}" {
		t.Errorf("plaintext = %q, want %q", df.Payload, "{}")
	}
}

// TestDecodeFrameRejectsCorruption verifies that decodeFrame rejects
// malformed or tampered frames.
func TestDecodeFrameRejectsCorruption(t *testing.T) {
	// Build a valid frame first.
	key := []byte("0123456789abcdef")
	iv := bytes.Repeat([]byte{0xBB}, ivLen)
	frame, err := encodeRequest(key, iv, 1, 0x10, []byte("{}"))
	if err != nil {
		t.Fatalf("encodeRequest: %v", err)
	}

	t.Run("truncated frame", func(t *testing.T) {
		if _, err := decodeFrame(key, frame[:10]); err == nil {
			t.Error("expected error for truncated frame")
		}
	})

	t.Run("bad prefix", func(t *testing.T) {
		bad := append([]byte(nil), frame...)
		bad[0] = 0xFF
		if _, err := decodeFrame(key, bad); err == nil {
			t.Error("expected error for bad prefix")
		}
	})

	t.Run("bad suffix", func(t *testing.T) {
		bad := append([]byte(nil), frame...)
		bad[len(bad)-1] ^= 0xFF
		if _, err := decodeFrame(key, bad); err == nil {
			t.Error("expected error for bad suffix")
		}
	})

	t.Run("tampered ciphertext", func(t *testing.T) {
		bad := append([]byte(nil), frame...)
		// Flip a byte in the ciphertext area (after header + IV).
		bad[headerLen+ivLen+2] ^= 0xFF
		if _, err := decodeFrame(key, bad); err == nil {
			t.Error("expected GCM authentication failure for tampered ciphertext")
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		wrongKey := []byte("fedcba9876543210")
		if _, err := decodeFrame(wrongKey, frame); err == nil {
			t.Error("expected decryption failure with wrong key")
		}
	})
}

// TestStripRetcode verifies the 4-byte retcode handling on response payloads.
func TestStripRetcode(t *testing.T) {
	t.Run("zero retcode", func(t *testing.T) {
		payload := append([]byte{0, 0, 0, 0}, []byte(`{"dps":{}}`)...)
		got, err := stripRetcode(payload)
		if err != nil {
			t.Fatalf("stripRetcode: %v", err)
		}
		if string(got) != `{"dps":{}}` {
			t.Errorf("got %q, want %q", got, `{"dps":{}}`)
		}
	})

	t.Run("nonzero retcode", func(t *testing.T) {
		payload := []byte{0, 0, 0, 1, 0x7b, 0x7d}
		if _, err := stripRetcode(payload); err == nil {
			t.Error("expected error for nonzero retcode")
		}
	})

	t.Run("payload too short", func(t *testing.T) {
		if _, err := stripRetcode([]byte{0, 0}); err == nil {
			t.Error("expected error for payload shorter than retcode")
		}
	})
}
