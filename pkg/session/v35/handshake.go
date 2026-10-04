package v35

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"

	transport "github.com/alex-bluetrain/notuya-go/pkg/transport/v35"
)

const nonceLen = 16

// The handshake, three messages under the device's static local_key:
//
//	step 1  you → bulb  code 3  local_nonce
//	step 2  bulb → you  code 4  remote_nonce || HMAC-SHA256(local_key, local_nonce)
//	step 3  you → bulb  code 5  HMAC-SHA256(local_key, remote_nonce)
//
// The device does not answer step 3; the next frame on the wire is always
// the client's next request. Both sides then switch to the session key.

func newNonce() ([]byte, error) {
	n := make([]byte, nonceLen)
	if _, err := rand.Read(n); err != nil {
		return nil, err
	}
	return n, nil
}

// parseStep2 validates the device's reply to step 1 and returns its nonce.
// The HMAC is checked before the nonce is trusted: a mismatch means a
// wrong local_key, or a spoofed or corrupted reply.
func parseStep2(localKey, localNonce []byte, f transport.Frame) ([]byte, error) {
	if f.Cmd != cmdSessKeyNegResp {
		return nil, fmt.Errorf("v35: handshake: expected code %d, got %d", cmdSessKeyNegResp, f.Cmd)
	}
	payload, err := transport.StripRetcode(f.Payload)
	if err != nil {
		return nil, err
	}
	if len(payload) < nonceLen+sha256.Size {
		return nil, fmt.Errorf("v35: handshake: step 2 payload too short (%d bytes)", len(payload))
	}
	remote := payload[:nonceLen]
	if !hmac.Equal(payload[nonceLen:nonceLen+sha256.Size], hmacSHA256(localKey, localNonce)) {
		return nil, fmt.Errorf("v35: handshake: step 2 HMAC mismatch (wrong local_key?)")
	}
	return remote, nil
}

// step3Payload proves to the device that we hold local_key too.
func step3Payload(localKey, remoteNonce []byte) []byte {
	return hmacSHA256(localKey, remoteNonce)
}

// deriveSessionKey XORs the two nonces, AES-GCM-seals the result under
// local_key with the first 12 bytes of localNonce as IV and no AAD, and
// keeps the 16-byte ciphertext, dropping the tag. Matches tinytuya, and is
// checked against an independent implementation's output in the tests.
func deriveSessionKey(localKey, localNonce, remoteNonce []byte) ([]byte, error) {
	if len(localNonce) != nonceLen || len(remoteNonce) != nonceLen {
		return nil, fmt.Errorf("v35: handshake: nonces must be %d bytes", nonceLen)
	}
	xored := make([]byte, nonceLen)
	for i := range xored {
		xored[i] = localNonce[i] ^ remoteNonce[i]
	}
	block, err := aes.NewCipher(localKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Seal(nil, localNonce[:transport.IVLen], xored, nil)[:nonceLen], nil
}

func hmacSHA256(key, msg []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(msg)
	return mac.Sum(nil)
}
