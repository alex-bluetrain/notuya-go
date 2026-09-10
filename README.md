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
  `scan`. "Music mode" (live drag, persistent socket) is left for a future
  phase.
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
