package device

import (
	"context"
	"encoding/json"
	"testing"
)

// decodeDPs pulls the dps map out of the last CONTROL_NEW payload captured
// by a mockSession (skipping the 15-byte version header).
func decodeDPs(t *testing.T, payload []byte) map[string]json.RawMessage {
	t.Helper()
	if len(payload) < 15 {
		t.Fatalf("payload too short (%d bytes)", len(payload))
	}
	var body struct {
		Data struct {
			DPS map[string]json.RawMessage `json:"dps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload[15:], &body); err != nil {
		t.Fatalf("invalid control JSON: %v", err)
	}
	return body.Data.DPS
}

func TestSetColourTempPercent(t *testing.T) {
	mock := &mockSession{}
	d := NewDevice(mock, "test")

	if err := d.SetColourTempPercent(context.Background(), 25, true); err != nil {
		t.Fatalf("SetColourTempPercent: %v", err)
	}
	dps := decodeDPs(t, mock.lastPayload)
	if string(dps[DPMode]) != `"white"` {
		t.Errorf("mode = %s, want white", dps[DPMode])
	}
	if string(dps[DPColourTemp]) != "250" {
		t.Errorf("colourtemp = %s, want 250", dps[DPColourTemp])
	}
}

func TestGetterMissingDP(t *testing.T) {
	status := map[string]json.RawMessage{DPSwitch: json.RawMessage(`true`)}
	if _, err := GetModeFrom(status); err == nil {
		t.Error("expected error for missing mode DP")
	}
}

// TestGettersFromStatus verifies the *From variants read every value out of
// a single already-fetched status map (no per-getter round-trip).
func TestGettersFromStatus(t *testing.T) {
	status := map[string]json.RawMessage{
		DPMode:       json.RawMessage(`"colour"`),
		DPBrightness: json.RawMessage(`600`),
		DPColourTemp: json.RawMessage(`300`),
		DPColour:     json.RawMessage(`"000003e803e8"`),
	}

	if mode, err := GetModeFrom(status); err != nil || mode != ModeColour {
		t.Fatalf("GetModeFrom = %q, %v; want colour", mode, err)
	}
	if b, err := GetBrightnessFrom(status); err != nil || b != 600 {
		t.Fatalf("GetBrightnessFrom = %d, %v; want 600", b, err)
	}
	if bp, err := GetBrightnessPercentFrom(status); err != nil || bp != 60.0 {
		t.Fatalf("GetBrightnessPercentFrom = %g, %v; want 60", bp, err)
	}
	if c, err := GetColourTempFrom(status); err != nil || c != 300 {
		t.Fatalf("GetColourTempFrom = %d, %v; want 300", c, err)
	}
	if cp, err := GetColourTempPercentFrom(status); err != nil || cp != 30.0 {
		t.Fatalf("GetColourTempPercentFrom = %g, %v; want 30", cp, err)
	}
	if h, s, v, err := ColourHSVFrom(status); err != nil || h != 0 || s != 1 || v != 1 {
		t.Fatalf("ColourHSVFrom = (%g,%g,%g), %v; want (0,1,1)", h, s, v, err)
	}
}
