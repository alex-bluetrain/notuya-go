package control

import (
	"context"
	"fmt"
	"sync"

	"github.com/averstraeten/notuya-go/pkg/bulb"
	"github.com/averstraeten/notuya-go/pkg/device"
	"github.com/averstraeten/notuya-go/pkg/protocol35"
)

// Control owns one persistent protocol35 session for a single device and
// serializes every command behind a mutex, since a session is not
// concurrency-safe. It issues discrete waited commands over this session and
// borrows music mode during a live colour drag.
type Control struct {
	dev        Device
	streamOpts bulb.StreamOptions

	mu   sync.Mutex
	sess *protocol35.Session
	bulb *bulb.Bulb

	// live is a short-lived music-mode streamer borrowed for a colour drag.
	// While non-nil the command session is closed so the bulb only ever has
	// one session open at a time.
	live *streamer
}

// New builds a Control for a device. streamOpts carries the default transition
// and interval used during a live drag.
func New(d Device, streamOpts bulb.StreamOptions) *Control {
	return &Control{dev: d, streamOpts: streamOpts}
}

// Device returns the controlled device.
func (c *Control) Device() Device { return c.dev }

// Name returns a human label for logs.
func (c *Control) Name() string { return c.dev.DisplayName() }

// connectLocked lazily opens the session (idempotent). The caller must hold mu.
func (c *Control) connectLocked(ctx context.Context) error {
	if c.sess != nil {
		return nil
	}
	sess := protocol35.NewSession(c.dev.IPAddress, []byte(c.dev.LocalKey))
	openCtx, cancel := context.WithTimeout(ctx, CommandTimeout)
	defer cancel()
	if err := sess.Open(openCtx); err != nil {
		return fmt.Errorf("%s: opening session: %w", c.Name(), err)
	}
	c.sess = sess
	c.bulb = bulb.NewBulb(sess, c.Name())
	return nil
}

// withBulb runs fn against the connected bulb under the mutex, opening the
// session first if needed. All device I/O funnels through here so a single
// session is never touched concurrently.
//
// The persistent session can go stale: a bulb drops an idle TCP connection
// after a while, leaving c.sess non-nil but half-open, so the next write fails
// with "broken pipe". When that happens on a session we had already cached, we
// tear it down and retry once with a fresh connection. A genuinely unreachable
// device fails again on the reopen and surfaces the real error.
func (c *Control) withBulb(ctx context.Context, fn func(b *bulb.Bulb) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live != nil {
		return fmt.Errorf("%s: a live colour drag is in progress", c.Name())
	}
	reused := c.sess != nil
	if err := c.connectLocked(ctx); err != nil {
		return err
	}
	err := fn(c.bulb)
	if err != nil && reused {
		// The write failed on a session we inherited; assume it went stale,
		// drop it, and try once more on a fresh connection.
		c.closeLocked()
		if reErr := c.connectLocked(ctx); reErr != nil {
			return reErr
		}
		err = fn(c.bulb)
	}
	return err
}

// closeLocked tears down the session. The caller must hold mu.
func (c *Control) closeLocked() {
	if c.sess != nil {
		c.sess.Close()
		c.sess = nil
		c.bulb = nil
	}
}

// Close tears down the session. Safe to call more than once.
func (c *Control) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live != nil {
		c.live.Close()
		c.live = nil
	}
	c.closeLocked()
}

// Refresh queries the device once and parses a Status snapshot.
func (c *Control) Refresh(ctx context.Context) (Status, error) {
	var st Status
	err := c.withBulb(ctx, func(b *bulb.Bulb) error {
		qctx, cancel := context.WithTimeout(ctx, CommandTimeout)
		defer cancel()
		dps, err := b.Raw().Status(qctx)
		if err != nil {
			return err
		}
		st = parseStatus(dps)
		return nil
	})
	return st, err
}

// SetPower turns the bulb on or off.
func (c *Control) SetPower(ctx context.Context, on bool) error {
	return c.withBulb(ctx, func(b *bulb.Bulb) error {
		cctx, cancel := context.WithTimeout(ctx, CommandTimeout)
		defer cancel()
		if on {
			return b.TurnOn(cctx)
		}
		return b.TurnOff(cctx)
	})
}

