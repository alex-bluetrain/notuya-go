package dp

import (
	"encoding/base64"
	"fmt"
)

// The advanced DPs (30–33, 209, 210) are raw binary. On the wire a raw DP
// is a base64 JSON string; Encode/Parse below work on the decoded bytes,
// and RawValue/ParseRaw convert to and from the wire form.
//
// Byte layouts are transcribed from Tuya's "Function Definition of Lighting
// Products" page. None of them could be tested against a real bulb (the
// A60TY10W does not report these DPs), only against the page's own field
// tables.

// RawValue is the JSON value for raw bytes b.
func RawValue(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// ParseRaw decodes a raw DP's JSON value.
func ParseRaw(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("dp: raw value is not base64: %w", err)
	}
	return b, nil
}

// Weekdays is a schedule's weekday bitmask: bit0 Sunday … bit6 Saturday.
// Zero means "run once".
type Weekdays uint8

const (
	Sunday Weekdays = 1 << iota
	Monday
	Tuesday
	Wednesday
	Thursday
	Friday
	Saturday

	EveryDay = Sunday | Monday | Tuesday | Wednesday | Thursday | Friday | Saturday
)

func (w Weekdays) validate() error {
	if w&^EveryDay != 0 {
		return fmt.Errorf("dp: weekday mask %#x sets the reserved bit", uint8(w))
	}
	return nil
}

// NodeColour is the colour a schedule node sets. Every field except Hue is
// a percentage (0–100), not the 0–1000 of the string DPs.
type NodeColour struct {
	Hue        int // 0–360
	Saturation int
	Value      int
	Brightness int
	ColourTemp int
}

func (c NodeColour) validate() error {
	if err := between("hue", c.Hue, 0, 360); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		v    int
	}{{"saturation", c.Saturation}, {"value", c.Value}, {"brightness", c.Brightness}, {"colour temp", c.ColourTemp}} {
		if err := between(f.name, f.v, 0, 100); err != nil {
			return err
		}
	}
	return nil
}

// put appends c as hue(2, "hundreds" then "tens and ones") s v b t.
func (c NodeColour) put(b []byte) []byte {
	return append(b, byte(c.Hue/100), byte(c.Hue%100),
		byte(c.Saturation), byte(c.Value), byte(c.Brightness), byte(c.ColourTemp))
}

const nodeColourLen = 6

func getNodeColour(b []byte) NodeColour {
	return NodeColour{int(b[0])*100 + int(b[1]), int(b[2]), int(b[3]), int(b[4]), int(b[5])}
}

func flag(b bool) byte {
	if b {
		return 1
	}
	return 0
}

func u16(b []byte) int { return int(b[0])<<8 | int(b[1]) }

func putU16(b []byte, v int) []byte { return append(b, byte(v>>8), byte(v)) }

// --- DP 30: biorhythm ---

// RhythmNode is one time node of a biorhythm.
type RhythmNode struct {
	On     bool
	Hour   int // 0–23
	Minute int // 0–59
	Colour NodeColour
}

// Rhythm is DP 30: the light fades between up to 8 time nodes.
type Rhythm struct {
	On bool
	// Gradient is 0 to fade over the whole gap between nodes, otherwise how
	// many minutes (at least 15) the fade takes once a node is reached.
	Gradient int
	Days     Weekdays
	Nodes    []RhythmNode
}

const rhythmNodeLen = 3 + nodeColourLen

// Encode returns DP 30's bytes (format version 0).
func (r Rhythm) Encode() ([]byte, error) {
	if len(r.Nodes) < 1 || len(r.Nodes) > 8 {
		return nil, fmt.Errorf("dp: biorhythm needs 1-8 nodes, got %d", len(r.Nodes))
	}
	if r.Gradient != 0 {
		if err := between("biorhythm gradient minutes", r.Gradient, 15, 255); err != nil {
			return nil, err
		}
	}
	if err := r.Days.validate(); err != nil {
		return nil, err
	}
	b := []byte{0, flag(r.On), byte(r.Gradient), byte(r.Days), byte(len(r.Nodes))}
	for i, n := range r.Nodes {
		if err := validTime(n.Hour, n.Minute); err != nil {
			return nil, fmt.Errorf("dp: biorhythm node %d: %w", i, err)
		}
		if err := n.Colour.validate(); err != nil {
			return nil, fmt.Errorf("dp: biorhythm node %d: %w", i, err)
		}
		b = n.Colour.put(append(b, flag(n.On), byte(n.Hour), byte(n.Minute)))
	}
	return b, nil
}

