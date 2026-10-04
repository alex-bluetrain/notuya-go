package bulb

import (
	"context"
	"fmt"

	"github.com/alex-bluetrain/notuya-go/pkg/dp"
	"github.com/alex-bluetrain/notuya-go/pkg/session"
)

// Bulb is one Tuya bulb driven over an open session.
//
// The session's lifecycle (open, close) belongs to the caller. Bulb methods
// are safe for concurrent use as far as the session is; at most one Watch
// may run at a time, because a session has a single push stream.
type Bulb struct {
	Name string
	sess session.Session
}

// New wraps an open session.
func New(sess session.Session, name string) *Bulb {
	return &Bulb{Name: name, sess: sess}
}

// Session returns the session the bulb was created with.
func (b *Bulb) Session() session.Session { return b.sess }

func (b *Bulb) errorf(what string, err error) error {
	return fmt.Errorf("bulb: %s: %s: %w", b.Name, what, err)
}

// Status queries the bulb's current state.
func (b *Bulb) Status(ctx context.Context) (dp.State, error) {
	body, err := b.sess.Query(ctx)
	if err != nil {
		return dp.State{}, b.errorf("status", err)
	}
	st, err := dp.Decode(body)
	if err != nil {
		return dp.State{}, b.errorf("status", err)
	}
	return st, nil
}

// Capabilities lists the DPs the bulb reports in its status. Write-only
// DPs (27, 28, …) never appear: a bulb does not say whether it accepts them.
func (b *Bulb) Capabilities(ctx context.Context) ([]dp.ID, error) {
	st, err := b.Status(ctx)
	if err != nil {
		return nil, err
	}
	return st.IDs(), nil
}

// Refresh asks the bulb to re-report the given DPs. The answer arrives as
// a push, so it is seen through Watch, not returned here.
func (b *Bulb) Refresh(ctx context.Context, ids ...dp.ID) error {
	n := make([]int, len(ids))
	for i, id := range ids {
		n[i] = int(id)
	}
	if err := b.sess.Refresh(ctx, n); err != nil {
		return b.errorf("refresh", err)
	}
	return nil
}

// Watch delivers the state changes the bulb pushes — whether caused by
// this session, another controller or the bulb itself — until ctx is
// cancelled or the session ends. Each State carries only the DPs that
// push reported. Pushes this package cannot decode are skipped.
func (b *Bulb) Watch(ctx context.Context) <-chan dp.State {
	out := make(chan dp.State, 1)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case p, ok := <-b.sess.Pushes():
				if !ok {
					return
				}
				st, err := dp.Decode(p.Body)
				if err != nil || len(st.Raw) == 0 {
					continue
				}
				select {
				case out <- st:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// Set writes vs and waits for the bulb to acknowledge. It is the escape
// hatch for DPs the named methods do not cover.
func (b *Bulb) Set(ctx context.Context, vs dp.Values) error {
	return b.write(ctx, "set", vs)
}

func (b *Bulb) write(ctx context.Context, what string, vs dp.Values) error {
	body, err := dp.Body(vs)
	if err == nil {
		err = b.sess.Control(ctx, body, true)
	}
	if err != nil {
		return b.errorf(what, err)
	}
	return nil
}

// TurnOn switches the bulb on.
func (b *Bulb) TurnOn(ctx context.Context) error {
	return b.write(ctx, "turn on", dp.Schema20.Power(true))
}

// TurnOff switches the bulb off.
func (b *Bulb) TurnOff(ctx context.Context) error {
	return b.write(ctx, "turn off", dp.Schema20.Power(false))
}

// SetColour switches to colour mode with c. It also ends a colour stream,
// persisting c as the bulb's colour.
func (b *Bulb) SetColour(ctx context.Context, c dp.RGB) error {
	return b.write(ctx, "set colour", dp.Schema20.Colour(c))
}

// SetColourHSV switches to colour mode with c.
func (b *Bulb) SetColourHSV(ctx context.Context, c dp.HSV) error {
	vs, err := dp.Schema20.ColourHSV(c)
	if err != nil {
		return b.errorf("set colour", err)
	}
	return b.write(ctx, "set colour", vs)
}

// SetWhiteBrightness switches to white mode at pct (0–100 %; values below
// the bulb's 1 % minimum clamp up to it).
func (b *Bulb) SetWhiteBrightness(ctx context.Context, pct float64) error {
	vs, err := dp.Schema20.WhitePercent(pct)
	if err != nil {
		return b.errorf("set white brightness", err)
	}
	return b.write(ctx, "set white brightness", vs)
}

// SetColourTempPercent switches to white mode with the colour temperature
// at pct (0 % warmest, 100 % coolest).
func (b *Bulb) SetColourTempPercent(ctx context.Context, pct float64) error {
	vs, err := dp.Schema20.ColourTempPercent(pct)
	if err != nil {
		return b.errorf("set colour temp", err)
	}
	return b.write(ctx, "set colour temp", vs)
}

// SetScene switches to scene mode and plays sc.
func (b *Bulb) SetScene(ctx context.Context, sc dp.SceneValue) error {
	vs, err := dp.Schema20.Scene(sc)
	if err != nil {
		return b.errorf("set scene", err)
	}
	return b.write(ctx, "set scene", vs)
}

// SetTimer starts the countdown (seconds, 0 cancels) after which the bulb
// toggles on/off.
func (b *Bulb) SetTimer(ctx context.Context, seconds int) error {
	vs, err := dp.Schema20.Timer(seconds)
	if err != nil {
		return b.errorf("set timer", err)
	}
	return b.write(ctx, "set timer", vs)
}

// SetDoNotDisturb turns the do-not-disturb (power-outage memory guard) on
// or off.
func (b *Bulb) SetDoNotDisturb(ctx context.Context, on bool) error {
	vs, err := dp.Schema20.DoNotDisturb(on)
	if err != nil {
		return b.errorf("set do not disturb", err)
	}
	return b.write(ctx, "set do not disturb", vs)
}

// SetMusicSync writes one DP 27 ("music sync") value.
func (b *Bulb) SetMusicSync(ctx context.Context, a dp.Adjust) error {
	vs, err := dp.Schema20.MusicSync(a)
	if err != nil {
		return b.errorf("set music sync", err)
	}
	return b.write(ctx, "set music sync", vs)
}
