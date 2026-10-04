// Package dp is the presentation layer: what each Tuya lighting data point
// (DP) means, how its value is encoded, and the JSON envelope DPs travel
// in. It is the only package that knows DP numbers. It does no I/O — the
// bodies it builds and reads are carried by pkg/session.
//
// The source for everything here is PRIMITIVES.md (Tuya's "Function
// Definition of Lighting Products" page, plus tinytuya for the legacy DP
// 1–8 set).
package dp

import "strconv"

// ID is a DP number.
type ID int

func (id ID) String() string { return strconv.Itoa(int(id)) }

// Standard lighting DPs (Tuya's numbering, the "20+" set).
const (
	Switch         ID = 20  // switch_led
	Mode           ID = 21  // work_mode
	Brightness     ID = 22  // bright_value
	ColourTemp     ID = 23  // temp_value
	Colour         ID = 24  // colour_data
	Scene          ID = 25  // scene_data
	Timer          ID = 26  // countdown
	Music          ID = 27  // music_data
	Control        ID = 28  // control_data ("real-time adjustment")
	Debug          ID = 29  // debug_data ("gamma debug")
	Biorhythm      ID = 30  // rhythm_mode
	SleepMode      ID = 31  // sleep_mode ("light to sleep")
	WakeMode       ID = 32  // wakeup_mode ("light to wake")
	PowerMemory    ID = 33  // power_memory
	DoNotDisturb   ID = 34  // do_not_disturb
	CycleTiming    ID = 209 // cycle_timing
	VacationTiming ID = 210 // random_timing
)

// Access says whether a DP can be read back from a status report.
type Access uint8

const (
	ReadWrite Access = iota // Tuya: "send and report"
	WriteOnly               // Tuya: "send only"; never appears in a status
)

// Kind is a DP's JSON value type.
type Kind uint8

const (
	Bool   Kind = iota
	Enum        // JSON string from a fixed set
	Value       // JSON integer
	String      // JSON string with a DP-specific hex format
	Raw         // binary, carried as a base64 JSON string
)

// Info describes one standard DP.
type Info struct {
	ID         ID
	Identifier string
	Access     Access
	Kind       Kind
	Min, Max   int // for Value DPs
}

// Table lists the standard lighting DPs, as PRIMITIVES.md states them.
var Table = []Info{
	{Switch, "switch_led", ReadWrite, Bool, 0, 0},
	{Mode, "work_mode", ReadWrite, Enum, 0, 0},
	{Brightness, "bright_value", ReadWrite, Value, 10, 1000},
	{ColourTemp, "temp_value", ReadWrite, Value, 0, 1000},
	{Colour, "colour_data", ReadWrite, String, 0, 0},
	{Scene, "scene_data", ReadWrite, String, 0, 0},
	{Timer, "countdown", ReadWrite, Value, 0, TimerMax},
	{Music, "music_data", WriteOnly, String, 0, 0},
	{Control, "control_data", WriteOnly, String, 0, 0},
	{Debug, "debug_data", WriteOnly, String, 0, 0},
	{Biorhythm, "rhythm_mode", ReadWrite, Raw, 0, 0},
	{SleepMode, "sleep_mode", ReadWrite, Raw, 0, 0},
	{WakeMode, "wakeup_mode", ReadWrite, Raw, 0, 0},
	{PowerMemory, "power_memory", ReadWrite, Raw, 0, 0},
	{DoNotDisturb, "do_not_disturb", ReadWrite, Bool, 0, 0},
	{CycleTiming, "cycle_timing", ReadWrite, Raw, 0, 0},
	{VacationTiming, "random_timing", ReadWrite, Raw, 0, 0},
}

// Lookup returns the standard DP's description.
func Lookup(id ID) (Info, bool) {
	for _, in := range Table {
		if in.ID == id {
			return in, true
		}
	}
	return Info{}, false
}

// TimerMax is the longest countdown, in seconds (shown as 23:59).
const TimerMax = 86400

// WorkMode is a DP 21 (or legacy DP 2) value.
type WorkMode string

const (
	ModeWhite  WorkMode = "white"
	ModeColour WorkMode = "colour"
	ModeScene  WorkMode = "scene"
	ModeMusic  WorkMode = "music"
)

// ParseMode normalises a mode read from a bulb. Tuya's page spells colour
// mode "color"; bulbs (and tinytuya) use "colour". Both decode to
// ModeColour, and ModeColour is what gets sent.
func ParseMode(s string) WorkMode {
	if s == "color" {
		return ModeColour
	}
	return WorkMode(s)
}
