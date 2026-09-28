package bulb

import (
	"context"

	"github.com/alex-bluetrain/notuya-go/pkg/device"
	"github.com/alex-bluetrain/notuya-go/pkg/protocol"
)

// Bulb is the high-level control surface for one Tuya bulb. Its commands
// always wait for the device's ack; the exception is StreamColours, which
// drives fire-and-forget music-mode sends through the underlying Device.
type Bulb struct {
	dev  *device.Device
	Name string
}

// NewBulb wraps an already-constructed protocol.Session (e.g.
// protocol35.NewSession) as a Bulb. The session must still be Open'd by the
// caller before use, and Closed when done — Bulb does not own its
// lifecycle, matching ctl.py's per-command open/command/close pattern.
func NewBulb(session protocol.Session, name string) *Bulb {
	return &Bulb{dev: device.NewDevice(session, name), Name: name}
}

// Raw exposes the underlying low-level Device for DPs not covered by a
// business method.
func (b *Bulb) Raw() *device.Device { return b.dev }

// Status probes connectivity — mirrors ctl.py's `bulb.status()` warm-up
// call, whose response it also never inspects.
func (b *Bulb) Status(ctx context.Context) error {
	_, err := b.dev.Status(ctx)
	return err
}

// TurnOn switches the bulb on.
func (b *Bulb) TurnOn(ctx context.Context) error {
	return b.dev.TurnOn(ctx, true)
}

// TurnOff switches the bulb off.
func (b *Bulb) TurnOff(ctx context.Context) error {
	return b.dev.TurnOff(ctx, true)
}

// SetColour switches the bulb to colour mode and sets the given RGB colour,
// mirroring tinytuya's set_colour.
func (b *Bulb) SetColour(ctx context.Context, r, g, bl uint8) error {
	return b.dev.SetColour(ctx, r, g, bl, true)
}

// SetColourBrightness adjusts the brightness of the current colour (the "v"
// of DP 24) as a 0-100 percentage without changing hue/saturation or leaving
// colour mode (see device.Device.SetColourBrightness).
func (b *Bulb) SetColourBrightness(ctx context.Context, pct float64) error {
	return b.dev.SetColourBrightness(ctx, pct, true)
}

// SetWhiteBrightness sets white-mode brightness as a 0-100 percentage,
// switching the bulb to white mode (see device.Device.SetWhiteBrightness).
func (b *Bulb) SetWhiteBrightness(ctx context.Context, pct float64) error {
	return b.dev.SetWhiteBrightness(ctx, pct, true)
}
