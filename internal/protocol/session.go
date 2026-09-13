package protocol

import "context"

// Session is a single logical connection to one Tuya device, already past
// whatever handshake its protocol version requires.
type Session interface {
	// Open performs any connection setup (TCP dial, session-key
	// negotiation, etc.) needed before Command can be called.
	Open(ctx context.Context) error

	// Command sends a framed request and, if wait is true, blocks for and
	// returns the decrypted response payload. wait=false sends and returns
	// immediately with a nil payload — the hook a future persistent/
	// fire-and-forget mode (e.g. "music mode") will use without needing any
	// interface change.
	Command(ctx context.Context, cmd uint32, payload []byte, wait bool) ([]byte, error)

	// Close releases the underlying connection. Safe to call multiple times.
	Close() error
}

// StreamSession is an optional extension implemented by protocol versions
// that support a persistent fire-and-forget streaming mode. Consumers should
// type-assert a Session to StreamSession and fall back gracefully (or report
// the version as unsupported) when the assertion fails.
type StreamSession interface {
	Session

	// DrainInbound reads and discards inbound frames until ctx is cancelled
	// or the connection fails, returning nil in the former case. It exists
	// because a device streamed to with wait=false still pushes status and
	// heartbeat frames; left unread, they fill the receive buffer and stall
	// the device.
	//
	// Run it in its own goroutine for the duration of a stream. While it is
	// running, Command must only be called with wait=false: the drain
	// goroutine owns the read side, the caller's goroutine owns the write
	// side, and net.Conn permits exactly that split.
	DrainInbound(ctx context.Context) error
}
