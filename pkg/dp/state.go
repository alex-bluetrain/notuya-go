package dp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// State is a decoded status report. A status only carries the DPs the bulb
// chose to report (a push usually carries just the ones that changed), so
// every field is meaningful only if Has says its DP is present.
type State struct {
	Schema Schema
	// Raw holds every reported DP, decoded or not.
	Raw map[ID]json.RawMessage

	On           bool
	Mode         WorkMode
	Brightness   int // raw, in Schema's range
	ColourTemp   int // raw, 0–Schema.ColourTempMax
	Colour       HSV // always hue 0–360, saturation/value 0–1000
	Scene        SceneValue
	Timer        int // seconds
	DoNotDisturb bool
}

// Has reports whether the status carried DP id.
func (st State) Has(id ID) bool {
	_, ok := st.Raw[id]
	return ok
}

// IDs lists the reported DPs in order. On a full status (from a query) this
// is the bulb's readable capability set; write-only DPs never appear.
func (st State) IDs() []ID {
	ids := make([]ID, 0, len(st.Raw))
	for id := range st.Raw {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// BrightnessPercent is Brightness as 0–100 %.
func (st State) BrightnessPercent() float64 {
	return float64(st.Brightness) / float64(st.Schema.BrightnessMax) * 100
}

// ColourTempPercent is ColourTemp as 0–100 %.
func (st State) ColourTempPercent() float64 {
	return float64(st.ColourTemp) / float64(st.Schema.ColourTempMax) * 100
}

// Decode parses a status body: a query reply ({"dps":{...}}) or a push
// ({"data":{"dps":{...}}}), using Schema20.
//
// A DP whose value does not parse is an error: the bulb said something this
// package misunderstands, and silently ignoring it would hide that.
func Decode(body []byte) (State, error) {
	var env struct {
		DPS  map[string]json.RawMessage `json:"dps"`
		Data struct {
			DPS map[string]json.RawMessage `json:"dps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return State{}, fmt.Errorf("dp: decoding status: %w", err)
	}
	dps := env.DPS
	if dps == nil {
		dps = env.Data.DPS
	}
	st := State{Raw: make(map[ID]json.RawMessage, len(dps))}
	for k, v := range dps {
		n, err := strconv.Atoi(k)
		if err != nil {
			return State{}, fmt.Errorf("dp: status has non-numeric DP %q", k)
		}
		st.Raw[ID(n)] = v
	}
	st.Schema = Schema20
	s := st.Schema

	var err error
	field := func(id ID, dst any) {
		if raw, ok := st.Raw[id]; ok && err == nil {
			if e := json.Unmarshal(raw, dst); e != nil {
				err = fmt.Errorf("dp: DP %d: %w", id, e)
			}
		}
	}
	var mode, colour, scene string
	field(s.SwitchDP, &st.On)
	field(s.ModeDP, &mode)
	field(s.BrightnessDP, &st.Brightness)
	field(s.ColourTempDP, &st.ColourTemp)
	field(s.ColourDP, &colour)
	field(s.TimerDP, &st.Timer)
	field(s.SceneDP, &scene)
	field(DoNotDisturb, &st.DoNotDisturb)
	if err != nil {
		return State{}, err
	}
	st.Mode = ParseMode(mode)
	if colour != "" {
		st.Colour, err = ParseHSV(colour)
	}
	if err == nil && scene != "" {
		st.Scene, err = ParseScene(scene)
	}
	if err != nil {
		return State{}, err
	}
	return st, nil
}
