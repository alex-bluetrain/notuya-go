package device

import "fmt"

const (
	// DefaultTransition is the change mode sent with each music colour.
	// Per Tuya's DP 28 (music_data) spec the leading digit is a change-mode
	// flag, not a magnitude: 0 = direct output (jump), 1 = gradual change
	// (fade). 1 is the value the Python picker settled on.
	DefaultTransition = 1

	// MaxTransition is the highest accepted change-mode value. DP 28 only
	// defines 0 (jump) and 1 (fade); the field is boolean, so anything
	// above 1 is treated as fade by the firmware and carries no extra
	// meaning.
	MaxTransition = 1
)

// RGB is a 24-bit colour.
type RGB struct {
	R, G, B uint8
}

// musicColourHex encodes an RGB colour as a DP 28 value:
//
//	{transition:%x}{hsv16:12 hex}{white_brightness:0000}{colourtemp:0000}
//
// e.g. pure red with transition 0 is "0" + "000003e803e8" + "0000" + "0000".
// The two trailing fields drive the bulb's separate white channel, not the
// colour's intensity, and are deliberately pinned to zero: non-zero values
// change how the device reads the payload and can leave it ignoring colour
// updates altogether. Dim a streamed colour by scaling r/g/b instead.
func musicColourHex(transition int, r, g, b uint8) (string, error) {
	if transition < 0 || transition > MaxTransition {
		return "", fmt.Errorf("device: transition must be 0-%d, got %d", MaxTransition, transition)
	}
	return fmt.Sprintf("%x%s%04x%04x", transition, rgbToHSV16Hex(r, g, b), 0, 0), nil
}
