# heat it USB protocol

Wire-verified against the dongle (VID/PID `32f9:0001`, serial
`250107105020-1`) and cross-checked against the decompiled official app
(APK v2.8.1.187, `jadx --show-bad-code`). This is the complete protocol
as of firmware version block `02 00 03 75 81 5e 02 03 1a ff ff 02`.

## USB transport

- Vendor 0x32f9, Product 0x0001.
- Two USB configurations; **configuration 2** is "app mode" (LED green,
  protocol active). Configuration 1 is the stock/default mode.
- One interface, one alternate setting; bulk endpoints: **OUT 0x02**,
  **IN 0x82** (endpoint number 2 in both directions).
- Strict request/response: write exactly one 12-byte frame, read exactly
  one 12-byte frame (1.5 s timeout in practice). The device never sends
  unsolicited frames in app mode.

## Frame format (both directions)

```
byte:  0     1     2 .. 2+N-1   2+N   2+N+1 .. 11
       ff    id    fields ...   ck    zero-pad to 12 bytes
```

- `id` — message identifier (see table below).
- `ck` — 8-bit checksum, **position is variable: directly after the last
  field byte**.
  - Request: `ck = sum(field bytes) % 256` (id not included).
  - Response: `ck = sum(id + field bytes) % 256`.
- Because the checksum position varies with the message, a parser must
  locate it: for known message ids the payload length is schema-driven
  (`ck` at `2 + payloadLen`); for unknown ids, scan with a running sum
  and take the rightmost validating position.

### ACK / NACK

```
ff F0 00 00 00 00 00 00 00 00 00 00   (ACK, id 0xF0)
ff F1 ...                             (NACK, id 0xF1)
```

No checksum — the payload is all zero bytes. All "fire-and-forget"
commands (start heating, abort, set LED, …) are answered with ACK.

## Message table

| id  | name | direction | fields | response |
|-----|------|-----------|--------|----------|
| 0x00 | STATUS | resp | s16 temp [0.1 °C], u8 internal, u8 external, u16 | — |
| 0x02 | GET_STATUS | req | — | STATUS (6 payload bytes, ck@8) |
| 0x07 | GET_STATISTICS | req | u8 page (0–8) | 9 raw bytes (ck@11) |
| 0x08 | START_HEATING | req | u8 tempLevel, u8 durLevel | PREHEATING_TIME (2 bytes, ck@4) |
| 0x08 | PREHEATING_TIME | resp | u16 preheat time [0.1 s] | — |
| 0x0A | SET_LED_SCALE | req | u8 R, u8 G, u8 B, u8 modifier | ACK |
| 0x0C | ABORT | req | — | ACK |
| 0x0D | GET_FLASH | req | u16 addr (BE), u8 len (max 6) | u16 addr, u8 len, 6 data bytes (ck@11) |
| 0x18 | UPDATE_HEATING_TIME | req | — | NEW_HEATING_TIME (2 bytes, ck@4) |
| 0x18 | NEW_HEATING_TIME | resp | u16 heating time [0.1 s] | — |
| 0xF0 / 0xF1 | ACK / NACK | resp | (all zeros) | — |

Note: request and response ids can share a value (0x08, 0x18) — the
direction disambiguates.

### Verified frames (from live captures, see `logs/`)

```text
; status request / response (47.8 °C, phase active)
OUT  ff 02 00 00 00 00 00 00 00 00 00
IN   ff 00 00 e0 00 02 00 00 22 00 00     ; ck 0x22 = 00+00+e0+00+02+00+00

; statistics page 0 (ck 0xbb = 07+00+84+02+2e)
OUT  ff 07 00 07 00 00 00 00 00 00 00 00
IN   ff 07 00 84 02 2e 00 00 00 00 00 bb
; statistics page 8
IN   ff 07 a7 df e7 e3 1b d0 f5 fa 51 82

; flash read, address 0 (ck 0x6c)
OUT  ff 0d 00 00 06 02 00 00 00 00 00 00
IN   ff 0d 00 00 06 02 00 03 75 81 5e 6c
; flash read, address 6 (ck 0x38)
IN   ff 0d 00 06 06 02 03 1a ff ff 02 38

; preheat time response for Child+Short (0x001c = 2.8 s; ck 0x24)
IN   ff 08 00 1c 24 00 00 00 00 00 00 00
```

