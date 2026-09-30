# notuya-go

A Go reimplementation of the Tuya local protocol v3.5 for controlling smart
bulbs (model A60TY10W) over the LAN, with no third-party dependencies and no
Python runtime.

## Architecture

```
pkg/
  protocol/                  Version-agnostic interface (Session)
  protocol35/                v3.5 implementation: 6699 framing, AES-GCM, session handshake
  device/                    Raw DP-centric API and DP encoding (tinytuya BulbDevice surface)
  bulb/                      Business layer: Bulb domain methods + music streaming
  discovery/                 UDP scanner (ports 6667/7000)
```

### Dependency flow

```
bulb         → device, protocol
device       → protocol (the interface, never protocol35 directly)
discovery    → protocol, protocol35 (uses EncodeFrame/DecodeFrame with the fixed discovery key)
protocol35   → protocol (command constants)
```

`device` and `bulb` depend only on `protocol.Session`. Adding support
for another version (3.1/3.3) means writing a new `protocol3x.Session`
implementing the same interface — without touching `device` or `bulb`.

The split mirrors tinytuya: `device.Device` is the raw, DP-centric layer
(`SetDPs`, `SetValue`, and typed helpers like `SetColour`/`SetBrightness`/
`SetColourTemp`/`SetColourTempPercent`/`SetWhiteBrightness`/`SetMusicColour`,
plus `Status` and the `GetXFrom(status)` readers, each write taking
`wait bool` and speaking DP numbers), the equivalent of `BulbDevice`.
`bulb.Bulb` is the business layer built *on top*
of `*device.Device`: its domain methods (`TurnOn`, `SetColour`,
`SetWhiteBrightness`, `StreamColours`) always use `wait=true` internally
and never expose a DP number. `device.Device` is exported so a caller can drive an arbitrary DP
the business layer does not cover.

`pkg/` is the library: it talks to bulbs and knows nothing else. No
package under it prints, exits, reads the environment, or touches the
filesystem — errors travel up and the caller decides what to show. That
is what lets the whole streaming path be tested against an in-process fake
device.

These five packages lived under `internal/` originally, which made them
unimportable outside this module. They were promoted to `pkg/` so a sibling
module (`notuya-gui`, a native color-wheel picker) can consume the library
in-process — importing `bulb`/`device`/`discovery`/`protocol35` directly
and driving `bulb.StreamColours` itself. In dev the sibling wires them up
with a gitignored `go.work` pointing at `../notuya-go`, so nothing here has
to be published or tagged. The trade-off, taken deliberately: the exported
surface (`protocol.Session`, `device.Device`, `bulb.Bulb`, …) is now public
API, so changing those signatures can break an external consumer — whereas
under `internal/` the whole surface was free to churn.

This module is **only the protocol library** — there is no binary.
Application concerns — config/rooms/scenes, per-device session control, the
live-drag streamer — live in `notuya-gui`. An HTTP/WS daemon
(`cmd/notuyad`), its backing `pkg/control`, and a CLI (`cmd/notuya`,
including `config.json` handling and `scan --update`) used to live here;
all were removed as obsolete.

### Core interface

```go
// protocol.Session — the contract every version fulfills
type Session interface {
    Open(ctx context.Context) error
    Command(ctx context.Context, cmd uint32, payload []byte, wait bool) ([]byte, error)
    Close() error
}

// protocol.StreamSession — optional, for versions that support streaming
type StreamSession interface {
    Session
    DrainInbound(ctx context.Context) error
}
```

`wait=false` in `Command` is the fire-and-forget send used by music mode
(see below). `StreamSession` is an optional add-on: `bulb.StreamColours`
type-asserts for it and reports a clear error for a version that lacks it,
so 3.1/3.3 can be added later without implementing streaming on day one.

## Tuya protocol v3.5

The whole implementation was verified byte-for-byte against `tinytuya`'s
source code (not against third-party documentation).

### 6699 framing

- Header: `prefix(0x00006699, 4) + unknown(2) + seqno(4) + cmd(4) + length(4)` = 18 bytes.
- Body: `IV(12) || ciphertext_GCM || tag(16)`.
- Suffix: `0x00009966` (4 bytes).
- GCM AAD = the 14 header bytes after the prefix.
- `length` = `len(plaintext) + 12(iv) + 16(tag)`.

### Session handshake (3 messages)

1. **Client → Device** (cmd 3): `local_nonce` (16 random bytes), encrypted with `local_key`.
2. **Device → Client** (cmd 4): `remote_nonce(16) || HMAC-SHA256(local_key, local_nonce)(32)`. Verify the HMAC before continuing.
3. **Client → Device** (cmd 5): `HMAC-SHA256(local_key, remote_nonce)(32)`. No device response.
4. **Derivation**: `xor(local_nonce, remote_nonce)` → AES-GCM-seal with `local_key` and `iv=local_nonce[:12]` → take the 16 ciphertext bytes (discard the tag) = session key.

