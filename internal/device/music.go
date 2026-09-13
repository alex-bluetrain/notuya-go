package device

import "fmt"

const (
	// DefaultTransition is the fade length sent with each music colour: 0
	// is instant but visibly steppy, higher values smear; 1 is the value
	// the Python picker settled on.
	DefaultTransition = 1

	// MaxTransition is the highest value confirmed working on the target
	// hardware. It is also the largest that still encodes as one hex digit,
	// which the payload format requires.
	MaxTransition = 10
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
