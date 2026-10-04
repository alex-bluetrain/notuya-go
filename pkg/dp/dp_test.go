package dp

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The three examples on Tuya's page (PRIMITIVES.md "Formats").
func TestTuyaDocumentedExamples(t *testing.T) {
	c, err := ParseHSV("00DC004B004E")
	if err != nil || c != (HSV{220, 75, 78}) {
		t.Fatalf("ParseHSV = %+v, %v; want {220 75 78}", c, err)
	}
	if h, _ := c.Hex(); h != "00dc004b004e" {
		t.Errorf("HSV.Hex = %q", h)
	}

	sc, err := ParseScene("010b0a02000003e803e800000000")
	want := SceneValue{1, []SceneUnit{{11, 10, SceneGradient, HSV{0, 1000, 1000}, 0, 0}}}
	if err != nil || !reflect.DeepEqual(sc, want) {
		t.Fatalf("ParseScene = %+v, %v; want %+v", sc, err, want)
	}
	if h, _ := sc.Hex(); h != "010b0a02000003e803e800000000" {
		t.Errorf("SceneValue.Hex = %q", h)
	}

	a, err := ParseAdjust("1007603e803e800120025")
	wantA := Adjust{ChangeFade, HSV{118, 1000, 1000}, 18, 37}
	if err != nil || a != wantA {
		t.Fatalf("ParseAdjust = %+v, %v; want %+v", a, err, wantA)
	}
	if h, _ := a.Hex(); h != "1007603e803e800120025" {
		t.Errorf("Adjust.Hex = %q", h)
	}
}

// Reference vectors from tinytuya's rgb_to_hexvalue(r, g, b, 'hsv16').
func TestHSVFromRGBMatchesTinytuya(t *testing.T) {
	cases := []struct {
		c    RGB
		want string
	}{
		{RGB{255, 0, 0}, "000003e803e8"},
		{RGB{0, 255, 0}, "007803e803e8"},
		{RGB{0, 0, 255}, "00f003e803e8"},
		{RGB{255, 255, 255}, "0000000003e8"},
		{RGB{0, 0, 0}, "000000000000"},
		{RGB{255, 136, 0}, "002003e803e8"},
		{RGB{18, 52, 86}, "00d203160151"},
		{RGB{1, 2, 3}, "00d2029a000b"},
	}
	for _, c := range cases {
		if got, _ := HSVFromRGB(c.c).Hex(); got != c.want {
			t.Errorf("%v: got %q, want %q", c.c, got, c.want)
		}
	}
	for _, c := range []RGB{{255, 0, 0}, {0, 0, 255}, {255, 255, 255}} {
		if got := HSVFromRGB(c).RGB(); got != c {
			t.Errorf("round trip %v -> %v", c, got)
		}
	}
}