All subsequent traffic uses the derived session key, not the static
`local_key`.

### Version prefix

- `CONTROL_NEW` (0x0d): carries a `"3.5" + 12×0x00` prefix (15 bytes) before the JSON, before encrypting.
- `DP_QUERY_NEW` (0x10), `HEART_BEAT`, handshake (cmd 3/4/5): **no** prefix.

### Commands

```
SESS_KEY_NEG_START=0x03  SESS_KEY_NEG_RESP=0x04  SESS_KEY_NEG_FINISH=0x05
CONTROL=0x07             HEART_BEAT=0x09          DP_QUERY=0x0a
CONTROL_NEW=0x0d         DP_QUERY_NEW=0x10        REQ_DEVINFO=0x25
```

## Hardware DPs (A60TY10W)

| DP | Name | Type | Range |
|----|------|------|-------|
| 20 | switch_led | bool | on/off |
| 21 | work_mode | enum | `white`, `colour`, `scene`, `music` |
| 22 | bright_value_v2 | int | 10-1000 |
| 23 | temp_value_v2 | int | 0-1000 |
| 24 | colour_data_v2 | hex string | `hhhhssssvvvv` (h:0-360, s/v:0-1000, 4 hex digits each) |
| 28 | music_data | hex string | `t` + `hhhhssssvvvv` + `bbbb` + `cccc` (see below) |

**DP 24 is NOT JSON on the wire** — it is a 12-character ASCII hex string.
E.g. pure red = `"000003e803e8"`. It travels as the value of key `"24"`
inside the control JSON: `{"20":true,"21":"colour","24":"016903e803e8"}`.

### DP 28 (music mode)

A 21-character ASCII hex string: `change_mode(1) + hsv16(12) +
white_brightness(4) + colourtemp(4)`. E.g. pure red with jump is
`"0000003e803e800000000"`.

