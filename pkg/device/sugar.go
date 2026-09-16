package device

import (
	"context"
	"encoding/json"
	"fmt"
)

// This file is the "sugar" surface of Device: the named colour/white/
// brightness/mode/scene/music setters and the getters, all built on top of
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

// SetHSV switches the bulb to colour mode and sets the colour from h, s, v
// (each 0-1). Like SetColour (and unlike tinytuya's set_hsv, which also
// asserts switch:true) it does not force the bulb on: it only changes mode
// and colour, leaving the on/off state to TurnOn/TurnOff.
func (d *Device) SetHSV(ctx context.Context, h, s, v float64, wait bool) error {
	if h < 0 || h > 1 || s < 0 || s > 1 || v < 0 || v > 1 {
		return fmt.Errorf("device: h, s, v must each be 0-1, got %g/%g/%g", h, s, v)
	}
	return d.SetDPs(ctx, map[string]any{
		DPMode:   ModeColour,
		DPColour: hsvToHSV16Hex(h, s, v),
	}, wait)
}

// SetMode sets the work mode DP (white/colour/scene/music), mirroring
// tinytuya's set_mode. DPSwitch=true is asserted alongside the mode because
// the hardware requires the switch to be set together with the mode for the
// change to take effect (an undocumented quirk tinytuya carries too) — not a
// design choice replicated by inertia.
func (d *Device) SetMode(ctx context.Context, mode string, wait bool) error {
	return d.SetDPs(ctx, map[string]any{
		DPSwitch: true,
		DPMode:   mode,
	}, wait)
}

// SetScene switches the bulb to scene mode. On this hardware (a "Type A"
// scene layout, where the scene index is part of the mode enum value —
// confirmed the target model has no separate scene_data DP) the scene is
// encoded as "scene_<n>" in DPMode, mirroring tinytuya's set_scene branch
// for that layout. The 1-4 range is A60TY10W-specific (its four built-in
// scenes), not a property of the Type-A layout itself; other Type-A models
// expose a different count and would need this bound adjusted.
func (d *Device) SetScene(ctx context.Context, scene int, wait bool) error {
	if scene < 1 || scene > 4 {
		return fmt.Errorf("device: scene must be 1-4, got %d", scene)
	}
	return d.SetValue(ctx, DPMode, fmt.Sprintf("%s_%d", ModeScene, scene), wait)
}

// SetWhitePercent sets white mode with brightness and colour temperature as
// 0-100 percentages, mirroring tinytuya's set_white_percentage: each percent
// is scaled against the full-scale value (BrightnessMax) and truncated to the
// raw DP int, then handed to SetWhite. Percentages are float64 so they match
// what the getters return and accept fractional values.
func (d *Device) SetWhitePercent(ctx context.Context, brightnessPct, colourtempPct float64, wait bool) error {
	if brightnessPct < 0 || brightnessPct > 100 {
		return fmt.Errorf("device: brightness percent must be 0-100, got %g", brightnessPct)
	}
	if colourtempPct < 0 || colourtempPct > 100 {
		return fmt.Errorf("device: colour temp percent must be 0-100, got %g", colourtempPct)
	}
	b := int(BrightnessMax * brightnessPct / 100)
	c := int(BrightnessMax * colourtempPct / 100)
	return d.SetWhite(ctx, b, c, wait)
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

// SetWhite sets white mode with an explicit brightness and colour
// temperature in one write, mirroring tinytuya's set_white.
func (d *Device) SetWhite(ctx context.Context, brightness, temp int, wait bool) error {
	if brightness < 0 || brightness > BrightnessMax {
		return fmt.Errorf("device: brightness must be 0-%d, got %d", BrightnessMax, brightness)
	}
	if temp < 0 || temp > BrightnessMax {
		return fmt.Errorf("device: colour temp must be 0-%d, got %d", BrightnessMax, temp)
	}
	return d.SetDPs(ctx, map[string]any{
		DPMode:       ModeWhite,
		DPBrightness: brightness,
		DPColourTemp: temp,
	}, wait)
}

// SetColourBrightness adjusts the brightness of the current colour without
// changing hue or saturation, as a 0-100 percentage. On this hardware
// brightness in colour mode is not a separate DP: it is the "v" component of
// the colour value in DP 24 (hhhhssssvvvv). This reads the current colour,
// rewrites it with the new v, and never touches DPMode — so it only makes
// sense while the bulb is already in colour mode. It errors if DP 24 is
// missing or unparseable (there is no colour to preserve); it never falls
// back to a white-mode write.
func (d *Device) SetColourBrightness(ctx context.Context, pct float64, wait bool) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("device: brightness percent must be 0-100, got %g", pct)
	}
	value := int(BrightnessMax * pct / 100)

	dps, err := d.Status(ctx)
	if err != nil {
		return fmt.Errorf("device: reading current colour for brightness: %w", err)
	}
	raw, ok := dps[DPColour]
	if !ok {
		return fmt.Errorf("device: DP %s not present; cannot set colour brightness", DPColour)
	}
	var hex string
	if err := json.Unmarshal(raw, &hex); err != nil {
		return fmt.Errorf("device: DP %s is not a string: %w", DPColour, err)
	}
	h, s, _, err := parseHSV16Hex(hex)
	if err != nil {
		return fmt.Errorf("device: parsing current colour: %w", err)
	}
	return d.SetValue(ctx, DPColour, hsv16Hex(h, s, value), wait)
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

