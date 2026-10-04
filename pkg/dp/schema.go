package dp

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// Values is a set of DP writes, keyed by DP number. Values are already in
// wire form (bool, int, or encoded string).
type Values map[ID]any

// Merge adds v's entries to a copy of vs and returns it.
func (vs Values) Merge(v Values) Values {
	out := make(Values, len(vs)+len(v))
	for k, x := range vs {
		out[k] = x
	}
	for k, x := range v {
		out[k] = x
	}
	return out
}

// Body encodes vs as a protocol 3.5 control body:
// {"protocol":5,"t":<unix>,"data":{"dps":{...}}}. Only the DPs present are
// changed on the bulb. The session layer adds the version header.
func Body(vs Values) ([]byte, error) {
	if len(vs) == 0 {
		return nil, fmt.Errorf("dp: nothing to write")
	}
	dps := make(map[string]any, len(vs))
	for id, v := range vs {
		dps[id.String()] = v
	}
	return json.Marshal(map[string]any{
		"protocol": 5,
		"t":        time.Now().Unix(),
		"data":     map[string]any{"dps": dps},
	})
}

// Schema is the set of DP numbers a bulb uses for its basic functions.
// tinytuya's rule: a bulb uses either the standard 20+ set or the legacy
// 1–8 set, never a mix. Advanced DPs (27–34, 209, 210) only exist in the
// 20+ set.
type Schema struct {
	Legacy                                                                   bool
	SwitchDP, ModeDP, BrightnessDP, ColourTempDP, ColourDP, SceneDP, TimerDP ID
	BrightnessMin, BrightnessMax, ColourTempMax                              int
}

var (
	// Schema20 is the standard set (Tuya's page): values 0–1000.
	Schema20 = Schema{false, Switch, Mode, Brightness, ColourTemp, Colour, Scene, Timer, 10, 1000, 1000}
	// Schema1 is the legacy set (tinytuya, "type A"): 8-bit values.
	Schema1 = Schema{true, 1, 2, 3, 4, 5, 6, 7, 25, 255, 255}
)

// DetectSchema picks the schema from a status: the legacy set if the bulb
// reports DP 1 and not DP 20, the standard set otherwise.
func DetectSchema(st State) Schema {
	if !st.Has(Switch) && st.Has(1) {
		return Schema1
	}
	return Schema20
}

// Power switches the bulb on or off.
func (s Schema) Power(on bool) Values { return Values{s.SwitchDP: on} }

// Colour switches to colour mode and sets c.
func (s Schema) Colour(c RGB) Values {
	if s.Legacy {
		return Values{s.ModeDP: string(ModeColour), s.ColourDP: legacyColourHex(c)}
	}
	h, _ := HSVFromRGB(c).Hex() // always in range
	return Values{s.ModeDP: string(ModeColour), s.ColourDP: h}
}

// ColourHSV switches to colour mode and sets c (standard schema only, which
// stores HSV directly).
func (s Schema) ColourHSV(c HSV) (Values, error) {
	if s.Legacy {
		return s.Colour(c.RGB()), nil
	}
	h, err := c.Hex()
	if err != nil {
		return nil, err
	}
	return Values{s.ModeDP: string(ModeColour), s.ColourDP: h}, nil
}

// White switches to white mode and sets the raw brightness, which must be
// in the schema's range (10–1000, or 25–255 for legacy bulbs).
func (s Schema) White(brightness int) (Values, error) {
	if err := between("brightness", brightness, s.BrightnessMin, s.BrightnessMax); err != nil {
		return nil, err
	}
	return Values{s.ModeDP: string(ModeWhite), s.BrightnessDP: brightness}, nil
}

// WhitePercent is White with brightness as 0–100 %. Tuya's app shows
// brightness as 1–100 %, so values below the minimum clamp up to it.
func (s Schema) WhitePercent(pct float64) (Values, error) {
	if pct < 0 || pct > 100 {
		return nil, fmt.Errorf("dp: brightness percent must be 0-100, got %g", pct)
	}
	return s.White(max(s.BrightnessMin, int(math.Round(float64(s.BrightnessMax)*pct/100))))
}

