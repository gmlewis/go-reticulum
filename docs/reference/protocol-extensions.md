# Reticulum Protocol & Micron Markup Extensions

This document defines extensions introduced by the Go Reticulum and Go NomadNet projects to the RRC (Reticulum Relay Chat) protocol and the Micron page markup language.

All extensions are designed with **strict backward compatibility**:
- Systems that support the extensions gain new capabilities.
- Legacy clients and hubs gracefully ignore or cleanly degrade without errors or dropped packets.
- Complete Python reference implementation guides are provided so that authors of `rrcd` and `nomadnet` can easily back-port these features.

---

## 1. RRC Capability: `CAP_PRIVATE_COMMAND = 3`

### 1.1 Overview & Motivation

In standard RRC (`rrcd`), client slash commands (`/who`, `/list`, `/stats`, `/reload`, `/dnotice <nick> <text>`) could only be issued within an active chat room. While the hub intercepts slash commands before room fanout, this design had significant drawbacks:
1. A client had to join a room before issuing administrative or query commands.
2. In buggy or third-party client implementations, private slash commands could accidentally leak into public room history.
3. Sending a direct notice (`K_DST`) to another client required knowing their raw 16-byte cryptographic destination hash ahead of time. A client could not send a direct message simply by knowing a user's nickname unless the hub resolved it.

`CAP_PRIVATE_COMMAND = 3` introduces a private out-of-band command channel between a client and an RRC hub using direct notices (`T_NOTICE`, type 21) addressed to the hub's own identity.

### 1.2 Capability Advertisement

In the RRC `WELCOME` envelope body key 2 (`BWelcomeCaps`, CBOR map of integer capability IDs to booleans):

```text
Standard RRC Hub Capabilities:
  0: CAP_RESOURCE_ENVELOPE (Resource envelope transfer)
  1: CAP_ACTION            (IRC-style /me actions)
  2: CAP_DIRECT_NOTICE     (Client-to-client direct notices via K_DST)

Extension:
  3: CAP_PRIVATE_COMMAND   (Hub accepts direct notices as private commands)
```

When enabled on the hub, the capabilities map sent in `WELCOME` contains:

```python
{0: True, 1: True, 2: True, 3: True}
```

Clients MUST check `HasCapability(3)` before attempting to send private commands to a hub. If a hub does not advertise capability 3, direct notices addressed to the hub identity will be rejected as "destination not connected".

### 1.3 Wire Protocol & Message Flow

```
+--------+                                                 +--------+
| Client |                                                 |  Hub   |
+--------+                                                 +--------+
    |                                                           |
    | 1. Connect & WELCOME (captures hub identity hash)         |
    |<----------------------------------------------------------|
    |    WELCOME carries Caps: {..., 3: True}                   |
    |                                                           |
    | 2. Direct Notice to Hub Identity:                         |
    |    T_NOTICE (21)                                          |
    |    KSrc  = <client_identity_hash>                         |
    |    KDst  = <hub_identity_hash>                            |
    |    KRoom = nil                                            |
    |    KBody = "/dnotice alice Meeting at 18:00 UTC"          |
    |---------------------------------------------------------->|
    |                                                           | [Hub resolves "alice"]
    |                                                           | [Routes to Alice's link]
    | 3. Direct Notice Reply from Hub:                          |
    |    T_NOTICE (21)                                          |
    |    KSrc  = <hub_identity_hash>                            |
    |    KDst  = <client_identity_hash>                         |
    |    KBody = "Direct notice delivered to alice"             |
    |<----------------------------------------------------------|
```

#### Envelope Specification

When transmitting a private command, the client constructs a standard RRC `T_NOTICE` (21) envelope:

| Key | CBOR Key Index | Type | Value / Description |
|-----|----------------|------|---------------------|
| `KSrc` | `0` | `bytes` | Sender's 16-byte Reticulum identity hash. |
| `KDst` | `1` | `bytes` | **Hub's 16-byte identity hash** (extracted from `WELCOME` source). |
| `KRoom` | `2` | `nil` | **Must be omitted or nil**. Direct notices never specify a room. |
| `KNick` | `3` | `text` | Sender's nickname (optional; normalized by hub). |
| `KBody` | `4` | `text` | Command string starting with `/` (e.g. `/dnotice <nick> <text>`, `/who`, `/stats`). |
| `KMsgID`| `6` | `bytes` | 8-byte unique random message ID. |
| `KTs` | `7` | `int` | Millisecond Unix timestamp. |

#### Hub Processing Rules

1. When a hub receives a `T_NOTICE` envelope with `KDst` matching `hub_identity_hash`:
   - If `enable_private_commands` is false or capability 3 is not enabled, return an error notice: `"unrecognized destination"`.
   - If `KRoom` is present, reject with error notice: `"direct notice must not include room"`.
   - If `KBody` is a text string starting with `/`:
     - Dispatch `KBody` to the hub's operator command handler (`HandleOperatorCommand`).
     - Route command output directly back to the sender's link via direct notice (`KDst = client_hash`).
     - **Neither the command line nor any reply is ever posted to any room**.
   - If `KBody` does not start with `/` or is unrecognized, return an error notice directly to the sender.

### 1.4 Python `rrcd` & `RRC.py` Reference Back-Port

#### In `rrcd` / Hub Daemon (`RRC.py`):

```python
# 1. Define the capability constant:
CAP_PRIVATE_COMMAND = 3

# 2. In Hub configuration (config.py / __init__):
self.enable_private_commands = True  # or read from config: config.get("enable_private_commands", True)

# 3. In Hub's WELCOME envelope generator:
caps = {
    CAP_RESOURCE_ENVELOPE: True,
    CAP_ACTION: True,
    CAP_DIRECT_NOTICE: True,
}
if self.enable_private_commands:
    caps[CAP_PRIVATE_COMMAND] = True
welcome_envelope[B_WELCOME_CAPS] = caps

# 4. In Hub's _handle_direct_notice method:
def _handle_direct_notice(self, link, env):
    dst = env.get(K_DST)
    if dst == self.identity.hash:
        if self.enable_private_commands:
            body = env.get(K_BODY)
            if isinstance(body, str) and body.strip().startswith("/"):
                # Handle command directly on link without room context:
                handled = self.handle_operator_command(link, env.get(K_SRC), None, body.strip())
                if not handled:
                    self.send_direct_notice(link, "Unrecognized command")
                return
        self.send_error(link, "destination not connected")
        return
    # ... regular peer-to-peer direct notice forwarding ...
```

#### In Client (`RRC.py` / `Channels.py`):

```python
def send_private_command(self, text):
    """Sends a private slash command to the hub without entering any room."""
    if not self.has_capability(CAP_PRIVATE_COMMAND):
        raise RuntimeError("Hub does not support CAP_PRIVATE_COMMAND")
    if not self.hub_identity:
        raise RuntimeError("Hub identity unknown; wait for WELCOME")
    
    # Construct direct notice envelope addressed to hub's identity hash
    env = {
        K_TYPE: T_NOTICE,
        K_SRC: self.identity.hash,
        K_DST: self.hub_identity,
        K_NICK: self.effective_nick,
        K_BODY: text,
        K_MSGID: os.urandom(8),
        K_TS: int(time.time() * 1000)
    }
    self.send_envelope(env)
```

---

## 2. Micron Extension: `` `T `` (Timestamp Localization)

### 2.1 Overview & Motivation

Micron pages travel over Reticulum across global timezones. A static timestamp written into a page (e.g. `Updated: Sep 15 20:00 UTC` or `Meeting at 14:00 EST`) forces every reader to calculate timezone offsets manually.