func TestHSVToRGB(t *testing.T) {
	cases := []struct {
		in   HSV
		want RGB
	}{
		{HSV{0, 0, 1000}, RGB{255, 255, 255}},
		{HSV{0, 1000, 1000}, RGB{255, 0, 0}},
		{HSV{120, 1000, 1000}, RGB{0, 255, 0}},
		{HSV{240, 1000, 1000}, RGB{0, 0, 255}},
		{HSV{360, 1000, 1000}, RGB{255, 0, 0}}, // 360° wraps to red
		{HSV{0, 0, 0}, RGB{0, 0, 0}},
		{HSV{30, 1000, 500}, RGB{128, 64, 0}}, // rounds, not truncates
	}
	for _, c := range cases {
		if got := c.in.RGB(); got != c.want {
			t.Errorf("%+v.RGB() = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestRealTimeStreamPayload(t *testing.T) {
	v, err := Schema20.RealTime(Adjust{Mode: ChangeJump, Colour: HSVFromRGB(RGB{255, 0, 0})})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v, Values{Control: "0000003e803e800000000"}) {
		t.Errorf("got %v", v)
	}
	if _, err := Schema20.RealTime(Adjust{Mode: 2}); err == nil {
		t.Error("change mode 2 accepted")
	}
}

func TestRangesAreEnforced(t *testing.T) {
	bad := []struct {
		name string
		err  error
	}{
		{"brightness 0", err2(Schema20.White(0))},
		{"brightness 9", err2(Schema20.White(9))},
		{"brightness 1001", err2(Schema20.White(1001))},
		{"temp -1", err2(Schema20.ColourTemp(-1))},
		{"temp 1001", err2(Schema20.ColourTemp(1001))},
		{"timer 86401", err2(Schema20.Timer(TimerMax + 1))},
		{"hue 361", err2(Schema20.ColourHSV(HSV{361, 0, 0}))},
		{"sat 1001", err2(Schema20.ColourHSV(HSV{0, 1001, 0}))},
		{"interval 101", err2(Schema20.Scene(SceneValue{1, []SceneUnit{{Interval: 101}}}))},
		{"transition 3", err2(Schema20.Scene(SceneValue{1, []SceneUnit{{Transition: 3}}}))},
		{"empty scene", err2(Schema20.Scene(SceneValue{ID: 1}))},
		{"legacy brightness 24", err2(Schema1.White(24))},
		{"legacy temp 256", err2(Schema1.ColourTemp(256))},
		{"legacy scene", err2(Schema1.Scene(SceneValue{1, []SceneUnit{{}}}))},
		{"not raw", err2(Schema20.Raw(Brightness, nil))},
	}
	for _, b := range bad {
		if b.err == nil {
			t.Errorf("%s: accepted", b.name)
		}
	}
	for _, ok := range []error{
		err2(Schema20.White(10)), err2(Schema20.White(1000)),
		err2(Schema20.ColourTemp(0)), err2(Schema20.Timer(0)),
		err2(Schema1.White(25)), err2(Schema1.White(255)),
	} {
		if ok != nil {
			t.Errorf("boundary rejected: %v", ok)
		}
	}
}

func err2[T any](_ T, err error) error { return err }

func TestWhitePercentClampsToMinimum(t *testing.T) {
	for pct, want := range map[float64]int{0: 10, 0.5: 10, 1: 10, 50: 500, 100: 1000} {
		v, err := Schema20.WhitePercent(pct)
		if err != nil || v[Brightness] != want || v[Mode] != "white" {
			t.Errorf("WhitePercent(%g) = %v, %v; want brightness %d", pct, v, err, want)
		}
	}
	if v, _ := Schema1.WhitePercent(0); v[3] != 25 {
		t.Errorf("legacy WhitePercent(0) = %v", v)
	}
}

func TestBodyShape(t *testing.T) {
	b, err := Body(Schema20.Power(true).Merge(Values{Timer: 60}))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Protocol int
		T        int64
		Data     struct{ DPS map[string]any }
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Protocol != 5 || got.T == 0 || got.Data.DPS["20"] != true || got.Data.DPS["26"] != float64(60) {
		t.Errorf("body = %s", b)
	}
	if _, err := Body(nil); err == nil {
		t.Error("empty body accepted")
	}
}

// A real A60TY10W query reply (testdata/handshake fixture).
func TestDecodeQueryReply(t *testing.T) {
	body := `{"dps":{"20":true,"21":"colour","22":1000,"23":0,"24":"000003e803e8","25":"000e0d0000000000000000c80000","26":0,"34":false}}`
	st, err := Decode([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if st.Schema.Legacy || !st.On || st.Mode != ModeColour || st.Brightness != 1000 ||
		st.Colour != (HSV{0, 1000, 1000}) || st.Scene.ID != 0 || st.Has(Control) {
		t.Errorf("state = %+v", st)
	}
	if !reflect.DeepEqual(st.IDs(), []ID{20, 21, 22, 23, 24, 25, 26, 34}) {
		t.Errorf("IDs = %v", st.IDs())
	}
	if st.BrightnessPercent() != 100 {
		t.Errorf("percent = %g", st.BrightnessPercent())
	}
}

func TestDecodePushAndSpellings(t *testing.T) {
	st, err := Decode([]byte(`{"protocol":4,"t":1,"data":{"dps":{"21":"color"}}}`))
	if err != nil || st.Mode != ModeColour || st.Has(Switch) || !st.Has(Mode) {
		t.Errorf("push: %+v, %v", st, err)
	}
	if _, err := Decode([]byte(`{"dps":{"24":"zz"}}`)); err == nil {
		t.Error("bad colour accepted")
	}
	if _, err := Decode([]byte(`{"dps":{"22":"high"}}`)); err == nil {
		t.Error("bad brightness type accepted")
	}
}

func TestLegacySchema(t *testing.T) {
	st, err := Decode([]byte(`{"dps":{"1":true,"2":"colour","3":255,"5":"ff00000000ffff"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Schema.Legacy || !st.On || st.Brightness != 255 || st.Colour != (HSV{0, 1000, 1000}) {
		t.Errorf("legacy state = %+v", st)
	}
	v := Schema1.Colour(RGB{255, 0, 0})
	if !reflect.DeepEqual(v, Values{2: "colour", 5: "ff00000000ffff"}) {
		t.Errorf("legacy colour = %v", v)
	}
}

func TestRawCodecsRoundTrip(t *testing.T) {
	col := NodeColour{Hue: 255, Saturation: 80, Value: 90, Brightness: 0, ColourTemp: 0}

	r := Rhythm{On: true, Gradient: 0, Days: EveryDay, Nodes: []RhythmNode{{true, 7, 30, col}, {true, 22, 0, NodeColour{Brightness: 20, ColourTemp: 10}}}}
	b, err := r.Encode()
	if err != nil {
		t.Fatal(err)
	}
	// version, on, gradient, days, count, then node 1: on 7:30 hue 2|55 80 90 0 0
	if !bytes.HasPrefix(b, []byte{0, 1, 0, 0x7f, 2, 1, 7, 30, 2, 55, 80, 90, 0, 0}) {
		t.Errorf("rhythm bytes = % x", b)
	}
	if got, err := ParseRhythm(b); err != nil || !reflect.DeepEqual(got, r) {
		t.Errorf("rhythm round trip = %+v, %v", got, err)
	}
	if _, err := (Rhythm{Gradient: 5, Nodes: r.Nodes}).Encode(); err == nil {
		t.Error("gradient below 15 accepted")
	}

	sleep := []FadeNode{{On: true, Days: Monday | Friday, Steps: 6, Hour: 23, Minute: 15, Colour: col}}
	if b, err := EncodeSleep(sleep); err != nil {
		t.Fatal(err)
	} else if got, err := ParseSleep(b); err != nil || !reflect.DeepEqual(got, sleep) {
		t.Errorf("sleep round trip = %+v, %v", got, err)
	}
	wake := []FadeNode{{On: true, Days: Saturday, Steps: 3, Hour: 6, Colour: col, StayOn: 12}}
	if b, err := EncodeWake(wake); err != nil {
		t.Fatal(err)
	} else if got, err := ParseWake(b); err != nil || !reflect.DeepEqual(got, wake) {
		t.Errorf("wake round trip = %+v, %v", got, err)
	}
	if _, err := EncodeSleep([]FadeNode{{Steps: 1, StayOn: 1}}); err == nil {
		t.Error("stay-on accepted for sleep")
	}

	pm := PowerMemoryValue{MemoryCustom, HSV{120, 1000, 500}, 0, 0}
	if b, err := pm.Encode(); err != nil {
		t.Fatal(err)
	} else if got, err := ParsePowerMemory(b); err != nil || got != pm {
		t.Errorf("power memory round trip = %+v, %v", got, err)
	}

	cyc := []TimingNode{{On: true, Channels: 1, Days: EveryDay, Start: 480, End: 1200, OnFor: 30, OffFor: 15, Colour: col}}
	if b, err := EncodeCycle(cyc); err != nil {
		t.Fatal(err)
	} else if got, err := ParseCycle(b); err != nil || !reflect.DeepEqual(got, cyc) {
		t.Errorf("cycle round trip = %+v, %v", got, err)
	}
	vac := []TimingNode{{On: true, Days: Sunday, Start: 1080, End: 1380, Colour: col}}
	if b, err := EncodeVacation(vac); err != nil {
		t.Fatal(err)
	} else if got, err := ParseVacation(b); err != nil || !reflect.DeepEqual(got, vac) {
		t.Errorf("vacation round trip = %+v, %v", got, err)
	}
	if _, err := EncodeVacation([]TimingNode{{OnFor: 1}}); err == nil {
		t.Error("on/off duration accepted for vacation")
	}

	v, err := Schema20.Raw(PowerMemory, []byte{0, 1})
	if err != nil || v[PowerMemory] != "AAE=" {
		t.Errorf("Raw = %v, %v", v, err)
	}
	if got, _ := ParseRaw("AAE="); !bytes.Equal(got, []byte{0, 1}) {
		t.Errorf("ParseRaw = % x", got)
	}
}

func TestTableMatchesSpec(t *testing.T) {
	for _, id := range []ID{Music, Control, Debug} {
		if in, _ := Lookup(id); in.Access != WriteOnly {
			t.Errorf("DP %d should be write-only", id)
		}
	}
	if in, _ := Lookup(Brightness); in.Min != 10 || in.Max != 1000 {
		t.Errorf("brightness range = %d-%d", in.Min, in.Max)
	}
	if _, ok := Lookup(99); ok {
		t.Error("unknown DP found")
	}
	if !strings.Contains(ChangeFade.String(), "fade") {
		t.Error("ChangeMode.String")
	}
}
