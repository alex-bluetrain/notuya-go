package device

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/averstraeten/notuya-go/internal/protocol"
)

// dpMusic carries music-mode colour data. Unlike DP 24 it takes a leading
// transition digit, which is the whole point of music mode: in plain colour
// mode the bulb always applies its own ~300-500ms fade with no way to
// shorten it, so a live colour drag lags behind the pointer.
const dpMusic = "28"

const (
	// DefaultStreamInterval throttles sends to ~25fps. Matches the sibling
	// Python picker, where this was found to be the fastest rate the bulb
	// keeps up with while dragging.
	DefaultStreamInterval = 40 * time.Millisecond

	// DefaultTransition is the fade length sent with each music colour: 0
	// is instant but visibly steppy, higher values smear; 1 is the value
	// the Python picker settled on.
	DefaultTransition = 1

	// MaxTransition is the highest value confirmed working on the target
	// hardware. It is also the largest that still encodes as one hex digit,
	// which the payload format requires.
	MaxTransition = 10

	// streamHeartbeatInterval bounds how long a stream stays silent. Tuya
	// devices drop idle connections, and a drag naturally pauses whenever
	// the user stops moving the pointer.
	streamHeartbeatInterval = 5 * time.Second
)

// musicColourHex encodes an RGB colour as a DP 28 value:
//
//	{transition:%x}{hsv16:12 hex}{white_brightness:0000}{colourtemp:0000}
//
// e.g. pure red with transition 0 is "0" + "000003e803e8" + "0000" + "0000".
// The two trailing fields drive the bulb's separate white channel, not the
// colour's intensity, and are deliberately pinned to zero: non-zero values
// change how the device reads the payload and can leave it ignoring colour
// updates altogether. Dim a streamed colour by scaling r/g/b instead.
func musicColourHex(transition int, r, g, b uint8) (string, error) {
	if transition < 0 || transition > MaxTransition {
		return "", fmt.Errorf("device: transition must be 0-%d, got %d", MaxTransition, transition)
	}
	return fmt.Sprintf("%x%s%04x%04x", transition, rgbToHSV16Hex(r, g, b), 0, 0), nil
}

// RGB is a 24-bit colour.
type RGB struct {
	R, G, B uint8
}

// StreamOptions tunes a StreamColours run. The zero value is valid and
// selects the defaults.
type StreamOptions struct {
	// Interval is the minimum spacing between sends. Colours arriving
	// faster are coalesced, newest wins.
	Interval time.Duration

	// Transition is the per-update fade length (0-10) sent on DP 28. Zero
	// is a meaningful setting (instant, visibly steppy) rather than
	// "unset", so leave it nil to take the default.
	Transition *int
}

// Transition returns a pointer suitable for StreamOptions.Transition.
func Transition(n int) *int { return &n }

func (o StreamOptions) withDefaults() StreamOptions {
	if o.Interval <= 0 {
		o.Interval = DefaultStreamInterval
	}
	if o.Transition == nil {
		d := DefaultTransition
		o.Transition = &d
	}
	return o
}

// StreamColours streams colours to the bulb over the already-open session
// until colours is closed or ctx is cancelled, then returns.
//
// Updates are sent fire-and-forget on DP 28 and coalesced to at most one
// per opts.Interval, keeping only the newest pending colour: when the
// producer outruns the bulb, the point is to track the latest colour, not
// to replay a backlog of stale ones.
//
// Sending DP 28 is also what puts the bulb into music mode — deliberately,
// instead of setting DP 21 first. Writing the mode DP resets the bulb's
// colour before the first update arrives, which shows up as a visible
// flicker at the start of every drag.
//
// The bulb only acks the first message of a run, so every send after it
// must not wait for a reply; a drain goroutine discards whatever the device
// pushes meanwhile. It is stopped before this returns, leaving the session
// safe for ordinary blocking commands — notably SetColour, which is how a
// caller should finish a stream: it exits music mode and persists the final
// colour in one step, where setting DP 21 back to "colour" would instead
// revert the bulb to the colour it had before the stream started.
func (b *Bulb) StreamColours(ctx context.Context, colours <-chan RGB, opts StreamOptions) error {
	opts = opts.withDefaults()

	stream, ok := b.client.session.(protocol.StreamSession)
	if !ok {
		return fmt.Errorf("device: %s: colour streaming needs a protocol version that supports it", b.Name)
	}

	// Warm-up on the still-exclusively-owned connection: this is a
	// blocking round trip, so it both confirms the device is reachable and
	// surfaces a dead session as an error here rather than as silently
	// dropped colours later.
	if err := b.Status(ctx); err != nil {
		return fmt.Errorf("device: %s: preparing colour stream: %w", b.Name, err)
	}

	drainCtx, stopDrain := context.WithCancel(ctx)
	drainErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		drainErr <- stream.DrainInbound(drainCtx)
	}()
	defer func() {
		stopDrain()
		wg.Wait()
	}()

	send := func(c RGB) error {
		hex, err := musicColourHex(*opts.Transition, c.R, c.G, c.B)
		if err != nil {
			return err
		}
		if err := b.client.controlNoWait(ctx, map[string]any{dpMusic: hex}); err != nil {
			return fmt.Errorf("device: %s: streaming colour: %w", b.Name, err)
		}
		return nil
	}

	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	var (
		pending  *RGB
		lastSent = time.Now()
	)

	for {
		select {
		case <-ctx.Done():
			return nil

		case err := <-drainErr:
			// The drain goroutine only returns early on a broken
			// connection; continuing would just discard colours.
			if err != nil {
				return fmt.Errorf("device: %s: colour stream connection lost: %w", b.Name, err)
			}
			return nil

		case c, ok := <-colours:
			if !ok {
				// Flush whatever the throttle was still holding, so
				// the bulb ends on the colour the caller last chose.
				if pending != nil {
					return send(*pending)
				}
				return nil
			}
			pending = &c

		case <-ticker.C:
			switch {
			case pending != nil:
				c := *pending
				pending = nil
				if err := send(c); err != nil {
					return err
				}
				lastSent = time.Now()
			case time.Since(lastSent) >= streamHeartbeatInterval:
				if _, err := b.client.session.Command(ctx, protocol.HeartBeat, nil, false); err != nil {
					return fmt.Errorf("device: %s: colour stream heartbeat: %w", b.Name, err)
				}
				lastSent = time.Now()
			}
		}
	}
}
