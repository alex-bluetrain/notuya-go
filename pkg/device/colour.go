package device

import (
	"fmt"
	"math"
	"strconv"
)

// rgbToHSV16Hex converts 0-255 RGB to Tuya's "hsv16" hex encoding used by
// DP 24 on this hardware (hhhhssssvvvv: h 0-360, s/v 0-1000, each 4 hex
// digits). Matches tinytuya's BulbDevice.rgb_to_hexvalue exactly: it goes
// through Python's colorsys.rgb_to_hsv and then *truncates* (not rounds)
// the scaled h/s/v to int before formatting — replicated bit-for-bit here,
// including the truncation, since a rounding difference would send a
// slightly different color than tinytuya does for the same RGB input.
func rgbToHSV16Hex(r, g, b uint8) string {
	h, s, v := rgbToHSV(float64(r)/255.0, float64(g)/255.0, float64(b)/255.0)
	return hsv16Hex(int(h*360), int(s*1000), int(v*1000))
}

// hsv16Hex formats integer h (0-360) / s,v (0-1000) as Tuya's "hsv16"
// 12-hex-digit encoding.
func hsv16Hex(h, s, v int) string {
	return fmt.Sprintf("%04x%04x%04x", h, s, v)
}

// parseHSV16Hex parses Tuya's "hsv16" hex encoding (as read back from a
// device's DP 24) into integer h/s/v.
func parseHSV16Hex(hex string) (h, s, v int, err error) {
	if len(hex) != 12 {
		return 0, 0, 0, fmt.Errorf("device: hsv16 value must be 12 hex chars, got %q", hex)
	}
	parse := func(part string) (int, error) {
		n, err := strconv.ParseInt(part, 16, 32)
		return int(n), err
	}
	if h, err = parse(hex[0:4]); err != nil {
		return 0, 0, 0, fmt.Errorf("device: parsing hsv16 hue %q: %w", hex, err)
	}
	if s, err = parse(hex[4:8]); err != nil {
		return 0, 0, 0, fmt.Errorf("device: parsing hsv16 saturation %q: %w", hex, err)
	}
	if v, err = parse(hex[8:12]); err != nil {
		return 0, 0, 0, fmt.Errorf("device: parsing hsv16 value %q: %w", hex, err)
	}
	return h, s, v, nil
}

// hsv16HexToHSV converts a device's "hsv16" DP 24 value back to h/s/v each in
// [0,1], matching tinytuya's hexvalue_to_hsv for the hsv16 format.
func hsv16HexToHSV(hex string) (h, s, v float64, err error) {
	hi, si, vi, err := parseHSV16Hex(hex)
	if err != nil {
		return 0, 0, 0, err
	}
	return float64(hi) / 360.0, float64(si) / 1000.0, float64(vi) / 1000.0, nil
}

// rgbToHSV is a direct port of Python's colorsys.rgb_to_hsv (r, g, b, h, s,
// v all in [0,1]).
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
