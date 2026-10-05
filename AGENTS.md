# notuya-go

A Go reimplementation of the Tuya local protocol v3.5 for controlling smart
bulbs (model A60TY10W) over the LAN, with no third-party dependencies and no
Python runtime.

## Architecture

Four layers, each importing only the one directly below it:

```
application   pkg/bulb, pkg/discovery     domain verbs, streaming, pushes, scanning
     ↓
presentation  pkg/dp                      DP table, value codecs, validation, JSON envelope
     ↓
session       pkg/session (+ v35)         handshake, typed messages, request/reply match, pushes, heartbeat
     ↓
transport     pkg/transport/v35, /udp     TCP/UDP I/O, 6699 framing, AES-GCM, seqno
```

`pkg/dp` and the `pkg/session` interface have no in-module imports, so
`bulb` combines them: it builds `dp.Values` with a `dp.Schema`, encodes them
with `dp.Body`, and sends them with `session.Control`. Rules, enforced by
`layers_test.go` (which reads `go list`):

- Only `session/v35` knows message codes. Only `dp` knows DP numbers.
- Version-specific bytes (the `"3.5"` control header, the handshake) live in
  `transport/v35` and `session/v35` only. Another protocol version is a new
  `transport/vXX` + `session/vXX` pair; nothing above `session` changes.
- `discovery` uses `transport/udp` only.
- No package prints, exits, reads the environment, or touches the
  filesystem (no `os`, `log`, `log/slog`). Errors travel up; the caller
  decides what to show. That is what lets the whole streaming path be
  tested against an in-process fake device.

This module is **only the protocol library** — there is no binary. The one
consumer is the sibling `notuya-gui`, wired in dev with a gitignored
`go.work` pointing at `../notuya-go`. Everything exported is public API.

### Layers in detail

- **`transport/v35`** — `Frame` encode/decode (6699 framing + AES-GCM), and
  `Conn`: `ReadFrame`/`WriteFrame` with a mutex-guarded outgoing seqno,
  per-call deadlines, `SetKey` to switch to the session key. No session state.
- **`transport/udp`** — the fixed discovery key, a `Listener` that accepts
  encrypted or plaintext announcements, and `Solicit` (one broadcast per
  interface, bound to it).
- **`session`** — the version-agnostic contract:

  ```go
  type Session interface {
      Query(ctx) ([]byte, error)                 // DP_QUERY_NEW 0x10
      Control(ctx, body []byte, wait bool) error // CONTROL_NEW 0x0d
      Refresh(ctx, dpIDs []int) error            // UPDATEDPS 0x12; reply arrives on Pushes
      Heartbeat(ctx, wait bool) error            // HEART_BEAT 0x09
      Pushes() <-chan Push                       // STATUS 0x08
      Done() <-chan struct{}
      Err() error
      Close() error
  }
  ```

- **`session/v35`** — `Open` dials, handshakes and starts two goroutines.
  The **reader** is the only thing that reads the socket: it matches
  replies to waiting requests by command code, FIFO (the device answers
  with its own seqno, so seqno matching does not work), and routes status
  pushes to `Pushes()` (buffer 16; the oldest is dropped rather than
  stalling the reader). The **heartbeat** loop pings fire-and-forget
  when nothing was written for `DefaultHeartbeatInterval` (10s); it keeps
  the link alive, including through stream pauses. A read or write error
  fails the session and closes `Done()`, but a bulb that loses power never
  errors the socket: only a waited `Heartbeat(ctx, true)` timing out
  reveals it, and the caller has to send it. All methods are safe for
  concurrent use.
- **`dp`** — the DP table (`Lookup`), codecs for every documented value
  (`HSV` for 24, `SceneValue` for 25, `Adjust` for 27/28/29, raw binary
  `Rhythm`/`FadeNode`/`PowerMemory`… for 30–33/209/210), `Schema20` (the
  standard DP 20+ set; older DP 1–8 bulbs are not supported), `Decode`
  into a typed `State`, and `Body` for the control JSON. Every encoder
  validates the PRIMITIVES.md ranges, so an out-of-range value never reaches
  the wire.
- **`bulb`** — domain verbs (`TurnOn`, `SetColour`, `SetWhiteBrightness`,
  `SetColourTempPercent`, `SetScene`, `SetTimer`, `SetDoNotDisturb`,
  `SetMusicSync`), `Status`/`Capabilities`/`Refresh`, `Watch` (pushes decoded
  to `dp.State`), `StreamColours`/`SendLive` (DP 28, unacked), and `Set(dp.Values)` as the escape hatch
  for DPs without a verb. Verbs always wait for the ack.
- **`discovery`** — `Scan` (solicit + listen for a window) and `Listen`
  (passive announcements on 6667/6666).

## Spec deviations (PRIMITIVES.md)

