# grl — the Go Reticulum Lifesaver

`grl` is the **Go Reticulum Lifesaver**: a sovereign, pocket-sized off-grid
survival communicator and field assistant in **one executable**.

The same application is designed to run on an ultra-low-power ESP32-C5 as
bare-metal firmware and, unchanged in behaviour, on a workstation. `grl` is the
workstation half of that promise, and it is the reference the firmware is
measured against. There is no second implementation to drift out of sync: the
field tools, the sensors, and the dashboard are one library
([`github.com/gmlewis/go-reticulum/bot`](https://pkg.go.dev/github.com/gmlewis/go-reticulum/bot))
that both the appliance
and the RRC chat bot run.

!!! info "The Golden Architecture Rule"
    **Survival intelligence runs locally, in-process, with zero radio hops and
    zero airtime.** A first-aid card, a repeater bearing, the sunset, or the
    position card must resolve in microseconds, in RAM, whether or not a single
    packet can be sent. Long-range radio is reserved for what radio is for:
    distress beacons, coordinating with rescue teams, peer messaging, and syncing
    with a hub **only when a gateway happens to be reachable**.

---

## What it runs

One `grl` process brings up four things, in this order:

| Subsystem | What it is | When it runs |
|---|---|---|
| **Reticulum** | A local RNS stack, configured by `rns.config_path` | Always |
| **Sensors** | A pure-Go NMEA-0183 GNSS receiver and electronic compass, or their static fallbacks | When a port or a static value is configured |
| **Field assistant** | The shared in-process command engine (`bot.Engine`): `whereami`, `med`, `tower`, `sun`, `moon`, `sos`, `checkin`, `morse`, `conv`, `firstaid`, and the rest | Always |
| **Captive dashboard** | An HTTP dashboard and JSON API for a smartphone browser | When `portal_addr` is set |

Shutdown is the reverse, so nothing is left reading a device or holding a
socket. `grl` exits **0** on a clean exit, **1** on an operational failure, and
**2** on a usage error.

---

## The sovereign appliance model

Commercial satellite messengers cost $300–$600 and charge $15–$65 per month; if
the subscription lapses, the emergency hardware is **remotely deactivated**.
They also cannot tell you what to do about hypothermia, cannot find the nearest
amateur repeater, and cannot function without a satellite link.

`grl` inverts all three properties:

- **No subscription, no account, no app store.** The knowledge is compiled in.
- **No vendor silo.** It speaks Reticulum, so it talks to any Reticulum peer,
  any RRC hub, any LXMF destination, and any amateur operator running the same
  open stack.
- **No link required for the answers that matter.** The survival tools are pure
  mathematics and embedded static tables, so a canyon, a wet tree canopy, or a
  ridgeline between you and the nearest relay changes nothing about the first-aid
  card or the position card.

---

## The zero-hardware UI paradigm

Sourcing a custom LCD, a physical keyboard, and an injection-moulded case is
what drives open communicators to $80–$150 and produces fragile gadgets with
tiny keys. Almost every traveller already carries a better terminal: a phone
with a high-resolution touchscreen and a modern browser, whose Wi-Fi and browser
keep working in airplane mode with zero cellular coverage.

So `grl` serves the dashboard over HTTP and lets the phone be the screen:

- On the **ESP32-C5** target, the appliance brings up a Wi-Fi 6 SoftAP
  (`Reticulum-Lifesaver-[ID]`); the phone joins it and its operating system pops
  the dashboard up by itself.
- On a **desktop workstation**, `grl` hosts the same dashboard on
  `portal_addr` with no root privileges and without touching the host's Wi-Fi
  adapters. Open <http://localhost:9111/> in any browser — including the one on
  the phone next to you, when you bind `0.0.0.0:9111`.

Either way the URL routing is identical, because every captive-probe route the
common operating systems already request answers with `302 Found → /`:

| Probe route | Requested by |
|---|---|
| `/generate_204`, `/gen_204` | Android |
| `/hotspot-detect.html` | Apple iOS and macOS |
| `/ncsi.txt`, `/connecttest.txt` | Windows |

### The dashboard

| Card | What it shows |
|---|---|
| **Where Am I** | Plus Code (with a copy button), coordinates, Maidenhead grid, elevation, GNSS fix quality, local solar time, and the sunset countdown |
| **Compass & Direction Finding** | A live compass rose with a second needle vectored at the nearest repeater, cell site, or active distress beacon, and the relative turn that aims an antenna at it |
| **Emergency** | A single-button SOS that raises a distress beacon at the verified position |
| **Field Assistant** | A query box that runs the same offline commands the radio answers, with zero hops |

### JSON API

The dashboard is a client of its own API, so a widget, a second screen, or a
script can poll the same facts:

| Method | Path | Answer |
|---|---|---|
| `GET` | `/api/whereami` | The operational card as JSON, including `plus_code`, `coordinates`, `maidenhead`, `sunset_countdown`, `lines` (the card exactly as the radio renders it), and a live `heading` |
| `GET` | `/api/compass` | The live heading: magnetic, true, the local variation, the cardinal sector, and the nearest site or beacon |
| `POST` | `/api/query` | Runs one offline command. Body: `{"command": "med hypothermia"}` (or `{"text": "..."}`) |

```console
$ curl -s http://localhost:9111/api/whereami | python3 -m json.tool
$ curl -s -X POST http://localhost:9111/api/query \
    -d '{"command":"tower near"}' | python3 -m json.tool
```

---

## Quick start on a desktop

```console
$ go build -o bin/grl ./cmd/grl
$ ./bin/grl                        # creates ~/.grl/config.toml and starts
```

`grl` writes a fully commented `~/.grl/config.toml` on the first run and keeps
running — it needs no editing to be useful. Open
<http://localhost:9111/> and the dashboard is live.

With **no hardware attached at all**, the appliance still works: set a
`static_fix` and a `static_heading` and every position-aware tool answers from
them. That is how the same configuration serves a desk and a trail.

```console
# A rehearsed appliance on a desk, with a static position and heading:
$ ./bin/grl --gps-port '' --portal-addr 0.0.0.0:9111
```

To see exactly which file and which devices an appliance resolved:

```console
$ ./bin/grl --verbose
2026/01/01 12:00:00 grl: configuration /Users/you/.grl/config.toml: callsign GRL-NOMAD, ...
2026/01/01 12:00:00 grl: static GNSS fix 37.7553,-122.4527
2026/01/01 12:00:00 grl: static compass heading 042
2026/01/01 12:00:00 grl: captive portal on http://127.0.0.1:9111/
```

---

## Command-line flags

| Flag | Meaning |
|---|---|
| `-config PATH` | Configuration file to use. Default: `$GRL_HOME/config.toml`, or `~/.grl/config.toml` |
| `-portal-addr ADDR` | Dashboard listen address, like `127.0.0.1:9111` or `0.0.0.0:9111`. An empty value disables the dashboard |
| `-gps-port PORT` | GNSS receiver serial port, like `/dev/tty.usbserial-0001` or `/dev/ttyUSB0` |
| `-compass-port PORT` | Electronic compass serial port, like `/dev/tty.usbserial-0002` or `/dev/ttyUSB1` |
| `-quiet` | Only report errors |
| `-verbose` | Print the resolved configuration and run the Reticulum log at INFO |
| `-version`, `-V` | Print the version and exit |
| `-h`, `-help` | Print the usage text and exit |

Every flag is an **override**: a flag that is not given leaves the
configuration file's value alone, and an override is validated with exactly the
same wording as a bad file, so a typo on the command line is reported before
anything binds.

`GRL_HOME` overrides the appliance's home directory (used literally, with no
expansion). It is how a second appliance — or a test — stays isolated on one
workstation.

---

## Configuration reference

`~/.grl/config.toml` is a TOML document. Every value below is the built-in
default, and the file `grl` generates *is* those defaults, with comments. A
configuration with no file at all still runs.

A leading `~` expands to your home directory in `storage_dir`,
`rns.config_path`, `gnss.port`, and `compass.port`.

### `[device]`

| Key | Default | Meaning |
|---|---|---|
| `callsign` | `"GRL-NOMAD"` | The name the appliance answers to on the mesh. The default is deliberately fictional — replace it before transmitting |
| `nickname` | `""` | An optional shorter name used on the air. Empty means "use the callsign" |
| `storage_dir` | `~/.grl/storage` | Where the appliance keeps distress beacons, situation reports, check-in timers, and saved history |

### `[portal]`

| Key | Default | Meaning |
|---|---|---|
| `portal_addr` | `"127.0.0.1:9111"` | The dashboard's listen address. Empty disables it entirely — no listener is bound |

| Value | Effect |
|---|---|
| `127.0.0.1:9111` | **Desktop testing mode.** Reachable only from this workstation, at <http://localhost:9111/>. Nothing is exposed to the local network |
| `0.0.0.0:9111` | **Field / LAN mode.** Reachable from every device on the same network at `http://<this-host>:9111/`. Use this to test the real smartphone experience before you have hardware |
| `:9111` | Reachable on every interface and address family |
| *(empty)* | No dashboard at all. The field assistant is still complete and still answers |

### `[gnss]`

| Key | Default | Meaning |
|---|---|---|
| `port` | `""` | The NMEA-0183 receiver device, e.g. `/dev/tty.usbserial-0001` (macOS) or `/dev/ttyUSB0` (Linux) |
| `baud` | `9600` | The receiver's line speed. See the note below |
| `static_fix` | `""` | A fixed position in any notation `whereami` accepts, e.g. `"37.7553,-122.4527"`. Used when no receiver is attached (or when no port is set) |

!!! note "Line speed"
    The shared NMEA reader opens the device **read-only without reconfiguring
    its line speed**, because changing it requires platform-specific termios
    ioctls that the standard library does not expose. A fresh USB-UART adapter
    is conventionally already at 9600, which is the default here. If your
    receiver needs another speed, set it on the device (`stty -f /dev/ttyUSB0
    38400`) or rely on the receiver's own auto-baud. `baud` records the intended
    hardware profile, and is the value the ESP32-C5 firmware's UART setup uses,
    so one configuration describes both targets.

### `[compass]`

| Key | Default | Meaning |
|---|---|---|
| `port` | `""` | The NMEA-0183 compass device, e.g. `/dev/tty.usbserial-0002` or `/dev/ttyUSB1` |
| `baud` | `9600` | The compass's line speed (same note as above) |
| `static_heading` | `""` | A fixed **magnetic** heading in degrees, e.g. `"042"`. Used when no compass is attached |

A magnetic heading is converted to **true north** with the World Magnetic Model
at the device's own position, so the dashboard and the field tools never
disagree about which way you are facing.

### `[rns]`

| Key | Default | Meaning |
|---|---|---|
| `config_path` | `""` | The Reticulum configuration directory this appliance uses. Empty means the Reticulum default, `~/.reticulum` |

### `[mesh]`

| Key | Default | Meaning |
|---|---|---|
| `rooms` | `["general", "emergency"]` | The RRC rooms this appliance joins when a hub is reachable, and serves from its own local broker when one is not |

---

## Hardware wiring on a desktop

Both peripherals are ordinary **USB-UART serial adapters**. No driver, no
library, no Cgo: `grl` reads NMEA-0183 ASCII sentences directly.

| Platform | Typical device names |
|---|---|
| macOS | `/dev/tty.usbserial-*`, `/dev/tty.usbmodem-*`, `/dev/cu.usbserial-*` |
| Linux Mint / Ubuntu | `/dev/ttyUSB*`, `/dev/ttyACM*` |

Find yours by listing the device nodes before and after plugging the adapter in:

```console
# macOS
$ ls /dev/tty.* /dev/cu.*
# Linux
$ ls /dev/ttyUSB* /dev/ttyACM*
```

Then name them in the configuration:

```toml
[gnss]
port = "/dev/ttyUSB0"
baud = 9600
static_fix = "37.7553,-122.4527"   # ignored while port is set

[compass]
port = "/dev/ttyUSB1"
baud = 9600
static_heading = "042"             # ignored while port is set
```

Notes for the bench:

- **`static_fix` and `static_heading` remain in the file** while a port is set.
  The port wins, so unplugging the receiver degrades to the last known position
  instead of to nothing.
- On macOS prefer the **callout** device (`/dev/cu.usbserial-*`) if the callin
  device (`/dev/tty.usbserial-*`) reports `EBUSY` with nothing holding it — the
  callin device waits on the modem-control lock.
- Permissions: on Linux, add yourself to the `dialout` group (then log back in)
  rather than running `grl` as root.
- Both peripherals are optional and independent. A receiver with no compass
  gives the position card; a compass with no receiver gives the heading, since a
  compass works standing still and a receiver that has not locked does not.
- The reader expects the conventional **9600 8N1** NMEA stream: `$GNRMC`/
  `$GNGGA` for position and `$HCHDG`/`$HCHDM` for heading.

---

## The appliance in the field

1. The appliance sits in a pocket or carabiner-clipped to a pack, listening.
2. The traveller opens Wi-Fi settings and taps `Reticulum-Lifesaver-[ID]`.
3. The phone's captive-portal probe is answered with a redirect, so the
   dashboard **pops up by itself** — no app, no store, no account.
4. The traveller reads the Plus Code aloud to a rescue team, steers an antenna
   at the nearest repeater, or asks the assistant `med snakebite`.
5. The phone goes back in the pocket; the appliance keeps listening, caching
   what arrives.

To rehearse step 2–3 before you have hardware, bind `0.0.0.0:9111` on a laptop
and open `http://<laptop-ip>:9111/` from the phone.

---

## Architecture: the shared `bot` package

`grl` contains almost no survival logic of its own. Everything it answers with
lives in [`github.com/gmlewis/go-reticulum/bot`](https://pkg.go.dev/github.com/gmlewis/go-reticulum/bot), which is also
what `gorrcbot` runs — so the radio reply and the dashboard answer can never
drift apart:

```
go-reticulum/
├── bot/                     # the shared engine: sensors, geodetics, field tools, portal
│   ├── bot.go              #   package documentation and the RRC engine
│   ├── engine.go           #   Engine.Eval — the zero-hop in-process evaluator
│   ├── run.go              #   Options/Run — the whole RRC bot appliance
│   ├── commands.go         #   the command registry and its policy
│   ├── gps.go, compass.go  #   NMEA-0183 readers (or static providers)
│   ├── declination.go      #   WMM2025 magnetic declination
│   ├── geo.go, olc.go      #   geodetics, Maidenhead grids, Plus Codes
│   ├── whereami.go         #   the operational card and its JSON form
│   ├── portal.go           #   the captive HTTP daemon and JSON API
│   └── portal-page.go      #   the mobile dashboard
├── cmd/
│   ├── gorrcbot/            # a thin CLI wrapper: flags + bot.Run
│   └── grl/                 # the appliance: flags, config, and App
```

`grl`'s own code is only the parts that are specific to being an appliance:

| File | What it does |
|---|---|
| `main.go` | Flags, configuration loading and overrides, signal handling, exit codes |
| `flags.go` | The CLI contract: `usageText` and a package-local parser |
| `config.go` | `~/.grl/config.toml`: parsing, defaults, validation, `~` expansion |
| `app.go` | The lifecycle: Reticulum → sensors → engine → dashboard, and back down |

Because `bot.Engine` opens no socket and starts no goroutine, an appliance whose
dashboard is switched off still has a complete offline field assistant
available to any caller, and an appliance whose radio cannot start is reported
rather than silently downgraded.

---

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `could not start Reticulum` | `rns.config_path` names a directory Reticulum cannot use. An empty value means `~/.reticulum` |
| Dashboard unreachable from a phone | `portal_addr` is still `127.0.0.1:9111` (this workstation only). Set `0.0.0.0:9111` |
| `listen ... address already in use` | Another process holds the port (often a previous `grl`). Change `portal_addr`, or find the holder with `lsof -i :9111` |
| `whereami` says no fix | No receiver is attached and no `static_fix` is set. Set one, or check the port and permissions |
| Heading is magnetic-only | A compass is attached but there is no position, so the WMM correction has nothing to work from. Set a fix |
| `portal.portal_addr: want host:port` | A typo such as `localhost` or `127.0.0.1` with no port. Use `host:port` |

---

## Security notes

- The configuration file is created **mode `0600`** and is never rewritten once
  it exists, so an operator's edits are the only source of truth.
- The default `portal_addr` binds the **loopback interface only**. Nothing about
  your position or heading is exposed to the local network until you deliberately
  choose `0.0.0.0` or a specific interface.
- The dashboard is a **read-mostly local surface**: it runs only the commands
  that compute their answer in-process. A command that needs a live hub link
  says so rather than failing.
- The appliance holds its own cryptographic identity via Reticulum; there is no
  account, no telemetry, and no vendor service in the path.

## See also

- [gorrcbot](gorrcbot.md) — the same engine as an always-on RRC chat bot
- [gobot](gobot.md) — one-shot CLI for asking a live bot a single question
- [Hardware guides](../guides/hardware.md) — LoRa RNodes and serial adapters
- [ASIC Plans §6.9–§6.12](https://github.com/gmlewis/go-reticulum/blob/master/ASIC-Plans.md)
  — the full GRL design, BOM, and pinout
