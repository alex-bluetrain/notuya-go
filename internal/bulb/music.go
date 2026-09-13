package bulb

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/averstraeten/notuya-go/internal/device"
	"github.com/averstraeten/notuya-go/internal/protocol"
)

const (
	// DefaultStreamInterval throttles sends to ~25fps. Matches the sibling
	// Python picker, where this was found to be the fastest rate the bulb
	// keeps up with while dragging.
	DefaultStreamInterval = 40 * time.Millisecond

	// streamHeartbeatInterval bounds how long a stream stays silent. Tuya
	// devices drop idle connections, and a drag naturally pauses whenever
	// the user stops moving the pointer.
	streamHeartbeatInterval = 5 * time.Second
)

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
		d := device.DefaultTransition
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
func (b *Bulb) StreamColours(ctx context.Context, colours <-chan device.RGB, opts StreamOptions) error {
	opts = opts.withDefaults()

	session := b.dev.Session()
	stream, ok := session.(protocol.StreamSession)
	if !ok {
		return fmt.Errorf("bulb: %s: colour streaming needs a protocol version that supports it", b.Name)
	}

	// Warm-up on the still-exclusively-owned connection: this is a
	// blocking round trip, so it both confirms the device is reachable and
	// surfaces a dead session as an error here rather than as silently
	// dropped colours later.
	if err := b.Status(ctx); err != nil {
		return fmt.Errorf("bulb: %s: preparing colour stream: %w", b.Name, err)
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

	send := func(c device.RGB) error {
		if err := b.dev.SetMusicColour(ctx, *opts.Transition, c.R, c.G, c.B, false); err != nil {
			return fmt.Errorf("bulb: %s: streaming colour: %w", b.Name, err)
		}
		return nil
	}

	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	var (
		pending  *device.RGB
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
				return fmt.Errorf("bulb: %s: colour stream connection lost: %w", b.Name, err)
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
				if _, err := session.Command(ctx, protocol.HeartBeat, nil, false); err != nil {
					return fmt.Errorf("bulb: %s: colour stream heartbeat: %w", b.Name, err)
				}
				lastSent = time.Now()
			}
		}
	}
}
