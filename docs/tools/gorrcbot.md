# gorrcbot — Autonomous RRC Bot & Field Assistant

`gorrcbot` is a headless, always-on RRC (Reticulum Relay Chat) bot and autonomous off-grid assistant. It operates as a **client, not a hub**: it connects to one or more RRC hubs simultaneously, joins configured rooms, maintains connectivity across link drops, and serves requests addressed to its nick or cryptographic identity hash.

---

## Architecture & Principles

- **Single Identity Everywhere**: `gorrcbot` generates one 64-byte Reticulum private identity (`bot_identity`) and uses it across all connected hubs. Its identity hash is identical on all hubs, allowing users to reach it using consistent address prefixes.
- **Strict Addressing Contract**: The bot is completely silent unless addressed directly. It will never spam channels, and it completely ignores chat traffic not directed at it.
- **Rate-Limiting & Cooldowns**: Built-in per-identity cooldown prevents abuse or channel flooding over low-bandwidth LoRa links.
- **Offline First**: The vast majority of tactical and field assistant commands (Plus Codes, geodesy, dead reckoning, sun/moon ephemeris, wilderness medicine cards, Morse code, unit conversions, and the cell/repeater finder with its built-in WGS-84 ↔ GCJ-02 conversion) compute entirely in-process with **zero internet connection required**. The marine, aviation, navigation, and communications-site catalogs are embedded too, so `search`, `near`, and `list` find a station id before any provider is configured and while a provider is unreachable.
- **Low-Bandwidth by Design**: Long answers are paginated to the operator's `max_reply_lines` budget, every page carries the exact command that asks for the next one, and `more` / `next` continue a walk through a catalog without retyping the query.
- **Graceful Telemetry Degradation**: For commands that query external live telemetry (weather, marine tides, buoys, river gauges, flight tracking, space weather), providers are queried through sanitized HTTP/HTTPS templates. If an endpoint is unconfigured or unreachable, the bot reports honest failure without guessing or hallucinating.

---

## Quickstart

### 1. First Run Bootstrap

Run `gorrcbot` without arguments:

```bash
gorrcbot
```

On its very first invocation, `gorrcbot`:
1. Creates the storage directory `~/.gorrcbot/storage/`.
2. Generates a fresh 64-byte private key file `~/.gorrcbot/bot_identity` with restrictive permissions (`0600`).
3. Writes a fully documented, pre-populated default configuration file to `~/.gorrcbot/config.toml`.
4. Prints a notice and exits cleanly with code `0`, allowing you to review and configure hubs before establishing network connections.

### 2. Check Configuration

Perform a dry-run check without connecting to any hubs:

```bash
gorrcbot --check-config
```

### 3. Normal Execution

```bash
gorrcbot
```

Use `--log-level DEBUG` or `--log-file /var/log/gorrcbot.log` as desired.

---

## Configuration Reference (`~/.gorrcbot/config.toml`)

Here is the complete configuration format with verified, working default providers:

```toml
# Top level: paths to identity and client storage
identity_path = "~/.gorrcbot/bot_identity"
storage_dir = "~/.gorrcbot/storage"

[bot]
# Advertised nickname and default trigger nick
nick = "gobot"

# Reply routing policy:
#   "auto"   - direct notices reply by direct notice; room queries reply to room
#   "direct" - all replies are sent via direct notice (K_DST)
#   "room"   - all replies are posted to the room
reply = "auto"

# Minimum seconds between replies to the same user identity
cooldown_s = 8.0

# Introduce itself when joining rooms
announce_on_join = false

# Maximum reply lines before truncation occurs
max_reply_lines = 12

# Render the discovery rows (search/near/list) as clickable Micron links,
# which a NomadNet client shows as buttons and every other client shows
# literally. Leave false unless the room reads Micron.
micron_links = false

# ---------------------------------------------------------------------
# Live Telemetry & API Provider Templates
# ---------------------------------------------------------------------

# Weather via plain-text wttr.in endpoint (HTTP avoids expired TLS cert)
weather_url = "http://wttr.in/{place}?format=%l:+%C+%t+%w+%h"

# NOAA Tides & Currents API: 48-hour high/low water predictions
tide_url = "https://api.tidesandcurrents.noaa.gov/api/prod/datagetter?product=predictions&datum=MLLW&time_zone=gmt&units=english&interval=hilo&format=json&station={place}&begin_date={date}&range=48"

# NOAA National Data Buoy Center: real-time sea state from ocean buoys
buoy_url = "https://www.ndbc.noaa.gov/data/realtime2/{place}.txt"

# USGS Streamflow & Gauge Height: instantaneous stage and discharge
river_url = "https://waterservices.usgs.gov/nwis/iv/?sites={place}&format=json&parameterCd=00065,00060&period=P1D"

# NOAA National Water Prediction Service: official flood stages
river_flood_url = "https://api.water.noaa.gov/nwps/v1/gauges/{place}"

# NOAA Space Weather Prediction Center: planetary K-index & geomagnetic storms
space_weather_url = "https://services.swpc.noaa.gov/products/noaa-planetary-k-index.json"

# Aviation Weather Center: raw METAR reports for airport ICAO codes
metar_url = "https://aviationweather.gov/api/data/metar?ids={place}&format=raw"

# National Weather Service: active weather alerts & warnings
weather_alert_url = "https://api.weather.gov/alerts/active?area={place}"

# The Space Devs: upcoming and past orbital rocket launches
launch_url = "https://ll.thespacedevs.com/2.3.0/launches/{mode}/?limit={limit}"

# ADSB.lol & ADS-B DB: live flight tracking and route decoding
flight_url = "https://api.adsb.lol/v2/callsign/{flight}"
flight_route_url = "https://api.adsbdb.com/v0/callsign/{flight}"

# ---------------------------------------------------------------------
# Offline Text, LXMF & Emergency Dispatch
# ---------------------------------------------------------------------

# Path to King James Bible text file (one verse per line).
# Full kjv.txt file available for download here:
# https://github.com/gmlewis/kjv-ref/blob/master/kjv.txt
kjv_txt_file = ""

# Optional local dataset for the tower/repeater/cell finder, merged over the
# cell, repeater, and emergency-site catalog embedded in the binary. The column
# order is fixed:
#   id,name,type,lat,lng,freq,offset,tone,operator,city,region,country,elev
# type is RPT, CELL, EMERG, or MAR; coordinates are WGS-84 decimal degrees; a
# row whose id matches an embedded one replaces it, and a new id is added. The
# file may simply be dropped in at this path; it need not exist, and the
# command stays fully offline either way.
towers_path = "~/.gorrcbot/towers.csv"

# LXMF messaging support (msg/lxmf command)
lxmf_enabled = false
lxmf_propagation_node = ""
lxmf_announce_minutes = 360

# Optional 32-hex LXMF destination hash for emergency SOS beacons
emergency_lxmf_destination = ""

# ---------------------------------------------------------------------
# Go Reticulum Lifesaver (GRL): GNSS & the captive survival portal
# ---------------------------------------------------------------------

# GNSS receiver streaming NMEA-0183 sentences, for example /dev/ttyUSB0 or
# /dev/ttyACM0. It supplies the position that /whereami, "tower near",
# "tide near", "sun", and /sos use automatically.
gps_port = ""

# Static position for a node with no receiver (any location notation).
gps_fix = ""

# Electronic compass streaming NMEA-0183 heading sentences ($HCHDG, $HCHDM,
# $HCHDT), for example a QMC5883L or LSM303 magnetometer behind a serial
# bridge. It supplies the heading a GNSS receiver cannot while standing still.
compass_port = ""

# Static magnetic heading for a node with no compass sensor: degrees ("042")
# or a compass point ("NE"). The World Magnetic Model converts it to true north
# from the node's position.
compass_heading = ""

# Captive survival dashboard for any phone that joins this node's Wi-Fi:
# "127.0.0.1:8080" while testing, ":80" in the field. Empty binds nothing.
portal_addr = ""

# ---------------------------------------------------------------------
# Connected Hubs & Rooms
# ---------------------------------------------------------------------

[[hubs]]
name = "gonomadnet Public Hub"
destination = "a012129c10205c0b9441fcd2b755b2a7"
rooms = ["general"]
nick = ""
respond_to = { general = "gobot" }
```

