# SimHub Ambient Lights — device interfaces

Research notes for building a "fake" device layer: something that pretends to
be one of SimHub's supported ambient-light devices and forwards the colours to
our own Tuya bulbs (via `bulb.StreamColours`).

SimHub is closed source. Everything below describes each **device's protocol**
from its own documentation or from public reverse-engineering. Exactly which
subset SimHub sends is **unverified** — capture SimHub's traffic before
implementing.

## Officially supported hardware

Source: <https://github.com/SHWotever/SimHub/wiki/Ambient-lights> (the feature
is marked experimental, "as is").

| Backend | Requirements / limits stated by SimHub |
|---|---|
| Standard Adalight | Via a dedicated plugin; DIY Arduino + WS2812B strips |
| Bluetooth flood lights | "Happy Lighting" app family; no model list; prefer flood lights; PC needs BLE; no phone connected at the same time |
| Govee | Since SimHub 8.3.0; must be "DreamView" compatible and on SimHub's own list; LAN control enabled in the Govee app; max 10 segments; disable other controllers (app, Alexa, Google) |
| Philips Hue | "Entertainment" lights through a Hue Bridge; max 12 lights per entertainment area; dimmer than direct control |
| Razer Chroma | Chroma Link exposes 5 zones; other Chroma devices get one colour each; higher latency |

The developer does not plan to support other brands.

## 1. Adalight — serial

- **Transport:** USB serial (COM) port. SimHub writes, device reads.
- **Header (6 bytes):** `'A' 'd' 'a'`, LED count high byte, LED count low
  byte, checksum = `hi ^ lo ^ 0x55`. The count is sent as `N - 1`
  (actual count = `hi*256 + lo + 1`).
- **Payload:** 3 bytes per LED, `R G B`, 0 = off, 255 = full.
- **Auth / discovery:** none; the user picks the COM port.
- **Faking it:** needs a COM port on the Windows SimHub machine — a virtual
  null-modem pair (e.g. com0com) with our process on the other end, or a cheap
  USB microcontroller that relays frames over the network.

Refs: Adafruit forum (Adalight ledstream protocol),
<https://github.com/dmadison/Adalight-FastLED>.

## 2. Bluetooth flood lights — Triones / Happy Lighting (BLE GATT)

- **Service** `0xFFD5`, **write characteristic** `0xFFD9` (write without
  response). Fire and forget, no timely acks. Status notifications come from
  `0xFFD4`.
- **Commands:**
  - Power on: `CC 23 33`
  - Power off: `CC 24 33`
  - Colour: `56 RR GG BB WW MM AA`, where `MM` = `F0` RGB, `0F` white
  - Status query: `EF 01 77` → 12-byte reply `66 … 99`
- **Discovery is a filter:** Happy Lighting only handles devices whose BLE name
  starts with `Triones`, `BRGlight`, `Dream` or `Light`. SimHub also filters by
  "bluetooth signature" (developer, on the SimHub forum: an unrecognised
  device won't appear). The exact rule is not published — copy a real
  device's advertisement (name + services).
- **Faking it:** needs a BLE peripheral (ESP32, or Linux/BlueZ as a GATT
  server). The most hardware-dependent option.

Refs: <https://github.com/madhead/saberlight/blob/master/protocols/Triones/protocol.md>,
<https://github.com/8none1/pytrionesmqtt>.

## 3. Govee DreamView — UDP on the LAN

- **Ports:**
  - Discovery: client multicasts to `239.255.255.250:4001`
    (`{"msg":{"cmd":"scan","data":{"account_topic":"reserve"}}}`).
  - The device replies to the sender's IP on **UDP 4002** (not the source port).
  - Commands go to the device on **UDP 4003**.
- **Streaming is NOT the documented LAN API** (that one only sets a single
  whole-device colour). It is the undocumented "razer" command used by Govee
  Desktop and Razer Synapse:

  ```json
  {"msg":{"cmd":"razer","data":{"pt":"<base64 binary>"}}}
  ```

  The binary payload is `BB 00 <len> <cmd> <data…> <xor checksum over all bytes>`:
  - `0xB1` — enable/disable streaming (`01`/`00`). The device drops out of
    streaming after ~1 minute with no LED data.
  - `0xB0` — per-segment LED data (RGB triplets).
- **SimHub specifics (assumed, verify by capture):** the scan reply likely
  needs a DreamView-capable `sku` that appears on SimHub's compatibility list.
  SimHub caps it at 10 segments.
- **Faking it:** a UDP responder for the scan (4001 in, reply to 4002) plus a
  listener on 4003 that decodes `razer` frames. No auth, no TLS, standard
  library only.

Refs: Govee LAN API guide <https://app-h5.govee.com/user-manual/wlan-guide>,
OpenRGB MR !2172 (razer protocol notes),
<https://github.com/fu-raz/signalrgb-govee-direct-connect>,
<https://github.com/wez/govee2mqtt/blob/main/docs/LAN.md>.

## 4. Philips Hue Entertainment — bridge emulation

Requires emulating a whole Hue Bridge:

1. mDNS advertisement as `_hue._tcp`.
2. Hue REST API (v1) for pairing ("press link button") → returns `username`
   + `clientkey`.
3. Entertainment group configuration (lights + positions).
4. Streaming on **UDP 2100**, **DTLS 1.2, PSK only**, cipher
   `TLS_PSK_WITH_AES_128_GCM_SHA256` (PSK identity = username, key =
   clientkey), carrying **HueStream** frames.

A third-party SimHub plugin calls SimHub's backend "native Hue V1 output",
which suggests SimHub uses API v1 + HueStream v1 (unverified).

- **Faking it:** the largest job. Go's standard library has no DTLS, so a Go
  implementation would need e.g. `pion/dtls` — that breaks this repo's
  zero-dependency rule.

Existing emulators to study: <https://github.com/83noit/ha-hue-entertainment>,
diyHue.

## 5. Razer Chroma — local SDK server

- **REST:** `http://localhost:54235/razer/chromasdk`. POST app info → a
  per-session address; then PUT effects to `<session>/chromalink`, e.g.
  `{"effect":"CHROMA_CUSTOM","param":[5 BGR ints]}`.
- Sessions time out after 15 s without traffic (heartbeat PUT to
  `<session>/heartbeat`).
- **Caveat:** SimHub may call Razer's native SDK DLL instead of REST
  (unknown). Either way, faking it means replacing Razer's own software on the
  SimHub PC. Most fragile option, and only 5 zones.

Refs: <https://assets.razerzone.com/dev_portal/REST/html/index.html>.

## Recommendation

**Fake a Govee DreamView device.** It's plain UDP that the Go standard library
handles: no auth, no extra hardware, and it can run anywhere on the LAN.
Decoded `razer` frames map directly onto `bulb.StreamColour` values fed through
`bulb.StreamColours`. The A60TY10W is one colour zone, so report a single
segment or average the segments. The existing 40 ms throttle with newest-wins
coalescing already absorbs SimHub's frame rate.

Runner-up: **Adalight**. The format is trivial; the virtual COM port on
Windows is the annoying part.

### Before writing code

Capture SimHub ↔ Govee traffic with Wireshark on UDP 4001–4003, using a real
or borrowed device. That shows:

- which `sku`/fields in the scan reply SimHub accepts;
- the exact `razer` packets it sends (enable, segment count, frame rate);
- whether it sends anything else (status queries, `turn`, `brightness`).
