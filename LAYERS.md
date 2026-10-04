# Layers

notuya-go is split into four layers. Each one hides a single concern and
imports only the layer directly below it.

```
application   pkg/bulb, pkg/discovery     domain verbs, streaming, pushes, scanning
     ↓
presentation  pkg/dp                      DP table, value codecs, validation, JSON envelope
     ↓
session       pkg/session (+ v35)         handshake, typed messages, reply matching, pushes, heartbeat
     ↓
transport     pkg/transport/v35, /udp     TCP/UDP I/O, 6699 framing, AES-GCM, seqno
```

`dp` and the `session` interface do not import each other. `bulb` is where
they meet: it builds `dp.Values`, encodes them with `dp.Body` and sends them
with `session.Control`.

## How the layers were separated

Before the split there were three packages. `protocol`/`protocol35` mixed
framing, the handshake and message codes, and `device` mixed DP numbers,
value encoding and session calls. Version details such as the `"3.5"`
control header leaked into `device`. Streaming also needed a
`DrainInbound` workaround, because no one owned reads from the socket.

The split follows one question: **who is allowed to know what?**

| Knowledge | Only known by |
|---|---|
| Bytes on the wire, frame layout, AES-GCM, seqno | `transport/*` |
| Message codes (0x10, 0x0d, 0x08 …), handshake, `"3.5"` header | `session/v35` |
| DP numbers, value formats, valid ranges | `dp` |
| What a user wants ("turn on", "make it red") | `bulb` |

Rules:

1. **Imports point down only.** The allowed in-module imports are:

   | Package | May import |
   |---|---|
   | `transport/v35` | nothing |
   | `transport/udp` | `transport/v35` |
   | `session` | nothing (interface only) |
   | `session/v35` | `session`, `transport/v35` |
   | `dp` | nothing (pure codecs) |
   | `bulb` | `dp`, `session` |
   | `discovery` | `transport/udp` |

2. **Version-specific code lives in versioned packages.** Supporting
   another protocol version means adding a `transport/vXX` + `session/vXX`
   pair. `dp`, `bulb` and `discovery` do not change.
3. **No layer does I/O policy.** Nothing imports `os`, `log` or `log/slog`.
   Nothing prints, reads the environment or touches files. Errors travel
   up and the caller decides.
4. **One reader per socket.** Only the session's reader goroutine reads the
   connection. Everything else asks the session.

`layers_test.go` enforces rules 1 and 3. It reads `go list` output and
fails if a package imports something outside its allowed list, imports a
forbidden std package, or is not assigned to a layer.

## What each layer offers

### Transport: `pkg/transport/v35`, `pkg/transport/udp`

Moves encrypted frames. Knows nothing about what they mean.

- `v35.Frame`, `Encode`/`Decode`: 6699 framing, AES-GCM with a 12-byte IV,
  `StripRetcode`/`WithRetcode`.
- `v35.Conn`: `Dial`, `ReadFrame`, `WriteFrame` (mutex-guarded outgoing
  seqno, deadline from the context), `SetKey` to switch to the session key.
- `udp.Key()`: the fixed discovery key.
- `udp.Listen(port)` → `Listener.Read()`: accepts encrypted or plaintext
  announcements as `Packet`s.
- `udp.Solicit(body)`: one broadcast per interface, each bound to its
  interface.

### Session: `pkg/session`, `pkg/session/v35`

Turns frames into typed request/reply messages over one long-lived
connection.

`session.Session` is the version-agnostic contract:

```go
Query(ctx) ([]byte, error)                 // read status
Control(ctx, body []byte, wait bool) error // write DPs; wait=false is fire-and-forget
Refresh(ctx, dpIDs []int) error            // ask for fresh values; they arrive on Pushes
Heartbeat(ctx, wait bool) error
Pushes() <-chan Push                       // status the device sends on its own
Done() <-chan struct{}
Err() error
Close() error
```

`v35.Open(ctx, addr, localKey, Options)` dials, runs the three-message
handshake and starts two goroutines:

- **The reader.** It matches replies to waiting requests by message code,
  first in, first out. Seqno matching does not work because the device
  replies with its own seqno. It routes status pushes to `Pushes()`, which
  buffers 16 and drops the oldest rather than stall.
- **The heartbeat.** It pings fire-and-forget after
  `DefaultHeartbeatInterval` (10s) with no writes, which keeps the link
  alive (streams have no heartbeat of their own). A read or write error
  fails the session and closes `Done()`; callers that want an active probe
  send `Heartbeat(ctx, true)` and wait for the reply.