---

## How to Address the Bot

The bot answers when addressed in any of the following manners:

| Method | Example | Behavior |
|--------|---------|----------|
| **Room message with nick prefix** | `@gobot help` | Works in any room where the bot is present. Tolerates `:` or `,` suffix (`@gobot: ping`). |
| **Room message with hash prefix** | `@a012129c help` | Addressed by the first 6+ hex characters of the bot's identity hash. |
| **Direct Notice (`K_DST`)** | `/notice @gobot help` or `/msg gobot help` | Direct notices do not require typing `@gobot`. The bot replies directly back to the sender. |

---

## Private Messaging to @gobot

While `@gobot` can be triggered publicly inside any room it has joined, **private direct messaging is strongly recommended** for most commands:

```text
/msg gobot help
/msg gobot wx Denver
/msg gobot loc 849VCWC8+R9
/msg gobot sun 37.422,-122.084
/msg gobot checkin 849VCWC8+R9 overdue 4h Trail run to summit
```

> **From a shell:** the [**`gobot`**](gobot.md) CLI performs exactly this
> exchange without a chat client — it joins the hub, sends one `/msg gobot ...`
> line, prints the reply to stdout, and exits. Its hub destination is hard-coded
> to the official gonomadnet Public RRC Hub, so `gobot help` reaches the
> official `@gobot` out of the box.

### Why Private Messaging is Recommended

1. **Bandwidth Preservation on LoRa & Radio Meshes**:
   Reticulum channels often operate over bandwidth-constrained radio networks (such as LoRa at 1–5 kbps or VHF packet radio). Commands with verbose or multi-line responses (such as `help`, `metar`, `sun`, `weather`, `tide`, `loc`, `buoy`, or `kjv`) take several seconds to transmit. Running them privately keeps shared room airtime clear for human peer conversation.
2. **Operational Security (OPSEC) & Coordinate Privacy**:
   Commands such as `loc`, `proj`, `dist`, `sun`, `weather`, and especially `checkin` or `sitrep` involve exact geographic coordinates, personal waypoints, or overdue travel timelines. Querying privately ensures that your physical location and itinerary are not broadcast to every listener on a public hub.
3. **Channel Courtesy**:
   Queries for space weather (`spacewx`), flight tracking (`flight`), marine buoys (`buoy`), or unit conversions (`conv`) generate chatter that may distract or interrupt other room participants.

*(Note: Emergency distress beacons like `@gobot sos` should typically still be sent publicly to rooms so fellow operators and rescuers are alerted!)*

### How Private Addressing & Reply Routing Work

- **Addressing Flexibility**: In RRC, `/msg gobot <command>` (or `/dnotice gobot <command>`) packages the message into a direct notice envelope with `K_DST` set directly to the bot's 16-byte identity hash. Because the envelope itself addresses the bot, the trigger prefix is optional:
  - `/msg gobot help`
  - `/msg gobot @gobot help`
  Both work identically.
- **Strict Private Reply Guarantee**: When a request arrives via direct notice (`K_DST`), `gorrcbot`'s reply routing policy **always** transmits the response as a direct notice back to the sender's cryptographic identity hash (`msg.Src`). The response **never** leaks or appears in any public room.
- **Direct Notice Capabilities (`CAP_DIRECT_NOTICE`)**: Direct messaging requires that the connected hub supports RRC direct notice routing (`CAP_DIRECT_NOTICE = 2`), which all modern `rrcd` and `gorrcd` hubs provide. If a hub cannot deliver direct notices, `gorrcbot` safely drops the direct reply rather than leaking it into a public channel.

---

## Station Discovery & Search (`search`, `near`, `list`)

Three of the field assistant's commands are useless without an identifier: `tide`
takes a 7-digit NOAA station id, `buoy` takes a 4–6 character NDBC station id,
and `metar` takes a 4-letter ICAO code. None of them can be guessed. Rather than
leave an operator to find one on another device, every one of those catalogs is
**embedded in the binary** and searched offline:

| Form | What it does | Example |
|------|--------------|---------|
| `<cmd> search <query> [page]` | Case-insensitive match on the identifier, the name, the state or region, and (for `metar`) the city and IATA code. Every word of the query must appear somewhere in the row. | `@gobot tide search san francisco` |
| `<cmd> near <place\|coords\|pluscode>` | The three rows closest to a position, nearest first, with the distance in nautical miles and the compass bearing. The argument may be a position in any of the five notations, a Plus Code, or a name or identifier **from the catalog itself**. | `@gobot buoy near 37.8,-122.4` |
| `<cmd> list [state\|region] [page]` | Every row, or only those in a state (`CA`, `OR`, `WA`), a state's own name (`california`), or a basin or country code (`GOM`, `ATL`, `CAR`, `GB`). | `@gobot metar list CO` |

The catalogs are data, not guesses, and they are read without touching the
network — which is the point. A station id is discoverable **before** the
operator has configured `tide_url`, `buoy_url`, or `metar_url`, and while that
provider is unreachable:

```text
/msg gobot tide search san francisco
  Tide stations matching "san francisco" (Page 1 of 1):
    9414290: San Francisco (Golden Gate), CA
  [Page 1 of 1: end of results]

/msg gobot tide near 849VCWC8+R9
  Tide stations near 849VCWC8+R9 (Page 1 of 1):
    9414750 (23.4 nmi NNW): Alameda, CA
    9414764 (24.3 nmi NNW): Oakland Inner Harbor, CA
    9414290 (29.4 nmi NW): San Francisco (Golden Gate), CA
  [Page 1 of 1: end of results]

/msg gobot buoy list HI
  Weather buoys in HI (Page 1 of 3):
    51000: Northern Hawaii One, HI
    51001: Northwestern Hawaii One, HI
    51002: Southwest Hawaii, HI
    51004: Southeast Hawaii, HI
  [Page 1 of 3: ask "buoy list HI 2" or "more" for next]

/msg gobot metar search denver
  Airports matching "denver" (Page 1 of 1):
    KDEN: Denver International (CO)
    KBJC: Rocky Mountain Metropolitan (CO)
  [Page 1 of 1: end of results]
```

