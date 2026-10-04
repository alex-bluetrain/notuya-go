// Package v35 is the transport layer of Tuya local protocol 3.5: the
// "6699" frame codec, AES-GCM, sequence numbers and the TCP connection
// that carries them. It moves frames and knows nothing about what they
// mean — command codes, handshakes and request/response matching belong to
// pkg/session/v35.
package v35

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
)

// Frame layout, confirmed byte-for-byte against real captured traffic
// (see testdata/handshake/session1.json): prefix(4) + unknown(2) +
// seqno(4) + cmd(4) + length(4) = 18-byte header, AAD = the 14 bytes
// after the prefix, body = iv(12) || ciphertext || tag(16), suffix(4).
const (
	framePrefix uint32 = 0x00006699
	frameSuffix uint32 = 0x00009966

	HeaderLen  = 18 // prefix + unknown + seqno + cmd + length
	aadLen     = 14 // header bytes after the prefix
	IVLen      = 12
	tagLen     = 16
	retcodeLen = 4 // status code prepended to device -> client payloads

	// MaxFrameBody caps the body length accepted from a frame header.
	// Device frames are a few hundred bytes; a wildly larger value means
	// the stream desynchronised, and refusing it avoids a huge allocation
	// driven by network input.
	MaxFrameBody = 1 << 20
)

// ErrBadFrame indicates a frame that is malformed at the wire-format level
// (wrong prefix/suffix, truncated, bad length field) as opposed to a
// decrypt/authentication failure.
var ErrBadFrame = errors.New("v35: malformed frame")

// Frame is one decrypted 6699 frame. For device -> client frames Payload
// still carries the 4-byte retcode prefix; see StripRetcode.
type Frame struct {
	Seqno   uint32
	Cmd     uint32
	Payload []byte
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// NewIV returns a fresh random 12-byte GCM IV.
func NewIV() ([]byte, error) {
	iv := make([]byte, IVLen)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	return iv, nil
}

// Encode builds a 6699 frame for f under key. iv must be exactly 12 bytes:
// NewIV in production; tests pass a fixed IV to reproduce captured traffic
// byte-for-byte.
func Encode(key, iv []byte, f Frame) ([]byte, error) {
	if len(iv) != IVLen {
		return nil, fmt.Errorf("%w: iv must be %d bytes, got %d", ErrBadFrame, IVLen, len(iv))
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}

	length := uint32(len(f.Payload) + IVLen + tagLen)
	aad := make([]byte, aadLen)
	binary.BigEndian.PutUint32(aad[2:6], f.Seqno)
	binary.BigEndian.PutUint32(aad[6:10], f.Cmd)
	binary.BigEndian.PutUint32(aad[10:14], length)

	ciphertext := gcm.Seal(nil, iv, f.Payload, aad) // tag appended by Seal

	out := make([]byte, 0, HeaderLen+IVLen+len(ciphertext)+4)
	out = binary.BigEndian.AppendUint32(out, framePrefix)
	out = append(out, aad...)
	out = append(out, iv...)
	out = append(out, ciphertext...)
	out = binary.BigEndian.AppendUint32(out, frameSuffix)
	return out, nil
}

// Decode parses, decrypts and authenticates a single 6699 frame.
func Decode(key, raw []byte) (Frame, error) {
	if len(raw) < HeaderLen+IVLen+tagLen+4 {
		return Frame{}, fmt.Errorf("%w: too short (%d bytes)", ErrBadFrame, len(raw))
	}
	if p := binary.BigEndian.Uint32(raw[0:4]); p != framePrefix {
		return Frame{}, fmt.Errorf("%w: bad prefix %#08x", ErrBadFrame, p)
	}

	aad := raw[4:HeaderLen]
	seqno := binary.BigEndian.Uint32(aad[2:6])
	cmd := binary.BigEndian.Uint32(aad[6:10])
	length := binary.BigEndian.Uint32(aad[10:14])

	if uint64(len(raw)) < uint64(HeaderLen)+uint64(length)+4 {
		return Frame{}, fmt.Errorf("%w: length field %d exceeds frame size", ErrBadFrame, length)
	}
	body := raw[HeaderLen : HeaderLen+length]
	if s := binary.BigEndian.Uint32(raw[HeaderLen+length : HeaderLen+length+4]); s != frameSuffix {
		return Frame{}, fmt.Errorf("%w: bad suffix %#08x", ErrBadFrame, s)
	}
	if len(body) < IVLen+tagLen {
		return Frame{}, fmt.Errorf("%w: body too short for iv+tag", ErrBadFrame)
	}

	gcm, err := newGCM(key)
	if err != nil {
		return Frame{}, err
	}
	plaintext, err := gcm.Open(nil, body[:IVLen], body[IVLen:], aad)
	if err != nil {
		return Frame{}, fmt.Errorf("v35: decrypt/authenticate failed: %w", err)
	}
	return Frame{Seqno: seqno, Cmd: cmd, Payload: plaintext}, nil
}

// BodyLen reads the body length out of an 18-byte frame header, so a
// stream reader knows how many more bytes (body + 4-byte suffix) to read.
func BodyLen(header []byte) (uint32, error) {
	if len(header) < HeaderLen {
		return 0, fmt.Errorf("%w: header too short", ErrBadFrame)
	}
	n := binary.BigEndian.Uint32(header[14:18])
	if n > MaxFrameBody {
		return 0, fmt.Errorf("%w: frame body length %d exceeds maximum %d", ErrBadFrame, n, MaxFrameBody)
	}
	return n, nil
}

// StripRetcode removes the 4-byte status-code prefix that device -> client
// payloads carry ahead of the body, and returns an error if the device
// reported a non-zero (failure) code.
func StripRetcode(payload []byte) ([]byte, error) {
	if len(payload) < retcodeLen {
		return nil, fmt.Errorf("%w: response payload too short for retcode", ErrBadFrame)
	}
	if rc := binary.BigEndian.Uint32(payload[:retcodeLen]); rc != 0 {
		return nil, fmt.Errorf("v35: device returned retcode %d", rc)
	}
	return payload[retcodeLen:], nil
}

// WithRetcode prepends a zero retcode to body — the shape of every
// device -> client payload. Used by fake devices in tests.
func WithRetcode(body []byte) []byte {
	return append(make([]byte, retcodeLen), body...)
}