## Treatment parameters

- `tempLevel` (0–3) → target temperature: **47.0 / 48.5 / 50.0 / 51.5 °C**.
- `durLevel` (0–2) → treatment duration: **4 / 7 / 9 s** (active phase).
- The official app exposes two presets: *Child* = tempLevel 1, *Adult* =
  tempLevel 3, plus a *Sensitive* modifier that subtracts one level
  (−1.5 °C).
- Peak temperatures measured on the dongle: Child+Short ≈ 48.4 °C,
  Adult+Long ≈ 51.5 °C.

## Status / phases

`external` byte (STATUS frame, field 3):

| value | phase |
|---|---|
| 0x00 | idle (also: cooling/finished) |
| 0x01 | warmup (preheating) |
| 0x02 | active (ready/heating) |

`internal` byte (field 2) and the trailing u16 were observed to be
non-zero during treatments; semantics unconfirmed.

Temperature is a big-endian s16 in 0.1 °C units (e.g. `00 e0` = 22.4 °C
at rest; the probe reads ≈ 34 °C idle after being powered a while).

## Statistics blob ("INTERNAL_MEMORY")

9 pages (0–8) × 9 bytes = **81 bytes**. The app decodes the blob with a
version-dependent layout:

- **aligned** ("legacy" in the app, fw tuple ≤ 0.0.9): 6 u8 flags,
  treatment pairs start at byte 12.
- **extended** (new fw): one extra u8 (`flash_write_counter`) at byte 12,
  pairs start at byte 13.

> **Observed discrepancy:** the app's version heuristic selects the
> *extended* layout for this dongle, but the dongle actually emits the
> *aligned* layout (data decodes cleanly only at byte 12: all 12
> (count, max-temperature) pairs are sane; the extended layout produces
> garbage). This implementation therefore always uses the aligned layout.

Layout (aligned):

```
offset  size  field (app DB column names)
0       u16   blacklisted
2       u16   counter_boots
4       u16   counter_finished_treatments
6       u8    counter_reprog
7       u8    counter_watchdog
8       u8    error_heating_element
9       u8    error_over_temperature
10      u8    error_temp_switch
11      u8    error_temperature_sensor
12 + 4i u16   treatment_i_counter        (i = 0..11)
14 + 4i s16   treatment_i_max_t  [0.1 °C]
60      u16   tail counter 0  (observed ≈ 49)
62      u16   tail counter 1  (observed ≈ 379)
64..80  —     not decoded by the app schema (page 8 raw data)
```

Example (live read, aligned decode): `blacklisted=132 boots=558`,
treatment_00 count 17 / max 47.8 °C, …, tail `49 379`.

## Version block

Flash addresses 0–11 (two 6-byte `GET_FLASH` reads) → 12 raw bytes:

```
0  u8  tuple A.0        3  u8  tuple B.0
1  u8  tuple A.1        4  u8  tuple B.1
2  u8  tuple A.2        5  u8  tuple B.2
6  u8  variant ("a-N")  8  u8  tuple C.0
7  u8  flag             9  u8  tuple C.1
                         10 u8  tuple C.2
11 u8  (unused in app)
```

The app builds `V.k(type, flag[7], (n,o,p), (i,j,k)+"a-"+[6], (f,g,h))`
with a semver class (`C2268c`) and compares, among others:
`>= (1,0,2)` → preheat-time feature, `<= (0,0,9)` → legacy statistics
layout, `>= (1,0,0)` → LED dimming. Raw block observed on this dongle:
`02 00 03 75 81 5e 02 03 1a ff ff 02`.

## LED

See [led.md](led.md).
