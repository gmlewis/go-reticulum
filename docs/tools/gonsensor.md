# gonsensor — Sensor Bridge to NMEA-0183

`gonsensor` turns a sensor service's JSON into the NMEA-0183 sentences that a GNSS
reader already speaks. It reads one JSON object per line on **stdin** and writes
NMEA sentences on **stdout**, which is normally a named pipe that `gorrcbot` or
`gonomadnet` reads.

It exists because the same position feeds two very different callers — a chat bot
answering over radio, and a terminal client on a tablet — and neither of them
should learn a second way to acquire a position. Both already own a strict
NMEA-0183 parser, so the bridge is done once, in the package that owns that
parser, and everything downstream stays unchanged.

```bash
echo '{"t":"2026-10-08T15:53:40.123Z","provider":"gps","lat":35.123456,"lng":-106.567890,"alt":1620.5,"acc":3.8,"sats":8,"quality":1,"valid":true}' \
  | gonsensor > /run/gonomadnet/gps.nmea
```

```text
$GNRMC,155340.123,A,3507.4074,N,10634.0734,W,0,0,081026,,,A*6F
$GPGGA,155340.123,3507.4074,N,10634.0734,W,1,08,,1620.5,M,,M,,*46
$GNGST,155340.123,3.8,3.8,3.8,0,2.68700576850888,2.68700576850888,*44
$HCHDM,47.5,M*1F
```

---

## Why use it

- **Your GNSS reader does not change.** `gonsensor` speaks NMEA-0183, the format
  every receiver already uses, so anything that can read a GPS can read your
  sensor service.
- **A position, a heading, or both.** Feed it only fixes, only headings, or the two
  interleaved. A device with a receiver and no magnetometer works, and so does the
  reverse.
- **It will not stop your node.** The output keeps its framing as long as stdin is
  open, so a reader never sees an end of file that is not the end of the service.
- **Bad samples are dropped quietly.** A half-flushed buffer or a restarting sensor
  service is normal; the next good sample is what matters. A periodic count on
  stderr tells you how many were dropped.

## Input

One JSON object per line. A **fix** carries a timestamp, a provider, a position and a
validity flag:

```json
{"t":"2026-10-08T15:53:40.123Z","provider":"gps","lat":35.123456,"lng":-106.567890,
 "alt":1620.5,"acc":3.8,"speed":0.4,"course":271.3,"sats":8,"quality":1,"valid":true}
```

A **heading** carries the angle and the frame it is measured in:

```json
{"t":"2026-10-08T15:53:40.223Z","heading":47.5,"frame":"magnetic"}
```

An absent `frame` means `magnetic`, because Android's rotation vector is referenced
to magnetic north. A sample in a magnetic frame produces the magnetic sentence and a
sample in a true frame produces the true one; **the two are never both emitted for
the same sample**, since a reader that took the second for a correction of the first
would be wrong by the local declination.

## Output

NMEA-0183 on stdout: each sentence begins with `$`, carries its XOR checksum, and is
terminated with CRLF. `gonsensor` exits 0 when stdin reaches end of file.

| Sentence | Emitted when | Carries |
| --- | --- | --- |
| `$GNRMC` | a valid fix | position, speed, course, date |
| `$GPGGA` | a valid fix | position, altitude, satellite count, fix quality |
| `$GNGST` | a fix with an accuracy estimate | position error |
| `$HCHDM` | a heading in a magnetic frame | magnetic heading |
| `$HCHDT` | a heading in a true frame | true heading |

## Options

| Option | Meaning |
| --- | --- |
| `--rate` | Maximum location sentences per second; `0` disables the limit |
| `--compass-rate` | Maximum heading sentences per second; `0` disables the limit |
| `--status-interval` | Seconds between status lines on stderr; `0` disables |
| `--no-rmc` | Do not emit the recommended-minimum position sentence |
| `--no-gga` | Do not emit the fix-quality sentence |
| `--no-gst` | Do not emit the position-error sentence |
| `--no-hdm` | Do not emit the magnetic-heading sentence |
| `--no-hdt` | Do not emit the true-heading sentence |
| `--version` | Print the version and exit |

The `--no-*` flags exist because a consumer asked to be told less. A reader that
wants only a position should not receive a sentence it will discard, and a stream
that carries fewer sentences is a stream with less to lose.

## Where it is used

- **`gorrcbot`** reads the pipes directly: `gps_port` and `compass_port` accept a
  path or a socket, so the bot can take its position from a converter in the same
  place a real receiver would be plugged in.
- **The Android appliance** runs one `gonsensor` per pipe, fed by a foreground
  service that translates `LocationManager` and `SensorManager` objects into the
  JSON above. See the `android` guide for that side.
- **`gonomadnet`** reads the same stream over a socket across an application
  boundary, so a client in Termux can use the sensors of the app that owns them.

## Related

- [`gorrcbot`](gorrcbot.md) — the bot that reads these sentences.
- [`gobot`](gobot.md) — ask a bot a question from a shell.
- The [Android guide](../guides/android.md) — the appliance that produces the JSON.