PRIMITIVES.md is the spec. Where observed hardware disagrees, the code
follows the hardware:

- **Live streaming writes DP 28 alone.** The spec's music feature is DP 21
  `"music"` + DP 27. Streaming uses DP 28 (real-time adjustment) instead and
  never writes DP 21: on the A60TY10W, writing DP 21 first resets the colour
  (a visible flicker at the start of every drag). See *DP 28* below.
- **Mode is sent as `"colour"`.** The spec spells it `"color"`; the bulbs
  and tinytuya use `"colour"`. `dp.ParseMode` accepts both on decode.
- **DP 27/28 white fields are sent as zero.** The spec's example has
  non-zero `bbbb cccc`; non-zero values make the A60TY10W ignore colour
  updates.
- **Not testable on this hardware:** DPs 25–27, 29–34, 209, 210 are not
  reported by the A60TY10W. Their codecs are checked only against Tuya's
  documented examples and field tables.

## Tuya protocol v3.5

The whole implementation was verified byte-for-byte against `tinytuya`'s
source code (not against third-party documentation).

### 6699 framing

- Header (18 bytes):
  `prefix(0x00006699, 4) + unknown(2) + seqno(4) + cmd(4) + length(4)`.
- Body: `IV(12) || ciphertext_GCM || tag(16)`.
- Suffix: `0x00009966` (4 bytes).
- GCM AAD = the 14 header bytes after the prefix.
- `length` = `len(plaintext) + 12(iv) + 16(tag)`.

### Session handshake (3 messages)

1. **Client → Device** (cmd 3): `local_nonce` (16 random bytes),
   encrypted with `local_key`.
2. **Device → Client** (cmd 4):
   `remote_nonce(16) || HMAC-SHA256(local_key, local_nonce)(32)`. Verify
   the HMAC before continuing.
3. **Client → Device** (cmd 5): `HMAC-SHA256(local_key, remote_nonce)(32)`.
   No device response.
4. **Derivation**: `xor(local_nonce, remote_nonce)` → AES-GCM-seal with
   `local_key` and `iv=local_nonce[:12]` → the 16 ciphertext bytes
   (discard the tag) are the session key.

All subsequent traffic uses the derived session key, not the static
`local_key`.

### Version prefix

- `CONTROL_NEW` (0x0d): carries a `"3.5" + 12×0x00` prefix (15 bytes)
  before the JSON, before encrypting.
- `DP_QUERY_NEW` (0x10), `HEART_BEAT`, handshake (cmd 3/4/5): **no** prefix.

### Commands

```
SESS_KEY_NEG_START=0x03  SESS_KEY_NEG_RESP=0x04  SESS_KEY_NEG_FINISH=0x05
STATUS=0x08              HEART_BEAT=0x09          CONTROL_NEW=0x0d
DP_QUERY_NEW=0x10        UPDATEDPS=0x12           REQ_DEVINFO=0x25
```

## Hardware DPs (A60TY10W)

| DP | Name | Type | Range |
|----|------|------|-------|
| 20 | switch_led | bool | on/off |
| 21 | work_mode | enum | `white`, `colour`, `scene`, `music` |
| 22 | bright_value_v2 | int | 10-1000 |
| 23 | temp_value_v2 | int | 0-1000 |
| 24 | colour_data_v2 | hex string | `hhhhssssvvvv` (h:0-360, s/v:0-1000, 4 hex digits each) |
| 25 | scene_data | hex string | `id(2)` + per unit `interval(2) duration(2) transition(2) hsv16(12) bright(4) temp(4)` |
| 26 | countdown | int | 0-86400 seconds |
| 27 | music_data | hex string, write-only | same format as DP 28; Tuya app's music-rhythm DP |
| 28 | control_data | hex string, write-only | `t` + `hhhhssssvvvv` + `bbbb` + `cccc` (see below) |
| 34 | do_not_disturb | bool | on/off |

**DP 24 is NOT JSON on the wire** — it is a 12-character ASCII hex string.
E.g. pure red = `"000003e803e8"`. It travels as the value of key `"24"`
inside the control JSON: `{"20":true,"21":"colour","24":"016903e803e8"}`.

### DP 28 (real-time adjustment)

Tuya calls DP 28 `control_data` ("real-time adjustment"); DP 27
`music_data` takes the same payload. Streaming uses DP 28
(`dp.Schema.RealTime`). It is a 21-character ASCII hex string:
`change_mode(1) + hsv16(12) + white_brightness(4) + colourtemp(4)`. E.g.
pure red with jump is `"0000003e803e800000000"`.

