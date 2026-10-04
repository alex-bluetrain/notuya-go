// Package session is the session layer: typed request/response messages
// over an encrypted connection to one device. It knows Tuya's message
// codes and nothing about data points — the bodies it carries are opaque
// JSON, built and read by pkg/dp.
//
// pkg/session/v35 implements Session for protocol 3.5.
package session

import "context"

// Push is a status report the device sent on its own initiative (message
// code 8): after a control command takes effect, after a Refresh, or when
// the device is changed by something else (the Tuya app, a wall switch).
// Body is the JSON status object, with any protocol prefix removed.
type Push struct {
	Body []byte
}

// Session is one live connection to a device. All methods are safe for
// concurrent use. A Session cannot be reopened: once Done is closed, make
// a new one.
type Session interface {
	// Query asks for every DP's current value (code 16) and returns the
	// JSON status body.
	Query(ctx context.Context) ([]byte, error)

	// Control sends a "set these DPs" request (code 13). body is the JSON
	// control envelope. With wait, it returns once the device has
	// acknowledged; without, as soon as the frame is written — required
	// while streaming, when the device stops acknowledging.
	Control(ctx context.Context, body []byte, wait bool) error

	// Refresh asks the device to re-report the given DPs (code 18). The
	// values arrive on Pushes, not as a reply.
	Refresh(ctx context.Context, dpIDs []int) error

	// Heartbeat sends a keep-alive (code 9). Sessions already send one on
	// their own whenever the link is idle; this is for callers that want
	// to probe the link explicitly. With wait, it returns once the device
	// answers.
	Heartbeat(ctx context.Context, wait bool) error

	// Pushes delivers status pushes. It is closed when the session ends.
	// Nobody has to read it: when the buffer is full the oldest push is
	// dropped, never the connection stalled.
	Pushes() <-chan Push

	// Done is closed when the session ends, by Close or a broken link.
	Done() <-chan struct{}

	// Err reports why the session ended: nil while it is live or after a
	// plain Close.
	Err() error

	// Close ends the session. Safe to call more than once.
	Close() error
}
