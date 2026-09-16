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

// hsvToHSV16Hex converts h/s/v (each 0-1, as tinytuya's set_hsv takes them)
// to Tuya's "hsv16" 12-hex-digit encoding, matching tinytuya's
// hsv_to_hexvalue for the hsv16 format: it truncates the scaled components
// (h*360, s*1000, v*1000) rather than rounding, for bit-for-bit parity.
func hsvToHSV16Hex(h, s, v float64) string {
	return hsv16Hex(int(h*360), int(s*1000), int(v*1000))
}

// hsv16HexToRGB converts a device's "hsv16" DP 24 value back to 0-255 RGB,
// matching tinytuya's hexvalue_to_rgb for the hsv16 format: h/s/v are scaled
// back to [0,1], run through colorsys.hsv_to_rgb, and the result truncated to
// int after *255.
func hsv16HexToRGB(hex string) (r, g, b uint8, err error) {
	h, s, v, err := parseHSV16Hex(hex)
	if err != nil {
		return 0, 0, 0, err
	}
	rf, gf, bf := hsvToRGB(float64(h)/360.0, float64(s)/1000.0, float64(v)/1000.0)
	return uint8(rf * 255), uint8(gf * 255), uint8(bf * 255), nil
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

// hsvToRGB is a direct port of Python's colorsys.hsv_to_rgb (h, s, v, r, g,
// b all in [0,1]).
func hsvToRGB(h, s, v float64) (r, g, b float64) {
	if s == 0.0 {
		return v, v, v
	}
	i := int(h * 6.0)
	f := (h * 6.0) - float64(i)
	p := v * (1.0 - s)
	q := v * (1.0 - s*f)
	t := v * (1.0 - s*(1.0-f))
	switch i % 6 {
	case 0:
		return v, t, p
	case 1:
		return q, v, p
	case 2:
		return p, v, t
	case 3:
		return p, q, v
	case 4:
		return t, p, v
	default:
		return v, p, q
	}
}
