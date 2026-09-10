package device

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/averstraeten/notuya-go/internal/protocol"
)

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

// client wraps a protocol.Session with the DP_QUERY_NEW / CONTROL_NEW JSON
// envelope tinytuya uses for protocol v3.4+/v3.5.
type client struct {
	session protocol.Session
}

func newClient(session protocol.Session) *client {
	return &client{session: session}
}

// query sends an empty DP_QUERY_NEW request and returns the device's
// current data points. DP_QUERY_NEW responses carry no version-header
// prefix (unlike CONTROL_NEW) — confirmed against a real capture.
func (c *client) query(ctx context.Context) (map[string]json.RawMessage, error) {
	payload, err := c.session.Command(ctx, protocol.DPQueryNew, []byte("{}"), true)
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

// control sends a CONTROL_NEW "set these dps" request (a partial update —
// only the DPs present in dps are changed, confirmed against a real
// capture of tinytuya's own set_colour call). Like ctl.py's
// turn_on/turn_off/set_colour/set_brightness_percentage, only
// success-or-failure matters here: the device's direct ack often carries
// no body at all (the actual state change arrives moments later as a
// separate, unsolicited status push on the same connection), so the
// response payload is intentionally not parsed.
//
// wait=false returns as soon as the frame is on the wire. Colour streaming
// needs it: the device stops acking after the first message of a run, so a
// blocking read would hang (see StreamColours).
func (c *client) control(ctx context.Context, dps map[string]any, wait bool) error {
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
	if _, err := c.session.Command(ctx, protocol.ControlNew, plaintext, wait); err != nil {
		return fmt.Errorf("device: control command: %w", err)
	}
	return nil
}