- `change_mode` is a **two-valued flag**, typed as `device.ChangeMode`:
  `ChangeJump` (0, Tuya's "direct") or `ChangeFade` (1, "gradient"). No
  other digit is defined; the fade's duration is fixed by the firmware, so
  drag smoothness is tuned via the send interval. This is the whole point of music mode — DP 24 always applies
  the device's own ~300-500ms fade, which makes a live colour drag lag.
- The two trailing fields drive the **separate white channel**, not the
  colour's intensity, and are pinned to `0000`: non-zero values change how
  the device reads the payload and can make it ignore colour updates
  entirely. Dim a streamed colour by scaling r/g/b instead.

Writing DP 28 is what **enters** music mode. Do not set DP 21 to `"music"`
first: that resets the bulb's colour before the first update lands, which
shows up as a visible flicker at the start of every drag. To **exit**, do a
normal DP 21/24 colour write (`Bulb.SetColour`) — it leaves music mode and
persists the final colour in one step, whereas setting DP 21 back to
`"colour"` alone reverts the bulb to its pre-stream colour.

The device acks only the **first** message of a streaming run, so every
later send must use `wait=false`. A `DrainInbound` goroutine consumes
whatever the device pushes meanwhile, so its frames do not pile up
unread — and it must be stopped before the session is handed back for
ordinary blocking commands, or it would swallow their responses.

### Control payloads

- **Query**: cmd `DP_QUERY_NEW`(0x10), payload = `{}` (no version prefix).
- **Set**: cmd `CONTROL_NEW`(0x0d), payload = `{"protocol":5,"t":<unix>,"data":{"dps":{"<dp>":<value>,...}}}`, **with** the version prefix.

## Discovery

- Fixed key (not the `local_key`): `md5("yGAdlopoPVldABfn")` (16 bytes).
- Unsolicited: listen on UDP/6667, 6699 frames with the fixed key.
- Solicited: broadcast `{"from":"app","ip":"<my-ip>"}` as a 6699 frame (cmd `REQ_DEVINFO=0x25`) to UDP/7000.

Four things the scanner gets wrong if written naively — each one produced
an empty scan that looked exactly like a network limitation:

- **Replies carry a retcode.** A solicited reply (UDP/7000) prefixes the
  JSON with the same 4-byte retcode as any device-originated frame, so it
  needs `StripRetcode`; the unsolicited announcements on UDP/6667 do not,
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
- **Zero external dependencies** — everything with the stdlib (`crypto/aes`, `crypto/cipher`, `crypto/hmac`, `crypto/sha256`, `crypto/md5`, `encoding/binary`, `encoding/json`).
- `Session` deadlines are set per direction (`SetReadDeadline`/`SetWriteDeadline`, never `SetDeadline`): during a stream a `wait=false` write must not disturb the read deadline owned by the concurrent `DrainInbound`.
- A deadline must never outlive the operation it bounds. A session lives far
  longer than the short context that opened it, so `Open` clears the
  dial/handshake deadline before returning, and every `Command` sets the
  deadline its own context implies instead of inheriting whatever the last
  call left on the socket. Getting this wrong killed streams ~10s in, and
  silently: fire-and-forget sends never look at the error.
- Warm-up with `Status()` (a throwaway query) before the real command — mirrors `ctl.py`'s pattern.
- Color conversion: an exact port of Python's `colorsys.rgb_to_hsv`, with truncation (not rounding) for bit-for-bit parity with tinytuya.

## Decisions taken against tinytuya

The implementation was checked line by line against `tinytuya`'s `Device.py`
and `XenonDevice.py`. Most of what is missing here is missing on purpose.

**Not adopted.** `XenonDevice` carries 20+ configuration methods
(`set_socketPersistent`, `set_socketNODELAY`, `set_socketRetryLimit`,
`set_socketRetryDelay`, `set_socketTimeout`, `set_sendWait`,
`cached_status`, `detect_available_dps`…), each one a flag multiplying the
reachable states. `protocol.Session` has three methods. That is the point
of this codebase, and feature parity is not a reason to give it up.

**Adopted, but layered.** tinytuya's generic `set_value`/raw-DP access is
replicated — `device.Device.SetValue`/`SetDPs` — but it lives in the raw
layer, not at the same level as the domain commands. `bulb.Bulb` is the
layer that knows what each DP *means*, so domain callers never touch a DP
number; a caller who needs an uncovered DP reaches
for `device.Device` deliberately.

`device.Device` also carries a trimmed subset of `BulbDevice`'s "sugar"
surface — only what `notuya-gui` uses — with faithful signatures apart from
`nowait bool` → `wait bool`, to match `Session`. It lives in
`device/sugar.go`, split from the generic DP access (`SetDPs`/`SetValue`/
`Status`) in `device/device.go`. The percent helpers scale against
`BrightnessMax` (1000, the Type-B full scale), take `float64`, and truncate
to the raw DP int, so fractional percents scale (33.3% → 333). The rest of
tinytuya's sugar (`SetHSV`, `SetScene`, `SetMode`, `SetWhite`, `SetWhitePercent`, the
`GetX(ctx)` getters, …) was removed as unused; add back only what a caller
needs.

Two deliberate departures from tinytuya's exact behaviour here:

- **`SetColour` does not force the bulb on.** tinytuya's `set_colour`
  asserts `switch:true`; here it only sets mode + colour and leaves on/off
  to `TurnOn`/`TurnOff`. Setting a colour on an off bulb should not
  silently turn it on.
- **Getters read an already-fetched status.** tinytuya's getters take an
  optional `state=`; here only that form exists: `GetXFrom(status)`
  (`GetModeFrom`, `ColourHSVFrom`, …) reads from a map the caller got from
  one `Status` call, so reading full state costs one round-trip.

**Deferred, with a known trigger.**

- `frame.go` does framing and crypto in one file, where tinytuya splits
  `message_helper` from `crypto_helper`. They are right that these are two
  concerns, but the split only pays off with a second cipher: 3.3 uses
  AES-ECB instead of GCM. Splitting now would be designing for a version
  nobody has written. It is the first cut to make when 3.1/3.3 lands.
- `max_simultaneous_dps` (`Device.py:141-153`): when a multi-DP `CONTROL`
  comes back `Err`, tinytuya retries one DP at a time and permanently lowers
  its limit. `SetColour` sends DP 21+24 together and `SetWhiteBrightness`
  DP 21+22; the A60TY10W accepts both. Adaptive state for hardware that is
  not on this LAN is not worth carrying, but this is the likeliest thing to
  break on a different model — the symptom is a set that returns an error
  payload while each DP works alone.
- `set_timer` (`BulbDevice.py:489`): not wrapped. The A60TY10W's DP table
  has no timer DP, and inventing a number for one that is not on this
  hardware is worse than the gap. A caller that has a timer DP drives it
  with `SetValue(dp, secs, wait)`, which is exactly tinytuya's `dps_id`
  override path.

