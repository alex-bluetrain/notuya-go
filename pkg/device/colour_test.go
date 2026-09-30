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