The `` `T `` extension allows pages to embed epoch timestamps in UTC seconds along with a standard `strftime` formatting template. The reader's NomadNet client renders the timestamp in the reader's own local timezone.

### 2.2 Syntax & Grammar

```text
`T<unix-seconds>`T
`T<unix-seconds>|<strftime-format>`T
```

- **Opening & Closing Delimiters**: `` `T `` (backtick followed by capital `T`).
- **`<unix-seconds>`**: Base-10 integer Unix epoch seconds (UTC).
- **`<strftime-format>`**: Optional POSIX `strftime` format string. If omitted, defaults to:
  `"%a %b %d, %Y %-I:%M:%S%p %Z"` (e.g. `Tue Sep 15, 2026 8:46:25PM EDT`).

#### Grammar (ABNF)

```abnf
timestamp-directive = "`T" unix-seconds [ "|" strftime-format ] "`T"
unix-seconds        = 1*12( %x30-39 )       ; 1 to 12 digits
strftime-format     = 1*64( VCHAR / WSP )   ; format tokens
```

### 2.3 Supported `strftime` Tokens

| Token | Meaning | Example Output |
|-------|---------|----------------|
| `%Y` | 4-digit year | `2026` |
| `%y` | 2-digit zero-padded year (00-99) | `26` |
| `%m` | 2-digit zero-padded month (01-12) | `09` |
| `%b`, `%h` | Abbreviated month name | `Sep` |
| `%B` | Full month name | `September` |
| `%d` | 2-digit zero-padded day of month (01-31) | `15` |
| `%e` | 2-character space-padded day of month ( 1-31) | ` 5` |
| `%H` | 2-digit 24-hour clock hour (00-23) | `20` |
| `%I` | 2-digit 12-hour clock hour (01-12) | `08` |
| `%M` | 2-digit zero-padded minute (00-59) | `46` |
| `%S` | 2-digit zero-padded second (00-59) | `25` |
| `%p` | AM or PM indicator | `PM` |
| `%a` | Abbreviated weekday name | `Tue` |
| `%A` | Full weekday name | `Tuesday` |
| `%j` | 3-digit day of the year (001-366) | `258` |
| `%z` | UTC offset (`+HHMM` or `-HHMM`) | `-0400` |
| `%Z` | Timezone abbreviation | `EDT` |
| `%s` | Unix epoch seconds | `1789519585` |
| `%F` | ISO 8601 date (`%Y-%m-%d`) | `2026-09-15` |
| `%T` | 24-hour time (`%H:%M:%S`) | `20:46:25` |
| `%R` | 24-hour time without seconds (`%H:%M`) | `20:46` |
| `%D` | Date (`%m/%d/%y`) | `09/15/26` |
| `%%` | Literal percent sign | `%` |
| `%-` | Modifier: strips leading padding (e.g. `%-I`, `%-d`, `%-m`) | `8` (instead of `08`) |

### 2.4 Backward Compatibility & Degradation

- Clients with no `` `T `` support strip the delimiters and display the raw payload (e.g. `1789519585|%F %T`), ensuring no text is lost.
- Malformed inputs (non-integer seconds) fall back to displaying the raw text verbatim without crashing the parser.

### 2.5 Python `nomadnet` Reference Back-Port

In `nomadnet/ui/textui/MicronParser.py`:

```python
import datetime
import re

TIMESTAMP_PATTERN = re.compile(r'`T(\d+)(?:\|([^`]*))?`T')
DEFAULT_TIME_FORMAT = "%a %b %d, %Y %-I:%M:%S%p %Z"

def expand_timestamps(text):
    def _replace(match):
        secs = int(match.group(1))
        fmt = match.group(2) if match.group(2) else DEFAULT_TIME_FORMAT
        # Convert UTC seconds to local time
        dt = datetime.datetime.fromtimestamp(secs).astimezone()
        try:
            return dt.strftime(fmt)
        except Exception:
            return dt.isoformat()
    return TIMESTAMP_PATTERN.sub(_replace, text)