// SetColour switches to colour mode and applies an RGB colour, committing it so
// it sticks.
func (c *Control) SetColour(ctx context.Context, rgb device.RGB) error {
	return c.withBulb(ctx, func(b *bulb.Bulb) error {
		cctx, cancel := context.WithTimeout(ctx, CommandTimeout)
		defer cancel()
		return b.SetColour(cctx, rgb.R, rgb.G, rgb.B)
	})
}

// SetColourBrightness adjusts the "v" of the current colour (DP 24) without
// changing hue/sat or leaving colour mode.
func (c *Control) SetColourBrightness(ctx context.Context, pct float64) error {
	return c.withBulb(ctx, func(b *bulb.Bulb) error {
		cctx, cancel := context.WithTimeout(ctx, CommandTimeout)
		defer cancel()
		return b.SetColourBrightness(cctx, pct)
	})
}

// SetWhiteBrightness switches to white mode and sets brightness (DP 22).
func (c *Control) SetWhiteBrightness(ctx context.Context, pct float64) error {
	return c.withBulb(ctx, func(b *bulb.Bulb) error {
		cctx, cancel := context.WithTimeout(ctx, CommandTimeout)
		defer cancel()
		return b.SetWhiteBrightness(cctx, pct)
	})
}

// SetColourTempPercent switches to white mode and sets colour temperature as a
// cold↔warm percentage (0-100).
func (c *Control) SetColourTempPercent(ctx context.Context, pct float64) error {
	return c.withBulb(ctx, func(b *bulb.Bulb) error {
		cctx, cancel := context.WithTimeout(ctx, CommandTimeout)
		defer cancel()
		return b.Raw().SetColourTempPercent(cctx, pct, true)
	})
}

// SetBrightness turns the bulb on and adjusts brightness in whichever mode the
// bulb is currently in (colour-v aware).
func (c *Control) SetBrightness(ctx context.Context, pct float64) error {
	return c.withBulb(ctx, func(b *bulb.Bulb) error {
		cctx, cancel := context.WithTimeout(ctx, CommandTimeout)
		defer cancel()
		if err := b.TurnOn(cctx); err != nil {
			return err
		}
		mode, err := b.Raw().GetMode(cctx)
		if err != nil {
			return err
		}
		if mode == device.ModeColour {
			return b.SetColourBrightness(cctx, pct)
		}
		return b.SetWhiteBrightness(cctx, pct)
	})
}

// ApplyState drives the device to the state captured in a scene. An off state
// only cuts power. An on state first turns the switch on, then applies the mode
// data. In colour mode brightness is baked into a single SetColour write; in
// white mode temp and brightness are two writes (temp first, brightness last).
func (c *Control) ApplyState(ctx context.Context, st SceneState) error {
	if !st.On {
		return c.SetPower(ctx, false)
	}
	if err := c.SetPower(ctx, true); err != nil {
		return err
	}
	switch st.Mode {
	case device.ModeColour:
		r, g, b := HSVToRGBInt(st.Hue, st.Sat, st.Bright/100.0)
		return c.SetColour(ctx, device.RGB{R: r, G: g, B: b})
	case device.ModeWhite:
		if err := c.SetColourTempPercent(ctx, st.Temp); err != nil {
			return err
		}
		return c.SetWhiteBrightness(ctx, st.Bright)
	default:
		// Captured from a mode we don't model; the switch is already on.
		return nil
	}
}

// BeginLiveDrag closes the command session and opens a short-lived music-mode
// streamer seeded with the current colour, so wheel drags stream smoothly. It
// is a no-op if a drag is already in progress.
func (c *Control) BeginLiveDrag(seed device.RGB) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live != nil {
		return
	}
	if c.sess != nil {
		c.sess.Close()
		c.sess = nil
		c.bulb = nil
	}
	c.live = newStreamer([]Device{c.dev}, seed, c.streamOpts)
}

// UpdateLiveDrag streams a colour during a drag (newest-wins, non-blocking).
func (c *Control) UpdateLiveDrag(rgb device.RGB, transition *int) {
	c.mu.Lock()
	live := c.live
	c.mu.Unlock()
	if live != nil {
		live.Set(rgb, transition)
	}
}

// EndLiveDrag closes the streamer, which flushes the pending colour and leaves
// music mode with a normal SetColour so the final colour sticks; the command
// session then re-opens lazily on the next command.
func (c *Control) EndLiveDrag() {
	c.mu.Lock()
	live := c.live
	c.live = nil
	c.mu.Unlock()
	if live != nil {
		live.Close()
	}
}
