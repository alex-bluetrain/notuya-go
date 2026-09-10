# notuya-go

A dependency-free Go reimplementation of the Tuya local protocol v3.5 — the
protocol spoken by the smart bulbs previously driven by a sibling Python
project built on `tinytuya`.

No Go or Rust library today supports protocol 3.5 (GCM + session handshake)
— every community alternative stops at 3.1-3.3 (plain AES-ECB, no session
negotiation). This project implements it from scratch, using
[`tinytuya`](https://github.com/jasonacox/tinytuya)'s own source as a
byte-for-byte reference.

## Status

In development. See the milestone plan in `PLAN.md` (if present) or in the
commit history — the order of work is: session handshake (validated
byte-for-byte against real captured traffic) → control-command layer →
bulb API → CLI → discovery scanner → Linux/Windows cross-compile.

## Scope

- Tuya local protocol **v3.5 only** for now, with the `protocol.Session`
  interface designed so that adding other versions (3.1/3.3/3.4) does not
  require touching the layers above it.
- Basic control: `on`, `off`, `color`, `brightness`, `get-color`, `list`,
  `scan`, plus `music` for live colour streaming over a persistent socket.
- No external dependencies — only the Go stdlib (`crypto/aes`,
  `crypto/cipher`, `crypto/hmac`, `crypto/sha256`, `crypto/md5`).
- Cross-platform: `make build-all` produces binaries for Linux and Windows
  (`CGO_ENABLED=0`, no cgo in any package).

## Usage

```bash
make build
./dist/notuya list
./dist/notuya on
./dist/notuya color ff8800
./dist/notuya brightness 60
```

Expected configuration (see `internal/config`): the same format as the
original Python project's `config.json`; only the `devices` array is read.

### `notuya music`

Streams colours read from stdin — one `RRGGBB` per line — to every
configured device, keeping one session open for the whole run:

```bash
# Live drag from any tool that prints colours as it goes.
my-color-picker --follow | ./dist/notuya music

# Or a quick sweep.
for i in $(seq 0 255); do printf '%02x00%02x\n' "$i" $((255-i)); sleep 0.02; done | ./dist/notuya music
```

`notuya color` is a one-shot command, and the bulb applies its own fade of
roughly half a second to it, which makes dragging a colour picker feel
laggy. `music` instead sends on the bulb's music-mode datapoint, which takes
a per-update transition time, so colours track the pointer.

Updates are coalesced: if colours arrive faster than the bulb can accept
them, the newest wins and the stale ones are dropped, rather than the bulb
falling progressively further behind. A slow or unreachable device is
reported on stderr without stalling the others.

The stream ends when stdin closes or on Ctrl-C, and each bulb is left
holding the last colour it received (also saved for `notuya get-color`).

### `notuya scan`

Listens for Tuya discovery broadcasts (unsolicited UDP/6667, solicited
UDP/7000) for 10s. On some networks (APs with client isolation, or that do
not forward broadcast between segments) this finds nothing — that is a
network limitation, not a protocol one: in this project's development
environment, neither `notuya scan` nor `python -m tinytuya scan` (the
official reference) find the devices via broadcast, even though direct
control by IP (`on`/`off`/`color`/etc.) works fine against those same
bulbs. If the same happens to you, use fixed `ip_address` values in
`config.json` (or a MAC-based DHCP reservation on your router) instead of
relying on the scanner.
