package bulb

import (
	"context"

	"github.com/averstraeten/notuya-go/pkg/device"
	"github.com/averstraeten/notuya-go/pkg/protocol"
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

// SetBrightnessPercent sets brightness as a 0-100 percentage, preserving an
// in-progress colour when the bulb is in colour mode (see
// device.Device.SetBrightnessPercent).
func (b *Bulb) SetBrightnessPercent(ctx context.Context, pct float64) error {
	return b.dev.SetBrightnessPercent(ctx, pct, true)
}
