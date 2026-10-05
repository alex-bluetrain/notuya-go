package dp

import (
	"fmt"
	"strconv"
)

// HSV is a colour as Tuya bulbs store it: hue 0–360, saturation and value
// 0–1000.
type HSV struct{ H, S, V int }

func (c HSV) validate() error {
	if c.H < 0 || c.H > 360 || c.S < 0 || c.S > 1000 || c.V < 0 || c.V > 1000 {
		return fmt.Errorf("dp: colour %+v out of range (h 0-360, s/v 0-1000)", c)
	}
	return nil
}

// Hex encodes c as DP 24's "hhhhssssvvvv".
func (c HSV) Hex() (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%04x%04x%04x", c.H, c.S, c.V), nil
}

// ParseHSV decodes DP 24's "hhhhssssvvvv".
func ParseHSV(s string) (HSV, error) {
	if len(s) != 12 {
		return HSV{}, fmt.Errorf("dp: colour must be 12 hex chars, got %q", s)
	}
	var f [3]int
	for i := range f {
		n, err := strconv.ParseUint(s[i*4:i*4+4], 16, 16)
		if err != nil {
			return HSV{}, fmt.Errorf("dp: colour %q: %w", s, err)
		}
		f[i] = int(n)
	}
	c := HSV{f[0], f[1], f[2]}
	return c, c.validate()
}