**Where tinytuya is structurally worse.** Composition instead of
inheritance (`bulb.Bulb` → `device.Device` → `Session`, not
`Device(XenonDevice)`), and
version-as-package instead of `if self.version` scattered through the
methods. Worth an honest caveat: this stays clean partly because the
problem is smaller. tinytuya carries five protocol versions, a cloud API and
dozens of device types. Nothing has yet failed to fit behind `Session`.

## Testing

```bash
go test ./...                    # unit tests (no hardware)
NOTUYA_INTEGRATION=1 \
  NOTUYA_TEST_IP=<ip> \
  NOTUYA_TEST_KEY=<key> \
  go test ./... -v               # integration tests (requires a bulb on the LAN)
```

- `protocol35/frame_test.go`: 6699 frame encode/decode round-trip, corruption rejection, retcode stripping.
- `protocol35/handshake_test.go`: validation against a real capture fixture (`testdata/handshake/session1.json`).
- `device/device_test.go`: CONTROL_NEW/DP_QUERY_NEW payload construction (`SetDPs`/`SetValue`), fire-and-forget sends, and `Status` response parsing against a mock session.
- `device/sugar_test.go`: the sugar setters produce the right DP payloads, the `GetXFrom(status)` readers parse a status map, and range checks reject out-of-range input.
- `device/colour_test.go`: RGB → hsv16 hex against tinytuya reference vectors.
- `device/music_test.go`: DP 28 (`musicColourHex`) encoding and change-mode validation.
- `bulb/music_test.go`: streaming payload shape, latest-wins coalescing, jump (zero-value) handling, per-colour change-mode override (nil falls back to the run default), drain shutdown, and the StreamSession capability check.
- `discovery/discovery_test.go`: fixed discovery key validation, plus the announcement parsing each scanner bug hid in — retcode-prefixed replies, bare announcements, our own request echoed back, duplicate broadcast targets.
- Integration tests gated behind `NOTUYA_INTEGRATION` so `go test ./...` is hermetic by default.

The streaming path is also covered **without hardware**:
`protocol35/fakedevice_test.go` is an in-process device that speaks the real
handshake over a real TCP socket. `protocol35/stream_test.go` uses it for the
concurrency contract (fire-and-forget writes racing `DrainInbound`, session
hand-back, dropped connection, and a send outliving the context that opened
the session), and `protocol35/music_e2e_test.go` runs the
actual `bulb.StreamColours` loop against it end-to-end. These live in
`protocol35` rather than `bulb` because the fixture belongs there and
neither `device` nor `bulb` imports `protocol35`.

- `bulb/raw_test.go`: `Raw()` returns the Bulb's own `*device.Device` wired
  to the same session, so a raw DP write goes out as a CONTROL_NEW. `Raw()`
  has no in-module consumer; this test is what keeps that deliberate public
  surface exercised.
- `bulb/bulb_integration_test.go`: the full business surface
  (`Status`, `TurnOn`, `SetColour`, `TurnOff`)
  against real hardware, gated behind `NOTUYA_INTEGRATION`.

Run the streaming tests with `-race`: they are the only concurrent paths in
the project.

## Build

```bash
make build                       # go build ./...
make check                       # vet + test
make test                        # go test ./...
make vet                         # go vet ./...
make fmt                         # gofmt -l .
```

There is no cgo in any package.

## Music mode

`bulb.StreamColours` streams colours from a channel to one device over a
persistent session, so an interactive colour picker can drive a bulb live.
See the DP 28 section above for the wire format and the mode entry/exit
rules.

The unit that flows through the stream is `bulb.StreamColour` (a
`device.RGB` plus an optional `*device.ChangeMode`); a nil mode falls back to
`StreamOptions.ChangeMode`, which is what lets a picker's jump/fade toggle
take effect live mid-stream. Both are pointers because `ChangeJump` is the
zero value and must not double as "unset"; use `device.ChangeJump.Ptr()` /
`device.ChangeFade.Ptr()`.

The flow, in `bulb.StreamColours`:

1. Blocking `Status()` warm-up, while the connection is still exclusively
   owned — a dead session fails here instead of silently dropping colours.
2. Start `DrainInbound` in a goroutine.
3. Send DP 28 with `wait=false`, throttled to `DefaultStreamInterval` (40ms,
   ~25fps) and coalesced newest-wins.
4. `HEART_BEAT` if nothing was sent for ~5s, since Tuya devices drop idle
   connections and a drag pauses whenever the pointer stops.
5. On channel close or ctx cancel: flush the pending colour, stop the drain,
   then `SetColour` to leave music mode with the final colour persisted.

Cancel the context for a clean stop; abandoning the stream leaves the bulb
stuck in music mode.

Known limitations: no reconnect if a device drops mid-stream (it is reported
and that device stops), and colour-temperature/white-mode dragging is not
supported — only RGB.

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
