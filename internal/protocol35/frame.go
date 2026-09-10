package protocol35

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
)

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// encodeRequest builds a 6699 frame for a message we send to the device.
// iv must be exactly 12 bytes — crypto/rand.Read in production; tests pass
// a fixed IV to reproduce captured traffic byte-for-byte.
func encodeRequest(key, iv []byte, seqno, cmd uint32, plaintext []byte) ([]byte, error) {
	if len(iv) != ivLen {
		return nil, fmt.Errorf("%w: iv must be %d bytes, got %d", ErrBadFrame, ivLen, len(iv))
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}

	length := uint32(len(plaintext) + ivLen + tagLen)
	aad := make([]byte, aadLen)
	binary.BigEndian.PutUint32(aad[2:6], seqno)
	binary.BigEndian.PutUint32(aad[6:10], cmd)
	binary.BigEndian.PutUint32(aad[10:14], length)

	ciphertext := gcm.Seal(nil, iv, plaintext, aad) // tag appended by Seal

	frame := make([]byte, 0, headerLen+ivLen+len(ciphertext)+4)
	frame = binary.BigEndian.AppendUint32(frame, framePrefix)
	frame = append(frame, aad...)
	frame = append(frame, iv...)
	frame = append(frame, ciphertext...)
	frame = binary.BigEndian.AppendUint32(frame, frameSuffix)
	return frame, nil
}

// decodedFrame is a parsed and decrypted 6699 frame. Payload is the raw
// plaintext — for response frames it still carries the 4-byte retcode
// prefix; callers use stripRetcode to remove it.
type decodedFrame struct {
	Seqno   uint32
	Cmd     uint32
	Payload []byte
}

// decodeFrame parses, decrypts and authenticates a single 6699 frame.
func decodeFrame(key, frame []byte) (*decodedFrame, error) {
	if len(frame) < headerLen+ivLen+tagLen+4 {
		return nil, fmt.Errorf("%w: too short (%d bytes)", ErrBadFrame, len(frame))
	}
	prefix := binary.BigEndian.Uint32(frame[0:4])
	if prefix != framePrefix {
		return nil, fmt.Errorf("%w: bad prefix %#08x", ErrBadFrame, prefix)
	}

	aad := frame[4:headerLen]
	seqno := binary.BigEndian.Uint32(aad[2:6])
	cmd := binary.BigEndian.Uint32(aad[6:10])
	length := binary.BigEndian.Uint32(aad[10:14])

	if uint64(len(frame)) < uint64(headerLen)+uint64(length)+4 {
		return nil, fmt.Errorf("%w: length field %d exceeds frame size", ErrBadFrame, length)
	}
	body := frame[headerLen : uint32(headerLen)+length]
	suffix := binary.BigEndian.Uint32(frame[uint32(headerLen)+length : uint32(headerLen)+length+4])
	if suffix != frameSuffix {
		return nil, fmt.Errorf("%w: bad suffix %#08x", ErrBadFrame, suffix)
	}
	if len(body) < ivLen+tagLen {
		return nil, fmt.Errorf("%w: body too short for iv+tag", ErrBadFrame)
	}

	iv := body[:ivLen]
	ciphertext := body[ivLen:]
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, iv, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("protocol35: decrypt/authenticate failed: %w", err)
	}
	return &decodedFrame{Seqno: seqno, Cmd: cmd, Payload: plaintext}, nil
}

// stripRetcode removes the 4-byte status-code prefix that response
// payloads (device -> client) carry ahead of the actual body, and returns
// an error if the device reported a non-zero (failure) code.
func stripRetcode(payload []byte) ([]byte, error) {
	if len(payload) < retcodeLen {
		return nil, fmt.Errorf("%w: response payload too short for retcode", ErrBadFrame)
	}
	retcode := binary.BigEndian.Uint32(payload[:retcodeLen])
	if retcode != 0 {
		return nil, fmt.Errorf("protocol35: device returned retcode %d", retcode)
	}
	return payload[retcodeLen:], nil
}
