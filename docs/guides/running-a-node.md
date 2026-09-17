# Running a Reticulum Node: Multi-Daemon Orchestration

A complete Reticulum infrastructure node delivers multiple services simultaneously: packet routing across physical interfaces, store-and-forward message propagation, real-time group chat, automated field assistance, and distributed Micron page hosting.

This guide explains how to orchestrate the complete suite of pure-Go Reticulum daemons both interactively for testing and as persistent background services in production.

---

## The 5-Daemon Ecosystem

A full Reticulum node comprises five cooperating components:

```
+-----------------------------------------------------------------------------------+
|                               Physical Interfaces                                 |
|            (LoRa RNodes, TCP Server on :4242, UDP AutoInterface, Relays)          |
+-----------------------------------------+-----------------------------------------+
                                          |
                                 +--------v--------+
                                 |    gornsd -s    |  (Layer 3 Transport)
                                 +--------+--------+
                                          | Shared Instance IPC (@rns/default)
      +-------------------+---------------+-------------------+---------------------+
      |                   |                                   |                     |
+-----v-----+       +-----v-----+                       +-----v-----+         +-----v-----+
|  golxmd   |       |  gorrcd   |                       | gorrcbot  |         | gonomadnet|
| (LXMF PN) |       | (RRC Hub) |                       |(Assistant)|         |(Pages/TUI)|
+-----------+       +-----------+                       +-----------+         +-----------+
```

| Daemon | Role | Layer | Function |
|--------|------|-------|----------|
| [**gornsd**](../tools/cli-utilities.md) `-s` | Transport Daemon | Layer 3 | Owns all physical network interfaces (TCP server, AutoInterface, LoRa radios). Provides the shared IPC socket. |
| [**golxmd**](../tools/golxmd.md) `-p` | LXMF Propagation Node | Layer 4 | Caches, synchronizes, and delivers asynchronous encrypted messages for offline peers across the mesh. |
| [**gorrcd**](../tools/gorrcd.md) | Chat Hub Daemon | Layer 4 | Manages persistent Reticulum Relay Chat (RRC) rooms, channel history, and client notifications. |
| [**gorrcbot**](../tools/gorrcbot.md) | Autonomous Field Assistant | Layer 4 | Connects to the local hub, providing offline station databases, navigation tools, and telemetry to chat users. On a field node it is also the **Go Reticulum Lifesaver (GRL)**: it reads a GNSS receiver, answers `/whereami` locally, and serves the captive survival portal to any smartphone that joins the node's Wi-Fi. |
| **gonomadnet** | Micron Server & TUI | Layer 7 | Serves Micron markdown pages and file downloads over Reticulum, with an interactive terminal UI for the operator. |

---

## The Shared-Instance Architecture

In Reticulum, only **one process** may bind a given physical network interface, listen on port `4242`, or open a serial LoRa RNode device. If multiple independent Reticulum instances attempt to start on the same machine without coordination, whichever starts first claims the interfaces, while subsequent processes fail or fall back to unstable routing loops.

To solve this, Reticulum uses a **Shared Instance** architecture:

1. **`gornsd -s` starts first:** It substantiates all interface drivers defined in `~/.reticulum/config` and creates a local IPC socket (`@rns/default` on Linux, or local domain socket / TCP loopback on macOS).
2. **Client daemons attach:** When `golxmd`, `gorrcd`, `gorrcbot`, or `gonomadnet` start up with `share_instance = yes` (the default), they detect the active shared socket and route all packets through `gornsd`.
3. **No contention:** Every daemon gains full access to all radios, TCP links, and neighbor announcements through a single, stable transport layer.

---

## Interactive Local Node: `run-node-stack.sh`

For development, testing, or field laptops, `go-nomadnet` includes a one-stop orchestration script:

```bash
# In the go-nomadnet repository:
./scripts/run-node-stack.sh
```

### What it does:
1. Stops any stale or orphaned stack processes.
2. Compiles the latest tools with WebAssembly plugin support (`-tags=wago`).
3. Launches `gornsd -s` and waits for the shared-instance IPC socket to appear.
4. Starts `golxmd -p` (LXMF propagation node).
5. Starts `gorrcd` (chat hub) and `gorrcbot` (chat assistant).
6. Hands the terminal foreground to the `gonomadnet` interactive TUI.

### Script Options:
- `--headless`: Start `gonomadnet` as a background daemon (`-d`) instead of launching the TUI.
- `--no-build`: Skip re-compiling and use existing binaries on `$PATH`.
- `-h, --help`: Display usage summary.

To cleanly shut down all background daemons started by the script:
```bash
pkill -x gornsd gorrcd gorrcbot golxmd gonomadnet
```

---