// ParseRhythm decodes DP 30 (format version 0 only).
func ParseRhythm(b []byte) (Rhythm, error) {
	if len(b) < 5 || b[0] != 0 {
		return Rhythm{}, fmt.Errorf("dp: unsupported biorhythm data (version or length)")
	}
	n := int(b[4])
	if len(b) != 5+n*rhythmNodeLen {
		return Rhythm{}, fmt.Errorf("dp: biorhythm: %d nodes need %d bytes, got %d", n, 5+n*rhythmNodeLen, len(b))
	}
	r := Rhythm{On: b[1] != 0, Gradient: int(b[2]), Days: Weekdays(b[3])}
	for p := b[5:]; len(p) > 0; p = p[rhythmNodeLen:] {
		r.Nodes = append(r.Nodes, RhythmNode{p[0] != 0, int(p[1]), int(p[2]), getNodeColour(p[3:])})
	}
	return r, nil
}

// --- DPs 31 and 32: light to sleep / light to wake ---

// FadeNode is one schedule of DP 31 (dim to sleep) or DP 32 (brighten to
// wake).
type FadeNode struct {
	On     bool
	Days   Weekdays
	Steps  int // fade length in 5-minute steps, 1–24
	Hour   int
	Minute int
	Colour NodeColour
	// StayOn (DP 32 only) is how long, in 5-minute steps (0–24), the light
	// stays on at full brightness; 0 means it stays on.
	StayOn int
}

func encodeFade(name string, nodes []FadeNode, wake bool) ([]byte, error) {
	if len(nodes) < 1 || len(nodes) > 4 {
		return nil, fmt.Errorf("dp: %s needs 1-4 schedules, got %d", name, len(nodes))
	}
	b := []byte{0, byte(len(nodes))}
	for i, n := range nodes {
		err := n.Days.validate()
		if err == nil {
			err = between("steps", n.Steps, 1, 24)
		}
		if err == nil {
			err = validTime(n.Hour, n.Minute)
		}
		if err == nil {
			err = n.Colour.validate()
		}
		if err == nil && wake {
			err = between("stay-on steps", n.StayOn, 0, 24)
		}
		if err == nil && !wake && n.StayOn != 0 {
			err = fmt.Errorf("stay-on is only for light to wake")
		}
		if err != nil {
			return nil, fmt.Errorf("dp: %s schedule %d: %w", name, i, err)
		}
		b = n.Colour.put(append(b, flag(n.On), byte(n.Days), byte(n.Steps), byte(n.Hour), byte(n.Minute)))
		if wake {
			b = append(b, byte(n.StayOn))
		}
	}
	return b, nil
}

func parseFade(name string, b []byte, wake bool) ([]FadeNode, error) {
	size := 5 + nodeColourLen
	if wake {
		size++
	}
	if len(b) < 2 || b[0] != 0 || len(b) != 2+int(b[1])*size {
		return nil, fmt.Errorf("dp: unsupported %s data (version or length)", name)
	}
	var out []FadeNode
	for p := b[2:]; len(p) > 0; p = p[size:] {
		n := FadeNode{On: p[0] != 0, Days: Weekdays(p[1]), Steps: int(p[2]), Hour: int(p[3]), Minute: int(p[4]), Colour: getNodeColour(p[5:])}
		if wake {
			n.StayOn = int(p[5+nodeColourLen])
		}
		out = append(out, n)
	}
	return out, nil
}

// EncodeSleep returns DP 31's bytes.
func EncodeSleep(nodes []FadeNode) ([]byte, error) { return encodeFade("light to sleep", nodes, false) }

// ParseSleep decodes DP 31.
func ParseSleep(b []byte) ([]FadeNode, error) { return parseFade("light to sleep", b, false) }

// EncodeWake returns DP 32's bytes.
func EncodeWake(nodes []FadeNode) ([]byte, error) { return encodeFade("light to wake", nodes, true) }

// ParseWake decodes DP 32.
func ParseWake(b []byte) ([]FadeNode, error) { return parseFade("light to wake", b, true) }

// --- DP 33: power-off memory ---

// MemoryMode is what a bulb does when power returns.
type MemoryMode uint8

const (
	MemoryDefault MemoryMode = 0 // the bulb's initial state
	MemoryLast    MemoryMode = 1 // the state before the power cut
	MemoryCustom  MemoryMode = 2 // the Colour/Brightness/ColourTemp below
)

// PowerMemoryValue is DP 33. The custom-state fields are 0–1000 (hue
// 0–360), like the string DPs.
type PowerMemoryValue struct {
	Mode       MemoryMode
	Colour     HSV
	Brightness int
	ColourTemp int
}