Once an id is known, the original form is unchanged and answers with live
telemetry: `@gobot tide 9414290`, `@gobot buoy 46026`, `@gobot metar KDEN`.

### What Each Catalog Covers

| Command | Catalog | Size | Region codes |
|---------|---------|------|--------------|
| `tide` | NOAA tide and current stations an operator is likely to name | ~110 stations | US state codes, plus `GU`, `AS` |
| `buoy` | NDBC offshore weather buoys that report meteorological data, from the Pacific, Atlantic, Gulf, Hawaii, Alaska, and the Great Lakes | ~115 buoys | US state codes, plus `ATL`, `GOM`, `CAR`, `PAC` |
| `metar` | Every large US airport with an IATA code, the regional fields that carry scheduled passenger service, and the world's major international hubs | ~625 airfields | US state codes, plus two-letter country codes (`GB`, `FR`, `JP`, …) |
| `tower` | Curated mountain-top and regional communications sites: amateur repeaters, cellular masts, public-safety relays, and marine VHF stations, balanced between the United States and China with major international hubs | ~220 sites | US state codes, Chinese province codes (`BJ`, `GD`, `SC`, `XJ`, …), and country codes (`US`, `CN`, `GB`, `JP`, …) |

> [!NOTE]
> A `metar` region may be a state code or a country code, and the two can
> collide (`CO` is both Colorado and Colombia). A **state always wins**: `metar
> list CO` lists Colorado, and Colombia's fields are reached by name — `metar
> list colombia`. The same rule applies to `AR`, `CA`, `DE`, `ID`, `IN`, `MA`,
> and `TN`. Every country code that does not collide works unchanged.

A station id that is **not** in a catalog is still accepted by its command,
because each provider is authoritative about its own stations; the embedded
catalog is a shortcut for finding the right one, not a filter on what may be
asked.

---

## Low-Bandwidth Pagination & `more` / `next`

A catalog answer is deliberately bite-sized. On a 1–5 kbps LoRa link a long
reply is not merely slow, it is *truncated*: the reply policy emits one NOTICE
per line and cuts anything past `max_reply_lines`. A page is therefore sized from
that same budget:

- **Page size follows the budget.** The bot reserves two of the `max_reply_lines`
  lines for the header and the footer, and puts at most four rows on a page
  (`max_reply_lines = 12` → 4 rows; `max_reply_lines = 4` → 2 rows;
  `max_reply_lines = 3` → 1 row). Raising the budget widens every page with no
  command change.
- **Every page says where it is.** The first line names the query and the
  position — `Tide stations in CA (Page 1 of 3):`.
- **Every page says how to continue.** The last line carries the exact command
  for the next page, and the `more` shortcut:
  `[Page 1 of 3: ask "tide list CA 2" or "more" for next]`. The last page ends
  with `[Page 3 of 3: end of results]`.
- **Pages are explicit and repeatable.** `<cmd> search <query> 2` and
  `<cmd> list CA 2` reach page 2 directly, so a page can be quoted, shared, and
  re-read. A page past the end answers `page 9 not found (total 3 pages)`
  instead of an empty reply.

### The `more` and `next` Shortcuts

For a one-to-one private session, `/msg gobot more` (or `/msg gobot next`)
answers the page after the one just sent, with no need to retype the query:

```text
/msg gobot tide list CA
  Tide stations in CA (Page 1 of 3):
    9410135: South San Diego Bay, CA
    9410660: Los Angeles (Outer Harbor), CA
    9410840: Santa Monica, Municipal Pier, CA
    9410678: Long Beach Fire Boat Pier, CA
  [Page 1 of 3: ask "tide list CA 2" or "more" for next]

/msg gobot more
  Tide stations in CA (Page 2 of 3):
    ...
  [Page 2 of 3: ask "tide list CA 3" or "more" for next]
```

- The pending page is remembered **per identity**, in memory only, for **five
  minutes** and for at most 256 requesters. Nothing is written to disk, a restart
  forgets every pager, and one asker's page is never handed to another.
- The cache holds the *command for the next page*, not the page itself, so a
  walk through a hundred rows costs a few bytes per asker and every page is
  rendered with its own correct footer.
- With nothing pending, or after the timeout, the answer is
  `no more pages or search expired`.
- `more` and `next` are exempt from the per-identity cooldown, because a page
  turn answers a page the asker was just told to ask for and can only produce
  offline catalog rows. Every other command keeps its cooldown.

### Clickable Micron Links (`micron_links`)

A NomadNet client renders Micron markup, so the bot can offer the same pages as
buttons instead of as commands to retype. Set `micron_links = true` in
`config.toml` to render each row as a link to the command that fetches it, and
the footer as a **Next Page** link:

```text
/msg gobot tide list OR
  Tide stations in OR (Page 1 of 1):
    ["9432845":/msg gobot tide 9432845]: Coos Bay, OR
    ["9435308":/msg gobot tide 9435308]: Weiser Point, Yaquina River, OR
    ["9439040":/msg gobot tide 9439040]: Astoria (Tongue Point), Oreg., OR
    ["9439221":/msg gobot tide 9439221]: Portland Morrison Street Bridge, OR
  [Page 1 of 1: end of results]
```

The option is **off by default**: an RRC NOTICE is plain text to every reader
except a NomadNet one, and a link that is not rendered is just noise. The links
address the nick the bot really answers to, so they work as written in the room
they were sent to.

---

## Cell & Radio Tower Finder (`tower`, `repeater`, `cell`)

`tower` answers the question that decides whether an off-grid party can
communicate at all: **what transmits near here, which way is it, and how far?**
The catalog, the geometry, and the coordinate conversion are all embedded in the
binary, so the answer is available with no network connection of any kind.

### Why it matters

1. **Aiming a directional antenna.** With a weak or unusable signal on a
   rubber-duck antenna, a Yagi, log-periodic, or grid antenna pointed at the
   right mast can hold an emergency uplink. What the operator needs is not a map
   but a **bearing and a distance** — which is exactly what `tower near` prints.
2. **Choosing a direction to walk.** A stranded driver or hiker who has lost
   coverage needs to know which way regains it, and how far the walk is.
3. **Amateur radio when the cellular network is gone.** Mountain-top VHF/UHF
   repeaters are frequently solar- and battery-backed and survive the event that
   took the commercial network down — but only if the operator knows the
   frequency, the offset, and the CTCSS/PL tone.
4. **Public safety and marine channels.** Search-and-rescue relays and coastal
   marine VHF stations are in the same catalog, so the same query reaches the
   people whose job the emergency is.

### Syntax

```text
tower near <place|coords|pluscode>
tower search <query> [page]
tower list [country|region] [page]
tower info <id>
more / next
```

`repeater`, `cell`, and `mast` are aliases for `tower` and behave identically.

### Examples

A proximity answer names the three closest sites, with the distance, the bearing
and compass point, the service, and the radio details needed to use the site:

```text
/msg gobot tower near 37.7553,-122.4527
  Tower sites near 37.7553,-122.4527 (Page 1 of 1):
    W6PW-2M (0.0 km 0° N) [RPT]: 145.150 MHz -0.6 (PL 114.8) - Sutro Tower, San Francisco, CA
    W6PW-70C (0.0 km 0° N) [RPT]: 442.700 MHz +5.0 (PL 114.8) - Sutro Tower 70cm, San Francisco, CA
    US-CA-T042 (0.0 km 218° SW) [CELL]: Band 2/4/12/71 - Sutro Cell Mast, San Francisco, CA
  [Page 1 of 1: end of results]
