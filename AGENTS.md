# notuya-go

A Go reimplementation of the Tuya local protocol v3.5 for controlling smart
bulbs (model A60TY10W) over the LAN, with no third-party dependencies. It
replaces a sibling Python project (built on `tinytuya`), removing the need
for a Python runtime.

## Architecture

```
cmd/notuya/main.go          CLI: on, off, color, brightness, get-color, list, scan
cmd/notuya/music.go         CLI: music (stdin colour streaming)
internal/
  protocol/                  Version-agnostic interface (Session)
  protocol35/                v3.5 implementation: 6699 framing, AES-GCM, session handshake
  device/                    Bulb API and DP encoding
  discovery/                 UDP scanner (ports 6667/7000)
  config/                    config.json loading and last-color cache
```

### Dependency flow

```
cmd/notuya → config, device, discovery, protocol35
device     → protocol (the interface, never protocol35 directly)
discovery  → protocol, protocol35 (uses EncodeFrame/DecodeFrame with the fixed discovery key)
protocol35 → protocol (command constants)
```

`device` depends only on `protocol.Session`. Adding support for another
version (3.1/3.3) means writing a new `protocol3x.Session` implementing the
same interface — without touching `device` or the CLI.

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
(see below). `StreamSession` is an optional add-on: `device.StreamColours`
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

A 21-character ASCII hex string: `transition(1) + hsv16(12) +
white_brightness(4) + colourtemp(4)`. E.g. pure red with transition 0 is
`"0000003e803e800000000"`.

- `transition` is a **single hex digit** (0-10 → `0`-`a`): the per-update
  fade length. This is the whole point of music mode — DP 24 always applies
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

> **Note**: the scanner does not work on every network (AP isolation,
> segmentation). Direct control by IP always works. If the scanner fails,
> use fixed IPs in `config.json`.

## Configuration

Precedence for the `config.json` path: `--config` flag → `NOTUYA_CONFIG`
env → `os.UserConfigDir()/notuya-go/config.json`.

Only the `devices` array of the JSON is read. Each device has:
`device_id`, `ip_address`, `local_key`, `name`. The format is compatible
with the Python project's `config.json`.

## Code conventions

- **Go 1.23**, module `github.com/averstraeten/notuya-go`.
- **Zero external dependencies** — everything with the stdlib (`crypto/aes`, `crypto/cipher`, `crypto/hmac`, `crypto/sha256`, `crypto/md5`, `encoding/binary`, `encoding/json`).
- Every CLI operation is open → handshake → command(s) → close (non-persistent connections). `music` is the exception: one session per device for the whole run.
- Concurrent fan-out to all devices with `sync.WaitGroup`, per-device error isolation.
- `Session` deadlines are set per direction (`SetReadDeadline`/`SetWriteDeadline`, never `SetDeadline`): during a stream a `wait=false` write must not disturb the read deadline owned by the concurrent `DrainInbound`.
- Warm-up with `Status()` (a throwaway query) before the real command — mirrors `ctl.py`'s pattern.
- Color conversion: an exact port of Python's `colorsys.rgb_to_hsv`, with truncation (not rounding) for bit-for-bit parity with tinytuya.

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
- `config/config_test.go`: config.json parsing and last-color round-trip.
- `device/client_test.go`: CONTROL_NEW/DP_QUERY_NEW payload construction against a mock session.
- `device/colour_test.go`: RGB ↔ hsv16 hex round-trip.
- `device/music_test.go`: DP 28 encoding, streaming payload shape, latest-wins coalescing, drain shutdown.
- `discovery/discovery_test.go`: fixed discovery key validation.
- `cmd/notuya/music_test.go`: the stdin broadcast never blocks and keeps the newest colour.
- Integration tests gated behind `NOTUYA_INTEGRATION` so `go test ./...` is hermetic by default.

The streaming path is also covered **without hardware**:
`protocol35/fakedevice_test.go` is an in-process device that speaks the real
handshake over a real TCP socket. `protocol35/stream_test.go` uses it for the
concurrency contract (fire-and-forget writes racing `DrainInbound`, session
hand-back, dropped connection), and `protocol35/music_e2e_test.go` runs the
actual `device.StreamColours` loop against it end-to-end. These live in
`protocol35` rather than `device` because the fixture belongs there and
`device` does not import `protocol35`.

Run the streaming tests with `-race`: they are the only concurrent paths in
the project.

## Build

```bash
make build                       # local binary in dist/notuya
make build-all                   # cross-compile linux-amd64 + windows-amd64
make test                        # go test ./...
make vet                         # go vet ./...
make fmt                         # gofmt -l .
```

`CGO_ENABLED=0` on cross-compile — there is no cgo in any package.

## Music mode

`notuya music` reads `RRGGBB` lines from stdin and streams them to every
configured device over a persistent session — the equivalent of the Python
project's `picker.py`. See the DP 28 section above for the wire format and
the mode entry/exit rules.

The flow, in `device.StreamColours`:

1. Blocking `Status()` warm-up, while the connection is still exclusively
   owned — a dead session fails here instead of silently dropping colours.
2. Start `DrainInbound` in a goroutine.
3. Send DP 28 with `wait=false`, throttled to `DefaultStreamInterval` (40ms,
   ~25fps) and coalesced newest-wins.
4. `HEART_BEAT` if nothing was sent for ~5s, since Tuya devices drop idle
   connections and a drag pauses whenever the pointer stops.
5. On stdin close or ctx cancel: flush the pending colour, stop the drain,
   then `SetColour` to leave music mode with the final colour persisted.

The CLI broadcasts one stdin to per-device channels (`offer` in
`cmd/notuya/music.go`) so a slow or dead bulb cannot stall the reader or the
other devices. SIGINT is a clean stop, not a kill: otherwise a bulb is left
stuck in music mode.

Known limitations: no reconnect if a device drops mid-stream (it is reported
and that device stops), and colour-temperature/white-mode dragging is not
supported — only RGB.
