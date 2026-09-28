package control

import "github.com/alex-bluetrain/notuya-go/pkg/device"

// StateFromStatus maps a refreshed Status to a SceneState. An off light stores
// only device_id + on; an on light stores its mode plus the data that mode
// needs (colour hue/sat or white temp) and brightness.
func StateFromStatus(deviceID string, st Status) SceneState {
	out := SceneState{DeviceID: deviceID, On: st.On}
	if !st.On {
		return out
	}
	switch {
	case st.Mode == device.ModeColour && st.HasColour:
		out.Mode = device.ModeColour
		out.Hue = st.Hue
		out.Sat = st.Sat
		out.Bright = st.BrightPct
	case st.Mode == device.ModeWhite && st.HasTemp:
		out.Mode = device.ModeWhite
		out.Temp = st.TempPct
		out.Bright = st.BrightPct
	}
	return out
}
