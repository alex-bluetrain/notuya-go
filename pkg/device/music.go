package device

import "fmt"

// ChangeMode is DP 28's (music_data) leading change-mode digit: how the bulb
// moves from its current colour to a newly streamed one. Tuya defines only
// two values; any other digit is not part of the spec.
type ChangeMode uint8

const (
	// ChangeJump applies the colour immediately ("direct" in Tuya's cloud
	// instruction set). Snappy, but a drag looks visibly steppy.
	ChangeJump ChangeMode = 0

	// ChangeFade glides to the colour ("gradient"). The fade's duration is
	// fixed by the firmware; to change how smooth a stream feels, tune the
	// send interval instead.
	ChangeFade ChangeMode = 1

	// DefaultChangeMode is sent when a stream does not pick one. Fade is the
	// value the Python picker settled on.
	DefaultChangeMode = ChangeFade
)

// Valid reports whether m is one of the two modes DP 28 defines.
func (m ChangeMode) Valid() bool { return m == ChangeJump || m == ChangeFade }

// Ptr returns a pointer to a copy of m, for the optional (nil = default)
// change-mode fields of the streaming API.
func (m ChangeMode) Ptr() *ChangeMode { return &m }

func (m ChangeMode) String() string {
	switch m {
	case ChangeJump:
		return "jump"
	case ChangeFade:
		return "fade"
	}
	return fmt.Sprintf("ChangeMode(%d)", uint8(m))
}

// RGB is a 24-bit colour.
type RGB struct {
	R, G, B uint8
}

// musicColourHex encodes an RGB colour as a DP 28 value:
//
//	{change mode:%x}{hsv16:12 hex}{white_brightness:0000}{colourtemp:0000}
//
// e.g. pure red with ChangeJump is "0" + "000003e803e8" + "0000" + "0000".
// The two trailing fields drive the bulb's separate white channel, not the
// colour's intensity, and are deliberately pinned to zero: non-zero values
// change how the device reads the payload and can leave it ignoring colour
// updates altogether. Dim a streamed colour by scaling r/g/b instead.
func musicColourHex(mode ChangeMode, r, g, b uint8) (string, error) {
	if !mode.Valid() {
		return "", fmt.Errorf("device: invalid change mode %v", mode)
	}
	return fmt.Sprintf("%x%s%04x%04x", uint8(mode), rgbToHSV16Hex(r, g, b), 0, 0), nil
}