```

With a live compass heading and no typed argument, each row adds the **relative
steering instruction** that aims an antenna at the site without arithmetic — see
[Radio direction finding](#radio-direction-finding-aiming-an-antenna-in-one-step):

```text
/msg gobot tower near
    W6PW-2M (14.2 km 144° True SE) [RPT]: 145.150 MHz -0.6 (PL 114.8) - Sutro Tower, San Francisco, CA [Turn 15° RIGHT · 1 o'clock]
```

Search and list are offline lookups over the whole catalog, and both paginate to
the reply budget:

```text
/msg gobot tower search sutro
  Tower sites matching "sutro" (Page 1 of 1):
    W6PW-2M: Sutro Tower, San Francisco, CA
    W6PW-70C: Sutro Tower 70cm, San Francisco, CA
    US-CA-T042: Sutro Cell Mast, San Francisco, CA
  [Page 1 of 1: end of results]

/msg gobot tower list BJ
  Tower sites in BJ (Page 1 of 2):
    BJ-RPT-01: Xiangshan Relay, Beijing, BJ
    BJ-RPT-02: Miaofengshan Relay, Beijing, BJ
    BJ-RPT-03: Beijing Central Radio Tower, Beijing, BJ
    BJ-RPT-04: Wuling Mountain Relay, Beijing, BJ
  [Page 1 of 2: ask "tower list BJ 2" or "more" for next]
```

`tower info` renders one site in full — both datums, the Maidenhead grid an
operator signs with, the elevation, and every radio detail:

```text
/msg gobot tower info W6PW-2M
W6PW-2M [RPT]: Sutro Tower, San Francisco, CA
  WGS-84 37.755300, -122.452700 | Maidenhead CM87ss | elevation 254 m | Amateur repeater
  145.150 MHz, offset -0.6 MHz, tone 114.8 Hz, operator W6PW
```

### International parity: the United States and China

The catalog is deliberately balanced between the United States and China, with a
selection of major international hubs, because the same tool has to be as useful
on the Sichuan–Tibet highway as it is on a Colorado fourteener. Chinese entries
carry province codes and pinyin place names, so an operator who thinks in
English and an operator who knows the two-letter code both reach the same rows:

```text
/msg gobot tower search beijing
/msg gobot tower search sichuan
/msg gobot tower list GD
/msg gobot tower near 30.05,101.96
```

**WGS-84 ↔ GCJ-02 ("Mars coordinates") conversion.** China mandates that
domestic maps publish coordinates in GCJ-02, which is GPS with a deliberately
non-linear offset of several hundred meters. A coordinate copied out of Amap,
Gaode, Tencent Maps, or WeChat and fed to a GPS receiver lands in the wrong
street, and a GPS coordinate dropped into Amap lands several hundred meters
away. The bot handles both directions:

- A site **inside China** prints its GCJ-02 coordinate alongside the WGS-84 one,
  ready to paste into a Chinese map app:

  ```text
  /msg gobot tower near 39.9055,116.3976
    Tower sites near 39.9055,116.3976 (Page 1 of 1):
      CN-BJ-T001 (0.4 km 359° N) [CELL]: Band 3/8/41 - China Mobile Beijing Mast, Beijing, BJ, CN
      BJ-E001 (0.8 km 100° E) [EMERG]: 439.000 MHz -5.0 (PL 88.5) - Beijing Emergency Comms, Beijing, BJ, CN
      BJ-RPT-03 (6.1 km 76° ENE) [RPT]: 438.500 MHz -5.0 (PL 88.5) - Beijing Central Radio Tower, Beijing, BJ, CN
    GCJ-02 CN-BJ-T001 (paste into Amap/Gaode/WeChat): 39.910204, 116.403744
    [Page 1 of 1: end of results]
  ```

- A GCJ-02 coordinate is handed **back** to the bot with a `gcj:` (or `gcj02:`)
  prefix, anywhere a location is accepted, and is converted to the GPS position
  behind it first. Nothing outside China is ever moved: the conversion is
  applied only inside the mandated bounding box, so every foreign coordinate
  round-trips bit-for-bit.

  ```text
  /msg gobot tower near gcj:39.9069,116.4038
  /msg gobot loc gcj:39.9069,116.4038
  ```

  The inverse transform is recovered by fixed-point refinement of the published
  forward equations, converging to better than a centimeter.

### Region and country filters

`tower list` accepts a region code (`CA`, `CO`, `BJ`, `GD`, `SC`, `XJ`), a US
state's name (`california`), a country code (`US`, `CN`, `GB`, `JP`), or a
country's name (`china`, `germany`, `canada`).

> [!NOTE]
> A two-letter code that is a US state names the **state**, so `tower list CA`
> is California rather than Canada, and `tower list DE` is Delaware rather than
> Germany — those countries are reached by name (`tower list canada`,
> `tower list germany`). A Chinese province that shares a US state's code is
> still listed beside it, because the region itself matches: `tower list SC`
> covers South Carolina and Sichuan, and `tower list SD`, `NM`, and `HI`
> similarly cover both countries' regions of that name.

### Using a local dataset (`towers.csv`)

The curated catalog is a planning reference, not a live directory. An operator
who needs micro-cell density — for example an OpenCelliD extract reduced to the
area of operations — drops a CSV at the path configured as `towers_path`
(by default `towers.csv` beside `config.toml`) and every query sees it:

```csv
id,name,type,lat,lng,freq,offset,tone,operator,city,region,country,elev
W6PW-2M,Local Sutro Repeater,RPT,37.7600,-122.4500,146.100 MHz,-0.6 MHz,100.0 Hz,W6LOCAL,San Francisco,CA,US,250
ZZ-OP-0001,Ops Ridge Mast,CELL,39.0000,-120.0000,Band 2/4/12,,,OpsNet,Testville,CA,US,1200
```

- The column order is fixed: `id,name,type,lat,lng,freq,offset,tone,operator,city,region,country,elev`.
- `type` is `RPT`, `CELL`, `EMERG`, or `MAR` (the words `repeater`, `cellular`,
  `emergency`, and `maritime` are accepted too).
- `lat` and `lng` are **WGS-84** decimal degrees; the GCJ-02 conversion is
  applied on display.
- A row whose `id` matches an embedded one **replaces** it, so a corrected
  frequency is corrected everywhere at once; a new `id` is **added**.
- The header line is optional, `#` comments are ignored, and a malformed row is
  skipped with a log line naming it rather than silently shortening the file.
- **An absent file is normal**, and an unreadable one degrades to the embedded
  catalog: a bad export can never cost the tool its built-in data.

### Offline guarantee

Nothing in a `tower` query touches the network. There is no provider URL for
this command and no configuration it needs: the catalog is compiled into the
binary, the distance, bearing, and datum arithmetic are closed form, and the
only file it ever reads is an optional local dataset. It is the one discovery
command that is fully useful before its operator has configured anything at all.

---

## The Go Reticulum Lifesaver (GRL)

The **Go Reticulum Lifesaver (GRL)** is the role `gorrcbot` plays on a
pocket-sized, off-grid survival communicator: a device that replaces a
$300–$600 commercial satellite messenger and its monthly subscription with an
inexpensive, open-source node that works when every link has already failed.

It obeys one rule above all others, the **Autonomous Local Intelligence Rule**:

> Survival intelligence must run locally, in-process. Emergency medicine,
> nearby masts, the sun, and "where am I?" answer in microseconds with **0 radio
> hops, 0 airtime, and 0 RF emissions**.

Everything in this section therefore works with the radio switched off, with no
internet, and with no account.

### GNSS / GPS receiver integration

`gorrcbot` speaks **NMEA-0183** directly, with no Cgo driver and no third-party
library. It reads `$GNRMC` / `$GPRMC` (position, time, date, validity, speed,
course) and `$GNGGA` / `$GPGGA` (fix quality, satellites, HDOP, altitude,
geoidal separation), validates the XOR checksum of every sentence byte-for-byte,
and merges the rotation of sentences a receiver emits into one live fix. A
sentence that fails validation is dropped silently: a corrupted sentence is a
wrong position, and a wrong position is worse than no position at all.

Two `[bot]` keys configure the source:

```toml
# A streaming receiver, for example /dev/ttyUSB0 or /dev/ttyACM0.
# The port must already be configured for the receiver's line speed.
gps_port = ""

# A static position for a headless node, a fixed relay, or an operator
# rehearsing the field tools. Accepts every location notation below.
gps_fix = "37.7553,-122.4527"
```

With neither key set, the node simply has no position, and every command that
would use one says so instead of guessing. With `gps_port` set, the sentences
are parsed in the background as they arrive; with `gps_fix`, the position is
constant and no device is opened.

Every position-aware command shares **one** receiver, so the radio reply and the
web dashboard can never disagree about where the device is.

### Electronic compass, true north & direction finding

A GNSS receiver has one blind spot that matters most exactly when it hurts: it
derives **course over ground** from motion, so an operator who is standing
still — injured, pinned down by weather, lost in fog, or aiming a Yagi at a
mountain-top repeater — has **no heading at all**. The receiver reports the last
course it saw, noise, or nothing.

Pairing the node with an inexpensive 3-axis magnetometer (a QMC5883L or an
LSM303 on I2C, behind any serial bridge) removes that blind spot. `gorrcbot`
reads the standard marine/electronic heading sentences directly, with the same
pure-Go NMEA parser and the same byte-for-byte XOR checksum validation it uses
for the receiver:

| Sentence | Carries | Example |
|----------|---------|---------|
| `$HCHDG` | Magnetic heading, deviation, and variation | `$HCHDG,101.1,,,13.1,E*1B` → 101.1° magnetic, +13.1° east → **114.2° true** |
| `$HCHDM` | Magnetic heading only | `$HCHDM,101.1,M*28` |
| `$HCHDT` | True heading only | `$HCHDT,114.2,T*2F` |

A sentence that fails its checksum is dropped in silence, because a corrupted
heading is a **wrong direction**, and a wrong direction is worse than none.

```toml
# A streaming compass, for example /dev/ttyUSB1 or /dev/ttyACM1.
compass_port = ""

# Or a static magnetic heading for a node with no sensor.
compass_heading = "042"   # or "NE"
```

With neither key set, the node simply has no heading, and every answer that
would use one says so instead of guessing. With `compass_port` set, the
sentences are parsed in the background as they arrive; with `compass_heading`,
the bearing is constant and no device is opened.

**True north, automatically.** A magnetic compass points at magnetic north,
which is not the north a map, a bearing, or a rescue grid uses — the difference
ranges from a fraction of a degree to more than twenty depending on where the
operator stands, and thirteen degrees of error at fourteen kilometers is more
than three kilometers of error. `gorrcbot` therefore converts every magnetic
heading to true north with the **World Magnetic Model (WMM2025)**, evaluated in
pure Go from the published spherical-harmonic coefficient table: no data files,
no network, no third-party library. The model reproduces its own published test
vectors to the precision they are printed at, and its declination is checked
against the reference values for San Francisco, Denver, Boston, London, and
Tokyo.

Three sources of variation are used in that order of preference:

1. The **variation field of the sentence itself** (`$HCHDG`), which the
   instrument measured where it stands.
2. The **World Magnetic Model** at the node's live GNSS fix, which is what
   turns a `$HCHDM` magnetic heading into a true one with no operator input.
3. Nothing at all, when the node has neither — in which case the heading is
   reported as **magnetic** and labelled as such everywhere, never passed off as
   true north.

### `/whereami` — the operational location card

`whereami` (also typed `/whereami`) prints the card an operator reads before
moving, assembled entirely from the in-tree geodetic engines:

```text
/whereami
------------------------------------------------------------
Plus Code (OLC)   : 849VQG4W+4W (Area: ~14m x 14m)
Coordinates       : 37.75532° N, 122.45272° W
Maidenhead Grid   : CM87ss (Amateur Radio QTH)
Elevation         : 142 m (467 ft) MSL
Heading / Course  : 042° True (029° Mag, Var: +13.0° E) · NE
GNSS Fix Status   : 3D Fix (9 satellites, HDOP 0.8)
Local Solar Time  : 12:45 UTC-8 (Solar noon: 12:04)
Sunset Countdown  : Sunset at 18:15 (5h 29m daylight remaining)
------------------------------------------------------------
```

The **Heading / Course** line appears only when the device actually has a
heading, and only on the card about the device's own position — a heading
describes which way the operator is facing, so printing it beside a remote
coordinate would invite the reader to think the two belong together. The line
never claims more than it knows:

| State | Line |
|-------|------|
| Variation known (sentence or WMM) | `042° True (029° Mag, Var: +13.0° E) · NE` |
| True only (`$HCHDT`) | `042° True · NE` |
| Magnetic only (no position) | `029° Mag · NNE` |
| No compass at all | the line is omitted cleanly; the card is unchanged |

Every notation is there because somebody else needs it: a Plus Code is what a
dispatcher can paste into a map, a Maidenhead square is what an amateur operator
logs, decimal degrees are what a GPS unit takes, and — inside China — the
**GCJ-02** pair is the only form Amap, Gaode, Tencent Maps, and WeChat accept, so
the card adds that line for a position in China.

| Form | Example | Notes |
|------|---------|-------|
| No argument | `@gobot whereami` | Uses the live GNSS fix, or reports that it is acquiring one. |
| Coordinates | `@gobot whereami 37.7553,-122.4527` | Places a remote position; the card says `manual position` and shows no altitude, because no receiver reported one. |
| Plus Code | `@gobot whereami 849VQG4W+4W` | Any notation `loc` accepts works. |
| Maidenhead grid | `@gobot whereami CM87ss` | The square's own center. |

The **eleven-character** high-precision Plus Code (about 3 m by 3 m) is computed
alongside the ten-character code and is carried by the dashboard's JSON API
rather than printed on the card.

The **local solar time** is the zone the position's own longitude defines —
`round(longitude / 15)` hours — because a node with no timezone database cannot
honestly claim to know an operator's civil zone or whether summer time is in
force. Solar noon and the sunset countdown are computed against that same clock,
for the operator's **local calendar day**.

### Zero-argument automatic context injection

A person with cold hands, in the dark, does not want to type coordinates. When a
GNSS fix is live, the field commands inherit it automatically:

| Command | With a fix | Without a fix |
|---------|-----------|---------------|
| `tower near` | The three closest masts and repeaters to the operator's own position — and, with a live compass heading, the relative turn that aims an antenna at each one. | Asks for `<place\|coords\|pluscode>`. |
| `cell near`, `repeater near`, `mast near` | Same, for one service. | Same. |
| `tide near` | The three closest tide stations, offline. | Asks for a location. |
| `sun` | The almanac at the current position; `sun 2026-06-21` keeps the date and takes the position from the fix. | Asks for a location. |
| `sos` | Raises a **RED** beacon at the verified position. | Asks for `<loc> <TRIAGE> <details>`. |
| `sos RED two hikers, one leg fracture` | Raises the beacon at the fix with the triage and details given. | Same as above. |

A beacon raised from the fix attaches the receiver facts to the record and to
the reply — satellites, HDOP, altitude, fix quality, and the receiver's own
timestamp — so a rescue party reading the registry after the link has failed
knows not just **where** the beacon is but **how much the position can be
trusted**:

```text
[SOS #1 RECORDED] RED @ 849VQG4W+4W by @gobot: two hikers, one leg fracture | Alerted 2 rooms
GNSS fix: 3D fix, 9 satellites, HDOP 0.8, 142 m MSL, fix quality 1, 2026-09-16T20:45:33Z
```

A **typed location always wins**: `sos 39.7392,-104.9903 RED climber hurt` stays
about the climber in Denver, never about the device's own position. And with no
fix at all, the automatic fallback never fires, so a mistyped request can never
become a beacon somewhere arbitrary.

### Radio direction finding: aiming an antenna in one step

A bearing is a fact; a **turn is an instruction**. With a live heading, a
zero-argument `tower near` stops printing a bearing to interpret and starts
printing the turn to make:

```text
/msg gobot tower near
  Tower sites near 849VQG4W+4W (Page 1 of 1):
    W6PW-2M (14.2 km 144° True SE) [RPT]: 145.150 MHz -0.6 (PL 114.8) - Sutro Tower, San Francisco, CA [Turn 15° RIGHT · 1 o'clock]
    W6PW-70C (14.6 km 148° True SSE) [RPT]: 442.700 MHz +5.0 (PL 114.8) - Sutro Tower 70cm, San Francisco, CA [Turn 19° RIGHT · 2 o'clock]
    US-CA-T042 (15.1 km 139° True SE) [CELL]: Band 2/4/12/71 - Sutro Cell Mast, San Francisco, CA [Turn 10° RIGHT · 1 o'clock]
  [Page 1 of 1: end of results]
```

The instruction is a real clock face — **twelve straight ahead, three to the
right, six behind, nine to the left** — so it can be acted on in the dark
without arithmetic:

| Relative bearing | Instruction |
|------------------|-------------|
| within ±5° | `[Ahead · 12 o'clock]` |
| +5° to +165° | `[Turn X° RIGHT · N o'clock]` |
| −165° to −5° | `[Turn X° LEFT · N o'clock]` |
| beyond ±165° | `[Behind · 6 o'clock]` |

The steering appears only when it is meaningful, which is a deliberately narrow
condition: the answer must be about the **device's own position** (a live or
static fix, no typed argument) and the heading must be **true**, never magnetic.
A target bearing is a true bearing, so steering against an uncorrected magnetic
heading would send the operator off by the local variation. A named place, or a
node with no compass, gets exactly the distance-and-bearing answer it always
got.