- `change_mode` is a **two-valued flag**, typed as `dp.ChangeMode`:
  `ChangeJump` (0, Tuya's "direct") or `ChangeFade` (1, "gradient"). No
  other digit is defined; the fade's duration is fixed by the firmware, so
  drag smoothness is tuned via the send interval. Jump is the whole point
  of DP 28: DP 24 always applies the device's own ~300-500ms fade, which
  makes a live colour drag lag.
- The two trailing fields drive the **separate white channel**, not the
  colour's intensity, and are pinned to `0000`: non-zero values change how
  the device reads the payload and can make it ignore colour updates
  entirely. Dim a streamed colour by scaling r/g/b instead.

Write DP 28 alone. Do not set DP 21 first: that resets the bulb's colour
before the first update lands, which shows up as a visible flicker at the
start of every drag. DP 28 is not music mode (that is DP 21 `"music"` +
DP 27, `SetMusicSync`); whether DP 28 changes the reported DP 21 has not
been verified. To **finish**, do a normal DP 21/24 colour write
(`Bulb.SetColour`) — it persists the final colour, whereas setting DP 21
back to `"colour"` alone reverts the bulb to its pre-stream colour.

The device acks only the **first** message of a streaming run, so every
later send uses `wait=false`. Whatever it pushes meanwhile is read by the
session's reader goroutine and delivered on `Pushes()`, so ordinary
blocking commands can run on the same session during and after a stream.

### Control payloads

- **Query**: cmd `DP_QUERY_NEW`(0x10), payload = `{}` (no version prefix).
- **Set**: cmd `CONTROL_NEW`(0x0d), **with** the version prefix, payload =
  `{"protocol":5,"t":<unix>,"data":{"dps":{"<dp>":<value>,...}}}`.

## Discovery

- Fixed key (not the `local_key`): `md5("yGAdlopoPVldABfn")` (16 bytes).
- Unsolicited: listen on UDP/6667, 6699 frames with the fixed key.
- Solicited: broadcast `{"from":"app","ip":"<my-ip>"}` as a 6699 frame
  (cmd `REQ_DEVINFO=0x25`) to UDP/7000.

Four things the scanner gets wrong if written naively — each one produced
an empty scan that looked exactly like a network limitation:

- **Replies carry a retcode.** A solicited reply (UDP/7000) prefixes the
  JSON with the same 4-byte retcode as any device-originated frame, so it
  needs the retcode stripped; the unsolicited announcements on UDP/6667 do not,
  so stripping unconditionally corrupts those instead. `parseAnnouncement`
  handles both.
- **The advertised IP must match the interface the request leaves by.**
  The request tells the device where to reply. With several interfaces on
  one subnet, an unbound socket announces one address while the kernel
  routes the packet out of another, and the reply goes somewhere nobody is
  listening. Send one request per interface, bound to it, advertising its
  own address.
- **One request is not enough.** Devices ignore the first couple after a
  period of quiet — measured at ~3s before any answer — and UDP guarantees
  nothing. `solicitRepeatedly` re-sends every 1.5s for the whole window.
- **The request comes back to you.** It is broadcast on the port the
  replies arrive on. It has no `gwId`, which is what filters it out.

> **Note**: broadcast genuinely does not reach devices on some networks
> (AP isolation, segmentation). Direct control by IP always works, so
> fixed IPs are always a fallback. Confirm a scan failure
> is the network before assuming it: the bugs above all presented
> identically.

## Code conventions

- **Go 1.23**, module `github.com/alex-bluetrain/notuya-go`.
- **Zero external dependencies** — everything with the stdlib
  (`crypto/aes`, `crypto/cipher`, `crypto/hmac`, `crypto/sha256`,
  `crypto/md5`, `encoding/binary`, `encoding/json`).
- Deadlines are set per call from the call's context, on `transport/v35.Conn`.
  A session outlives the short context that opened it, so `Open` clears the
  handshake deadline before returning. Getting this wrong once killed
  streams ~10s in, silently.
- Colours are `dp.HSV` end to end (hue 0–360, saturation/value 0–1000),
  the bulb's own model. There is no RGB type: converting is the caller's
  job, so the library never throws away resolution.

## Decisions taken against tinytuya

The implementation was checked line by line against `tinytuya`'s `Device.py`
and `XenonDevice.py`. Most of what is missing here is missing on purpose.

**Not adopted.** `XenonDevice` carries 20+ configuration methods
(`set_socketPersistent`, `set_socketRetryLimit`, `set_sendWait`,
`cached_status`, `detect_available_dps`…), each a flag multiplying the
reachable states. `Session` has a handful of typed methods and one option.
Feature parity is not a reason to give that up.

**Adopted, but layered.** tinytuya's raw `set_value` is `bulb.Set(dp.Values)`;
a caller who needs a DP without a verb builds the value with `dp` and says
so explicitly. `Capabilities()` (readable DPs from one status) replaces
`detect_available_dps`.

Two deliberate departures from tinytuya's behaviour:

- **`SetColour` does not force the bulb on.** tinytuya's `set_colour`
  asserts `switch:true`; setting a colour on an off bulb should not
  silently turn it on.
- **Status is read once and decoded.** tinytuya's getters each query;
  here `Status` returns a typed `dp.State`, so full state costs one
  round-trip.

**Deferred, with a known trigger.**

- `max_simultaneous_dps` (`Device.py:141-153`): when a multi-DP `CONTROL`
  comes back `Err`, tinytuya retries one DP at a time. `SetColour` sends DP
  21+24 together and the A60TY10W accepts it. This is the likeliest thing to
  break on a different model — the symptom is a set that errors while each
  DP works alone.
- Pairing / Wi-Fi provisioning: PRIMITIVES.md notes the payloads are not
  publicly documented.

**Where tinytuya is structurally worse.** Composition instead of
inheritance, and version-as-package instead of `if self.version` scattered
through the methods. Honest caveat: this stays clean partly because the
problem is smaller — tinytuya carries five protocol versions, a cloud API
and dozens of device types.

## Testing

```bash
go test ./...                    # unit tests (no hardware)
go test -race ./pkg/session/... ./pkg/bulb   # the concurrent paths
NOTUYA_INTEGRATION=1 \
  NOTUYA_TEST_IP=<ip> \
  NOTUYA_TEST_KEY=<key> \
  go test ./... -v               # integration tests (needs a LAN bulb)
```

- `layers_test.go`: the layering rules above.
- `transport/v35/frame_test.go`: frame round-trip, corruption rejection,
  retcode handling.
- `transport/udp/udp_test.go`: discovery key, unique broadcast targets,
  encrypted and plaintext announcements.
- `session/v35/handshake_test.go`: byte-identical handshake against
  `testdata/handshake/session1.json`.
- `session/v35/session_test.go`: against `fakedevice_test.go`, an in-process
  device speaking the real handshake over TCP — concurrent Query/Control,
  pushes during a fire-and-forget stream, Refresh, idle heartbeat, full push
  buffer, disconnect, wrong key.
- `session/v35/bulb_e2e_test.go`: `bulb.StreamColours` + `Watch` end-to-end
  over the fake device, and a paused stream kept alive by the session's idle
  heartbeat. It lives here because the fixture does.
- `dp/dp_test.go`: Tuya's documented examples, tinytuya colour vectors,
  range rejection, decode of a real A60TY10W status.
- `bulb/bulb_test.go`, `bulb/stream_test.go`: verbs write the documented DPs;
  streaming payload shape, coalescing, change-mode handling.
- `discovery/discovery_test.go`: announcement parsing (retcode-prefixed,
  bare, own echo).
- `bulb/bulb_integration_test.go`: real hardware, gated behind
  `NOTUYA_INTEGRATION`.

## Build

```bash
make build                       # go build ./...
make check                       # vet + test
make test                        # go test ./...
make vet                         # go vet ./...
make fmt                         # gofmt -l .
```

There is no cgo in any package.

## Live colour streaming

`bulb.StreamColours` streams colours from a channel to one device over a
persistent session, so an interactive picker can drive a bulb live. See
*DP 28* above for the wire format.

Each `bulb.StreamColour` is a `dp.HSV` plus an optional `*dp.ChangeMode`; nil
falls back to `StreamOptions.ChangeMode`. Pointers because `ChangeJump` is
the zero value and must not mean "unset" — use `dp.ChangeJump.Ptr()`.

The flow:

1. Blocking `Status()` warm-up — a dead session fails here instead of
   silently dropping colours.
2. DP 28 via `session.Control(..., wait=false)`, throttled to
   `DefaultStreamInterval` (40ms) and coalesced newest-wins.
3. No heartbeat of its own: when a drag pauses, the session's idle
   heartbeat keeps the link alive.
4. On channel close: flush the pending colour and return; the caller then
   calls `SetColour` so the final colour is persisted. On ctx cancel:
   return at once (a send would fail on the cancelled context anyway).

Known limitations: no reconnect if a device drops mid-stream (reported via
`session.Done()`), and colour only — no white-mode dragging.

## Working in this repo

**Always give `rg` a path argument.** With no path it reads stdin and blocks
forever:

```bash
rg -n 'pattern' --glob '*.go' .    # the trailing . is not optional
```

The failure is easy to misread. It surfaces as **exit 128 with no output at
all** — not even from `echo` statements chained after it with `;`, because
the shell never gets that far. 128 means killed by a signal, so what is
actually being observed is someone pressing Ctrl-C on a hung process, not
`rg` reporting an error. Do not "work around" it by switching to `grep`:
that hides a hang, and the same trap applies to any command that falls back
to stdin.

The shell here is **bash**, even though the user's login shell is fish, so
`$?` and other bashisms behave normally. `$SHELL` says fish and is
misleading; `${BASH_VERSION}` is the reliable check.
