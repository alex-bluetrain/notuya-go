package dp

import (
	"fmt"
	"strconv"
	"strings"
)

// ChangeMode is the leading digit of DPs 27, 28 and 29: how the bulb moves
// to the new colour.
type ChangeMode uint8

const (
	ChangeJump ChangeMode = 0 // Tuya: "jumping"
	ChangeFade ChangeMode = 1 // Tuya: "gradient"

	// DefaultChangeMode is used when a stream does not pick one.
	DefaultChangeMode = ChangeFade
)

// Valid reports whether m is one Tuya defines.
func (m ChangeMode) Valid() bool { return m == ChangeJump || m == ChangeFade }

// Ptr returns a pointer to a copy of m, for optional fields.
func (m ChangeMode) Ptr() *ChangeMode { return &m }

func (m ChangeMode) String() string {
	switch m {
	case ChangeJump:
		return "jump"
	case ChangeFade:
		return "fade"
	}
	return fmt.Sprintf("ChangeMode(%d)", uint8(m))
}

// ParseChangeMode parses "jump" or "fade".
func ParseChangeMode(s string) (ChangeMode, error) {
	switch s {
	case "jump":
		return ChangeJump, nil
	case "fade":
		return ChangeFade, nil
	}
	return 0, fmt.Errorf("dp: change mode must be jump or fade, got %q", s)
}

// Adjust is the value of DPs 27 (music sync), 28 (real-time adjustment)
// and 29 (gamma debug): "m hhhh ssss vvvv bbbb cccc".
//
// Brightness and ColourTemp drive the bulb's separate white channel. For
// colour streaming leave them zero: on the A60TY10W non-zero values change
// how the payload is read and can make the bulb ignore colour updates.
type Adjust struct {
	Mode       ChangeMode
	Colour     HSV
	Brightness int // 0–1000
	ColourTemp int // 0–1000
}

// Hex encodes a.
func (a Adjust) Hex() (string, error) {
	if !a.Mode.Valid() {
		return "", fmt.Errorf("dp: invalid change mode %v", a.Mode)
	}
	if err := a.Colour.validate(); err != nil {
		return "", err
	}
	if err := between("brightness", a.Brightness, 0, 1000); err != nil {
		return "", err
	}
	if err := between("colour temp", a.ColourTemp, 0, 1000); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x%04x%04x%04x%04x%04x", uint8(a.Mode), a.Colour.H, a.Colour.S, a.Colour.V, a.Brightness, a.ColourTemp), nil
}

// ParseAdjust decodes a DP 27/28/29 value.
func ParseAdjust(s string) (Adjust, error) {
	if len(s) != 21 {
		return Adjust{}, fmt.Errorf("dp: adjust value must be 21 hex chars, got %q", s)
	}
	f, err := hexFields(s[1:], 4, 4, 4, 4, 4)
	if err != nil {
		return Adjust{}, err
	}
	a := Adjust{Colour: HSV{f[0], f[1], f[2]}, Brightness: f[3], ColourTemp: f[4]}
	switch s[0] {
	case '0':
		a.Mode = ChangeJump
	case '1':
		a.Mode = ChangeFade
	default:
		return Adjust{}, fmt.Errorf("dp: invalid change mode in %q", s)
	}
	_, err = a.Hex()
	return a, err
}

// SceneTransition is how a scene moves into one of its units.
type SceneTransition uint8

const (
	SceneStatic   SceneTransition = 0
	SceneJump     SceneTransition = 1
	SceneGradient SceneTransition = 2
)

// SceneUnit is one step of a DP 25 scene: "tt dd mm hhhh ssss vvvv bbbb cccc".
// A colour unit sets Colour and leaves the white fields zero; a white unit
// does the opposite.
type SceneUnit struct {
	Interval   int // transition interval, 0–100
	Duration   int // how long the unit holds, 0–100
	Transition SceneTransition
	Colour     HSV
	Brightness int // 0–1000
	ColourTemp int // 0–1000
}

// SceneValue is a DP 25 value: a scene id and the units the bulb cycles
// through.
type SceneValue struct {
	ID    int // 0–255
	Units []SceneUnit
}

// Hex encodes s.
func (s SceneValue) Hex() (string, error) {
	if err := between("scene id", s.ID, 0, 255); err != nil {
		return "", err
	}
	if len(s.Units) == 0 {
		return "", fmt.Errorf("dp: scene %d has no units", s.ID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%02x", s.ID)
	for i, u := range s.Units {
		if err := u.validate(); err != nil {
			return "", fmt.Errorf("dp: scene unit %d: %w", i, err)
		}
		fmt.Fprintf(&b, "%02x%02x%02x%04x%04x%04x%04x%04x",
			u.Interval, u.Duration, uint8(u.Transition),
			u.Colour.H, u.Colour.S, u.Colour.V, u.Brightness, u.ColourTemp)
	}
	return b.String(), nil
}

func (u SceneUnit) validate() error {
	if err := between("interval", u.Interval, 0, 100); err != nil {
		return err
	}
	if err := between("duration", u.Duration, 0, 100); err != nil {
		return err
	}
	if u.Transition > SceneGradient {
		return fmt.Errorf("invalid transition %d", u.Transition)
	}
	if err := u.Colour.validate(); err != nil {
		return err
	}
	if err := between("brightness", u.Brightness, 0, 1000); err != nil {
		return err
	}
	return between("colour temp", u.ColourTemp, 0, 1000)
}

const sceneUnitLen = 26

// ParseScene decodes a DP 25 value.
func ParseScene(s string) (SceneValue, error) {
	if len(s) < 2+sceneUnitLen || (len(s)-2)%sceneUnitLen != 0 {
		return SceneValue{}, fmt.Errorf("dp: scene value has bad length %d", len(s))
	}
	id, err := hexFields(s[:2], 2)
	if err != nil {
		return SceneValue{}, err
	}
	v := SceneValue{ID: id[0]}
	for rest := s[2:]; rest != ""; rest = rest[sceneUnitLen:] {
		f, err := hexFields(rest[:sceneUnitLen], 2, 2, 2, 4, 4, 4, 4, 4)
		if err != nil {
			return SceneValue{}, err
		}
		u := SceneUnit{f[0], f[1], SceneTransition(f[2]), HSV{f[3], f[4], f[5]}, f[6], f[7]}
		if err := u.validate(); err != nil {
			return SceneValue{}, fmt.Errorf("dp: scene unit %d: %w", len(v.Units), err)
		}
		v.Units = append(v.Units, u)
	}
	return v, nil
}

// hexFields splits s into consecutive hex fields of the given widths.
func hexFields(s string, widths ...int) ([]int, error) {
	out := make([]int, len(widths))
	for i, w := range widths {
		if len(s) < w {
			return nil, fmt.Errorf("dp: hex value too short")
		}
		n, err := strconv.ParseUint(s[:w], 16, 32)
		if err != nil {
			return nil, fmt.Errorf("dp: bad hex field %q: %w", s[:w], err)
		}
		out[i], s = int(n), s[w:]
	}
	return out, nil
}

func between(what string, v, lo, hi int) error {
	if v < lo || v > hi {
		return fmt.Errorf("dp: %s must be %d-%d, got %d", what, lo, hi, v)
	}
	return nil
}