### Captive portal & smartphone dashboard

Set one key and the device serves a survival dashboard to any phone that joins
its Wi-Fi:

```toml
# "127.0.0.1:8080" while testing, ":80" in the field. Empty binds nothing.
portal_addr = ":80"
```

Nothing is installed, because the page **is** the binary: one self-contained
HTML document with inline CSS and JavaScript and **zero external references** —
no stylesheet, font, image, or script from a network. A traveler's phone in
airplane mode opens it with the browser it already has.

The portal answers the captive-network probes the operating systems use, so the
phone's native captive browser opens the dashboard by itself instead of
reporting "connected, no internet":

| Probe | Operating system |
|-------|------------------|
| `/hotspot-detect.html` | Apple iOS and macOS |
| `/generate_204`, `/gen_204` | Android |
| `/ncsi.txt`, `/connecttest.txt` | Windows |

Each probe is answered with a `302` redirect to `/` and `Cache-Control:
no-store`.

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/` | `GET` | The dashboard: the large Plus Code with a **Copy Plus Code** button, coordinates, grid, elevation, fix status, the local solar clock, the sunset countdown, the **live compass rose**, the red **SOS** button (with a confirmation dialog), and the field-assistant chat box. |
| `/api/whereami` | `GET` | The current fix and its full geodetic synthesis as JSON: both Plus Codes, the grid, the coordinates, altitude, speed, course, satellites, HDOP, fix quality, the local solar clock, the sunset, the GCJ-02 pair inside China, the **live heading**, and the card's own lines. |
| `/api/compass` | `GET` | The live compass reading as JSON, with the nearest communications site the rose vectors toward: `{"valid": true, "mag_deg": 29, "true_deg": 42, "declination_deg": 13, "cardinal": "NE", "target": {"kind": "site", "id": "W6PW-2M", "name": "Sutro Tower", "bearing_deg": 144.4, "distance_km": 0.0, "steering": "[Turn 102° RIGHT · 3 o'clock]"}}`. It answers even with **no GNSS fix**, because a compass works standing still. |
| `/api/query` | `POST` | Runs one **local** command and returns its reply lines: `{"command": "med hypothermia"}` → `{"command": "med hypothermia", "lines": ["[HYPOTHERMIA] …"]}`. An addressed nick and a leading slash are both accepted, so `@gobot med hypothermia`, `/med hypothermia`, and `med hypothermia` are the same request. |

### The live compass rose

The dashboard carries a **north-up compass dial**: a blue needle at the device's
own heading and a red needle at the nearest repeater — or at an **active SOS
beacon**, which outranks every routine site, because an operator looking at the
phone is looking for a person — together with the digital true and magnetic
readouts, the local variation, and the target's distance, frequency, and
steering instruction:

```text
Compass & Direction Finding
        N
    W       E        042° NE
        S            True 042° (var +13.0° E)
                     Mag 029°
                     Sutro Tower · 14.2 km at 144° · 145.150 MHz -0.6 (PL 114.8) [Turn 102° RIGHT · 3 o'clock]
■ Your heading   ■ Beacon or nearest site
```

With an unresolved beacon in the registry the red needle and the vector line
switch to it instead:

```text
                     SOS RED beacon · 0.6 km at 088° [Turn 46° RIGHT · 2 o'clock]
```

The dial is drawn entirely with inline SVG and CSS — no image, no external font,
no script from a network — so it renders on a phone in airplane mode with the
browser it already has. It refreshes on the same ten-second cadence as the rest
of the page, and a node with no compass shows a sentence in place of the dial
rather than a needle pointing at nothing.

The chat box is restricted to the commands whose answers are computed
in-process — `med` (first aid), `tower near`, `tide near`, `sun`, `whereami`,
`morse`, `conv`, and the rest of the offline field set. A command that needs a
live radio link answers with a line saying exactly that, rather than failing
obscurely.

The **SOS** button asks for confirmation, then raises a RED beacon through the
same registry command the radio uses: the beacon is persisted, every room of
every live hub session is alerted, and the LXMF dispatch copy is queued when one
is configured. A device with no hub currently connected still records the beacon
and says that no room could be alerted.

---

## Complete Command Reference

### Core & Administration

| Command | Syntax | Description & Example |
|---------|--------|-----------------------|
| `help` | `@gobot help [command]` | Lists all available commands, or provides comprehensive help for a specific command (e.g. `@gobot help loc`). |
| `ping` | `@gobot ping` | Responds `pong` to verify link liveness and latency. |
| `uptime` | `@gobot uptime` | Reports bot uptime, hub connection duration, and hub identity hash. |
| `whoami` | `@gobot whoami` | Displays your nickname and full 32-character identity hash as seen by the current hub. |
| `botinfo` | `@gobot botinfo` | Details the bot's identity hash, version, connected hubs, and active rooms. |
| `rooms` | `@gobot rooms` | Lists all rooms the bot is currently participating in. |
| `members` | `@gobot members [room]` | Lists members reported by the hub in the specified room. |
| `seen` | `@gobot seen <nick\|hash>` | Shows the timestamp when a given nick or identity hash was last seen speaking in joined rooms. |
| `id` | `@gobot id` | Displays the bot's full identity hash and configured trigger nicknames. |
| `more` / `next` | `@gobot more` | Shows the next page of the last `search`, `near`, or `list` answer (see [Low-Bandwidth Pagination](#low-bandwidth-pagination-more-next)). The pending page is remembered per identity for 5 minutes; with nothing pending the answer is `no more pages or search expired`. |

---

### Private & Direct Messaging

| Command | Syntax | Description & Example |
|---------|--------|-----------------------|
| `dn` / `dnotice` | `@gobot dn <nick\|hash\|me> <text>` | Sends an encrypted direct notice (`K_DST`) to the specified client. |
| `dnoticecap` | `@gobot dnoticecap [target]` | Checks if the current hub supports direct notice delivery, and checks if a target user is online. |
| `dnoticeme` | `@gobot dnoticeme <text>` | Sends a direct notice to your own identity (tests private path functionality). |
| `msg` / `lxmf` | `@gobot msg <nick\|hash> <text>` | Queues an asynchronous LXMF message to an offline peer via propagation nodes. |

---

### Network & Mesh Diagnostics

| Command | Syntax | Description & Example |
|---------|--------|-----------------------|
| `path` | `@gobot path <nick\|hash>` | Queries the Reticulum routing table to report hops, next-hop interface, and path age to a peer. |
| `watch` | `@gobot watch <name\|hash> [ttl]` | Requests a direct notice when a peer, node, or hub announces on the network. |
| `unwatch` | `@gobot unwatch <n\|all>` | Cancels an active watch. |
| `watches` | `@gobot watches` | Lists your active announce watches and remaining TTLs. |
| `net` | `@gobot net [max_hops]` | Mesh directory: lists recently heard hubs, LXMF nodes, and NomadNet pages with hop counts. |
| `catchup` | `@gobot catchup [window]` | Delivers messages from joined rooms that were missed while you were offline. |
| `search` | `@gobot search <term> [#room]` | Searches recent room history for messages containing the given keyword. |

---

### Offline Geodesy & Navigation

All location commands accept **5 coordinate notations** without network connectivity:
1. **Open Location Code / Plus Codes**: `849VCWC8+R9`
2. **Decimal Degrees**: `37.42205, -122.08409` or `N37.42205 W122.08409`
3. **Degrees & Decimal Minutes (DDM)**: `37°25.323'N 122°05.048'W`
4. **Degrees, Minutes & Seconds (DMS)**: `37°25'19"N 122°05'03"W`
5. **Maidenhead Grid Locator**: `CM87uk`

A sixth input form is a **GCJ-02 "Mars coordinate"** copied from a Chinese map
app (Amap/Gaode, Tencent, WeChat), marked with a `gcj:` or `gcj02:` prefix:
`loc gcj:39.9069,116.4038` or `tower near gcj:39.9069,116.4038`. It is converted
back to the WGS-84 GPS position before anything else is computed. See
[Cell & Radio Tower Finder](#cell-radio-tower-finder-tower-repeater-cell).

| Command | Syntax | Description & Example |
|---------|--------|-----------------------|
| `loc` | `@gobot loc <location>` | Converts any supported coordinate format and outputs it in all five notations simultaneously. A position inside China also prints the GCJ-02 "Mars coordinate" that Amap and Gaode expect. |
| `dist` | `@gobot dist <from> <to>` | Calculates great-circle distance (km, statute miles, nautical miles) and forward/reverse bearings between two points. |
| `proj` | `@gobot proj <origin> <bearing°> <distance>` | Dead reckoning: calculates the destination coordinate from a starting location, course, and distance (e.g. `@gobot proj CM87uk 045 15km`). |
| `whereami` | `@gobot whereami [location]` | The operational location card: Plus Code, coordinates, Maidenhead grid, elevation, the **heading**, fix status, local solar time, and the sunset countdown. With no argument it uses the live GNSS fix. Also accepted as `/whereami`. See [The Go Reticulum Lifesaver](#the-go-reticulum-lifesaver-grl). |

---

### Celestial & Ephemeris

| Command | Syntax | Description & Example |
|---------|--------|-----------------------|
| `sun` | `@gobot sun [location] [date]` | Computes UTC sunrise, sunset, civil twilight dawn/dusk, and total daylight hours for any location on Earth. With no location it uses the live GNSS fix, and a bare date (`sun 2026-06-21`) keeps the date while taking the position from the fix. |
| `moon` | `@gobot moon [location] [date]` | Reports moon phase, illumination percentage, lunar age, moonrise/moonset, nighttime illumination rating, and upcoming spring/neap tides. |

---

### Emergency, Search & Rescue, and Field Operations

| Command | Syntax | Description & Example |
|---------|--------|-----------------------|
| `sos` | `@gobot sos [loc] [RED\|YELLOW\|GREEN\|INFO] [details]` | Broadcasts an emergency distress beacon across all rooms, confirms receipt to sender, persists beacon to disk, and dispatches via LXMF to emergency responders. With no location, and with a live GNSS fix, the beacon is raised at the operator's own position (RED by default) and the receiver facts are attached. |
| `sos list` | `@gobot sos list` | Lists all active distress beacons. |
| `sos clear` | `@gobot sos clear <id>` | Resolves and clears an SOS beacon (restricted to the original sender). |
| `checkin` | `@gobot checkin <loc> overdue <duration> <note>` | Arms an overdue dead-man timer (e.g. `overdue 4h`). If not cleared before expiry, the bot raises an automatic overdue alarm. |
| `checkin ok` | `@gobot checkin ok` | Clears your active overdue check-in timer. |
| `sitrep add` | `@gobot sitrep add <loc> <HAZARD\|RESOURCE\|SHELTER\|ROAD\|INFO> <text>` | Files a geolocated situation report to the 7-day tactical board. |
| `sitrep near` | `@gobot sitrep near <loc> [radius_km]` | Finds situation reports within the specified radius, sorted nearest-first. |

---

### Wilderness Medicine & Tactical Survival

| Command | Syntax | Description & Example |
|---------|--------|-----------------------|
| `firstaid` / `rx` / `med` | `@gobot firstaid <topic>` | Offline clinical decision-support cards for wilderness medicine: `bleed`, `cpr`, `triage`, `shock`, `hypo` (hypothermia), `heat`, `burns`, `water`, `snake`. `med` is the short name the field guides and the portal chat box use. |
| `coldwater` | `@gobot coldwater [temp]` | 1-10-1 cold water survival rule and swim failure timelines for water temperatures (e.g. `@gobot coldwater 48F`). |
| `signal` | `@gobot signal [air\|sound\|light]` | Distress signaling standards: ground-to-air visual markers (V, X, N, Y), whistle cadences, mirror/torch patterns. |
| `morse` | `@gobot morse <text>` / `morse -d <code...>` | Bidirectional Morse code encoder and decoder. |
| `conv` | `@gobot conv <val><unit> <target>` | Tactical unit conversions: barometric pressure (`29.92inHg` → `hPa`), distance, speed, fuel/water weight, and battery watt-hours (`5000mAh@3.7V` → `Wh`). |

---

### Marine, Aviation & Space Telemetry

| Command | Syntax | Description & Example |
|---------|--------|-----------------------|
| `weather` / `wx` | `@gobot weather <location>` | Live conditions from plain-text weather feed. |
| `tide` | `@gobot tide <station\|coords\|place> [date]` | 48-hour high/low water predictions, Rule of Twelfths hourly depth interpolation, and spring/neap tide classification. |
| `tide search` | `@gobot tide search <query> [page]` | Finds a station offline by name, state, or id, four per page. |
| `tide near` | `@gobot tide near [place\|coords\|pluscode]` | The three closest stations, with distance in nautical miles and bearing. With no argument it uses the live GNSS fix. |
| `tide list` | `@gobot tide list [state] [page]` | Every station, or one state's (`CA`, `OR`, `WA`, `AK`, `HI`, …). |
| `buoy` | `@gobot buoy <buoy_id>` | Real-time ocean buoy sea state: wave height, dominant wave period, swell vs chop classification, water temp, pressure trend. |
| `buoy search` | `@gobot buoy search <query> [page]` | Finds a buoy offline by place, id, region, or state. |
| `buoy near` | `@gobot buoy near <place\|coords\|pluscode>` | The three closest buoys, with distance in nautical miles and bearing. |
| `buoy list` | `@gobot buoy list [region\|state] [page]` | Every buoy, or one region's (`CA`, `HI`, `AK`, `GOM`, `ATL`, …). |
| `river` | `@gobot river <usgs_gauge_id>` | Stream gauge stage, discharge rate, 3-hour trend, and official NOAA river forecast flood categories. |
| `metar` | `@gobot metar <ICAO>` | Decodes raw aviation weather reports into wind, visibility, temperature, dewpoint, and altimeter setting in both units. |
| `metar search` | `@gobot metar search <city\|name\|code> [page]` | Finds an airfield offline by city, airport name, ICAO code, or IATA code (`denver`, `heathrow`, `KDEN`, `LHR`). |
| `metar near` | `@gobot metar near <place\|coords\|pluscode>` | The three closest airfields, with distance in nautical miles and bearing. |
| `metar list` | `@gobot metar list [state\|country] [page]` | Every airfield, or one state's or country's (`CO`, `CA`, `TX`, `GB`, `JP`, …). |
| `wxalert` | `@gobot wxalert <place\|zone>` | Queries active National Weather Service severe weather warnings and advisories. |
| `spacewx` / `solar` | `@gobot spacewx` | Reports Solar Flux Index (SFI), Sunspot Number (SSN), K-index, geomagnetic storm levels, and recommended HF propagation bands. |
| `launches` | `@gobot launches [upcoming\|past]` | Schedules and status of upcoming orbital space launches. |
| `flight` | `@gobot flight <flight_num>` | Real-time ADS-B flight telemetry: route, altitude, groundspeed, climb rate, and squawk code. |

---

### Cell & Radio Tower Finder

| Command | Syntax | Description & Example |
|---------|--------|-----------------------|
| `tower` / `repeater` / `cell` / `mast` | `@gobot tower near [place\|coords\|pluscode]` | The three closest communications sites, with the distance in kilometers, the bearing, the service, the frequency with its offset and tone, and the place. Entirely offline; a site inside China also prints the GCJ-02 coordinate for Amap/Gaode/WeChat. With no argument beyond `near` it uses the live GNSS fix, and with a live compass heading it adds the relative steering instruction that aims an antenna — `[Turn 15° RIGHT · 1 o'clock]`. See [Cell & Radio Tower Finder](#cell-radio-tower-finder-tower-repeater-cell) and [Radio direction finding](#radio-direction-finding-aiming-an-antenna-in-one-step). |
| `tower search` | `@gobot tower search <query> [page]` | Finds a site offline by callsign, identifier, name, city, pinyin place name, state or province, frequency, or operator (`sutro`, `beijing`, `sichuan`, `145.150`, `china mobile`). |
| `tower list` | `@gobot tower list [country\|region] [page]` | Every site, or one country's (`US`, `CN`, `GB`), one country's by name (`china`, `germany`), one US state's (`CA`, `CO`) or its name, or one Chinese province's (`BJ`, `GD`, `SC`, `XJ`). |
| `tower info` | `@gobot tower info <id>` | One site in full: the exact WGS-84 position, the GCJ-02 position when the site is in China, the Maidenhead grid, the elevation, the frequency, the offset, the tone, and the operator. |

---

## Deploying as a Systemd Service

To run `gorrcbot` as a persistent Linux service:

Create `/etc/systemd/system/gorrcbot.service`:

```ini
[Unit]
Description=Go Reticulum RRC Bot Daemon
After=network.target

[Service]
Type=simple
User=reticulum
Group=reticulum
ExecStart=/usr/local/bin/gorrcbot --bot-config /var/lib/gorrcbot/config.toml
Environment=GORRCBOT_HOME=/var/lib/gorrcbot
Restart=always
RestartSec=10
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

Enable and start the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now gorrcbot
sudo journalctl -u gorrcbot -f
```
