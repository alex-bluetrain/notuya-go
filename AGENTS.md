# notuya-go

A Go reimplementation of the Tuya local protocol v3.5 for controlling smart
bulbs (model A60TY10W) over the LAN, with no third-party dependencies. It
replaces a sibling Python project (built on `tinytuya`), removing the need
for a Python runtime.

## Architecture

```
cmd/notuya/main.go          CLI: on, off, color, brightness, get-color, list, scan
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
```

`wait=false` in `Command` is the hook for a future "music mode" (Phase B):
fire-and-forget sends over a persistent connection, without changing the
interface.

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
CONTROL=0x07             DP_QUERY=0x0a            CONTROL_NEW=0x0d
DP_QUERY_NEW=0x10        REQ_DEVINFO=0x25
```

## Hardware DPs (A60TY10W)

| DP | Name | Type | Range |
|----|------|------|-------|
| 20 | switch_led | bool | on/off |
| 21 | work_mode | enum | `white`, `colour`, `scene`, `music` |
| 22 | bright_value_v2 | int | 10-1000 |
| 23 | temp_value_v2 | int | 0-1000 |
| 24 | colour_data_v2 | hex string | `hhhhssssvvvv` (h:0-360, s/v:0-1000, 4 hex digits each) |

**DP 24 is NOT JSON on the wire** — it is a 12-character ASCII hex string.
E.g. pure red = `"000003e803e8"`. It travels as the value of key `"24"`
inside the control JSON: `{"20":true,"21":"colour","24":"016903e803e8"}`.

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
- Every CLI operation is open → handshake → command(s) → close (non-persistent connections).
- Concurrent fan-out to all devices with `sync.WaitGroup`, per-device error isolation.
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
- `discovery/discovery_test.go`: fixed discovery key validation.
- Integration tests gated behind `NOTUYA_INTEGRATION` so `go test ./...` is hermetic by default.

## Build

```bash
make build                       # local binary in dist/notuya
make build-all                   # cross-compile linux-amd64 + windows-amd64
make test                        # go test ./...
make vet                         # go vet ./...
make fmt                         # gofmt -l .
```

`CGO_ENABLED=0` on cross-compile — there is no cgo in any package.

## Phase B (out of current scope)

"Music mode": a persistent connection with `Command(..., wait=false)` sends
for real-time color sync (equivalent to the Python project's `picker.py`,
DP 28). The current architecture already supports this without redesigning
the `Session` interface.
