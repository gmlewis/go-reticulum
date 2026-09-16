# gorrcbot — Autonomous RRC Bot & Field Assistant

`gorrcbot` is a headless, always-on RRC (Reticulum Relay Chat) bot and autonomous off-grid assistant. It operates as a **client, not a hub**: it connects to one or more RRC hubs simultaneously, joins configured rooms, maintains connectivity across link drops, and serves requests addressed to its nick or cryptographic identity hash.

---

## Architecture & Principles

- **Single Identity Everywhere**: `gorrcbot` generates one 64-byte Reticulum private identity (`bot_identity`) and uses it across all connected hubs. Its identity hash is identical on all hubs, allowing users to reach it using consistent address prefixes.
- **Strict Addressing Contract**: The bot is completely silent unless addressed directly. It will never spam channels, and it completely ignores chat traffic not directed at it.
- **Rate-Limiting & Cooldowns**: Built-in per-identity cooldown prevents abuse or channel flooding over low-bandwidth LoRa links.
- **Offline First**: The vast majority of tactical and field assistant commands (Plus Codes, geodesy, dead reckoning, sun/moon ephemeris, wilderness medicine cards, Morse code, unit conversions) compute entirely in-process with **zero internet connection required**. The marine, aviation, and navigation station catalogs are embedded too, so `search`, `near`, and `list` find a station id before any provider is configured and while a provider is unreachable.
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

# LXMF messaging support (msg/lxmf command)
lxmf_enabled = false
lxmf_propagation_node = ""
lxmf_announce_minutes = 360

# Optional 32-hex LXMF destination hash for emergency SOS beacons
emergency_lxmf_destination = ""

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

| Command | Syntax | Description & Example |
|---------|--------|-----------------------|
| `loc` | `@gobot loc <location>` | Converts any supported coordinate format and outputs it in all five notations simultaneously. |
| `dist` | `@gobot dist <from> <to>` | Calculates great-circle distance (km, statute miles, nautical miles) and forward/reverse bearings between two points. |
| `proj` | `@gobot proj <origin> <bearing°> <distance>` | Dead reckoning: calculates the destination coordinate from a starting location, course, and distance (e.g. `@gobot proj CM87uk 045 15km`). |

---

### Celestial & Ephemeris

| Command | Syntax | Description & Example |
|---------|--------|-----------------------|
| `sun` | `@gobot sun <location> [date]` | Computes UTC sunrise, sunset, civil twilight dawn/dusk, and total daylight hours for any location on Earth. |
| `moon` | `@gobot moon [location] [date]` | Reports moon phase, illumination percentage, lunar age, moonrise/moonset, nighttime illumination rating, and upcoming spring/neap tides. |

---

### Emergency, Search & Rescue, and Field Operations

| Command | Syntax | Description & Example |
|---------|--------|-----------------------|
| `sos` | `@gobot sos <loc> <RED\|YELLOW\|GREEN\|INFO> <details>` | Broadcasts an emergency distress beacon across all rooms, confirms receipt to sender, persists beacon to disk, and dispatches via LXMF to emergency responders. |
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
| `firstaid` / `rx` | `@gobot firstaid <topic>` | Offline clinical decision-support cards for wilderness medicine: `bleed`, `cpr`, `triage`, `shock`, `hypo`, `heat`, `burns`, `water`, `snake`. |
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
| `tide near` | `@gobot tide near <place\|coords\|pluscode>` | The three closest stations, with distance in nautical miles and bearing. |
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
