package device

import "testing"

func TestMusicColourHex(t *testing.T) {
	tests := []struct {
		name       string
		transition int
		r, g, b    uint8
		want       string
	}{
		// The documented reference value from the sibling Python
		// project's findings: transition 0, pure red.
		{"red transition 0", 0, 255, 0, 0, "0000003e803e800000000"},
		{"red transition 1", 1, 255, 0, 0, "1000003e803e800000000"},
		{"transition 10 is one hex digit", 10, 255, 0, 0, "a000003e803e800000000"},
		{"black", 0, 0, 0, 0, "0000000000000" + "00000000"},
		{"white", 0, 255, 255, 255, "0" + "0000000003e8" + "00000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := musicColourHex(tt.transition, tt.r, tt.g, tt.b)
			if err != nil {
				t.Fatalf("musicColourHex: %v", err)
			}
			if got != tt.want {
				t.Errorf("musicColourHex(%d, %d,%d,%d) = %q, want %q",
					tt.transition, tt.r, tt.g, tt.b, got, tt.want)
			}
			// Format is fixed-width: 1 transition + 12 hsv16 + 8 trailing.
			if len(got) != 21 {
				t.Errorf("length = %d, want 21", len(got))
			}
		})
	}
}

func TestMusicColourHexRejectsBadTransition(t *testing.T) {
	for _, transition := range []int{-1, 11, 16} {
		if _, err := musicColourHex(transition, 255, 0, 0); err == nil {
			t.Errorf("transition %d: expected an error, got nil", transition)
		}
	}
}
