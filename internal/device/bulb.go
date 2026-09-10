package device

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/averstraeten/notuya-go/internal/protocol"
)

// DP ids for the known target hardware (Tuya bulb "type B" / hsv16 colour
// format — model A60TY10W, see FINDINGS.md / devices.json in the sibling
// Python project).
const (
	dpSwitch     = "20"
	dpMode       = "21"
	dpBrightness = "22"
	dpColour     = "24"
)

const (
	modeWhite  = "white"
	modeColour = "colour"

	brightnessMax = 1000 // dpBrightness / hsv16 "v" component full scale
)

// Bulb is the high-level control surface for one Tuya bulb — TurnOn,
// TurnOff, SetColour, SetBrightnessPercent, Status — the Phase A
// equivalent of the Python project's ctl.py commands.
type Bulb struct {
	client *client
	Name   string
}

// NewBulb wraps an already-constructed protocol.Session (e.g.
// protocol35.NewSession) as a Bulb. The session must still be Open'd by
// the caller before use, and Closed when done — Bulb does not own its
// lifecycle, matching ctl.py's per-command open/command/close pattern.
func NewBulb(session protocol.Session, name string) *Bulb {
	return &Bulb{client: newClient(session), Name: name}
}

// Status probes connectivity — mirrors ctl.py's `bulb.status()` warm-up
// call, whose response it also never inspects.
func (b *Bulb) Status(ctx context.Context) error {
	_, err := b.client.query(ctx)
	return err
}

// TurnOn switches the bulb on.
func (b *Bulb) TurnOn(ctx context.Context) error {
	return b.client.control(ctx, map[string]any{dpSwitch: true})
}

// TurnOff switches the bulb off.
func (b *Bulb) TurnOff(ctx context.Context) error {
	return b.client.control(ctx, map[string]any{dpSwitch: false})
}

// SetColour switches the bulb to colour mode and sets the given RGB
// colour, mirroring tinytuya's set_colour.
func (b *Bulb) SetColour(ctx context.Context, r, g, bl uint8) error {
	return b.client.control(ctx, map[string]any{
		dpMode:   modeColour,
		dpColour: rgbToHSV16Hex(r, g, bl),
	})
}

// SetBrightnessPercent sets brightness as a 0-100 percentage. Mirrors
// tinytuya's set_brightness_percentage/set_brightness, which is
// mode-dependent rather than a single flat DP write: it reads the
// device's current mode+colour first, and
//   - in colour mode, keeps the current hue/saturation and only changes
//     the "v" component of DP 24 (so an in-progress colour is preserved),
//   - otherwise, falls back to a plain white-mode brightness write on
//     DP 22 (colour temperature is left untouched — the device keeps its
//     prior value for any DP not present in a CONTROL_NEW write).
func (b *Bulb) SetBrightnessPercent(ctx context.Context, pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("device: brightness percent must be 0-100, got %d", pct)
	}
	value := (brightnessMax * pct) / 100

	dps, err := b.client.query(ctx)
	if err != nil {
		return fmt.Errorf("device: reading current state for brightness: %w", err)
	}

	mode := modeWhite
	if raw, ok := dps[dpMode]; ok {
		_ = json.Unmarshal(raw, &mode)
	}

	if mode == modeColour {
		if raw, ok := dps[dpColour]; ok {
			var hex string
			if err := json.Unmarshal(raw, &hex); err == nil {
				if h, s, _, err := parseHSV16Hex(hex); err == nil {
					return b.client.control(ctx, map[string]any{
						dpColour: hsv16Hex(h, s, value),
					})
				}
			}
		}
	}

	return b.client.control(ctx, map[string]any{
		dpMode:       modeWhite,
		dpBrightness: value,
	})
}