// SetMusicColour writes one DP 28 music-mode colour with the given per-update
// transition (fade length, 0-MaxTransition), mirroring tinytuya's
// set_music_colour. Writing DP 28 is what enters music mode; see the DP 28
// notes in the package docs. Streaming callers pass wait=false for every
// message after the first, since the bulb stops acking mid-run.
func (d *Device) SetMusicColour(ctx context.Context, transition int, r, g, b uint8, wait bool) error {
	hex, err := musicColourHex(transition, r, g, b)
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

// GetMode returns the bulb's current work mode (white/colour/scene/music),
// mirroring tinytuya's get_mode.
func (d *Device) GetMode(ctx context.Context) (string, error) {
	dps, err := d.Status(ctx)
	if err != nil {
		return "", err
	}
	return GetModeFrom(dps)
}

// GetModeFrom reads the work mode from an already-fetched status map.
func GetModeFrom(status map[string]json.RawMessage) (string, error) {
	return dpString(status, DPMode)
}

// GetBrightness returns the raw white-mode brightness DP (10-1000),
// mirroring tinytuya's brightness().
func (d *Device) GetBrightness(ctx context.Context) (int, error) {
	dps, err := d.Status(ctx)
	if err != nil {
		return 0, err
	}
	return GetBrightnessFrom(dps)
}

// GetBrightnessFrom reads the raw brightness DP from an already-fetched
// status map.
func GetBrightnessFrom(status map[string]json.RawMessage) (int, error) {
	return dpInt(status, DPBrightness)
}

// GetBrightnessPercent returns the brightness as a 0-100 percentage of
// full scale, mirroring tinytuya's get_brightness_percentage.
func (d *Device) GetBrightnessPercent(ctx context.Context) (float64, error) {
	dps, err := d.Status(ctx)
	if err != nil {
		return 0, err
	}
	return GetBrightnessPercentFrom(dps)
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

// GetColourTemp returns the raw white colour-temperature DP (0-1000),
// mirroring tinytuya's colourtemp().
func (d *Device) GetColourTemp(ctx context.Context) (int, error) {
	dps, err := d.Status(ctx)
	if err != nil {
		return 0, err
	}
	return GetColourTempFrom(dps)
}

// GetColourTempFrom reads the raw colour-temperature DP from an
// already-fetched status map.
func GetColourTempFrom(status map[string]json.RawMessage) (int, error) {
	return dpInt(status, DPColourTemp)
}

// GetColourTempPercent returns the colour temperature as a 0-100 percentage
// of full scale, mirroring tinytuya's get_colourtemp_percentage.
func (d *Device) GetColourTempPercent(ctx context.Context) (float64, error) {
	dps, err := d.Status(ctx)
	if err != nil {
		return 0, err
	}
	return GetColourTempPercentFrom(dps)
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

// ColourRGB returns the current colour DP as 0-255 RGB, mirroring
// tinytuya's colour_rgb.
func (d *Device) ColourRGB(ctx context.Context) (r, g, b uint8, err error) {
	dps, err := d.Status(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	return ColourRGBFrom(dps)
}

// ColourRGBFrom reads the colour DP as 0-255 RGB from an already-fetched
// status map.
func ColourRGBFrom(status map[string]json.RawMessage) (r, g, b uint8, err error) {
	hex, err := dpString(status, DPColour)
	if err != nil {
		return 0, 0, 0, err
	}
	return hsv16HexToRGB(hex)
}

// ColourHSV returns the current colour DP as h/s/v (each 0-1), mirroring
// tinytuya's colour_hsv.
func (d *Device) ColourHSV(ctx context.Context) (h, s, v float64, err error) {
	dps, err := d.Status(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	return ColourHSVFrom(dps)
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