```

---

## 3. Micron Extension: `` `L `` (Location / Plus Code Offline Geo-Rendering)

### 3.1 Overview & Motivation

NomadNet pages frequently display coordinates for radio repeaters, emergency shelters, water sources, or trailheads. However, bare geographic coordinates or raw Open Location Codes (Plus Codes) fail to answer a traveler's most critical questions:
- *How far away is it?*
- *In what direction should I go?*

The `` `L `` extension renders an Open Location Code, computes the great-circle distance and 16-point compass bearing from the client's current location, and presents rich guidance offline using zero internet connection.

### 3.2 Syntax & Grammar

```text
`L<code>`L
`L<code>|<format>`L
```

- **Opening & Closing Delimiters**: `` `L `` (backtick followed by capital `L`).
- **`<code>`**: A canonical 8- to 11-character Open Location Code (Plus Code) with `+` separator (e.g. `849VCWC8+R9` or `8FW4V75V+8R`).
- **`<format>`**: One of five format tokens (`%default`, `%c`, `%d`, `%b`, `%ll`). Defaults to `%default` if omitted.

#### Grammar (ABNF)

```abnf
location-directive = "`L" code [ "|" format ] "`L"
code               = 8*15( olc-char )
olc-char           = %x30-39 / %x41-5A   ; 0-9, A-Z (excluding I, L, O, U)
format             = "%default" / "%c" / "%d" / "%b" / "%ll"
```

### 3.3 Format Tokens & Rendering

| Token | Description | Rendered Example |
|-------|-------------|------------------|
| *(none)*, `%default` | Code, followed by distance and bearing when client position is known. | `849VCWC8+R9 (3.2 km, bearing 048° NE)` |
| `%c` | Bare Plus Code alone. | `849VCWC8+R9` |
| `%d` | Distance alone when position is known. Empty string if unknown. | `3.2 km` |
| `%b` | 3-digit bearing and 16-point compass name. Empty string if unknown. | `048° NE` |
| `%ll` | Decimal degrees latitude/longitude. Always computes (needs no client fix). | `37.422063, -122.084062` |

### 3.4 Geodetic Formulas & Units

#### 1. Coordinate Decoding
The code is decoded to its bounding box centroid $(\phi_2, \lambda_2)$ in decimal degrees and converted to radians.

#### 2. Great-Circle Distance (Haversine Formula)
Using the standard mean spherical Earth radius $R = 6,371,000\text{ meters}$:

$$\Delta\phi = \phi_2 - \phi_1, \quad \Delta\lambda = \lambda_2 - \lambda_1$$

$$a = \sin^2\left(\frac{\Delta\phi}{2}\right) + \cos(\phi_1)\cos(\phi_2)\sin^2\left(\frac{\Delta\lambda}{2}\right)$$

$$c = 2 \cdot \operatorname{atan2}\left(\sqrt{a}, \sqrt{1-a}\right)$$

$$d = R \cdot c$$

#### 3. Initial Forward Bearing (Azimuth)

$$y = \sin(\Delta\lambda)\cos(\phi_2)$$

$$x = \cos(\phi_1)\sin(\phi_2) - \sin(\phi_1)\cos(\phi_2)\cos(\Delta\lambda)$$

$$\theta = \left(\operatorname{atan2}(y, x) \cdot \frac{180}{\pi} + 360\right) \pmod{360}$$

#### 4. 16-Point Compass Rose Mapping

The bearing $\theta$ is mapped to a 16-point compass name using $22.5^\circ$ sectors:

$$\text{index} = \left\lfloor \frac{\theta + 11.25}{22.5} \right\rfloor \pmod{16}$$

Names: `N`, `NNE`, `NE`, `ENE`, `E`, `ESE`, `SE`, `SSE`, `S`, `SSW`, `SW`, `WSW`, `W`, `WNW`, `NW`, `NNW`.

