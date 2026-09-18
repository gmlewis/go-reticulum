# Go Reticulum Network Stack (RNS) <a href="https://github.com/gmlewis/go-reticulum/actions/workflows/build.yml"><img align="right" src="https://github.com/gmlewis/go-reticulum/actions/workflows/build.yml/badge.svg"/></a>

![gonomadnet mascot
The Go gopher was designed by Renee French.
The design is licensed under the Creative Commons 4.0 Attribution license.](assets/gonomadnet-mascot.png)

This is an experimental port of the [Reticulum Network Stack](https://github.com/markqvist/Reticulum) from Python to Go.
It is based upon the following Python original works:

* https://github.com/markqvist/lxmf
  ```
  commit 795fdaa2b0777c13033787d933d1afc94a2377cb (master, tag: 1.1.0)
  Author: Mark Qvist <bc7291552be7a58f361522990465165c>
  Date:   Mon Jul 20 18:49:54 2026 +0200
  ```
* https://github.com/markqvist/Reticulum
  ```
  commit b48b96e61676504e0a4e527b33b9a0b4495c6872 (master, tag: 1.4.2)
  Author: Mark Qvist <bc7291552be7a58f361522990465165c>
  Date:   Sun Jul 26 17:59:12 2026 +0200
  ```
* https://github.com/acehoss/rnsh
  ```
  commit 50f042008e9dafa7ae906ef9870b4b17b8a2aa45
  Author: acehoss <5148966+acehoss@users.noreply.github.com>
  Date:   Mon Jan 12 09:17:07 2026 -0500
  ```

> [!WARNING]
> ### ⚠️ Emergency, Medical, and Safety Disclaimer
> **NOT A CERTIFIED LIFE-SAFETY OR MEDICAL DEVICE.**
> Communications over unlicensed LoRa/ISM frequencies are best-effort and **never guaranteed**. This software and associated hardware (including the Go Reticulum Lifesaver) are **NOT** connected to official 911/112 emergency dispatch, government rescue agencies, or COSPAS-SARSAT search-and-rescue satellites, and are **NOT** a substitute for certified EPIRBs, PLBs, or commercial satellite messengers. First-aid protocols (`med`), navigation fixes (`/whereami`), and direction-finding vectors (`tower near`) are informational references only. Users assume all risks of wilderness travel and off-grid communications. Read [**DISCLAIMER.md**](DISCLAIMER.md) for the full legal terms and release of liability.

> [!NOTE]
> ### 📖 Official Documentation Site
> For complete guides, searchable command references, API provider templates, and hardware provisioning tutorials, visit the **[Go Reticulum Documentation Site](https://gmlewis.github.io/go-reticulum/)**.
> - [**Protocol & Markup Extensions Guide**](https://gmlewis.github.io/go-reticulum/reference/protocol-extensions/): Full specifications and Python back-port guides for `CAP_PRIVATE_COMMAND = 3`, `` `T `` (Timestamp Localization), and `` `L `` (Plus Code Offline Geo-Rendering).

> [!TIP]
> ### Standalone Off-Grid Hardware Projects
> Looking to build, buy parts for, or flash standalone handheld Reticulum hardware devices?
> Jump straight to the [**Reticulum Hardware Projects Guide**](https://github.com/gmlewis/asic-reticulum/tree/master/Hardware-Projects-Guide.md).
>
> It contains complete bills of materials (BOM), PCB manufacturing instructions, direct links to pre-compiled GitHub Release binaries, and zero-install in-browser web flashing for:
> - **Project 1: The Pocket Linux Terminal** (Raspberry Pi Zero 2W, 2.8" SPI LCD, CardKB keyboard, LoRa)
> - **Project 2: The Standalone Pocket Communicator** (ESP32-C5 RISC-V SoC, display, keyboard, LoRa)
> - **Project 3: The Autonomous Pocket Hub & Repeater** (ESP32-C5, LoRa, Wi-Fi 6 SoftAP mesh relay daemon)

## Go Port Security & Dependency Policy
- **Vendored Compression Snapshot**: A local in-repo snapshot (with source commit
  `39efe44ab707ffd2c1ef32cc7dbebfe584718686`) of `github.com/dsnet/compress/bzip2`
  is included under `compress/bzip2` for parity work so that the rest of `lxmf` and `rns`
  can rely solely upon the Go standard library.
- **No External Interface Plugin Runtime**: External interface plugins (like Python's
  `<configdir>/interfaces/<Type>.py`) are intentionally not supported in the Go port.
- **Supply-Chain Risk Posture**: This design is deliberate to minimize dependency-chain
  attack surface, informed by incidents such as the Jia Tan/XZ backdoor.

## Go Port Support Matrix

The checked-in Go tree is currently verified on **Linux** and **macOS
(Darwin)**; standalone release executables are additionally published for
**Windows** and **FreeBSD** (the GitHub Releases page shows exactly which
executables are provided per platform). When the upstream Python README later
in this file makes broader claims, this support matrix is the one that applies
to the Go port.

| Surface | Linux | macOS (Darwin) | FreeBSD | Windows |
| --- | --- | --- | --- | --- |
| Core `rns`, `lxmf`, and CLI unit/integration suites from this repo | **Supported** and verified | **Supported** and verified | Not in the certified test matrix | Not in the certified test matrix |
| Live serial `gornodeconf` workflows | **Supported** | **Supported** | **Supported** | **Unsupported** |
| `gornodeconf` firmware `--extract`, `--flash`, and `--update` recovery flows | **Supported** | **Supported** | **Supported** | **Unsupported** |
| Live RNode destructive tests | Opt-in only | Opt-in only | Unsupported | Unsupported |
| External Python interface plugins | Unsupported | Unsupported | Unsupported | Unsupported |

Additional notes:

- The Go codebase includes checked-in support for I2P, Weave, KISS, RNode, TCP,
  UDP, pipe, and serial-facing interface paths, but the actively verified Go
  platform matrix today is Linux and macOS.
- `gornodeconf` live serial and firmware workflows are supported on Linux,
  macOS, and FreeBSD. Windows intentionally returns explicit `not supported on
  platform %v` errors for those flows, so no `gornodeconf` Windows executable is
  published.
- The `cmd/gornodeconf` README below the command directory is the detailed
  support contract for live RNode and recovery workflows.

## Manual RNode Workflow on Linux, macOS, or FreeBSD

The Go utilities in this repository are designed to talk to a live RNode over a
serial port on Linux, macOS, or FreeBSD. The commands below exercise the common manual
workflow in the same order you would normally use on a fresh or
already-provisioned device. You may need to install `python3-serial` or
`pyserial` for full functionality.

Before starting, make sure:

- You have access to the serial device, for example `/dev/ttyUSB0` or `/dev/ttyACM0`.
- Your user can open the serial device without permission errors.
- You are willing to let the tools write under `~/.config/rnodeconf/`.
- You understand that `--rom`, `--flash`, `--update`, and `--eeprom-wipe` can
  change live device state.

If you want the run to stay isolated from your everyday Reticulum config, point
`HOME` at a temporary directory before invoking the commands. The tools will
then store firmware artifacts, EEPROM backups, and generated keys under that
temporary home instead of your normal profile.

### 1. Inspect the device

Start by checking the device and recording its current state:

```bash
gornodeconf -i /dev/ttyUSB0
gornodeconf --public /dev/ttyUSB0
gornodeconf --get-firmware-hash /dev/ttyUSB0
gornodeconf --get-target-firmware-hash /dev/ttyUSB0
```

The info command shows the current EEPROM contents and mode. The public-key and
hash commands are useful when you want to compare the live device against a
known-good build or a saved firmware image.

### 2. Generate local signing material

Before bootstrapping or signing a device, generate the local firmware keys once:

```bash
gornodeconf --key
gornodeconf --public
```

This creates `signing.key` and `device.key` under `~/.config/rnodeconf/firmware/`.
The `--public` command prints the EEPROM signing public key and the device
signing public key so you can verify what was created.

### 3. Back up the current EEPROM

If the device already has useful settings, take a backup before changing it:

```bash
gornodeconf --eeprom-backup /dev/ttyUSB0
gornodeconf --eeprom-dump /dev/ttyUSB0
```

`--eeprom-backup` writes a timestamped binary backup under
`~/.config/rnodeconf/eeprom/`. `--eeprom-dump` prints the full EEPROM contents
to the terminal so you can inspect the live state before modifying anything.

### 4. Bootstrap a fresh device

For a new device, or after a full wipe, bootstrap the EEPROM with the live
serial port:

```bash
gornodeconf --rom --product 03 --model a4 --hwrev 5 /dev/ttyUSB0
```

The bootstrap flow uses the local `signing.key`, writes the EEPROM identity,
stores a device_db backup, and records the next serial number under
`~/.config/rnodeconf/firmware/serial.counter`.

Use `--autoinstall` with the same bootstrap path if you want the tool to wipe
old EEPROM contents before provisioning the new identity.

### 5. Flash or update firmware

Once the device identity is in place, you can flash or update firmware:

```bash
gornodeconf --extract /dev/ttyUSB0
gornodeconf --flash /dev/ttyUSB0
gornodeconf --update /dev/ttyUSB0
gornodeconf --update --use-extracted /dev/ttyUSB0
```

`--extract` saves firmware artifacts under `~/.config/rnodeconf/update/`.
`--flash` programs the current firmware choice and then bootstraps EEPROM.
`--update` performs the same workflow but follows the update path and version
selection logic. `--use-extracted` tells the updater to reuse firmware that was
previously extracted from a compatible device.

### 6. Maintain or recover the device

These commands are useful once the device is provisioned:

```bash
gornodeconf --firmware-hash <hex-sha256> /dev/ttyUSB0
gornodeconf --sign /dev/ttyUSB0
gornodeconf --eeprom-wipe /dev/ttyUSB0
gornodeconf --clear-cache
```

`--firmware-hash` records the installed firmware hash on the device.
`--sign` writes a device signature when a valid local device key is available.
`--eeprom-wipe` clears the EEPROM contents and resets the device on supported
platforms. `--clear-cache` removes locally cached firmware downloads.

### Practical order of operations

A sensible manual session is usually:

1. Inspect the device.
2. Back up the EEPROM.
3. Generate signing keys if they do not already exist.
4. Bootstrap or wipe/flash the device as needed.
5. Re-run `--eeprom-dump`, `--public`, and the firmware hash commands to
  confirm the device now matches the expected state.

That sequence keeps the live device state observable at each step and makes it
easy to recover if you need to back out of a change.

### Fleet radio diagnostics

`gornode-diagnostics` answers a different question than `gornodeconf`: not
"what is this device?" but "how well does this radio actually transmit and
receive over the air?" It sniffs every serial device on the machine for an
RNode (completely ignoring `~/.reticulum/config`), refuses to touch a serial
line held by another process (serial lines cannot be shared), and then runs a
coordinated over-the-air test: every participating node transmits
uniquely-identified test packets and acknowledges the packets it hears from
the other nodes, so a radio whose transmitter is dead shows up as "sent many,
heard by nobody" in the fleet comparison.

Start it on every node of the fleet inside the grace period (60s by default,
so all nodes can be launched before the test begins):

```bash
gornode-diagnostics                     # 60s grace, 300s test, ACK every 5th packet
gornode-diagnostics -grace 120 -duration 600
gornode-diagnostics -ack-every 1        # ACK every packet (heavier channel load — use on tiny fleets)
gornode-diagnostics -sniff-only         # just identify RNodes, run no test
```

Sniffing ignores duplicate device paths that name the same physical radio
(Linux exposes one radio as both `/dev/serial/by-id/…` and `/dev/ttyACM0`;
the tool reports it once) and never mistakes its own open port for one held
by another process. The serial input queue is flushed on open and fleet
packets are ignored until the test window opens, so stale packets buffered
by a previous run (or another program) cannot be counted or acknowledged as
fleet traffic. During the idle grace window the tool re-queries any
firmware-version/platform details the radio has not reported yet, so the
final report shows the full radio identity.

The final report per radio shows the detected hardware and firmware, the
configured LoRa parameters as validated against the radio's own report,
firmware RX/TX packet counters (with the delta observed during the test),
per-peer packet/acknowledgement counts and round-trip times, hardware error
reports (e.g. `TXFAILED`, `MODEM_TIMEOUT`), and a verdict of whether the radio
transmits and receives. Like `gornodeconf`, it requires exclusive access to
the serial device — stop `gornsd`/`gonomadnet` (or anything else holding the
port) first.

### The gonomadnet Public Hub

The Go port also runs a public node as a live test target for the stack:

- **Hub**: `go-nomadnet.duckdns.org` — a public RNS TCP gateway
- **Endpoint**: `go-nomadnet.duckdns.org:4242`

Add it to your Reticulum client config (`~/.reticulum/config`):

```
[[gonomadnet Public Hub]]
  type = TCPClientInterface
  interface_enabled = yes
  target_host = go-nomadnet.duckdns.org
  target_port = 4242
```

**Live Public Demo Destinations:**

```text
gonomadnet Public RRC Hub  (gonomadnet node + gornsd + gorngit + gorrcd + golxmd)
  gonomadnet node dest  <c7d0e7bbd883e595f53e14fa6986188c>  nomadnetwork://c7d0e7bbd883e595f53e14fa6986188c
  gorngit repos dest    <58a0406047ec2e7ce23e9e9a83b744df>  rns://58a0406047ec2e7ce23e9e9a83b744df/<repo>
  gorngit page dest     <cb3677a1bb8e37f334096566ed8ff895>  nomadnetwork://cb3677a1bb8e37f334096566ed8ff895
  gorrcd hub dest       <a012129c10205c0b9441fcd2b755b2a7>  rrc://a012129c10205c0b9441fcd2b755b2a7/#general
  golxmd prop dest      <7acc095f0e83182feb58c888d090a3cc>  lxmf.propagation
```

- **NomadNet Page**: Open `c7d0e7bbd883e595f53e14fa6986188c` (`Ctrl-U` in `gonomadnet`) to browse Micron pages and live [Wasm Executable Pages](#wasm-executable-pages).
- **RRC Chat & `@gobot`**: Join `rrc://a012129c10205c0b9441fcd2b755b2a7/#general` to chat and interact with [`@gobot`](#gorrcbot--the-rrc-bot-client). From a shell, the [`gobot`](#gobot--the-one-shot-cli-for-gobot) CLI reaches the same official bot with one command and no setup.
- **Git over Reticulum**: Clone repositories directly over the mesh using `gorngit` / `git`: `git clone rns://58a0406047ec2e7ce23e9e9a83b744df/go-reticulum`.
- **LXMF Propagation Node**: Use `7acc095f0e83182feb58c888d090a3cc` as your LXMF propagation node for offline store-and-forward message delivery.
- **Go Reticulum Lifesaver (GRL)**: Run [`grl`](#grl--the-go-reticulum-lifesaver-appliance) — the whole off-grid appliance in one executable — on a field node or a desktop, and any phone that joins its network gets the survival dashboard: `/whereami` Plus Codes, one-word `tower near` / `sun` queries, and a `/sos` that raises a beacon at the verified position, all with zero radio hops.

### gorrcbot — the RRC bot client

`gorrcbot` is a headless, always-on RRC (Reticulum Relay Chat) bot. It is a
**client, not a hub**: it needs no hub-side support and works against any RRC
hub, including `gorrcd` and the Python `rrcd`. It dials every hub in its
configuration file at once, joins that hub's rooms, keeps itself connected
across link flaps and restarts, and answers **only** when it is addressed by
name. Everything else it hears is ignored in silence.

> For comprehensive documentation, see the [**gorrcbot Documentation Guide**](https://gmlewis.github.io/go-reticulum/tools/gorrcbot/).

**First run** creates its configuration and its identity, then exits so the
hubs can be edited before anything connects:

```bash
gorrcbot                                   # writes ~/.gorrcbot/{config.toml,bot_identity}, exits 0
$EDITOR ~/.gorrcbot/config.toml            # set your hubs and rooms
gorrcbot                                   # connects and stays up
gorrcbot --check-config                    # dry run: print what it would do, connect to nothing
```

`GORRCBOT_HOME` overrides the state directory (default `~/.gorrcbot`), and
`--bot-config`, `--identity`, `--home`, and `--nick` override individual paths
and the advertised nick. `--log-level` (a name such as `NOTICE`, `WARNING`, or
`DEBUG`, or the matching RNS number) and `--log-file` control logging; an
explicit level wins over the one in the Reticulum configuration, so
`--log-level WARNING` really is quiet. `--version` prints the version.

The bot owns exactly one 64-byte Reticulum identity (`bot_identity`, created
with mode `0600`) and uses it on every hub, so its **identity hash is the same
everywhere** — a peer can address it by hash prefix without knowing which hub it
is on. Next to it, `storage/` is the RRC client's own directory for the
per-room message history it saves.

**Configuration** (`~/.gorrcbot/config.toml`):

```toml
# Top level: where the bot keeps its identity and its saved room history.
identity_path = "~/.gorrcbot/bot_identity"   # 64 bytes of private key, mode 0600, created on first run
storage_dir = "~/.gorrcbot/storage"          # the RRC client's directory; per-room history lives here

[bot]
nick = "gobot"             # advertised nick, and the default trigger nick
reply = "auto"             # auto | direct | room — see "Reply routing" below
cooldown_s = 8.0           # minimum seconds between replies to the same identity
announce_on_join = false   # false = silent like any member; true = one self-introduction NOTICE per room per session
max_reply_lines = 12       # a reply longer than this is truncated, visibly; it also sets the catalog page size
micron_links = false       # true renders discovery rows and the next-page footer as clickable Micron links for NomadNet clients
weather_url = "http://wttr.in/{place}?format=%l:+%C+%t+%w+%h"
tide_url = "https://api.tidesandcurrents.noaa.gov/api/prod/datagetter?product=predictions&datum=MLLW&time_zone=gmt&units=english&interval=hilo&format=json&station={place}&begin_date={date}&range=48"
buoy_url = "https://www.ndbc.noaa.gov/data/realtime2/{place}.txt"
river_url = "https://waterservices.usgs.gov/nwis/iv/?sites={place}&format=json&parameterCd=00065,00060&period=P1D"
river_flood_url = "https://api.water.noaa.gov/nwps/v1/gauges/{place}"
space_weather_url = "https://services.swpc.noaa.gov/products/noaa-planetary-k-index.json"
metar_url = "https://aviationweather.gov/api/data/metar?ids={place}&format=raw"
weather_alert_url = "https://api.weather.gov/alerts/active?area={place}"
launch_url = "https://ll.thespacedevs.com/2.3.0/launches/{mode}/?limit={limit}"
flight_url = "https://api.adsb.lol/v2/callsign/{flight}"
flight_route_url = "https://api.adsbdb.com/v0/callsign/{flight}"
lxmf_enabled = false       # true adds the LXMF sender (msg/lxmf), so a peer can be reached while offline
lxmf_propagation_node = "" # optional 32-hex LXMF propagation node, for store-and-forward
lxmf_announce_minutes = 360 # how often the bot announces its own lxmf.delivery address, so a reply can be routed back (at least 1)
kjv_txt_file = ""          # optional King James text file (one verse per line); enables the kjv command. Empty disables it
towers_path = "~/.gorrcbot/towers.csv" # optional local cell/repeater dataset merged over the embedded catalog; absent is normal
emergency_lxmf_destination = "" # optional 32-hex lxmf.delivery hash: every new sos beacon is also queued there
portal_addr = ""            # Go Reticulum Lifesaver: captive survival dashboard for any phone that joins this node's Wi-Fi; empty binds nothing
gps_port = ""               # GNSS receiver streaming NMEA-0183 (for example /dev/ttyACM0); enables the live fix
gps_fix = ""                # static position for a headless node (for example "37.7553,-122.4527"); used when gps_port is empty
compass_port = ""           # electronic compass streaming NMEA-0183 headings ($HCHDG/$HCHDM/$HCHDT); gives a heading while standing still
compass_heading = ""        # static magnetic heading for a node with no sensor ("042" or "NE"); converted to true north from the node's position

# One [[hubs]] entry per hub. Every entry is dialed on startup.
[[hubs]]
name = "gonomadnet Public Hub"
destination = "a012129c10205c0b9441fcd2b755b2a7"   # the hub's rrc.hub hash, 32 hex
rooms = ["general"]                                 # or { name = "...", key = "..." } for a +k room
nick = ""                                           # optional per-hub nick override
respond_to = { general = "gobot" }                  # optional per-room trigger nick
```

Unknown keys warn and never fail, so a configuration written for a newer bot
still starts. `--check-config` shows the parsed hubs, rooms, trigger, and
identity hash without connecting.

**Go Reticulum Lifesaver (GRL).** On a field node, `gorrcbot` is also the
[Go Reticulum Lifesaver](https://gmlewis.github.io/go-reticulum/tools/gorrcbot/#the-go-reticulum-lifesaver-grl),
a pocket-sized off-grid survival communicator. It reads an **NMEA-0183 GNSS
receiver** with no Cgo and no third-party library (`gps_port`, or a static
`gps_fix`), and it answers the survival questions **locally, in-process, with
zero radio hops**:

- **`/whereami`** prints the operational location card: the Plus Code, both
  coordinates, the Maidenhead grid, the altitude, the heading, the fix status,
  the local solar time, and the sunset countdown — plus the GCJ-02 "Mars
  coordinate" when the position is inside China, so it can be pasted straight
  into Amap, Gaode, or WeChat.
- **Zero-argument context injection:** with a live fix, `tower near`,
  `tide near`, and `sun` use the operator's own position, and `/sos` raises a RED
  beacon there, attaching the satellites, HDOP, altitude, fix quality, and
  receiver timestamp to the alert. A typed location always wins.
- **An electronic compass, so the device knows which way it points while
  standing still:** `compass_port` reads `$HCHDG`/`$HCHDM`/`$HCHDT` heading
  sentences from any magnetometer, or `compass_heading` supplies a static
  bearing. The heading is converted from magnetic to **true north** with the
  **World Magnetic Model (WMM2025)**, evaluated in pure Go from its published
  coefficients — so `/whereami` prints a real heading, and `tower near` adds the
  relative turn that aims a directional antenna: `[Turn 15° RIGHT · 1 o'clock]`.
- **A captive portal with no app to install:** set `portal_addr` and any
  smartphone that joins the node's Wi-Fi has the survival dashboard opened for it
  by the operating system itself, answering the Apple, Android, and Windows
  captive-network probes. The page is one self-contained document served from the
  binary — the big Plus Code with a copy button, a **live compass rose** with a
  needle at the heading and another at the nearest repeater, the SOS button, and
  the offline field assistant (`med hypothermia`, `tower near`, `sun`) — with
  `/api/whereami`, `/api/compass`, and `/api/query` JSON endpoints behind it.

**Addressing contract.** The bot is silent unless one of these is true, and it
then answers with a NOTICE:

| Form | Example |
|------|---------|
| `@<nick> <command>` at the start of a room message, case-insensitive, tolerating a trailing `:` or `,` | `@gobot help` |
| `@<identity-hash-prefix>` with at least 6 hex characters | `@0032a96e help` |
| A **direct NOTICE** addressed to the bot (RRC `K_DST`), which may omit the address entirely | `/msg gobot help` or `help` |

**Private Messaging (`/msg gobot <command>`).**
While `@gobot` can be addressed publicly in any joined room, **private direct messaging is strongly recommended** for most commands (e.g. `/msg gobot help`, `/msg gobot wx Denver`, `/msg gobot loc <coords>`, `/msg gobot sun <coords>`, `/msg gobot checkin ...`). Private messaging saves bandwidth on low-speed LoRa/radio links, keeps channels clear for peer conversation, protects the privacy of operational coordinates, and guarantees that replies travel as direct notices (`K_DST`) back to your identity alone without entering public rooms.

A mention in the middle of a sentence, a longer or shorter nick, another bot's
`!command` prefix, and the bot's own messages are all ignored. Room notices from
the hub (the MOTD), system rows, and error rows never trigger anything.

**Commands** mirror the official RNS Community hub bot minus its `!` prefix,
plus a few that only matter on a mesh:

| Command | What it does |
|---------|--------------|
| `help` | list every command, or explain one (`help dnotice`) |
| `ping` | answer `pong` — a liveness check |
| `uptime` | runtime, hub hash, and how long this connection has been up |
| `whoami` | your nick and full identity hash as this hub sees them |
| `botinfo` | the bot, this hub, and the bot's own identity hash |
| `dn`, `dnotice <nick\|hash\|me> <text>` | send one client a direct NOTICE |
| `dnoticecap [target]` | whether the hub supports direct notices, and whether a target is reachable |
| `dnoticeme <text>` | send yourself a direct NOTICE — a live test of the private path |
| `weather`, `wx <place>` | look up the weather (needs `weather_url`; the place is any real name — see below — and the answer is stripped of terminal escapes) |
| `launches [upcoming\|past] [1-5]` | the next few launches, or the most recent ones (needs `launch_url`; answers are cached, because the provider allows 15 anonymous calls per hour) |
| `flight <number>` | where one flight is right now, by the number a passenger knows (`BA123`): the route it is flying, then its altitude, climb or descent, speed, track, position, squawk and how old that position is (needs `flight_url`; see below) |
| `path <nick\|hash>` | how the transport would reach a peer: the destinations its identity publishes, with hops, next hop, interface, and path age |
| `watch <name\|hash> [ttl]` | ask for a direct NOTICE when something announces — a peer, a node, or a hub. At most 10 per client, 100 per bot, 24 h by default and never more than 7 d |
| `unwatch <n\|all>` | stop watching for one of them, or all of them |
| `watches` | what you are watching for, and how long each has left |
| `msg`, `lxmf <nick\|hash> <text>` | send an LXMF message to a peer, so it is handed over when they come back (needs `lxmf_enabled = true`; store-and-forward additionally needs a propagation node) |
| `catchup [window]` | what was said in your joined rooms while you were away |
| `search <term> [#room]` | find where a term appeared in the rooms the bot has joined, newest first |
| `kjv <reference\|words\|regex>` | look up a Bible verse or search the King James text (needs `kjv_txt_file`; see below) |
| `seen <nick\|hash>` | when a client last spoke in a joined room |
| `members [room]` | the clients the hub reports in a room |
| `rooms` | the rooms the bot has joined |
| `id` | the identity hash and nicks a client can address the bot by |

Each of these can be explained on demand: `@gobot help kjv` prints that one
command's purpose, its usage, its inputs, and a worked example — as does
`help <command>` for every other command.

**Field assistant commands.** Reticulum is used where the telephone network has
failed or never existed, so the bot also carries the tools a party in the field
actually needs. Every one of them works with no internet at all: the geodesy,
the almanac, the medical cards, the conversions, the signal guide, and the Morse
translator are computed or embedded in the binary, and the mesh directory reads
the announce cache the bot already keeps.

| Command | What it does |
|---------|--------------|
| `loc <pluscode\|coords\|grid>` | resolve any of the five location notations and render it in all of them: `DD: 37.4220°N, 122.0841°W \| DDM: … \| Grid: CM87wk \| OLC: 849VCWC8+R9`, plus the GCJ-02 "Mars coordinate" when the position is inside China |
| `dist <from> <to>` | great-circle distance and both headings between two locations, in km, miles, and nautical miles |
| `proj <origin> <bearing> <distance>` | dead reckoning: where a course and distance from a known point ends up, as a Plus Code, a coordinate, and a grid locator |
| `sun <loc> [date]` | sunrise, sunset, civil twilight, day length, and the moon's phase and illumination, all in UTC |
| `sos <loc> <RED\|YELLOW\|GREEN\|INFO> <details>` | raise a distress beacon: recorded on disk, alerted in every joined room, confirmed to the sender by direct NOTICE, and queued to the configured LXMF dispatch destination when there is one. `sos list` and `sos clear <id>` (the sender only) complete it |
| `checkin <loc> overdue <duration> <note>` | arm a dead-man switch: if the check-in never comes, the bot broadcasts an overdue alarm on its own. `checkin ok` clears it, `checkin list` shows them; windows are capped between 10 minutes and 48 hours |
| `sitrep add <loc> <category> <text>` | file a geolocated situation report (HAZARD, RESOURCE, SHELTER, ROAD, INFO) on a board that expires after 7 days. `sitrep near <loc> [radius]` answers what is within reach, closest first, and `sitrep recent [n]` lists the newest |
| `firstaid <topic>` (aliases `rx`, `triage`) | one-line offline wilderness-medicine action cards: bleed, cpr, triage, shock, hypo, heat, burns, water, snake. Decision support, not a substitute for training |
| `spacewx` (alias `solar`) | solar flux, sunspot number, K-index, geomagnetic storm scale, and which HF bands are worth trying — the diagnosis for an HF link that stopped working. `spacewx set sfi=N ssn=N kp=N` enters a reading by hand |
| `net [hops]` | the mesh directory: every announced hub, LXMF node, and NomadNet node the bot has heard, with its hop count and interface |
| `conv <value><unit> <target>` | tactical conversions: pressure and altimeter settings, distance, speed, temperature, water and fuel weight, and battery capacity (`5000mAh@3.7V` → `18.50 Wh`) |
| `signal [air\|sound\|light]` | the distress-signal guide: ground-to-air markings, whistle and horn cadences, and mirror or torch flashes |
| `morse <text>` / `morse -d <code…>` | translate text to Morse code and back |
| `metar <ICAO>` | decode the aviation weather report for an airfield into wind, visibility, temperature, dewpoint, and altimeter setting (needs `metar_url`). `metar search <city\|name\|code>`, `metar near <place>`, and `metar list [state\|country]` find the code offline first |
| `wxalert <place\|area>` | severe weather warnings in force (needs `weather_alert_url`; answers are cached for 15 minutes) |
| `moon [loc] [date]` | the lunar almanac: phase and age, moonrise, transit, moonset, the coming night's illumination rating (Dark Night, Moderate Light, Bright Moonlight), and the next new, first-quarter, full and last-quarter moons — the new and full ones named as the spring tides they drive |
| `tide <station\|coords\|place> [date]` | high and low water for a station (a 7-digit provider id like `9414290`, a port name, or a position resolved against the bot's own reference table of ~110 stations), the state of the tide now, and the spring/neap assessment (needs `tide_url`, which carries `{place}` and `{date}`). `tide search <query>`, `tide near <place>`, and `tide list [state]` find the station offline first |
| `buoy <station_id>` | the sea state from an offshore weather buoy: wave height, dominant period and direction, wind, water temperature, and the pressure trend. The period, not the height, decides whether the sea is groundswell or chop, and the answer names it (needs `buoy_url`). `buoy search <query>`, `buoy near <place>`, and `buoy list [region\|state]` find the buoy offline first |
| `river <gauge_id>` | a stream gauge's stage and discharge, how the stage has moved over three hours, and the flood category (needs `river_url`; `river_flood_url` adds the flood thresholds and the action stage). The gauge is a USGS site number: a river *name* is deliberately not accepted, because resolving one offline would risk answering for a different river of the same name |
| `coldwater [temp_f\|temp_c]` (alias `immersion`) | the 1-10-1 cold-water rule, or the swim-failure and survival windows for a water temperature (`48F`, `8.9C`; a bare number is Fahrenheit). Entirely offline |
| `tower near <place\|coords\|pluscode>` (aliases `repeater`, `cell`, `mast`) | the nearest communications sites — cellular masts, amateur VHF/UHF repeaters, emergency/public-safety relays, and marine VHF — with the **distance in kilometers and the bearing** to aim a directional antenna or choose a direction to walk, plus the frequency, the repeater offset, the CTCSS/PL tone, and the operator. With no argument and a live compass heading it adds the **relative steering instruction** that aims an antenna with no arithmetic: `[Turn 15° RIGHT · 1 o'clock]`. Entirely offline: the catalog is embedded and no provider is needed |
| `tower search <query> [page]` | find a site offline by callsign, identifier, name, city, pinyin place name, state or province, frequency, or operator (`sutro`, `beijing`, `sichuan`, `145.150`, `china mobile`) |
| `tower list [country\|region] [page]` | every site, or one country's (`US`, `CN`, `GB`), one country's by name (`china`, `germany`), one US state's (`CA`, `CO`) or its name, or one Chinese province's (`BJ`, `GD`, `SC`, `XJ`) |
| `tower info <id>` | one site in full: the exact WGS-84 position, the GCJ-02 "Mars coordinate" when the site is in China, the Maidenhead grid, the elevation, the frequency, the offset, the tone, and the operator |

**Station ids are discoverable offline.** `tide`, `buoy`, and `metar` all take
an opaque identifier — a NOAA station number, an NDBC buoy id, an ICAO code —
and each carries its provider's own station catalog **embedded in the binary**,
so the identifier can be found before `tide_url`, `buoy_url`, or `metar_url` is
configured, and while that provider is unreachable. `tide search san francisco`
matches a name, a state, or a partial id; `metar search denver` matches a city
name, an airport name, an ICAO code (`KDEN`), or the IATA code on a ticket
(`DEN`); `tide near 849VCWC8+R9` and `buoy near 37.8,-122.4` name the three
closest stations with the distance in nautical miles and the bearing; and
`metar list CO`, `buoy list HI`, and `tide list OR` filter by state, basin, or
country. A station id outside a catalog is still accepted, because the provider
is authoritative about its own stations.

**The cell and repeater finder needs no provider at all.** `tower` (aliases
`repeater`, `cell`, `mast`) answers "what transmits near here, which way, and how
far?" from a hand-maintained catalog of ~220 mountain-top and regional communications
sites — amateur repeaters, cellular masts, public-safety relays, and marine VHF
— **balanced between the United States and China**, with major international
hubs. `tower near 37.7553,-122.4527` names the three closest sites with the
distance in kilometers and the bearing (`W6PW-2M (0.0 km 0° N) [RPT]: 145.150 MHz
-0.6 (PL 114.8) - Sutro Tower, San Francisco, CA`); `tower search` and
`tower list` page through the catalog offline; and `tower info` prints both
datums, the Maidenhead grid, the elevation, and every radio detail. Chinese
province codes and pinyin place names are searchable, so `tower search beijing`,
`tower search sichuan`, and `tower list GD` all work. A site **inside China**
additionally prints its **GCJ-02** coordinate, ready to paste into Amap, Gaode,
Tencent Maps, or WeChat, and a GCJ-02 coordinate from any of those apps can be
searched from by prefixing it with `gcj:` (`tower near gcj:39.9069,116.4038`,
`loc gcj:39.9069,116.4038`) — the conversion is a pure-Go implementation of the
mandated offset, and nothing outside China is ever moved. Operators who need
micro-cell density drop an OpenCelliD-style extract at `towers_path` (by default
`towers.csv` beside `config.toml`): a row whose `id` matches an embedded one
replaces it, a new `id` is added, and an absent file simply means the embedded
catalog alone.

**Long answers are paginated.** Every catalog answer is cut to the operator's
`max_reply_lines` budget — two lines for the header and footer, at most four
rows — and every page ends with the exact command that asks for the next one
(`[Page 1 of 3: ask "tide list CA 2" or "more" for next]`). In a private session
`/msg gobot more` (or `next`) continues the walk without retyping the query: the
pending page is remembered per identity for five minutes, in memory only, and
one asker's page is never handed to another. With `micron_links = true` the rows
and the next-page footer are rendered as clickable Micron links for NomadNet
clients.

**Locations are resolved offline.** `loc`, `dist`, `proj`, `sun`, `sitrep`, and
the `sos` and `checkin` position arguments all accept the same five notations —
a Plus Code (`849VCWC8+R9`), decimal degrees (`37.42205, -122.08409` or
`N37.42205 W122.08409`), degrees/minutes/seconds (`37°25'19"N 122°05'03"W`), degrees
and decimal minutes (`37°25.323'N 122°05.048'W`), and a Maidenhead grid locator
(`CM87uk`). A place *name* cannot be resolved without a geocoder, so the
commands say what they accept instead of guessing. The Plus Codes are produced
by a full implementation of the specification, checked against the reference
implementation's own test data. A sixth input form is a **GCJ-02 "Mars
coordinate"** copied from a Chinese map app, marked with a `gcj:` (or `gcj02:`)
prefix — `loc gcj:39.9069,116.4038`, `tower near gcj:39.9069,116.4038` — which
is converted back to GPS automatically.

**Marine and coastal operations.** The tide, buoy, and river commands each read
one optional provider, and each degrades honestly when it cannot: an
unconfigured or unreachable provider produces one line saying so, never a
guessed number. Two of the three carry information the provider does not: the
tide command interpolates between the two predictions that bracket the present
moment with the rule of twelfths (1, 2, 3, 3, 2, 1 twelfths of the range), so a
single pair of predictions gives the depth at any moment in the six hours
between them, and it reads the spring/neap cycle from the Moon's elongation
rather than from a second feed. The buoy command converts the feed's metric
units to the knots and feet a mariner uses, and names the sea state from the
wave period, because a six-foot sea at five seconds and the same six feet at
sixteen seconds are entirely different problems. The river command compares the
stage against the river-forecast center's own categories, so "flood stage" is
the local definition rather than a generic one.

**`moon` is accurate, and `sun` inherits it.** The lunar almanac implements
Meeus's abridged lunar theory (the full 60-term longitude, distance and latitude
series) in Terrestrial Time, with a Delta T polynomial bridging the clock, and
it is checked against an independent implementation of the same algorithms:
positions to a fraction of an arcminute, and rise, set, transit and the four
principal phase instants to within a couple of minutes. Those phase instants
also anchor the phase name, the age and the illumination, so they can never
disagree with each other, and the `sun` command's moon line reports the same
values. The night rating is the one deliberate simplification: the Moon is
sampled at the middle of the dark window, and a Moon below the horizon there
means "Dark Night" whatever the phase says.

**A distress beacon is acted on, not just recorded.** `sos` writes the beacon to
`<storage_dir>/sos.json` (atomically, before anything else), alerts every room
the bot has joined, sends the sender a direct NOTICE, and — when
`emergency_lxmf_destination` is set and LXMF is enabled — queues the same report
to that destination, which is store-and-forward and keeps trying after the local
link has failed. Only the identity that raised a beacon can clear it. The
check-in watchdog is the one part of the bot that acts with no request behind
it: it wakes every 30 seconds, and a timer that expires without a check-in
produces one overdue alarm naming the last known position and the note the
traveller left.

**Bible lookup and search never leaves the bot.** With `kjv_txt_file` pointing
at a King James text file (one verse per line, a book abbreviation first:
`John3:16 For God so loved the world, ...`), the `kjv` command reads it once,
read-only, and answers three ways from one argument list:

- **A reference**, however loosely spelled — `jn3:16`, `John 3:16`,
  `Psalm 23:1-6`, `ps23`, `psalms23:3`, `1 jn 2 1`, `rom8:28` — is looked up.
  Book names are fuzzy: a prefix resolves to every book it could be, and the
  abbreviations the data itself uses (`Ge`, `Psa`, `1Jn`, `SSol`) all work. A
  bare abbreviation that is not a word in the text (`gen`, `isa`) resolves to
  that book's first verse; a bare word that does occur (`is`, `am`) is searched
  for instead, so a two-letter abbreviation never steals a common word.
- **Bare words** search for every verse that contains them, so
  `kjv shepherd` finds Psalms 23:1 and `kjv love of god` finds the verses that
  contain all three. The syntax also takes phrases (`"the love of God"`), OR
  (`love|charity`), exclusions (`love -hate`), and wildcards (`lov*`, `l?ve`,
  `l[ai]ve`). A term that finds nothing is retried as a prefix, so `shepher`
  still reaches `shepherd`.
- **A regular expression** — `love.*life`, `^In the beginning` — is matched
  across a whole verse, for the cases the word syntax cannot express.

Every answer is one complete verse per line with its full canonical name
(`John 3:16: For God so loved the world, ...`), so a search result reads as
scripture rather than as a fragment. The reply is bounded by `max_reply_lines`,
and a cut-off answer says how many matching verses it did not show. A room
mention is answered in the room and a `/msg gorrbot kjv ...` direct notice is
answered privately, exactly like every other command; `kjv_txt_file` may be
written in `[bot]`, above it, or (tolerantly, with a warning) after a `[[hubs]]`
block.

**Flight status uses two keyless providers, and never guesses.** `flight <number>`
takes the number a passenger knows (`BA123`, `ba 123`, and `BA-123` all work) and
asks two providers. `flight_route_url` is asked first: it resolves the number to
the radio callsign the live feed uses (`BA123` → `BAW123`) and names the airline
and the two airports. `flight_url` is then asked with that callsign for the live
state: altitude, climb or descent, ground speed, track, position, squawk and
aircraft type. Both have sensible keyless defaults, both are plain HTTP templates
with a `{flight}` placeholder, and either can be pointed at a keyed provider
instead — the bot never repeats a configured URL, which may carry a key.

The answer keeps the two apart on purpose. An empty aircraft list is reported as
"no aircraft is transmitting that callsign right now: it may be between flights,
or beyond receiver range", which is a real answer and not a failure; a number no
route provider publishes is reported as an unknown number; and if one provider is
unreachable the other's answer is still reported with a note saying what was
missing. Every position is reported with its age ("position heard 4s ago"), a
position older than a minute is called out as old, and cached answers keep ageing
honestly rather than being re-presented as live. Emergencies are raised in plain
words — the emergency field, the alert flag, and the 7500, 7600 and 7700 squawk
codes — because the point of the command is the person on board.

**Places and names are text, not bytes.** `weather` and the watch filters accept
real names in any script — `Zürich`, `京都`, `São Paulo` — along with spaces,
commas, periods, hyphens, and apostrophes. What they refuse is structure: a
slash, a URL separator such as `?`, `#`, `&`, `=`, or `%`, an `@`, a quote, an
angle bracket, a backslash, an emoji, an invisible separator, or any control or
bidirectional formatting character. A name is escaped before it reaches a URL,
and the built URL must still address the configured template's own scheme and
host, so no argument can retarget the request. In the other direction, every field the bot posts is
somebody else's text: provider answers, announced display names, and room history
are stripped of terminal escapes and control characters, shortened, and truncated
before they are sent.

An unrecognized command produces exactly one short line (`unknown command — try
@gobot help`), subject to the cooldown. `dnotice` accepts a nick or a hash
prefix so a human does not have to copy a 32-character hash, and `me` resolves
to the requester.

**Reply routing.** `reply` chooses the route; the choice is made *before*
anything is sent, so a reply is never delivered twice:

- `auto` (default) — an in-room NOTICE, and a **direct NOTICE** (RRC `K_DST`)
  only when the request itself arrived as one, so a private question is answered
  privately and a room question is answered where it was asked.
- `direct` — always direct, and silent when that is impossible.
- `room` — always an in-room NOTICE.

A hub advertises its own capabilities in WELCOME and never publishes what
another client announced in HELLO, so no bot can learn whether a requester could
display a private answer. The stock RRC client renders no direct notice at all,
which is why `auto` does not send one to a room asker: the room is the one route
the asker is known to be able to read.

Every reply line is one NOTICE and one MTU-sized envelope: long lines are split
on a rune boundary with a `…` continuation marker, and a reply longer than
`max_reply_lines` ends with `… [truncated]`. Oversized envelopes are dropped
silently by the link layer, so the bot measures every envelope before sending.

`cooldown_s` is a per-requester rate limit that gates every reply, including the
refusal to answer a message that waited in flight too long, so a client that
repeats a request cannot make the bot repeat itself.

**Watching for announces.** RRC is connection-oriented, so a peer only speaks
while it is online; an announce is broadcast, so watching for one is the only way
to notice a peer the bot has never met. `watch` registers a filter — a
case-insensitive substring of the announced display name, or a hash prefix of the
destination or identity — and answers with a direct NOTICE the moment something
matches, including how far away it is. Watches are deliberately small and
forgiving: at most 10 per client and 100 per bot, one notice per filter per
minute, memory only (`a bot restart forgets it`, exactly as the confirmation
says), and a watcher who cannot be reached right now keeps the subscription
rather than being told the notice arrived.

**LXMF, when it is enabled.** RRC delivers to whoever is connected, and a mention
to an offline peer is simply lost. With `lxmf_enabled = true` the bot also runs an
LXMF router on the same identity, so `msg <nick|hash> <text>` can hand a message
to the store-and-forward network. The reply says what actually happened: the
immediate line is `queued for LXMF delivery to <nick> (<hash>…) via <method>`, and
the outcome follows as a direct NOTICE — `delivered`, `failed after 5 attempts`,
or `accepted by propagation node <hash> (store-and-forward)`. It writes into
somebody else's inbox rather than into a room, so it carries its own budget on
top of the reply cooldown: five messages per asker per minute, twenty for the
whole bot. Without a propagation node there is no store-and-forward, by design:
the bot never guesses at one, and a message to a peer with no known path fails
visibly instead of quietly waiting forever. Inbound LXMF is logged, not relayed
into a room, and offline catch-up from a propagation node is a later task.

**Deployment.** Any always-on supervisor works; the bot runs in the foreground,
logs to stderr, and shuts down cleanly on `SIGINT`/`SIGTERM`:

```ini
[Unit]
Description=gorrcbot RRC bot
After=network-online.target

[Service]
ExecStart=/usr/local/bin/gorrcbot
Restart=always
RestartSec=5
Environment=GORRCBOT_HOME=/var/lib/gorrcbot

[Install]
WantedBy=multi-user.target
```

Run `gorrcbot` once by hand before installing the unit, so the configuration and
the identity exist (and so the identity is backed up: losing `bot_identity`
changes the bot's identity hash, which is what other clients key on).

### grl — the Go Reticulum Lifesaver appliance

`grl` is the [Go Reticulum Lifesaver](https://gmlewis.github.io/go-reticulum/tools/grl/)
as a single executable: the sovereign, pocket-sized off-grid survival
communicator and field assistant, running natively on macOS, Linux Mint, or any
workstation. It is the reference the ESP32-C5 firmware is measured against, and
it runs the **whole appliance in one process**:

- a local **Reticulum** stack,
- a pure-Go **NMEA-0183 GNSS receiver** and **electronic compass** (or their
  configured static fallbacks, so it works on a desk with nothing plugged in),
- the shared **zero-hop field assistant** — the same command table `gorrcbot`
  answers over a radio link, computed in-process with no airtime,
- and the **captive survival dashboard** a smartphone reads with no app
  installed.

```console
$ go build -o bin/grl ./cmd/grl
$ ./bin/grl                     # creates ~/.grl/config.toml, then serves
$ open http://localhost:9111/   # the survival dashboard
```

```console
$ ./bin/grl --verbose                                   # show the resolved configuration
$ ./bin/grl --portal-addr 0.0.0.0:9111                  # let a phone on the LAN reach it
$ ./bin/grl --gps-port /dev/ttyUSB0 --compass-port /dev/ttyUSB1
```

`portal_addr = "127.0.0.1:9111"` (the default) serves the dashboard to this
workstation only; `"0.0.0.0:9111"` exposes it to a smartphone on the same
network, which is how the captive-portal experience is rehearsed before you have
hardware. Every captive-probe route the common operating systems already request
(`/generate_204`, `/hotspot-detect.html`, `/ncsi.txt`, `/connecttest.txt`) is
answered with `302 Found → /`, so the phone pops the dashboard up by itself on a
field SoftAP and simply reaches it at `http://localhost:9111/` on a desk.

`GRL_HOME` overrides the state directory (default `~/.grl`), exactly as
`GORRCBOT_HOME` does for the bot.

**Embedded data.** The `tide`, `buoy`, and `metar` reference tables are the
public providers' catalogs **in full** — every NOAA CO-OPS tide station, every
NDBC station that reports weather, and every OurAirports field with an IATA code
and scheduled service — so `near` answers with the genuinely nearest station
anywhere on earth and `search`/`list` page through the rest. Only `tower` is
assembled by hand, because no public feed publishes a comparable site list. The
tables are refreshed from their live sources by
[`scripts/update-offline-data.sh`](https://gmlewis.github.io/go-reticulum/tools/update-offline-data/),
which is idempotent and names the stations that changed. For comprehensive
documentation, see
[**update-offline-data**](https://gmlewis.github.io/go-reticulum/tools/update-offline-data/).

**Shared engine.** Both tools run
[`github.com/gmlewis/go-reticulum/bot`](https://gmlewis.github.io/go-reticulum/tools/grl/#architecture-the-shared-bot-package),
so a radio reply and a dashboard answer can never drift apart; `cmd/gorrcbot`
and `cmd/grl` are thin wrappers over it. For comprehensive documentation, see
the [**grl Documentation Guide**](https://gmlewis.github.io/go-reticulum/tools/grl/).

### gobot — the one-shot CLI for `@gobot`

`gobot` is the shell-friendly counterpart to `gorrcbot`: a small client that
connects to a hub, privately asks a live bot one question the way `/msg gobot
...` would, prints the answer to stdout, and exits. It writes no state and
generates no identity — it reuses the one already in `~/.reticulum`, and fails
if there is none.

Its hub destination is **hard-coded to the official gonomadnet Public RRC Hub**,
so it reaches the official `@gobot` with no configuration; `-dest` overrides it
to reach your own bot.

```bash
go install ./cmd/gobot/

gobot help buoy                    # ask the official @gobot
gobot wx Denver                    # live weather
gobot -dest <your-hub-hash> --to <your-bot-nick> help
```

The reply is plain text on stdout, diagnostics go to stderr, and the exit status
is `0` for an answer, `1` for an operational failure, `2` for a usage error, and
`3` when the request was delivered but the bot stayed silent.

> For comprehensive documentation, see the [**gobot Documentation Guide**](https://gmlewis.github.io/go-reticulum/tools/gobot/).

### Private messages between RRC users

RRC itself has no private-message command. `gorrcd` adds one without changing
the protocol: a client sends a `NOTICE` whose body is a command line and whose
`K_DST` is the **hub's identity hash** (the hash the `WELCOME` came from — not
the hub's destination hash), and the hub answers on that client's link alone.

- The hub advertises the capability `CAP_PRIVATE_COMMAND` (`K_CAPS` key `3`) in
  its `WELCOME`. Unknown capability keys are ignored by `rrcd` and by RRC
  clients, so no Python hub or Python client needs any change, and a client only
  ever sends one of these commands to a hub that advertises the key.
- `gorrcd` advertises it while `enable_private_commands = true` (the default in
  the hub's config); set it to `false` and the hub is byte-identical to the
  Python `rrcd` `WELCOME`.
- Commands: `/dnotice <nick|hash|me> <text>` (aliases `/dn` and `/msg`),
  `/dnoticeme <text>`, and `/dnoticecap`. They are ordinary client commands, so
  a user may type them in a room as well; the reply then lands in that room.
- A target with spaces in its nick must be quoted:
  `/msg 'gonomadnet on MiniPC' yo dude`.
- Resolution is exact — a full identity hash, a hex prefix of at least six
  characters, or a nick — and a token that matches more than one participant is
  reported with nothing sent. There is no fuzzy matching, so a private message
  cannot reach a participant the sender did not name.
- Delivery is one `T_NOTICE` to the target's link with `K_DST` set to that
  participant's full identity hash, no room, and the sender's own hash and nick
  in `K_SRC`/`K_NICK`. The hub confirms to the sender with the recipient's full
  hash and the message id, or reports `not found`, the ambiguity list, or that
  the body does not fit the target's link.

In `gonomadnet`, `/msg <nick|hash> <text>` sends one (quoting preserved), the
line you typed is echoed in the room view exactly as typed — the way an IRC
client echoes its own input — and an arriving private notice renders as
`private from <nick>`. Both rows are recorded in the hub's room buffer like any
other row (a private notice is attributed to the active room, or to every
joined room when no room is active), so they survive each room-view rebuild
instead of vanishing the moment the reply arrives. Stock Python `nomadnet` records an inbound private
notice in `RRC.notices` and never draws it — `nomadnet/ui/textui.py` has no
notice widget — so a Python user can send but not yet read one.

---

What follows is Mark Qvist's original README.md.

If anything below conflicts with the Go port support matrix above, the Go port
matrix wins.

==========

<p align="center"><img width="200" src="https://raw.githubusercontent.com/markqvist/Reticulum/master/docs/source/graphics/rns_logo_512.png"></p>

*This repository is [a public mirror](./MIRROR.md). All development is happening elsewhere.*

To understand the foundational philosophy and goals of this system, read the [Zen of Reticulum](Zen%20of%20Reticulum.md).

Reticulum is the cryptography-based networking stack for building local and wide-area
networks with readily available hardware. It can operate even with very high latency
and extremely low bandwidth. Reticulum allows you to build wide-area networks
with off-the-shelf tools, and offers end-to-end encryption and connectivity,
initiator anonymity, autoconfiguring cryptographically backed multi-hop
transport, efficient addressing, unforgeable delivery acknowledgements and
more.

The vision of Reticulum is to allow anyone to be their own network operator,
and to make it cheap and easy to cover vast areas with a myriad of independent,
inter-connectable and autonomous networks. Reticulum **is not** *one* network.
It is **a tool** for building *thousands of networks*. Networks without
kill-switches, surveillance, censorship and control. Networks that can freely
interoperate, associate and disassociate with each other, and require no
central oversight. Networks for human beings. *Networks for the people*.

Reticulum is a complete networking stack, and does not rely on IP or higher
layers, but it is possible to use IP as the underlying carrier for Reticulum.
It is therefore trivial to tunnel Reticulum over the Internet or private IP
networks.

Having no dependencies on traditional networking stacks frees up overhead that
has been used to implement a networking stack built directly on cryptographic
principles, allowing resilience and stable functionality, even in open and
trustless networks.

No kernel modules or drivers are required. Reticulum runs completely in
userland, and can run on practically any system that runs Python 3.

## Read The Manual
The full documentation for Reticulum is available at [markqvist.github.io/Reticulum/manual/](https://markqvist.github.io/Reticulum/manual/).

You can also download the [Reticulum manual as a PDF](https://github.com/markqvist/Reticulum/raw/master/docs/Reticulum%20Manual.pdf) or [as an e-book in EPUB format](https://github.com/markqvist/Reticulum/raw/master/docs/Reticulum%20Manual.epub).

For more info, see [reticulum.network](https://reticulum.network/) and [the FAQ section of the wiki](https://github.com/markqvist/Reticulum/wiki/Frequently-Asked-Questions).

## Notable Features
- Coordination-less globally unique addressing and identification
- Fully self-configuring multi-hop routing over heterogeneous carriers
- Flexible scalability over heterogeneous topologies
  - Reticulum can carry data over any mixture of physical mediums and topologies
  - Low-bandwidth networks can co-exist and interoperate with large, high-bandwidth networks
- Initiator anonymity, communicate without revealing your identity
  - Reticulum does not include source addresses on any packets
- Asymmetric X25519 encryption and Ed25519 signatures as a basis for all communication
  - The foundational Reticulum Identity Keys are 512-bit Elliptic Curve keysets
- Forward Secrecy is available for all communication types, both for single packets and over links
- Reticulum uses the following format for encrypted tokens:
  - Ephemeral per-packet and link keys and derived from an ECDH key exchange on Curve25519
  - AES-256 in CBC mode with PKCS7 padding
  - HMAC using SHA256 for authentication
  - IVs are generated through os.urandom()
- Unforgeable packet delivery confirmations
- Flexible and extensible interface system
  - Reticulum includes a large variety of built-in interface types
  - Ability to load and utilise custom user- or community-supplied interface types
  - Easily create your own custom interfaces for communicating over anything
- Authentication and virtual network segmentation on all supported interface types
- An intuitive and easy-to-use API
  - Simpler and easier to use than sockets APIs, but more powerful
  - Makes building distributed and decentralised applications much simpler
- Reliable and efficient transfer of arbitrary amounts of data
  - Reticulum can handle a few bytes of data or files of many gigabytes
  - Sequencing, compression, transfer coordination and checksumming are automatic
  - The API is very easy to use, and provides transfer progress
- Lightweight, flexible and expandable Request/Response mechanism
- Efficient link establishment
  - Total cost of setting up an encrypted and verified link is only 3 packets, totalling 297 bytes
  - Low cost of keeping links open at only 0.44 bits per second
- Reliable sequential delivery with Channel and Buffer mechanisms

## Reference Implementation

The Python code in this repository is the Reference Implementation of Reticulum.
The Reticulum Protocol is defined entirely and authoritatively by this reference
implementation, and its associated manual. It is maintained by Mark Qvist,
identified by the Reticulum Identity `<bc7291552be7a58f361522990465165c>`.

Compatibility with the Reticulum Protocol is defined as having full interoperability,
and sufficient functional parity with this reference implementation. Any specific protocol
implementation that achieves this is Reticulum. Any that does not is not Reticulum.

The reference implementation is licensed under the Reticulum License.

The Reticulum Protocol was dedicated to the Public Domain in 2016.

## Examples of Reticulum Applications
If you want to quickly get an idea of what Reticulum can do, take a look at the
[Programs Using Reticulum](https://reticulum.network/manual/software.html)
section of the manual, or the following resources:

- You can use the [rnsh](https://github.com/acehoss/rnsh) program to establish remote shell sessions over Reticulum.
- [LXMF](https://github.com/markqvist/lxmf) is a distributed, delay and disruption tolerant message transfer protocol built on Reticulum
- The [LXST](https://github.com/markqvist/lxst) protocol and framework provides real-time audio and signals transport over Reticulum. It includes primitives and utilities for building voice-based applications and hardware devices, such as the `rnphone` program, that can be used to build hardware telephones.
- For an off-grid, encrypted and resilient mesh communications platform, see [Nomad Network](https://github.com/markqvist/NomadNet)
- The Android, Linux, macOS and Windows app [Sideband](https://github.com/markqvist/Sideband) has a graphical interface and many advanced features, such as file transfers, image and voice messages, real-time voice calls, a distributed telemetry system, mapping capabilities and full plugin extensibility.
- [MeshChat](https://github.com/liamcottle/reticulum-meshchat) is a user-friendly LXMF client with a web-based interface, that also supports image and voice messages, as well as file transfers. It also includes a built-in page browser for browsing Nomad Network nodes.

## Where can Reticulum be used?
Over practically any medium that can support at least a half-duplex channel
with greater throughput than 5 bits per second, and an MTU of 500 bytes. Data radios,
modems, LoRa radios, serial lines, AX.25 TNCs, amateur radio digital modes,
WiFi and Ethernet devices, free-space optical links, and similar systems are
all examples of the types of physical devices Reticulum can use.

An open-source LoRa-based interface called
[RNode](https://markqvist.github.io/Reticulum/manual/hardware.html#rnode) has
been designed specifically for use with Reticulum. It is possible to build
yourself, or it can be purchased as a complete transceiver that just needs a
USB connection to the host.

Reticulum can also be encapsulated over existing IP networks, so there's
nothing stopping you from using it over wired Ethernet, your local WiFi network
or the Internet, where it'll work just as well. In fact, one of the strengths
of Reticulum is how easily it allows you to connect different mediums into a
self-configuring, resilient and encrypted mesh, using any available mixture of
available infrastructure.

As an example, it's possible to set up a Raspberry Pi connected to both a LoRa
radio, a packet radio TNC and a WiFi network. Once the interfaces are
configured, Reticulum will take care of the rest, and any device on the WiFi
network can communicate with nodes on the LoRa and packet radio sides of the
network, and vice versa.

## How do I get started?
The best way to get started with the Reticulum Network Stack depends on what
you want to do. For full details and examples, have a look at the
[Getting Started Fast](https://markqvist.github.io/Reticulum/manual/gettingstartedfast.html)
section of the [Reticulum Manual](https://markqvist.github.io/Reticulum/manual/).

To simply install Reticulum and related utilities on your system, the easiest way is via `pip`.
You can then start any program that uses Reticulum, or start Reticulum as a system service with
[the rnsd utility](https://markqvist.github.io/Reticulum/manual/using.html#the-rnsd-utility).

```bash
pip install rns
```

If you are using an operating system that blocks normal user package installation via `pip`,
you can return `pip` to normal behaviour by editing the `~/.config/pip/pip.conf` file,
and adding the following directive in the `[global]` section:

```text
[global]
break-system-packages = true
```

Alternatively, you can use the `pipx` tool to install Reticulum in an isolated environment:

```bash
pipx install rns
```

When first started, Reticulum will create a default configuration file,
providing basic connectivity to other Reticulum peers that might be locally
reachable. The default config file contains a few examples, and references for
creating a more complex configuration.

If you have an old version of `pip` on your system, you may need to upgrade it first with `pip install pip --upgrade`. If you no not already have `pip` installed, you can install it using the package manager of your system with `sudo apt install python3-pip` or similar.

For more detailed examples on how to expand communication over many mediums such
as packet radio or LoRa, serial ports, or over fast IP links and the Internet using
the UDP and TCP interfaces, take a look at the [Supported Interfaces](https://markqvist.github.io/Reticulum/manual/interfaces.html)
section of the [Reticulum Manual](https://markqvist.github.io/Reticulum/manual/).

## Included Utilities
Reticulum includes a range of useful utilities for managing your networks,
viewing status and information, and other tasks. You can read more about these
programs in the [Included Utility Programs](https://markqvist.github.io/Reticulum/manual/using.html#included-utility-programs)
section of the [Reticulum Manual](https://markqvist.github.io/Reticulum/manual/).

- The system daemon `rnsd` for running Reticulum as an always-available service
- An interface status utility called `rnstatus`, that displays information about interfaces
- The path lookup and management tool `rnpath` letting you view and modify path tables
- A diagnostics tool called `rnprobe` for checking connectivity to destinations
- A simple file transfer program called `rncp` making it easy to transfer files between systems
- The identity management and encryption utility `rnid` let's you manage Identities and encrypt/decrypt files
- The remote command execution program `rnx` let's you run commands and
  programs and retrieve output from remote systems

All tools, including `rnx` and `rncp`, work reliably and well even over very
low-bandwidth links like LoRa or Packet Radio. For full-featured remote shells
over Reticulum, also have a look at the [rnsh](https://github.com/acehoss/rnsh)
program.

## Wasm Plugin Sandbox

Several Go tools in this repository can load third-party **wasm plugins** that
extend them without rebuilding anything. Plugins are compiled WebAssembly
modules running **in-process** through the
[wago](https://github.com/wago-org/wago) runtime: deny-by-default host
imports (a plugin only reaches the capabilities the host explicitly wires),
bounded linear memory (16 MiB) and tables (1024 entries), and a hard
per-invocation execution budget (2 seconds by default — runaway guest loops
are preempted by the runtime's interrupt mechanism and return
`context.DeadlineExceeded`).

### Tools that accept plugins

| Tool | Plugin directory (first existing wins) | Plugin ABI entry point | What plugins do |
|------|----------------------------------------|------------------------|-----------------|
| `gorrcd` | `$RRCD_HOME/plugins/` (default `~/.rrcd/plugins`) | `handle_command` | Unrecognized `/<cmd>` slash commands are executed by `<cmd>.wasm`; the response is delivered to the requesting client as a NOTICE |
| `gornsd` | `<config-dir>/plugins/` (default `~/.reticulum/plugins`; `-config` overrides) | `on_announce` | Observer plugins receive every incoming network announce as a serialized JSON event |
| `golxmd` | `<config-dir>/plugins/` (`/etc/lxmd` → `~/.config/lxmd` → `~/.lxmd`) | `filter_inbound` | Filter plugins accept (1) or drop (0) each inbound LXMF message before delivery |
| `gornx` | `/etc/rnx/plugins/` → `~/.config/rnx/plugins/` → `~/.rnx/plugins/` | `handle_command` | A remote command whose first token matches `<cmd>.wasm` runs in the sandbox instead of the raw shell; other commands keep the classic rnx behavior |

To install a plugin, copy its `.wasm` file into the tool's plugin directory
(the plugin's file name minus `.wasm` is its command name / KV scope) and
restart the tool. The data directory alongside it
(`<plugins-dir>/data/<plugin>/`) is that plugin's private scratch store.

### The plugin ABI

Every plugin is an ordinary WebAssembly module. The host calls these exports
(all pointers are offsets into the module's own linear memory):

| Export | Signature | Purpose |
|--------|-----------|---------|
| `wagoplugin_alloc` | `(len i32) -> (ptr i32)` | Reserve guest memory for the host's request payload |
| `handle_command` | `(req_ptr i32, req_len i32) -> (resp_ptr i32, resp_len i32)` | gorrcd / gornx: run a command; the response bytes become the output |
| `render_page` | `(req_ptr i32, req_len i32) -> (resp_ptr i32, resp_len i32)` | go-nomadnet: render a `.wasm` page to Micron markup |
| `filter_inbound` | `(msg_ptr i32, msg_len i32) -> (action i32)` | golxmd: 1 = accept the message, 0 = drop it |
| `on_announce` | `(ann_ptr i32, ann_len i32) -> (status i32)` | gornsd: observe an announce; 0 = acknowledged |

Host capabilities are wired one by one — a module importing anything else
fails to instantiate. Currently wired: `rns.log(ptr, len)` (forward a message
to the host logger), `rns.kv_set(key_ptr, key_len, val_ptr, val_len) ->
status`, and `rns.kv_get(key_ptr, key_len, out_ptr, out_cap) -> n` — a
per-plugin scratch key/value store (10 MiB quota per plugin, keys are
filenames under `<plugins-dir>/data/<plugin>/`).

### Writing a plugin

The repository ships ready-to-run examples under
[`assets/wasm-plugins/`](assets/wasm-plugins/), each with its WebAssembly text
(`.wat`) source and the assembled `.wasm` binary, plus install instructions in
the file header:

- [`echo`](assets/wasm-plugins/echo/) — echoes the request JSON; works with `gorrcd` and `gornx`
- [`kv-demo`](assets/wasm-plugins/kv-demo/) — stores and reads a value through the `rns.kv_*` imports
- [`announce-observer`](assets/wasm-plugins/announce-observer/) — acknowledges every network announce in `gornsd`
- [`filter-accept`](assets/wasm-plugins/filter-accept/) — accepts every inbound LXMF message in `golxmd`

The typical authoring loop uses the WebAssembly Binary Toolkit (`wat2wasm`)
and the `wago` CLI:

```bash
wat2wasm my-plugin.wat -o my-plugin.wasm
wago validate my-plugin.wasm              # module-level validation
wago module imports my-plugin.wasm        # list the host capabilities it needs
cp my-plugin.wasm ~/.rrcd/plugins/        # install, then restart the tool
```

The example binaries are smoke-tested by the repository's test suites
(`TestExamplePluginsWork` and friends), and any language that can emit plain
WebAssembly works — for example, [TinyGo](https://tinygo.org/) can build richer
plugins from Go source.

### Plugin ideas

Some things the sandbox is designed to make safe and easy:

- **gorrcd chat commands**: dice roller, karma tracker, reminder bot, poll
  bot, a bridge that relays `/<cmd>` to an LXMF feed
- **gornsd announce observers**: node census (count and age of heard nodes),
  first-seen alerts for new destination hashes, uptime heartbeat tracker
- **golxmd delivery filters**: keyword spam filter, message-size limiter,
  allowlist by source hash, quiet-hours filter
- **gornx sandboxed commands**: any operational tool you want remote peers to
  run without giving them a shell (disk report, time sync check, package list)
- **go-nomadnet dynamic pages**: hit counters, guestbooks, form processors,
  live status dashboards (see the go-nomadnet README)

### Security considerations

Read this before installing plugins on an **internet-facing** node (for
example one with a public TCP server interface):

- **Plugin installation is operator-only.** There is no remote write path:
  network peers cannot upload `.wasm` files, cannot write to plugin
  directories, and cannot reach the KV store except through a plugin the
  operator installed. The realistic plugin threat is a **supply-chain** one —
  only install plugins you built or reviewed. A hostile plugin *inside* the
  sandbox can still: burn its 2-second budget per invocation (CPU noise), emit
  arbitrary text/markup to users, and store up to 10 MiB in its own scratch
  store. It cannot open network connections, read files outside its store,
  spawn processes, or survive its execution budget.
- **The KV store is not a shared network resource.** It is scoped per plugin
  (`<plugins-dir>/data/<plugin>/`, keys validated as safe filenames, 10 MiB
  total quota, `0o700`/`0o600` permissions), and no host ever executes stored
  bytes — they are data a plugin chose to keep. "Malware in the KV store" is
  therefore only possible if you install a plugin that (a) persists
  attacker-controlled input and (b) later serves it to other users; the
  malicious behavior lives in the plugin, not the store. Cross-user exposure
  then becomes a **stored content-injection** problem (see next point).
- **Treat all plugin output as untrusted markup.** Plugin responses are
  delivered to clients that render Micron/terminal formatting. A plugin that
  echoes or re-renders user-controlled data (chat command arguments, announce
  app_data, form fields) can be used to phish other users or inject links
  inside their clients. Plugin authors must escape or strip user-controlled
  data before returning it; operators should review what a plugin does with
  remote input before exposing it.
- **Announces are unauthenticated broadcasts.** On a public interface, any
  peer can announce with arbitrary `app_data`, so gornsd observer plugins that
  persist or re-display app_data handle attacker-controlled input. The
  serialized JSON event neutralizes newline injection, but apply the same
  untrusted-data rules as above.
- **Bounded, but not rate-limited, execution.** Every execution is capped
  (2 seconds, 16 MiB) yet a public node lets any reachable peer trigger runs —
  each `.wasm` page request compiles a fresh instance. Mitigations: put a
  `.allowed` file next to a plugin/page to restrict who may execute it (the
  access check runs *before* the sandbox), keep public-node plugins trivial,
  and consider firewall/rate-limiting at the interface level.
- **`gornx` authentication is on by default** (allowed-identity lists) and
  `--no-auth` turns it off — never combine `--no-auth` with a public
  interface, since that lets anyone invoke commands.
- **Known hardening edge**: `pluginstore` writes store files through plain
  `os.WriteFile`, which follows symlinks. Only the local operator can plant a
  symlink in a plugin's data directory, so this requires host access first —
  but hardening with `O_NOFOLLOW` is a reasonable future improvement.

## Supported interface types and devices

Reticulum implements a range of generalised interface types that covers most of
the communications hardware that Reticulum can run over. If your hardware is
not supported, it's [simple to implement a custom interface module](https://markqvist.github.io/Reticulum/manual/interfaces.html#custom-interfaces).

Pull requests for custom interfaces are gratefully accepted, provided they are
generally useful and well-tested in real-world usage.

Currently, the following built-in interfaces are supported:

- Any Ethernet device
- LoRa using [RNode](https://unsigned.io/rnode/)
- Packet Radio TNCs (with or without AX.25)
- KISS-compatible hardware and software modems
- Any device with a serial port
- TCP over IP networks
- UDP over IP networks
- External programs via stdio or pipes
- Custom hardware via stdio or pipes

## Performance
Reticulum targets a *very* wide usable performance envelope, but prioritises
functionality and performance on low-bandwidth mediums. The goal is to
provide a dynamic performance envelope from 250 bits per second, to 1 gigabit
per second on normal hardware.

Currently, the usable performance envelope is approximately 150 bits per second
to 500 megabits per second, with physical mediums faster than that not being
saturated. Performance beyond the current level is intended for future
upgrades, but not highly prioritised at this point in time.

## Current Status
All core protocol features are implemented and functioning, but additions will
probably occur as real-world use is explored and understood. The API and wire-format
can be considered stable.

## Dependencies
The installation of the default `rns` package requires only two external dependencies, listed
below. Almost all systems and distributions have readily available packages for
these dependencies, and when the `rns` package is installed with `pip`, they
will be downloaded and installed as well.

- [PyCA/cryptography](https://github.com/pyca/cryptography)
- [pyserial](https://github.com/pyserial/pyserial)

On more unusual systems, and in some rare cases, it might not be possible to
install or even compile one or more of the above modules. In such situations,
you can use the `rnspure` package instead, which require no external
dependencies for installation. Please note that the contents of the `rns` and
`rnspure` packages are *identical*. The only difference is that the `rnspure`
package lists no dependencies required for installation.

No matter how Reticulum is installed and started, it will load external
dependencies only if they are *needed* and *available*. If for example you want
to use Reticulum on a system that cannot support
[pyserial](https://github.com/pyserial/pyserial), it is perfectly possible to
do so using the `rnspure` package, but Reticulum will not be able to use
serial-based interfaces. All other available modules will still be loaded when
needed.

**Please Note!** If you use the `rnspure` package to run Reticulum on systems
that do not support [PyCA/cryptography](https://github.com/pyca/cryptography),
it is important that you read and understand the [Cryptographic
Primitives](#cryptographic-primitives) section of this document.

## Bootstrapping Connectivity

Reticulum is not a service you subscribe to, nor is it a single global network you "join".
Reticulum provides functionality for discovering available public interfaces
over the network itself, and the broader community has provided various directories
of publicly available entrypoints to bootstrap connectivity.

To learn how to establish initial connectivity over Reticulum, read the [Bootstrapping Connectivity](https://reticulum.network/manual/gettingstartedfast.html#bootstrapping-connectivity) section of the manual.

If you already have a general idea of how this works, you can use community-run
sites such as [directory.rns.recipes](https://directory.rns.recipes/) and [rmap.world](https://rmap.world)
to find interface definitions for initial connectivity to the global distributed Reticulum backbone.

## Public Testnet
***Important!** Historically, a developer-targeted testnet was made available by the Reticulum project itself. As the amount of global Reticulum nodes and entrypoints have grown to a substantial quantity, this public testnet, including the Amsterdam Testnet entrypoint, has now been decommissioned. If your still have instances that relied on this entrypoint for connectivity, transition to using the distributed backbone instead. Reticulum now includes a full on-network interface discovery and connectivity bootstrapping system. Read the [Bootstrapping Connectivity](https://reticulum.network/manual/gettingstartedfast.html#bootstrapping-connectivity) section of the manual for pointers.*

## Support Reticulum
You can help support the continued development of open, free and private communications systems by donating via one of the following channels:

- Monero:
  ```
  84FpY1QbxHcgdseePYNmhTHcrgMX4nFfBYtz2GKYToqHVVhJp8Eaw1Z1EedRnKD19b3B8NiLCGVxzKV17UMmmeEsCrPyA5w
  ```
- Bitcoin
  ```
  bc1pgqgu8h8xvj4jtafslq396v7ju7hkgymyrzyqft4llfslz5vp99psqfk3a6
  ```
- Ethereum
  ```
  0x91C421DdfB8a30a49A71d63447ddb54cEBe3465E
  ```
- Liberapay: https://liberapay.com/Reticulum/

- Ko-Fi: https://ko-fi.com/markqvist

## Cryptographic Primitives
Reticulum uses a simple suite of efficient, strong and well-tested cryptographic
primitives, with widely available implementations that can be used both on
general-purpose CPUs and on microcontrollers.

One of the primary considerations for choosing this particular set of primitives is
that they can be implemented *safely* with relatively few pitfalls, on practically
all current computing platforms.

The primitives listed here **are authoritative**. Anything claiming to be Reticulum,
but not using these exact primitives **is not** Reticulum, and possibly an
intentionally compromised or weakened clone. The utilised primitives are:

- Reticulum Identity Keys are 512-bit Curve25519 keysets
  - A 256-bit Ed25519 key for signatures
  - A 256-bit X22519 key for ECDH key exchanges
- HKDF for key derivation
- Encrypted tokens are based on the [Fernet spec](https://github.com/fernet/spec/)
  - Ephemeral keys derived from an ECDH key exchange on Curve25519
  - HMAC using SHA256 for message authentication
  - IVs must be generated through `os.urandom()` or better
  - AES-256 in CBC mode with PKCS7 padding
  - No Fernet version and timestamp metadata fields
- SHA-256
- SHA-512

In the default installation configuration, the `X25519`, `Ed25519`,
and `AES-256-CBC` primitives are provided by [OpenSSL](https://www.openssl.org/)
(via the [PyCA/cryptography](https://github.com/pyca/cryptography) package).
The hashing functions `SHA-256` and `SHA-512` are provided by the standard
Python [hashlib](https://docs.python.org/3/library/hashlib.html). The `HKDF`,
`HMAC`, `Token` primitives, and the `PKCS7` padding function are always
provided by the following internal implementations:

- [HKDF.py](RNS/Cryptography/HKDF.py)
- [HMAC.py](RNS/Cryptography/HMAC.py)
- [Token.py](RNS/Cryptography/Token.py)
- [PKCS7.py](RNS/Cryptography/PKCS7.py)


Reticulum also includes a complete implementation of all necessary primitives
in pure Python. If OpenSSL and PyCA are not available on the system when
Reticulum is started, Reticulum will instead use the internal pure-python
primitives. A trivial consequence of this is performance, with the OpenSSL
backend being *much* faster. The most important consequence however, is the
potential loss of security by using primitives that has not seen the same
amount of scrutiny, testing and review as those from OpenSSL.

Please note that by default, installing Reticulum will **require** OpenSSL and
PyCA to also be automatically installed if not already available. It is only
possible to use the pure-python primitives if this requirement is specifically
overridden by the user, for example by installing the `rnspure` package instead
of the normal `rns` package, or by running directly from local source-code.

If you want to use the internal pure-python primitives, it is **highly
advisable** that you have a good understanding of the risks that this pose, and
make an informed decision on whether those risks are acceptable to you.

Reticulum is relatively young software, and should be considered as such. While
it has been built with cryptography best-practices very foremost in mind, it
_has not_ been externally security audited, and there could very well be
privacy or security breaking bugs. If you want to help out, or help sponsor an
audit, please do get in touch.

## Acknowledgements & Credits
Reticulum can only exist because of the mountain of Open Source work it was
built on top of, the contributions of everyone involved, and everyone that has
supported the project through the years. To everyone who has helped, thank you
so much.

A number of other modules and projects are either part of, or used by
Reticulum. Sincere thanks to the authors and contributors of the following
projects:

- [PyCA/cryptography](https://github.com/pyca/cryptography), *BSD License*
- [Pure-25519](https://github.com/warner/python-pure25519) by [Brian Warner](https://github.com/warner), *MIT License*
- [Pysha2](https://github.com/thomdixon/pysha2) by [Thom Dixon](https://github.com/thomdixon), *MIT License*
- [Python AES-128](https://github.com/orgurar/python-aes) by [Or Gur Arie](https://github.com/orgurar), *MIT License*
- [Python AES-256](https://github.com/boppreh/aes) by [BoppreH](https://github.com/boppreh), *MIT License*
- [Curve25519.py](https://gist.github.com/nickovs/cc3c22d15f239a2640c185035c06f8a3#file-curve25519-py) by [Nicko van Someren](https://gist.github.com/nickovs), *Public Domain*
- [I2Plib](https://github.com/l-n-s/i2plib) by [Viktor Villainov](https://github.com/l-n-s)
- [PySerial](https://github.com/pyserial/pyserial) by Chris Liechti, *BSD License*
- [Configobj](https://github.com/DiffSK/configobj) by Michael Foord, Nicola Larosa, Rob Dennis & Eli Courtwright, *BSD License*
- [ifaddr](https://github.com/pydron/ifaddr) by Stefan C. Mueller, *MIT License*
- [Umsgpack.py](https://github.com/vsergeev/u-msgpack-python) by [Ivan A. Sergeev](https://github.com/vsergeev)
- [Python](https://www.python.org)
