package device

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/averstraeten/notuya-go/internal/protocol"
)

// DP ids for the known target hardware (Tuya bulb "type B" / hsv16 colour
// format — model A60TY10W, see FINDINGS.md / devices.json in the sibling
// Python project). Exported so callers that need a DP not covered by a
// domain method can address one directly, the way tinytuya's set_value(dp,
// ...) does.
const (
	DPSwitch     = "20" // switch_led (bool)
	DPMode       = "21" // work_mode (enum: white/colour/scene/music)
	DPBrightness = "22" // bright_value_v2 (int 10-1000)
	DPColourTemp = "23" // temp_value_v2 (int 0-1000)
	DPColour     = "24" // colour_data_v2 (hsv16 hex string)
	DPMusic      = "28" // music_data (transition + hsv16 + white fields)
)

// Work-mode values for DPMode.
const (
	ModeWhite  = "white"
	ModeColour = "colour"
	ModeScene  = "scene"
	ModeMusic  = "music"
)

// BrightnessMax is the full-scale value for DPBrightness and the hsv16 "v"
// component.
const BrightnessMax = 1000

// versionHeader35 returns the 15-byte version prefix ("3.5" + 12 zero
// bytes) that tinytuya prepends to CONTROL_NEW payloads before encryption
// — confirmed against a real capture (see testdata/handshake) and against
// tinytuya's NO_PROTOCOL_HEADER_CMDS list, which excludes CONTROL_NEW.
func versionHeader35() []byte {
	h := make([]byte, 15)
	copy(h, "3.5")
	return h
}

// dpsResponse is the shape of a DP_QUERY_NEW response payload:
// {"dps":{"20":true,"21":"colour",...}}.
type dpsResponse struct {
	DPS map[string]json.RawMessage `json:"dps"`
}

// Device is the low-level, DP-centric control surface for one Tuya bulb —
// the equivalent of tinytuya's BulbDevice. It wraps a protocol.Session with
// the DP_QUERY_NEW / CONTROL_NEW JSON envelope tinytuya uses for protocol
// v3.4+/v3.5 and exposes generic DP access (SetValue/SetDPs) alongside the
// named colour/brightness/mode operations. It knows what each DP *means*;
// it does not own the session's lifecycle (the caller Open's and Close's
// it), matching ctl.py's per-command open/command/close pattern.
type Device struct {
	session protocol.Session
	Name    string
}

// NewDevice wraps an already-constructed protocol.Session (e.g.
// protocol35.NewSession) as a Device. The session must still be Open'd by
// the caller before use, and Closed when done.
func NewDevice(session protocol.Session, name string) *Device {
	return &Device{session: session, Name: name}
}

// Session exposes the underlying protocol.Session — needed by callers that
// drive streaming directly (e.g. the music loop, which type-asserts for
// protocol.StreamSession).
func (d *Device) Session() protocol.Session { return d.session }

// Status sends an empty DP_QUERY_NEW request and returns the device's
// current data points. DP_QUERY_NEW responses carry no version-header
// prefix (unlike CONTROL_NEW) — confirmed against a real capture. It is
// the equivalent of tinytuya's BulbDevice.status().
func (d *Device) Status(ctx context.Context) (map[string]json.RawMessage, error) {
	payload, err := d.session.Command(ctx, protocol.DPQueryNew, []byte("{}"), true)
	if err != nil {
		return nil, fmt.Errorf("device: status query: %w", err)
	}
	if len(payload) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var resp dpsResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil, fmt.Errorf("device: parsing status response: %w", err)
	}
	return resp.DPS, nil
}

// SetDPs sends a CONTROL_NEW "set these dps" request (a partial update —
// only the DPs present in dps are changed, confirmed against a real capture
// of tinytuya's own set_colour call). Like ctl.py's control commands, only
// success-or-failure matters here: the device's direct ack often carries no
// body at all (the actual state change arrives moments later as a separate,
// unsolicited status push on the same connection), so the response payload
// is intentionally not parsed.
//
// wait=false returns as soon as the frame is on the wire. Colour streaming
// needs it: the device stops acking after the first message of a run, so a
// blocking read would hang.
func (d *Device) SetDPs(ctx context.Context, dps map[string]any, wait bool) error {
	body := map[string]any{
		"protocol": 5,
		"t":        time.Now().Unix(),
		"data": map[string]any{
			"dps": dps,
		},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("device: encoding control payload: %w", err)
	}
	plaintext := append(versionHeader35(), encoded...)
	if _, err := d.session.Command(ctx, protocol.ControlNew, plaintext, wait); err != nil {
		return fmt.Errorf("device: control command: %w", err)
	}
	return nil
}

// SetValue sets a single arbitrary DP to value — the equivalent of
// tinytuya's set_value(dp, value, nowait=). Use it for any DP not covered
// by a named method.
func (d *Device) SetValue(ctx context.Context, dp string, value any, wait bool) error {
	return d.SetDPs(ctx, map[string]any{dp: value}, wait)
}

// TurnOn switches the bulb on (DPSwitch = true).
func (d *Device) TurnOn(ctx context.Context, wait bool) error {
	return d.SetValue(ctx, DPSwitch, true, wait)
}

// TurnOff switches the bulb off (DPSwitch = false).
func (d *Device) TurnOff(ctx context.Context, wait bool) error {
	return d.SetValue(ctx, DPSwitch, false, wait)
}

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

// SetBrightnessPercent sets brightness as a 0-100 percentage, mirroring
// tinytuya's set_brightness_percentage. It is mode-dependent rather than a
// single flat DP write: it reads the device's current mode+colour first,
// and
//   - in colour mode, keeps the current hue/saturation and only changes the
//     "v" component of DP 24 (so an in-progress colour is preserved),
//   - otherwise, falls back to a plain white-mode brightness write on DP 22
//     (colour temperature is left untouched — the device keeps its prior
//     value for any DP not present in a CONTROL_NEW write).
func (d *Device) SetBrightnessPercent(ctx context.Context, pct float64, wait bool) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("device: brightness percent must be 0-100, got %g", pct)
	}
	value := int(BrightnessMax * pct / 100)

	dps, err := d.Status(ctx)
	if err != nil {
		return fmt.Errorf("device: reading current state for brightness: %w", err)
	}

	mode := ModeWhite
	if raw, ok := dps[DPMode]; ok {
		_ = json.Unmarshal(raw, &mode)
	}

	if mode == ModeColour {
		if raw, ok := dps[DPColour]; ok {
			var hex string
			if err := json.Unmarshal(raw, &hex); err == nil {
				if h, s, _, err := parseHSV16Hex(hex); err == nil {
					return d.SetValue(ctx, DPColour, hsv16Hex(h, s, value), wait)
				}
			}
		}
	}

	return d.SetDPs(ctx, map[string]any{
		DPMode:       ModeWhite,
		DPBrightness: value,
	}, wait)
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
