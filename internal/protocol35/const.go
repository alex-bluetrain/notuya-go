package protocol35

import "errors"

// Frame layout, confirmed byte-for-byte against real captured traffic
// (see testdata/handshake/session1.json): prefix(4) + unknown(2) +
// seqno(4) + cmd(4) + length(4) = 18-byte header, AAD = the 14 bytes
// after the prefix, body = iv(12) || ciphertext || tag(16), suffix(4).
const (
	framePrefix uint32 = 0x00006699
	frameSuffix uint32 = 0x00009966

	headerLen  = 18 // prefix + unknown + seqno + cmd + length
	aadLen     = 14 // header bytes after the prefix
	ivLen      = 12
	tagLen     = 16
	retcodeLen = 4 // 4-byte status code prepended to response payloads only

	nonceLen = 16
)

// ErrBadFrame indicates a frame that is malformed at the wire-format level
// (wrong prefix/suffix, truncated, bad length field) as opposed to a
// decrypt/authentication failure.
var ErrBadFrame = errors.New("protocol35: malformed frame")
