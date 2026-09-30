package device

import (
	"context"
	"encoding/json"
	"fmt"
)

// This file is the "sugar" surface of Device: the named colour/white/
// brightness/music setters and the status readers, all built on top of
// the generic DP access in device.go (SetDPs/SetValue/Status). It mirrors the
// convenience methods tinytuya's BulbDevice adds over its raw DP access.

// SetColour switches the bulb to colour mode and sets the given RGB colour,
// mirroring tinytuya's set_colour.
func (d *Device) SetColour(ctx context.Context, r, g, b uint8, wait bool) error {
	return d.SetDPs(ctx, map[string]any{
		DPMode:   ModeColour,
		DPColour: rgbToHSV16Hex(r, g, b),
	}, wait)
}

// SetColourTempPercent sets the white colour-temperature DP from a 0-100
// percentage, mirroring tinytuya's set_colourtemp_percentage.
func (d *Device) SetColourTempPercent(ctx context.Context, pct float64, wait bool) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("device: colour temp percent must be 0-100, got %g", pct)
	}
	return d.SetColourTemp(ctx, int(BrightnessMax*pct/100), wait)
}

// SetBrightness sets the raw white-mode brightness DP (10-1000), mirroring
// tinytuya's set_brightness. It switches the bulb to white mode.
func (d *Device) SetBrightness(ctx context.Context, brightness int, wait bool) error {
	if brightness < 0 || brightness > BrightnessMax {
		return fmt.Errorf("device: brightness must be 0-%d, got %d", BrightnessMax, brightness)
	}
	return d.SetDPs(ctx, map[string]any{
		DPMode:       ModeWhite,
		DPBrightness: brightness,
	}, wait)
}

// SetColourTemp sets the white colour-temperature DP (0-1000), mirroring
// tinytuya's set_colourtemp. It switches the bulb to white mode.
func (d *Device) SetColourTemp(ctx context.Context, temp int, wait bool) error {
	if temp < 0 || temp > BrightnessMax {
		return fmt.Errorf("device: colour temp must be 0-%d, got %d", BrightnessMax, temp)
	}
	return d.SetDPs(ctx, map[string]any{
		DPMode:       ModeWhite,
		DPColourTemp: temp,
	}, wait)
}

// SetWhiteBrightness sets white-mode brightness as a 0-100 percentage,
// switching the bulb to white mode (DP 21) and writing DP 22. Colour
// temperature is left untouched (the device keeps its prior value for any DP
// not present in a CONTROL_NEW write). This is an alias of SetBrightness's
// percentage form and is unconditional — no state is read.
func (d *Device) SetWhiteBrightness(ctx context.Context, pct float64, wait bool) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("device: brightness percent must be 0-100, got %g", pct)
	}
	return d.SetBrightness(ctx, int(BrightnessMax*pct/100), wait)
}

// SetMusicColour writes one DP 28 music-mode colour with the given change
// mode (jump or fade), mirroring tinytuya's set_music_colour. Writing DP 28
// is what enters music mode; see the DP 28 notes in the package docs.
// Streaming callers pass wait=false for every message after the first, since
// the bulb stops acking mid-run.
func (d *Device) SetMusicColour(ctx context.Context, mode ChangeMode, r, g, b uint8, wait bool) error {
	hex, err := musicColourHex(mode, r, g, b)
	if err != nil {
		return err
	}
	return d.SetValue(ctx, DPMusic, hex, wait)
}

// The getters come in two forms, mirroring tinytuya's optional `state=`
// parameter. GetX(ctx) fetches a fresh Status and reads one value from it —
// convenient for a one-shot read. GetXFrom(status) reads from a status map
// the caller already fetched, so reading several values (e.g. to print full
// state) costs a single round-trip instead of one per getter.

// GetModeFrom reads the work mode from an already-fetched status map.
func GetModeFrom(status map[string]json.RawMessage) (string, error) {
	return dpString(status, DPMode)
}

// GetBrightnessFrom reads the raw brightness DP from an already-fetched
// status map.
func GetBrightnessFrom(status map[string]json.RawMessage) (int, error) {
	return dpInt(status, DPBrightness)
}

// GetBrightnessPercentFrom reads brightness as a percentage from an
// already-fetched status map.
func GetBrightnessPercentFrom(status map[string]json.RawMessage) (float64, error) {
	b, err := GetBrightnessFrom(status)
	if err != nil {
		return 0, err
	}
	return float64(b) / BrightnessMax * 100.0, nil
}

// GetColourTempFrom reads the raw colour-temperature DP from an
// already-fetched status map.
func GetColourTempFrom(status map[string]json.RawMessage) (int, error) {
	return dpInt(status, DPColourTemp)
}

// GetColourTempPercentFrom reads colour temperature as a percentage from an
// already-fetched status map.
func GetColourTempPercentFrom(status map[string]json.RawMessage) (float64, error) {
	c, err := GetColourTempFrom(status)
	if err != nil {
		return 0, err
	}
	return float64(c) / BrightnessMax * 100.0, nil
}

// ColourHSVFrom reads the colour DP as h/s/v (each 0-1) from an
// already-fetched status map.
func ColourHSVFrom(status map[string]json.RawMessage) (h, s, v float64, err error) {
	hex, err := dpString(status, DPColour)
	if err != nil {
		return 0, 0, 0, err
	}
	return hsv16HexToHSV(hex)
}

// dpString reads DP dp from a status map as a string, erroring if it is
// absent or not a string.
func dpString(dps map[string]json.RawMessage, dp string) (string, error) {
	raw, ok := dps[dp]
	if !ok {
		return "", fmt.Errorf("device: DP %s not present in status", dp)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("device: DP %s is not a string: %w", dp, err)
	}
	return s, nil
}

// dpInt reads DP dp from a status map as an int, erroring if it is absent or
// not a number.
func dpInt(dps map[string]json.RawMessage, dp string) (int, error) {
	raw, ok := dps[dp]
	if !ok {
		return 0, fmt.Errorf("device: DP %s not present in status", dp)
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("device: DP %s is not a number: %w", dp, err)
	}
	return n, nil
}
