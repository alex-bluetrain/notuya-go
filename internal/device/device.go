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
