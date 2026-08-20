# heat it dongle — LED behavior

All findings below were verified on the wire and by direct observation
(2026-08-19, step-by-step sequence).

## SET_LED_SCALE (id 0x0A)

```
request: ff 0A [R u8][G u8][B u8][modifier u8][ck] [pad]
response: ACK
```

`ck = sum(R,G,B,modifier) % 256`.

**Modifier bits:**

| bit | value | effect |
|---|---|---|
| 0 | 1 | BLINK (slow pulse) |
| 1 | 2 | FLASH (fast) |
| 2 | 4 | MANUAL — user color takes effect |

The R/G/B color only has a visible effect when MANUAL (4) is set;
otherwise the firmware keeps its own phase coloring.

## Phase LED state machine (firmware-driven)

The phase animation runs **in the firmware**, but only once "armed".
After a plain USB connect (config 2) with no SET_LED sent, the dongle
shows only its default state and **no phase animation**.

Arming command — what the official app sends on connect
(`e(false, null, null, null)`):

```
ff 0A ff ff ff 01 45 00 00 00 00 00      ; white + BLINK (modifier 1)
```

Once armed, the LED follows the treatment phase:

| phase | LED |
|---|---|
| idle / standby | green slow-flash |
| warmup (preheating, ext=1) | violet blinking |
| active (ready, ext=2) | blue steady |
| after finish | back to green slow-flash |

Desktop-app consequence: send `SetLed(255, 255, 255, 1)` once right
after connecting (done in `ctrl.connect()`).

## Official app "flashlight" feature

User-selectable custom LED color (device capability
`deviceHasFlashLightFeature`): on → modifier **7** (BLINK|FLASH|MANUAL)
with user R/G/B; off → modifier **1** (firmware phase LED returns).

## User-facing LED meanings (from the app's manual strings)

- **green pulsing** — plugged in / app connected
- **red pulsating** — can't connect to the app
- **red flashing** — power present, no data
- **not blinking** — no power
