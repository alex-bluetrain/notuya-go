# What a Tuya light bulb can do

A bulb is a set of **data points (DPs)**. Anything you can do to it is either
reading DPs or writing them.

## Data points

Source: Tuya's
[Function Definition of Lighting Products](https://developer.tuya.com/en/docs/iot/product-function-definition?id=K9s9rhj576ypf).
Everything in this section is as that page states it.

| DP | Name | Identifier | Access | Type | Values |
|---|---|---|---|---|---|
| 20 | On/off *(required)* | `switch_led` | read/write | Boolean | `true` / `false` |
| 21 | Mode *(required)* | `work_mode` | read/write | Enum | `white`, `color`, `scene`, `music` |
| 22 | Brightness | `bright_value` | read/write | Value | 10–1000 (1%–100% in the app) |
| 23 | Color temperature | `temp_value` | read/write | Value | 0–1000 (0%–100% in the app) |
| 24 | Color | `colour_data` | read/write | String | `hhhhssssvvvv`, see below |
| 25 | Scene | `scene_data` | read/write | String | see below |
| 26 | Timer | `countdown` | read/write | Value | 0–86400 seconds |
| 27 | Music sync | `music_data` | write only | String | see below |
| 28 | Real-time adjustment | `control_data` | write only | String | same format as 27 |
| 34 | Do not disturb | `do_not_disturb` | read/write | Boolean | `true` / `false` |

Access is Tuya's "send and report" (read/write) or "send only" (write only).

Notes from the page:

- **21:** the in-app menus depend on the DPs a product has: white needs 21 + 22,
  color needs 21 + 24, scene needs 21 + 25, music needs 21 + 27, and the timer
  needs 26.
- **23:** the range spans coolest to warmest white; the actual temperature
  depends on the LEDs (e.g. 2700K–6500K). The page's summary table says
  10–1000; its DP description says 0–1000.
- **26:** 60 is one minute; 86400 is shown as 23:59.
- **28:** "used to receive commands from the cloud when users control the
  light on the mobile app".
- **34:** for places with frequent power cuts. If the light was turned off
  from the app, it has to be switched on and off twice in a row to turn on.

### Formats

All fields are hex. Hue is 0–360 (`0x0000`–`0x0168`); every other field is
0–1000 (`0x0000`–`0x03E8`).

**24, color:** `hhhh ssss vvvv` (hue, saturation, value).
Example: `"00DC004B004E"` = hue 220, saturation 75, value 78.

**25, scene:** `ii` (scene id), followed by one or more color units:

```
tt dd mm hhhh ssss vvvv bbbb cccc
```

| Field | Meaning |
|---|---|
| `tt` | transition interval, 0–100 |
| `dd` | duration, 0–100 |
| `mm` | `00` static, `01` jumping, `02` gradient |
| `hhhh` `ssss` `vvvv` | color |
| `bbbb` `cccc` | brightness, color temperature |

Example: `"010b0a02000003e803e800000000"` is scene 01 with one color unit.

**27 and 28:** `m hhhh ssss vvvv bbbb cccc`, where `m` is `0` jumping or
`1` gradient. Example: `"1007603e803e800120025"`.

### Which DPs a bulb has

Only 20 and 21 are required. Every other DP depends on the bulb model: a
white-only bulb has no 24, a basic bulb may have no 25–28, and so on. Read the
bulb's status to see which of the read/write DPs it has. Write-only DPs never
show up there, so the only way to find out is to try them.

### Advanced DPs

Source: the same Tuya page. These are optional and model-dependent. Except for 29,
they are raw binary data with a version byte, and percentages are 0–100 here,
not 0–1000.

| DP | Name | Identifier | Access | What it does |
|---|---|---|---|---|
| 29 | Gamma debug | `debug_data` | write only | Gamma calibration with a debugging panel. Same format as 27. Factory use. |
| 30 | Biorhythm | `rhythm_mode` | read/write | Up to 8 time nodes, each with a color/brightness; the light fades between them. |
| 31 | Light to sleep | `sleep_mode` | read/write | Up to 4 schedules that dim the light over 5–120 minutes. |
| 32 | Light to wake | `wakeup_mode` | read/write | Up to 4 schedules that brighten the light over 5–120 minutes, then optionally turn it off. |
| 33 | Power-off memory | `power_memory` | read/write | State after power returns: default, last state, or a custom color/brightness. |
| 209 | Cycle timing | `cycle_timing` | read/write | Repeating on/off cycles (e.g. plant grow lights). |
| 210 | Vacation timing | `random_timing` | read/write | Turns the light on and off at random times inside a window. |

Schedules (30–32, 209, 210) use a weekday bitmask: bit0 = Sunday … bit6 =
Saturday; all zero means run once. The page documents each one byte by byte.

### Custom DPs

Manufacturers can add their own non-standard DPs ("custom functions" on the
same page). Their numbers and meaning are model-specific and not documented
anywhere public. A DP in a status reply that is not listed here is most likely
one of these.

### Older bulbs (DPs 1–8)

Not on Tuya's page; source: tinytuya (`BulbDevice.py`). Older bulbs use a
different, smaller set starting at DP 1, with 8-bit values instead of 0–1000.

| DP | Function | Values |
|---|---|---|
| 1 | On/off | `true` / `false` |
| 2 | Mode | `white`, `colour`, `scene`, `music`, `scene_1`–`scene_4` |
| 3 | Brightness | 25–255 |
| 4 | Color temperature | 0–255 |
| 5 | Color | RGB + HSV hex string |
| 6 | Scene | string |
| 7 | Timer | seconds |
| 8 | Music | string |

Some dimmer-only bulbs use just 1 (on/off), 2 (brightness) and 3 (color
temperature). tinytuya assumes a bulb uses either this set or the 20+ set,
not both.

## Messages

Source: codes and names from Tuya's embedded SDK header
[`lan_protocol.h`](https://github.com/tuya/tuya-iotos-embeded-sdk-wifi-ble-bk7231n/blob/master/sdk/include/lan_protocol.h).
The header has no descriptions; the "What it does" column comes from tinytuya.

| Message | Code (3.5) | Tuya name | Direction | What it does |
|---|---|---|---|---|
| Query | 16 | `FRM_QUERY_STAT_NEW` | you → bulb | Returns every readable DP |
| Control | 13 | `FRM_TP_NEW_CMD` | you → bulb | Sets the DPs you send (others are left alone) |
| Refresh | 18 | `FRM_LAN_QUERY_DP` | you → bulb | Asks the bulb to re-send specific DPs |
| Status push | 8 | `FRM_TP_STAT_REPORT` | bulb → you | Sent unprompted whenever a DP changes |
| Heartbeat | 9 | `FRM_TP_HB` | you → bulb | Keeps the connection alive; changes nothing |

## Discovery

How bulbs are found on the network, before any connection. All UDP broadcast.

Source: codes from `lan_protocol.h` where they exist; ports, key and payload
from tinytuya.

| Message | Code | Tuya name | Port | Direction | What it does |
|---|---|---|---|---|---|
| Announcement | 0x13 | `FR_TYPE_ENCRYPTION` | 6667 | bulb → everyone | Bulb broadcasts its id, IP and version every few seconds (3.3+; 3.1 used plaintext on 6666) |
| Announcement (3.4) | 0x23 | `FR_TYPE_BOARDCAST_LPV34` | ? | bulb → everyone | Listed in Tuya's header as the 3.4 broadcast; not seen from 3.5 bulbs |
| Info request | 0x25 | — | 7000 | you → everyone | `{"from":"app","ip":"<your ip>"}`: asks 3.5 bulbs to reply with their info |

Broadcasts are encrypted with a fixed key, `md5("yGAdlopoPVldABfn")`, not the
device's own key. Discovery returns id, IP and version only, never the local key.
`0x25` is not in Tuya's header.

## Pairing and Wi-Fi setup

Used once, to put a new bulb on your Wi-Fi and bind it to an account. Not
needed to control a paired bulb.

Source: codes and names from `lan_protocol.h`. The payloads are not publicly
documented.

| Message | Code | Tuya name | What it does |
|---|---|---|---|
| AP config (v3.0) | 1 | `FRM_TP_CFG_WF` | Sends Wi-Fi credentials to a bulb in AP mode (old format) |
| AP config (v4.0) | 0x14 | `FRM_AP_CFG_WF_V40` | Same, newer format |
| Wi-Fi info | 0x0f | `FRM_CFG_WIFI_INFO` | Wi-Fi configuration info |
| User bind | 0x0c | `FRM_USER_BIND_REQ` | Binds the device to a user/account |
| Add sub-device | 0x0e | `FRM_ADD_SUB_DEV_CMD` | Gateways only: pair a sub-device |

AP mode means the bulb hosts its own Wi-Fi network (`SmartLife-XXXX`) and the
app connects to it. The other pairing method, EZ / SmartConfig, sends no
messages like these at all.
