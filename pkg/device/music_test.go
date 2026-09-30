package device

import "testing"

func TestMusicColourHex(t *testing.T) {
	tests := []struct {
		name    string
		mode    ChangeMode
		r, g, b uint8
		want    string
	}{
		// The documented reference value from the sibling Python
		// project's findings: jump, pure red.
		{"red jump", ChangeJump, 255, 0, 0, "0000003e803e800000000"},
		{"red fade", ChangeFade, 255, 0, 0, "1000003e803e800000000"},
		{"black", ChangeJump, 0, 0, 0, "0000000000000" + "00000000"},
		{"white", ChangeJump, 255, 255, 255, "0" + "0000000003e8" + "00000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := musicColourHex(tt.mode, tt.r, tt.g, tt.b)
			if err != nil {
				t.Fatalf("musicColourHex: %v", err)
			}
			if got != tt.want {
				t.Errorf("musicColourHex(%v, %d,%d,%d) = %q, want %q",
					tt.mode, tt.r, tt.g, tt.b, got, tt.want)
			}
			// Format is fixed-width: 1 change mode + 12 hsv16 + 8 trailing.
			if len(got) != 21 {
				t.Errorf("length = %d, want 21", len(got))
			}
		})
	}
}

func TestMusicColourHexRejectsBadChangeMode(t *testing.T) {
	for _, mode := range []ChangeMode{2, 10, 16} {
		if _, err := musicColourHex(mode, 255, 0, 0); err == nil {
			t.Errorf("change mode %d: expected an error, got nil", uint8(mode))
		}
	}
}
