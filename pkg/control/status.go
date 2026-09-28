package control

import (
	"encoding/json"

	"github.com/alex-bluetrain/notuya-go/pkg/device"
)

// Status is the parsed snapshot rendered per device, derived from one
// device.Status round-trip so a refresh costs a single query. JSON tags match
// the wire shape the mobile client consumes.
type Status struct {
	On        bool    `json:"on"`
	Mode      string  `json:"mode"`       // device.ModeWhite / ModeColour / ModeScene / music
	BrightPct float64 `json:"bright_pct"` // 0-100
	TempPct   float64 `json:"temp_pct"`   // 0-100 (white mode colour temperature)
	Hue       float64 `json:"hue"`        // 0-1
	Sat       float64 `json:"sat"`        // 0-1
	HasColour bool    `json:"has_colour"` // DP 24 was present and parseable
	HasTemp   bool    `json:"has_temp"`   // DP 23 was present and parseable
}

// parseStatus derives a Status from a raw DP status map. Missing or
// unparseable DPs leave their fields at zero rather than failing the whole
// refresh.
func parseStatus(dps map[string]json.RawMessage) Status {
	var st Status

	if raw, ok := dps[device.DPSwitch]; ok {
		_ = json.Unmarshal(raw, &st.On)
	}
	if mode, err := device.GetModeFrom(dps); err == nil {
		st.Mode = mode
	}
	if pct, err := device.GetBrightnessPercentFrom(dps); err == nil {
		st.BrightPct = pct
	}
	if pct, err := device.GetColourTempPercentFrom(dps); err == nil {
		st.TempPct = pct
		st.HasTemp = true
	}
	if h, s, _, err := device.ColourHSVFrom(dps); err == nil {
		st.Hue = h
		st.Sat = s
		st.HasColour = true
	}
	return st
}
