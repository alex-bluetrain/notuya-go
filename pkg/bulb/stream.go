package bulb

import (
	"context"
	"time"

	"github.com/alex-bluetrain/notuya-go/pkg/dp"
)

// DefaultStreamInterval throttles sends to ~25fps, the fastest rate the bulb
// was found to keep up with while dragging.
const DefaultStreamInterval = 40 * time.Millisecond

// StreamOptions tunes a StreamColours run. The zero value is valid and
// selects the defaults.
type StreamOptions struct {
	// Interval is the minimum spacing between sends. Colours arriving
	// faster are coalesced, newest wins.
	Interval time.Duration

	// ChangeMode is the per-update jump/fade flag sent on DP 28. Its zero
	// value (ChangeJump) is a meaningful setting rather than "unset", so
	// leave it nil to take dp.DefaultChangeMode.
	ChangeMode *dp.ChangeMode
}

// StreamColour is one unit of a colour stream: a colour plus an optional
// change mode. A nil ChangeMode falls back to the run's default
// (StreamOptions.ChangeMode), so a producer that never sets it behaves
// exactly as if it streamed bare colours.
type StreamColour struct {
	Colour     dp.HSV
	ChangeMode *dp.ChangeMode
}

func (o StreamOptions) withDefaults() StreamOptions {
	if o.Interval <= 0 {
		o.Interval = DefaultStreamInterval
	}
	if o.ChangeMode == nil {
		o.ChangeMode = dp.DefaultChangeMode.Ptr()
	}
	return o
}

// StreamColours streams colours to the bulb over the already-open session
// until colours is closed or ctx is cancelled, then returns.
//
// Updates are sent fire-and-forget on DP 28 ("real-time adjustment") and
// coalesced to at most one per opts.Interval, keeping only the newest
// pending colour: when the producer outruns the bulb, the point is to track
// the latest colour, not to replay a backlog of stale ones.
//
// The stream writes DP 28 alone, never DP 21 first: writing the mode DP
// resets the bulb's colour before the first update arrives, which shows up
// as a visible flicker at the start of every drag.
//
// The bulb only acks the first message of a run, so sends never wait. The
// session's reader goroutine absorbs whatever the bulb sends meanwhile, so
// the session stays usable for ordinary commands during and after the
// stream. A stream has no keep-alive of its own: when a drag pauses, the
// session's idle heartbeat keeps the link open. Finish a stream with
// SetColour: it persists the final colour, where setting DP 21 back to
// "colour" alone would instead revert the bulb to the colour it had before
// the stream started.
func (b *Bulb) StreamColours(ctx context.Context, colours <-chan StreamColour, opts StreamOptions) error {
	opts = opts.withDefaults()

	// Warm-up: a blocking round trip confirms the bulb is reachable and
	// surfaces a dead session as an error here rather than as silently
	// dropped colours later.
	if _, err := b.Status(ctx); err != nil {
		return b.errorf("preparing colour stream", err)
	}

	send := func(c StreamColour) error {
		mode := *opts.ChangeMode
		if c.ChangeMode != nil {
			mode = *c.ChangeMode
		}
		return b.SendLive(ctx, c.Colour, mode)
	}

	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	var pending *StreamColour

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-b.sess.Done():
			// Continuing would just discard colours.
			return b.errorf("colour stream connection lost", b.sess.Err())

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
			if pending == nil {
				continue
			}
			c := *pending
			pending = nil
			if err := send(c); err != nil {
				return err
			}
		}
	}
}

// SendLive sends one colour on DP 28 ("real-time adjustment") without
// waiting for an ack, like a single StreamColours update. It is for callers
// that run their own send loop over the session; the same rules apply:
// finish with SetColour to persist the colour.
func (b *Bulb) SendLive(ctx context.Context, c dp.HSV, mode dp.ChangeMode) error {
	vs, err := dp.Schema20.RealTime(dp.Adjust{Mode: mode, Colour: c})
	if err != nil {
		return b.errorf("streaming colour", err)
	}
	body, err := dp.Body(vs)
	if err != nil {
		return b.errorf("streaming colour", err)
	}
	if err := b.sess.Control(ctx, body, false); err != nil {
		return b.errorf("streaming colour", err)
	}
	return nil
}