#### 5. Distance Formatting Rules
- Below $1,000\text{ m}$: Integer meters with space (`850 m`).
- Between $1,000\text{ m}$ and $1,000\text{ km}$: Kilometers with one decimal place (`3.2 km`).
- At or above $1,000\text{ km}$: Integer kilometers (`8967 km`).

### 3.5 Certified Reference Test Vectors

Use these reference values to verify new implementations:

| Reader Position | Plus Code Target | Target Centroid | Distance | Bearing | Rendered Output |
|-----------------|------------------|-----------------|----------|---------|-----------------|
| 37.4220°N, 122.0841°W (Mountain View) | `8FW4V75V+8R` (Eiffel Tower) | 48.858312°N, 2.294563°E | 8,967,033 m | 33.39° | `8FW4V75V+8R (8967 km, bearing 033° NE)` |
| 51.5000°N, 0.1200°W (London) | `8FW4V75V+8R` (Eiffel Tower) | 48.858312°N, 2.294563°E | 340,318 m | 148.72° | `8FW4V75V+8R (340.3 km, bearing 149° SSE)` |
| -33.8568°S, 151.2153°E (Sydney) | `849VCWC8+R9` (Googleplex) | 37.422063°N, 122.084063°W | 11,952,709 m | 56.23° | `849VCWC8+R9 (11953 km, bearing 056° NE)` |

### 3.6 Python `nomadnet` Reference Back-Port

In `nomadnet/ui/textui/MicronParser.py`:

```python
import math
import re
from openlocationcode import openlocationcode

LOCATION_PATTERN = re.compile(r'`L([A-Z0-9+]{8,15})(?:\|(%[a-z]+))?`L')
EARTH_RADIUS_M = 6371000.0

COMPASS_POINTS = [
    "N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE",
    "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"
]

def format_distance(meters):
    if meters < 1000:
        return f"{int(round(meters))} m"
    km = meters / 1000.0
    if km < 1000:
        return f"{km:.1f} km"
    return f"{int(round(km))} km"

def calculate_geodesy(lat1, lon1, lat2, lon2):
    phi1, phi2 = math.radians(lat1), math.radians(lat2)
    dphi = math.radians(lat2 - lat1)
    dlam = math.radians(lon2 - lon1)
    
    a = math.sin(dphi/2)**2 + math.cos(phi1)*math.cos(phi2)*math.sin(dlam/2)**2
    c = 2 * math.atan2(math.sqrt(a), math.sqrt(1-a))
    dist = EARTH_RADIUS_M * c
    
    y = math.sin(dlam) * math.cos(phi2)
    x = math.cos(phi1)*math.sin(phi2) - math.sin(phi1)*math.cos(phi2)*math.cos(dlam)
    bearing = (math.degrees(math.atan2(y, x)) + 360.0) % 360.0
    
    sector = int(math.floor((bearing + 11.25) / 22.5)) % 16
    compass = COMPASS_POINTS[sector]
    return dist, bearing, compass

def expand_locations(text, client_lat=None, client_lon=None):
    def _replace(match):
        code = match.group(1)
        fmt = match.group(2) or "%default"
        try:
            area = openlocationcode.decode(code)
            lat2, lon2 = area.latitudeCenter, area.longitudeCenter
        except Exception:
            return code  # Malformed code falls back to raw text
        
        if fmt == "%ll":
            return f"{lat2:.6f}, {lon2:.6f}"
        if fmt == "%c":
            return code
            
        if client_lat is None or client_lon is None:
            return code if fmt == "%default" else ""
            
        dist, bearing, compass = calculate_geodesy(client_lat, client_lon, lat2, lon2)
        dist_str = format_distance(dist)
        bearing_str = f"{int(round(bearing)):03d}° {compass}"
        
        if fmt == "%d":
            return dist_str
        if fmt == "%b":
            return bearing_str
        # Default:
        return f"{code} ({dist_str}, bearing {bearing_str})"
        
    return LOCATION_PATTERN.sub(_replace, text)
```