All methods are safe for concurrent use.

### Presentation: `pkg/dp`

Turns domain values into DP writes and DP status into typed values. It is
pure: no I/O, no goroutines.

- `Table`, `Lookup(id)`: number, identifier, access and range for every DP
  in PRIMITIVES.md.
- **Codecs.** Each type has `Hex()` (or an `Encode*` function) and a `Parse*`
  decoder:

  | DP | Codec |
  |---|---|
  | 24 colour | `HSV`, with `HSVFromRGB` to convert |
  | 25 scene | `SceneValue`, `SceneUnit` |
  | 27, 28, 29 (music, real-time, debug) | `Adjust` |
  | 30 biorhythm | `Rhythm` |
  | 31, 32 sleep/wake | `FadeNode` |
  | 33 power memory | `PowerMemoryValue` |
  | 209, 210 cycle/vacation timing | `TimingNode` |

- `Schema`: `Schema20` (DP 20+) or the legacy `Schema1` (DP 1–8), picked by
  `DetectSchema`. A bulb never mixes the two. Its methods return `Values`:
  `Power`, `Colour`, `ColourHSV`, `White`, `WhitePercent`, `ColourTemp`,
  `ColourTempPercent`, `Scene`, `Timer`, `RealTime`, `MusicSync`,
  `DoNotDisturb`, `Raw`.
- **Validation.** Every encoder checks the documented ranges (for example,
  brightness 10–1000), so an invalid value never reaches the wire.
- `Body(Values)`: the control JSON envelope.
- `Decode(body)` → `State`: typed `On`, `Mode`, `Brightness`, `ColourTemp`,
  `Colour`, `Scene`, `Timer` and `DoNotDisturb`, plus `Has`, `IDs` and
  percent helpers. Accepts both query replies and push envelopes, and both
  `colour` and `color`.

### Application: `pkg/bulb`, `pkg/discovery`

Speaks the user's language. Never names a DP number or a message code.

`bulb.New(sess, name)` wraps one open session:

- **Verbs:** `TurnOn`, `TurnOff`, `SetColour(RGB)`, `SetColourHSV`,
  `SetWhiteBrightness(%)`, `SetColourTempPercent(%)`, `SetScene`,
  `SetTimer`, `SetDoNotDisturb`, `SetMusicSync`.
- **Reads:** `Status` (also learns the schema), `Capabilities` (the DPs the
  bulb reports), `Refresh`, and `Watch(ctx)`, which delivers pushes as
  `dp.State`.
- **Streaming:** `StreamColours(ctx, colours, opts)` sends fire-and-forget
  real-time writes (DP 28), coalesced to one per `opts.Interval` with the
  newest colour winning. Finish with `SetColour` so the final colour sticks.
- **Escape hatches:** `Set(dp.Values)` for any DP, and `Session()` for the
  raw session.

`discovery` finds bulbs on the LAN:

- `Scan(ctx, timeout)` solicits and listens, then returns the devices it
  found.
- `Listen(ctx)` passively streams announcements.

Each `Device` has an `ID`, `IP` and `Version`. Discovery cannot provide
local keys; those come from Tuya's cloud.

**What `bulb` does not do:** it does not dial, reconnect or cache state.
The caller owns the session's lifetime (see `control.go` in notuya-gui).

## One request, top to bottom

`b.SetColour(ctx, dp.RGB{R: 255})`:

1. **`bulb`** asks its schema: `Schema20.Colour(rgb)` returns
   `Values{21: "colour", 24: "000003e803e8"}`.
2. **`dp`** validates the values and wraps them:
   `Body(...)` → `{"protocol":5,"t":…,"data":{"dps":{…}}}`.
3. **`session/v35`** prepends the `"3.5"` header, registers a waiter for
   code 0x0d, and writes the message. It returns when the reader matches
   the reply.
4. **`transport/v35`** assigns a seqno, encrypts with AES-GCM and writes a
   6699 frame to the TCP socket.

## Testing per layer

| Layer | Tested with |
|---|---|
| transport | Frame round trips, captured-frame fixtures, the UDP listener |
| session | An in-process fake device (real handshake and framing): concurrency, pushes during a stream, Refresh, heartbeat, disconnect |
| dp | Tuya's documented examples, tinytuya reference vectors, range rejection, a real A60TY10W status reply |
| bulb | A mock `session.Session` for the verbs; an end-to-end stream over the fake device; real hardware behind `NOTUYA_INTEGRATION=1` |
| whole module | `layers_test.go` |
