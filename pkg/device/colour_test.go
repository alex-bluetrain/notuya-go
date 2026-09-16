package device

import "testing"

// Reference vectors generated with the local tinytuya install as an oracle
// (`BulbDevice.rgb_to_hexvalue(r, g, b, 'hsv16')`) — including
// (255,0,0)->"000003e803e8", which also matches the literal DP 24 value
// captured from a real bulb during a real set_colour(255,0,0) call (see
// testdata/handshake and the M1 capture).
func TestRGBToHSV16Hex(t *testing.T) {
	cases := []struct {
		r, g, b uint8
		want    string
	}{
		{255, 0, 0, "000003e803e8"},
		{0, 255, 0, "007803e803e8"},
		{0, 0, 255, "00f003e803e8"},
		{255, 255, 255, "0000000003e8"},
		{0, 0, 0, "000000000000"},
		{255, 136, 0, "002003e803e8"},
		{18, 52, 86, "00d203160151"},
		{1, 2, 3, "00d2029a000b"},
	}
	for _, c := range cases {
		got := rgbToHSV16Hex(c.r, c.g, c.b)
		if got != c.want {
			t.Errorf("rgbToHSV16Hex(%d,%d,%d) = %q, want %q", c.r, c.g, c.b, got, c.want)
		}
	}
}

// TestHSVToHSV16Hex checks the h/s/v (0-1) -> hsv16 hex path used by SetHSV
// against tinytuya's hsv_to_hexvalue(h, s, v, 'hsv16'): scale by
// 360/1000/1000 and truncate.
func TestHSVToHSV16Hex(t *testing.T) {
	cases := []struct {
		h, s, v float64
		want    string
	}{
		{0, 1, 1, "000003e803e8"},       // red
		{1.0 / 3, 1, 1, "007803e803e8"}, // green (h=120)
		{2.0 / 3, 1, 1, "00f003e803e8"}, // blue (h=240)
		{0, 0, 1, "0000000003e8"},       // white
		{0, 0, 0, "000000000000"},       // off
	}
	for _, c := range cases {
		if got := hsvToHSV16Hex(c.h, c.s, c.v); got != c.want {
			t.Errorf("hsvToHSV16Hex(%g,%g,%g) = %q, want %q", c.h, c.s, c.v, got, c.want)
		}
	}
}

// TestHSV16HexToRGB checks the readback path used by ColourRGB. Values match
// tinytuya's hexvalue_to_rgb(hex, 'hsv16').
func TestHSV16HexToRGB(t *testing.T) {
	cases := []struct {
		hex     string
		r, g, b uint8
	}{
		{"000003e803e8", 255, 0, 0},
		{"007803e803e8", 0, 255, 0},
		{"00f003e803e8", 0, 0, 255},
		{"0000000003e8", 255, 255, 255},
		{"000000000000", 0, 0, 0},
	}
	for _, c := range cases {
		r, g, b, err := hsv16HexToRGB(c.hex)
		if err != nil {
			t.Fatalf("hsv16HexToRGB(%q): %v", c.hex, err)
		}
		if r != c.r || g != c.g || b != c.b {
			t.Errorf("hsv16HexToRGB(%q) = (%d,%d,%d), want (%d,%d,%d)", c.hex, r, g, b, c.r, c.g, c.b)
		}
	}
}