// Encode returns DP 33's bytes.
func (m PowerMemoryValue) Encode() ([]byte, error) {
	if m.Mode > MemoryCustom {
		return nil, fmt.Errorf("dp: invalid power-off memory mode %d", m.Mode)
	}
	if err := m.Colour.validate(); err != nil {
		return nil, err
	}
	if err := between("brightness", m.Brightness, 0, 1000); err != nil {
		return nil, err
	}
	if err := between("colour temp", m.ColourTemp, 0, 1000); err != nil {
		return nil, err
	}
	b := []byte{0, byte(m.Mode)}
	for _, v := range []int{m.Colour.H, m.Colour.S, m.Colour.V, m.Brightness, m.ColourTemp} {
		b = putU16(b, v)
	}
	return b, nil
}

// ParsePowerMemory decodes DP 33.
func ParsePowerMemory(b []byte) (PowerMemoryValue, error) {
	if len(b) != 12 || b[0] != 0 {
		return PowerMemoryValue{}, fmt.Errorf("dp: unsupported power-off memory data (version or length)")
	}
	return PowerMemoryValue{
		Mode:       MemoryMode(b[1]),
		Colour:     HSV{u16(b[2:]), u16(b[4:]), u16(b[6:])},
		Brightness: u16(b[8:]),
		ColourTemp: u16(b[10:]),
	}, nil
}

// --- DPs 209 and 210: cycle and vacation timing ---

// TimingNode is one node of DP 209 (cycle timing) or DP 210 (vacation
// timing). Times are minutes since midnight, 0–1439.
type TimingNode struct {
	On       bool
	Channels uint8 // channel bits, 0–127
	Days     Weekdays
	Start    int
	End      int
	// OnFor and OffFor (DP 209 only) are the minutes of each on/off phase.
	OnFor  int
	OffFor int
	Colour NodeColour
}

func encodeTiming(name string, nodes []TimingNode, cycle bool) ([]byte, error) {
	size := 6 + nodeColourLen
	if cycle {
		size += 4
	}
	b := []byte{0, byte(size)}
	for i, n := range nodes {
		err := n.Days.validate()
		if err == nil && n.Channels > 0x7f {
			err = fmt.Errorf("channel mask %#x has more than 7 bits", n.Channels)
		}
		for _, t := range []int{n.Start, n.End, n.OnFor, n.OffFor} {
			if err == nil {
				err = between("minutes", t, 0, 1439)
			}
		}
		if err == nil && !cycle && (n.OnFor != 0 || n.OffFor != 0) {
			err = fmt.Errorf("on/off durations are only for cycle timing")
		}
		if err == nil {
			err = n.Colour.validate()
		}
		if err != nil {
			return nil, fmt.Errorf("dp: %s node %d: %w", name, i, err)
		}
		b = append(b, n.Channels<<1|flag(n.On), byte(n.Days))
		b = putU16(putU16(b, n.Start), n.End)
		if cycle {
			b = putU16(putU16(b, n.OnFor), n.OffFor)
		}
		b = n.Colour.put(b)
	}
	return b, nil
}

func parseTiming(name string, b []byte, cycle bool) ([]TimingNode, error) {
	size := 6 + nodeColourLen
	if cycle {
		size += 4
	}
	if len(b) < 2 || b[0] != 0 || int(b[1]) != size || (len(b)-2)%size != 0 {
		return nil, fmt.Errorf("dp: unsupported %s data (version or length)", name)
	}
	var out []TimingNode
	for p := b[2:]; len(p) > 0; p = p[size:] {
		n := TimingNode{On: p[0]&1 != 0, Channels: p[0] >> 1, Days: Weekdays(p[1]), Start: u16(p[2:]), End: u16(p[4:])}
		c := p[6:]
		if cycle {
			n.OnFor, n.OffFor, c = u16(p[6:]), u16(p[8:]), p[10:]
		}
		n.Colour = getNodeColour(c)
		out = append(out, n)
	}
	return out, nil
}

// EncodeCycle returns DP 209's bytes.
func EncodeCycle(nodes []TimingNode) ([]byte, error) {
	return encodeTiming("cycle timing", nodes, true)
}

// ParseCycle decodes DP 209.
func ParseCycle(b []byte) ([]TimingNode, error) { return parseTiming("cycle timing", b, true) }

// EncodeVacation returns DP 210's bytes.
func EncodeVacation(nodes []TimingNode) ([]byte, error) {
	return encodeTiming("vacation timing", nodes, false)
}

// ParseVacation decodes DP 210.
func ParseVacation(b []byte) ([]TimingNode, error) { return parseTiming("vacation timing", b, false) }

func validTime(h, m int) error {
	if err := between("hour", h, 0, 23); err != nil {
		return err
	}
	return between("minute", m, 0, 59)
}
