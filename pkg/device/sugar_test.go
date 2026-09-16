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

func TestSetHSV(t *testing.T) {
	mock := &mockSession{}
	d := NewDevice(mock, "test")

	if err := d.SetHSV(context.Background(), 0, 1, 1, true); err != nil {
		t.Fatalf("SetHSV: %v", err)
	}
	dps := decodeDPs(t, mock.lastPayload)
	// SetHSV must not force the switch on (unlike tinytuya's set_hsv) —
	// it only sets mode + colour, matching SetColour.
	if _, ok := dps[DPSwitch]; ok {
		t.Errorf("switch present (%s), want absent", dps[DPSwitch])
	}
	if string(dps[DPMode]) != `"colour"` {
		t.Errorf("mode = %s, want colour", dps[DPMode])
	}
	if string(dps[DPColour]) != `"000003e803e8"` {
		t.Errorf("colour = %s, want red hsv16", dps[DPColour])
	}
}

func TestSetHSVRangeCheck(t *testing.T) {
	d := NewDevice(&mockSession{}, "test")
	if err := d.SetHSV(context.Background(), 1.5, 0, 0, true); err == nil {
		t.Error("expected error for h out of range")
	}
}

func TestSetScene(t *testing.T) {
	mock := &mockSession{}
	d := NewDevice(mock, "test")

	if err := d.SetScene(context.Background(), 3, true); err != nil {
		t.Fatalf("SetScene: %v", err)
	}
	dps := decodeDPs(t, mock.lastPayload)
	if string(dps[DPMode]) != `"scene_3"` {
		t.Errorf("mode = %s, want scene_3", dps[DPMode])
	}
}

func TestSetSceneRangeCheck(t *testing.T) {
	d := NewDevice(&mockSession{}, "test")
	if err := d.SetScene(context.Background(), 5, true); err == nil {
		t.Error("expected error for scene out of range")
	}
}

func TestSetWhitePercent(t *testing.T) {
	mock := &mockSession{}
	d := NewDevice(mock, "test")

	// 50% brightness, 100% colour temp -> 500 / 1000 against BrightnessMax.
	if err := d.SetWhitePercent(context.Background(), 50, 100, true); err != nil {
		t.Fatalf("SetWhitePercent: %v", err)
	}
	dps := decodeDPs(t, mock.lastPayload)
	if string(dps[DPMode]) != `"white"` {
		t.Errorf("mode = %s, want white", dps[DPMode])
	}
	if string(dps[DPBrightness]) != "500" {
		t.Errorf("brightness = %s, want 500", dps[DPBrightness])
	}
	if string(dps[DPColourTemp]) != "1000" {
		t.Errorf("colourtemp = %s, want 1000", dps[DPColourTemp])
	}
}

// TestSetWhitePercentFractional confirms a fractional percent scales past
// what integer division would allow: 33.3% of 1000 truncates to 333, not the
// 330 an int(33) would give.
func TestSetWhitePercentFractional(t *testing.T) {
	mock := &mockSession{}
	d := NewDevice(mock, "test")

	if err := d.SetWhitePercent(context.Background(), 33.3, 0, true); err != nil {
		t.Fatalf("SetWhitePercent: %v", err)
	}
	dps := decodeDPs(t, mock.lastPayload)
	if string(dps[DPBrightness]) != "333" {
		t.Errorf("brightness = %s, want 333", dps[DPBrightness])
	}
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

func TestSetModeAssertsSwitch(t *testing.T) {
	mock := &mockSession{}
	d := NewDevice(mock, "test")

	if err := d.SetMode(context.Background(), ModeMusic, true); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	dps := decodeDPs(t, mock.lastPayload)
	if string(dps[DPSwitch]) != "true" {
		t.Errorf("switch = %s, want true", dps[DPSwitch])
	}
	if string(dps[DPMode]) != `"music"` {
		t.Errorf("mode = %s, want music", dps[DPMode])
	}
}

func TestGetters(t *testing.T) {
	mock := &mockSession{
		resp: []byte(`{"dps":{"20":true,"21":"colour","22":600,"23":300,"24":"000003e803e8"}}`),
	}
	d := NewDevice(mock, "test")
	ctx := context.Background()

	mode, err := d.GetMode(ctx)
	if err != nil || mode != ModeColour {
		t.Fatalf("GetMode = %q, %v; want colour", mode, err)
	}

	b, err := d.GetBrightness(ctx)
	if err != nil || b != 600 {
		t.Fatalf("GetBrightness = %d, %v; want 600", b, err)
	}

	bp, err := d.GetBrightnessPercent(ctx)
	if err != nil || bp != 60.0 {
		t.Fatalf("GetBrightnessPercent = %g, %v; want 60", bp, err)
	}

	c, err := d.GetColourTemp(ctx)
	if err != nil || c != 300 {
		t.Fatalf("GetColourTemp = %d, %v; want 300", c, err)
	}

	cp, err := d.GetColourTempPercent(ctx)
	if err != nil || cp != 30.0 {
		t.Fatalf("GetColourTempPercent = %g, %v; want 30", cp, err)
	}

	r, g, bl, err := d.ColourRGB(ctx)
	if err != nil || r != 255 || g != 0 || bl != 0 {
		t.Fatalf("ColourRGB = (%d,%d,%d), %v; want red", r, g, bl, err)
	}

	h, s, v, err := d.ColourHSV(ctx)
	if err != nil || h != 0 || s != 1 || v != 1 {
		t.Fatalf("ColourHSV = (%g,%g,%g), %v; want (0,1,1)", h, s, v, err)
	}
}

func TestGetterMissingDP(t *testing.T) {
	mock := &mockSession{resp: []byte(`{"dps":{"20":true}}`)}
	d := NewDevice(mock, "test")
	if _, err := d.GetMode(context.Background()); err == nil {
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
	if r, g, bl, err := ColourRGBFrom(status); err != nil || r != 255 || g != 0 || bl != 0 {
		t.Fatalf("ColourRGBFrom = (%d,%d,%d), %v; want red", r, g, bl, err)
	}
	if h, s, v, err := ColourHSVFrom(status); err != nil || h != 0 || s != 1 || v != 1 {
		t.Fatalf("ColourHSVFrom = (%g,%g,%g), %v; want (0,1,1)", h, s, v, err)
	}
}