## Production 24/7 Deployment with systemd

For permanent infrastructure servers, manage each component as an independent systemd service unit.

### 1. `gornsd.service` (Base Transport)

`/etc/systemd/system/gornsd.service`:
```ini
[Unit]
Description=Go Reticulum Shared Transport Daemon
After=network.target

[Service]
Type=simple
User=reticulum
ExecStart=/usr/local/bin/gornsd -s -v
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

### 2. `golxmd.service` (LXMF Propagation)

`/etc/systemd/system/golxmd.service`:
```ini
[Unit]
Description=Go LXMF Propagation Node Daemon
After=gornsd.service
Requires=gornsd.service

[Service]
Type=simple
User=reticulum
ExecStart=/usr/local/bin/golxmd -p -s
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

### 3. `gorrcd.service` (RRC Chat Hub)

`/etc/systemd/system/gorrcd.service`:
```ini
[Unit]
Description=Go Reticulum Relay Chat Hub
After=gornsd.service
Requires=gornsd.service

[Service]
Type=simple
User=reticulum
ExecStart=/usr/local/bin/gorrcd
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### 4. `gorrcbot.service` (Chat Field Assistant)

`/etc/systemd/system/gorrcbot.service`:
```ini
[Unit]
Description=Go RRC Chat Bot & Field Assistant
After=gorrcd.service
Wants=gorrcd.service

[Service]
Type=simple
User=reticulum
ExecStart=/usr/local/bin/gorrcbot
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

### 5. `gonomadnet.service` (Nomad Network Node)

`/etc/systemd/system/gonomadnet.service`:
```ini
[Unit]
Description=Go Nomad Network Node Daemon
After=gornsd.service
Requires=gornsd.service

[Service]
Type=simple
User=reticulum
ExecStart=/usr/local/bin/gonomadnet -d
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start the entire stack:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now gornsd golxmd gorrcd gorrcbot gonomadnet
```

---

## Verifying Stack Health

Once running, verify node status across each layer:

### 1. Transport & Interfaces
```bash
gornstatus
```
Confirm `Shared Instance[0] is running` and that your physical interfaces report `Status: Up`.

### 2. LXMF Message Propagation
```bash
# Check propagation node status
golxmd --status

# View peered propagation nodes
golxmd --peers
```

### 3. Network Path Discovery
```bash
# Query path to a destination
gornpath <destination_hash>

# Ping a destination over the mesh
gornprobe <destination_hash>
```

### 4. Live Service Logs
```bash
journalctl -u gornsd -u golxmd -u gorrcd -f
```

---

## The Field Node: The Go Reticulum Lifesaver (GRL)

On a portable or vehicle node, `gorrcbot` takes on a second role: it is the
**Go Reticulum Lifesaver**, the survival communicator described in
[gorrcbot — The Go Reticulum Lifesaver](../tools/gorrcbot.md#the-go-reticulum-lifesaver-grl).
Three settings turn it on, and all are optional:

```toml
[bot]
# A GNSS receiver streaming NMEA-0183 sentences (or a static position)
gps_port = "/dev/ttyACM0"

# An electronic compass streaming heading sentences (or a static heading):
# the heading a GNSS receiver cannot give while the operator stands still
compass_port = "/dev/ttyACM1"

# The captive survival portal: a phone that joins this node's Wi-Fi opens it
portal_addr = ":80"
```

What that buys a field node:

- **A position the node knows without a network.** `/whereami` prints the Plus
  Code, both coordinates, the Maidenhead grid, the altitude, the heading, the fix
  quality, the local solar time, and the daylight remaining — all computed
  in-process.
- **One-word questions.** `tower near`, `tide near`, and `sun` inherit the live
  fix, so nothing has to be typed with cold hands. `/sos` raises a RED beacon at
  the verified position and attaches the receiver facts to it.
- **A heading while standing still.** A magnetometer supplies the orientation a
  receiver cannot, the World Magnetic Model converts it to true north from the
  node's own position, and `tower near` then prints the turn that aims a
  directional antenna — `[Turn 15° RIGHT · 1 o'clock]` — instead of a bearing to
  interpret.
- **A dashboard with no app.** A traveler's phone in airplane mode that joins the
  node's Wi-Fi has the survival dashboard opened for it by the operating
  system's captive-network probe, with the Plus Code, a copy button, a live
  compass rose pointing at the nearest repeater, the SOS button, and the offline
  field assistant.
- **The same answers on both paths.** The portal and the radio commands share one
  command registry restricted to its offline commands, and one GNSS receiver, so
  the two can never disagree.

The portal binds an HTTP listener, so put it on the field node's own access point
(and behind the node's firewall on a shared network), or leave `portal_addr`
empty to bind nothing at all.