// ColourTemp switches to white mode and sets the raw colour temperature
// (0–1000, or 0–255 for legacy bulbs). 0 is warmest.
func (s Schema) ColourTemp(temp int) (Values, error) {
	if err := between("colour temp", temp, 0, s.ColourTempMax); err != nil {
		return nil, err
	}
	return Values{s.ModeDP: string(ModeWhite), s.ColourTempDP: temp}, nil
}

// ColourTempPercent is ColourTemp as 0–100 %.
func (s Schema) ColourTempPercent(pct float64) (Values, error) {
	if pct < 0 || pct > 100 {
		return nil, fmt.Errorf("dp: colour temp percent must be 0-100, got %g", pct)
	}
	return s.ColourTemp(int(math.Round(float64(s.ColourTempMax) * pct / 100)))
}

// Timer sets the countdown, in seconds (0 cancels). When it expires the
// bulb toggles on/off.
func (s Schema) Timer(seconds int) (Values, error) {
	if err := between("timer seconds", seconds, 0, TimerMax); err != nil {
		return nil, err
	}
	return Values{s.TimerDP: seconds}, nil
}

// Scene switches to scene mode and writes sc (standard schema only: the
// legacy DP 6 format is undocumented).
func (s Schema) Scene(sc SceneValue) (Values, error) {
	if s.Legacy {
		return nil, errLegacy("scene")
	}
	h, err := sc.Hex()
	if err != nil {
		return nil, err
	}
	return Values{s.ModeDP: string(ModeScene), s.SceneDP: h}, nil
}

// RealTime writes one DP 28 ("real-time adjustment") value. Writing DP 28
// is what colour streaming uses; it does not need a mode write first.
func (s Schema) RealTime(a Adjust) (Values, error) {
	return s.adjust(Control, "real-time adjustment", a)
}

// MusicSync writes one DP 27 ("music sync") value.
func (s Schema) MusicSync(a Adjust) (Values, error) { return s.adjust(Music, "music sync", a) }

func (s Schema) adjust(id ID, what string, a Adjust) (Values, error) {
	if s.Legacy {
		return nil, errLegacy(what)
	}
	h, err := a.Hex()
	if err != nil {
		return nil, err
	}
	return Values{id: h}, nil
}

// DoNotDisturb sets DP 34.
func (s Schema) DoNotDisturb(on bool) (Values, error) {
	if s.Legacy {
		return nil, errLegacy("do not disturb")
	}
	return Values{DoNotDisturb: on}, nil
}

// Raw writes binary data b to a raw DP (30–33, 209, 210); build b with the
// Encode functions in this package.
func (s Schema) Raw(id ID, b []byte) (Values, error) {
	if s.Legacy {
		return nil, errLegacy("DP " + id.String())
	}
	if in, ok := Lookup(id); !ok || in.Kind != Raw {
		return nil, fmt.Errorf("dp: DP %d is not a raw DP", id)
	}
	return Values{id: RawValue(b)}, nil
}

func errLegacy(what string) error {
	return fmt.Errorf("dp: %s is not supported on legacy (DP 1-8) bulbs", what)
}

// legacyColourHex is tinytuya's "rgb8" format for DP 5:
// rrggbb 0hhh ssvv (hue 0–360, saturation and value 0–255).
func legacyColourHex(c RGB) string {
	h, s, v := rgbToHSV(float64(c.R)/255, float64(c.G)/255, float64(c.B)/255)
	return fmt.Sprintf("%02x%02x%02x0%03x%02x%02x", c.R, c.G, c.B, int(h*360), int(s*255), int(v*255))
}

// parseLegacyColour reads DP 5's HSV part, scaled to HSV's 0–1000.
func parseLegacyColour(s string) (HSV, error) {
	if len(s) != 14 {
		return HSV{}, fmt.Errorf("dp: legacy colour must be 14 hex chars, got %q", s)
	}
	f, err := hexFields(s[7:], 3, 2, 2)
	if err != nil {
		return HSV{}, err
	}
	c := HSV{f[0], f[1] * 1000 / 255, f[2] * 1000 / 255}
	return c, c.validate()
}
