package protocol35

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"

	"github.com/alex-bluetrain/notuya-go/pkg/protocol"
)

// generateNonce returns a fresh random 16-byte nonce for a handshake.
func generateNonce() ([]byte, error) {
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return nonce, nil
}

// buildStep1 encodes the SESS_KEY_NEG_START request: localNonce, encrypted
// under the device's real, static local_key.
func buildStep1(localKey, localNonce, iv []byte, seqno uint32) ([]byte, error) {
	return encodeRequest(localKey, iv, seqno, protocol.SessKeyNegStart, localNonce)
}

// parseStep2 decrypts and validates a SESS_KEY_NEG_RESP frame. The
// response must authenticate under localKey and decode to
// remote_nonce(16) || HMAC-SHA256(local_key, local_nonce)(32); the HMAC is
// verified against our own computation before the remote nonce is trusted.
func parseStep2(localKey, localNonce, frame []byte) (remoteNonce []byte, err error) {
	df, err := decodeFrame(localKey, frame)
	if err != nil {
		return nil, err
	}
	if df.Cmd != protocol.SessKeyNegResp {
		return nil, fmt.Errorf("protocol35: expected cmd %d (SESS_KEY_NEG_RESP), got %d", protocol.SessKeyNegResp, df.Cmd)
	}
	payload, err := StripRetcode(df.Payload)
	if err != nil {
		return nil, err
	}
	if len(payload) < nonceLen+sha256.Size {
		return nil, fmt.Errorf("%w: step2 payload too short (%d bytes)", ErrBadFrame, len(payload))
	}

	remoteNonce = payload[:nonceLen]
	gotHMAC := payload[nonceLen : nonceLen+sha256.Size]

	mac := hmac.New(sha256.New, localKey)
	mac.Write(localNonce)
	wantHMAC := mac.Sum(nil)

	if !hmac.Equal(gotHMAC, wantHMAC) {
		return nil, fmt.Errorf("protocol35: step2 HMAC mismatch (wrong local_key, or a spoofed/corrupted response)")
	}
	return remoteNonce, nil
}

// buildStep3 encodes the SESS_KEY_NEG_FINISH request:
// HMAC-SHA256(local_key, remote_nonce), still under the static local_key.
func buildStep3(localKey, remoteNonce, iv []byte, seqno uint32) ([]byte, error) {
	mac := hmac.New(sha256.New, localKey)
	mac.Write(remoteNonce)
	return encodeRequest(localKey, iv, seqno, protocol.SessKeyNegFinish, mac.Sum(nil))
}

// deriveSessionKey computes the post-handshake session key: XOR the two
// nonces, AES-GCM-seal that under the real local_key with an IV of the
// first 12 bytes of localNonce (no AAD), and take the 16-byte ciphertext
// (the tag is discarded) — confirmed exact against tinytuya's own source
// and cross-checked against its logged session key for a real device.
func deriveSessionKey(localKey, localNonce, remoteNonce []byte) ([]byte, error) {
	if len(localNonce) != nonceLen || len(remoteNonce) != nonceLen {
		return nil, fmt.Errorf("protocol35: nonces must be %d bytes", nonceLen)
	}
	xored := make([]byte, nonceLen)
	for i := range xored {
		xored[i] = localNonce[i] ^ remoteNonce[i]
	}
	gcm, err := newGCM(localKey)
	if err != nil {
		return nil, err
	}
	iv := localNonce[:ivLen]
	sealed := gcm.Seal(nil, iv, xored, nil) // ciphertext(16) || tag(16)
	return sealed[:nonceLen], nil
}
