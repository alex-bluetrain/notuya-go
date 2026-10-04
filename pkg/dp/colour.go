package dp

import (
	"fmt"
	"math"
	"strconv"
)

// RGB is a 24-bit colour.
type RGB struct{ R, G, B uint8 }

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

// HSVFromRGB converts the way tinytuya's rgb_to_hexvalue does: Python's
// colorsys.rgb_to_hsv, then the scaled values truncated (not rounded) — so
// the same RGB sends exactly the bytes tinytuya would.
func HSVFromRGB(c RGB) HSV {
	h, s, v := rgbToHSV(float64(c.R)/255, float64(c.G)/255, float64(c.B)/255)
	return HSV{int(h * 360), int(s * 1000), int(v * 1000)}
}

// RGB converts c back to 8-bit RGB.
func (c HSV) RGB() RGB {
	h := math.Mod(float64(c.H)/360, 1)
	s, v := float64(c.S)/1000, float64(c.V)/1000
	i := math.Floor(h * 6)
	f := h*6 - i
	p, q, t := v*(1-s), v*(1-s*f), v*(1-s*(1-f))
	var r, g, b float64
	switch int(i) % 6 {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	to8 := func(x float64) uint8 { return uint8(math.Round(x * 255)) }
	return RGB{to8(r), to8(g), to8(b)}
}

// rgbToHSV is a direct port of Python's colorsys.rgb_to_hsv (all in [0,1]).
func rgbToHSV(r, g, b float64) (h, s, v float64) {
	maxc := math.Max(r, math.Max(g, b))
	minc := math.Min(r, math.Min(g, b))
	v = maxc
	if minc == maxc {
		return 0, 0, v
	}
	s = (maxc - minc) / maxc
	rc := (maxc - r) / (maxc - minc)
	gc := (maxc - g) / (maxc - minc)
	bc := (maxc - b) / (maxc - minc)
	switch maxc {
	case r:
		h = bc - gc
	case g:
		h = 2.0 + rc - bc
	default:
		h = 4.0 + gc - rc
	}
	h = math.Mod(h/6.0, 1.0)
	if h < 0 {
		h += 1.0
	}
	return h, s, v
}
