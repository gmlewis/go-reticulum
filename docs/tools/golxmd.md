# golxmd — LXMF Propagation & Routing Daemon

`golxmd` is a standalone LXMF (Lightweight Extensible Messaging Format) routing daemon and store-and-forward message propagation node written in 100% Go.

It acts as an autonomous relay and cache for the Reticulum network, holding encrypted messages for offline recipients and peering with other propagation nodes to distribute messages across mesh and internet links. It is 100% wire-compatible with Python `lxmd` and clients such as **NomadNet**, **Sideband**, and **MeshChat**.

---

## Key Features

- **Store-and-Forward Propagation (`-p`)**: Caches inbound encrypted LXMF messages until recipients come online and pull their messages.
- **Autonomous Peering & Sync**: Automatically discovers and peers with neighboring propagation nodes up to a configurable hop limit (`autopeer_maxdepth`), exchanging message stores.
- **Strict Storage Bounding**: Enforces message storage quotas (`message_storage_limit`), prioritizing the newest and most compact messages when space is constrained.
- **Proof-of-Work Stamps**: Configurable stamp cost targets to throttle spam and balance delivery incentives across low-bandwidth links.
- **Shared Instance Architecture**: Automatically attaches to a running `gornsd -s` shared Reticulum instance, sharing network interfaces without port contention.
- **Inbound Message Hooks & Wasm Plugins**: Execute external commands (`--on-inbound`) or sandboxed WebAssembly filters (`-tags=wago`) to inspect, route, or react to messages.
- **Remote Diagnostics & Control**: Remotely query status, view active peers, request immediate synchronization, or break peering links over encrypted Reticulum links.

---

## Quickstart

Run `golxmd` in propagation node mode:

```bash
golxmd -p -v
```

On first run, `golxmd` creates its default configuration directory at `~/.lxmd/` (or the directory passed via `--config`) and generates its local routing identity.

To print a fully commented template configuration file:

```bash
golxmd --exampleconfig > ~/.lxmd/config
```

---

## Command-Line Usage

```bash
golxmd [options]
```

### Operational Modes

| Flag | Description |
|------|-------------|
| `-p`, `--propagation-node` | Run as an active LXMF Propagation Node (overrides `enable_node = no` in config). |
| `-s`, `--service` | Run as a background service; redirects log output to `<configdir>/logfile`. |
| `-v`, `--verbose` | Increase logging verbosity (stackable: `-v`, `-v -v`). |
| `-q`, `--quiet` | Decrease logging verbosity (stackable). |

### Peering & Diagnostics

| Flag | Description |
|------|-------------|
| `--status` | Display status and telemetry for the local propagation node. |
| `--status -r <hash>` | Query and display status from a remote propagation node destination hash. |
| `--peers` | List all currently peered propagation nodes and their metrics. |
| `--peers -r <hash>` | Query and display peered nodes from a remote propagation node. |
| `--sync <hash>` | Request an immediate message sync with the specified peer hash. |
| `-b`, `--break <hash>` | Break peering relationship with the specified peer hash. |
| `--timeout <sec>` | Timeout in seconds for remote queries and operations. |
| `--identity <path>` | Identity file used to sign remote control requests. |

### Configuration & Paths

| Flag | Description |
|------|-------------|
| `--config <dir>` | Path to alternative `golxmd` configuration directory (default: `~/.lxmd`). |
| `--rnsconfig <dir>` | Path to alternative Reticulum configuration directory (default: `~/.reticulum`). |
| `-i`, `--on-inbound <path>` | Path to an executable run whenever an inbound message is received. |
| `--exampleconfig` | Print annotated configuration example to stdout and exit. |
| `--version` | Display version information and exit. |

---

## Configuration Reference (`~/.lxmd/config`)

The configuration file is formatted in INI syntax:

```ini
[propagation]
# Enable propagation node functionality
enable_node = yes

# Optional human-readable name advertised in announces
node_name = North Ridge Relay

# Announce interval in minutes (default: 360 = 6 hours)
announce_interval = 360
announce_at_start = yes

# Automatically peer with other propagation nodes within hop limit
autopeer = yes
autopeer_maxdepth = 6
max_peers = 20

# Storage limit in megabytes; oldest/largest messages pruned first
message_storage_limit = 500

# Target stamp cost (proof-of-work) required for messages
propagation_stamp_cost_target = 16
propagation_stamp_cost_flexibility = 3

# Static peers to maintain permanent synchronization with (comma-separated hashes)
# static_peers = e17f833c4ddf8890dd3a79a6fea8161d, 5a2d0029b6e5ec87020abaea0d746da4

# Restrict control queries to specific identity hashes
# control_allowed = 7d7e542829b40f32364499b27438dba8

[lxmf]
# Announced display name for local LXMF delivery destination
display_name = Relay Operator

# Announce delivery destination at startup
announce_at_start = no

# External executable triggered on inbound message
# on_inbound = /usr/local/bin/on-message.sh

[logging]
# Log level: 0 (critical) to 7 (extreme); default 4 (info)
loglevel = 4
```

---

## WebAssembly (Wasm) Filter Plugins

When compiled with `-tags=wago` on supported platforms (`linux`, `darwin`, `windows` on `amd64`/`arm64`), `golxmd` loads sandboxed WebAssembly filters from:

```
~/.lxmd/plugins/*.wasm
```

Each filter executes in an isolated memory sandbox with a bounded execution budget (2 seconds per invocation) and can inspect inbound messages before they are stored or delivered.

---

## System Architecture

In a standard deployment, `golxmd` operates behind a shared Reticulum transport instance:

```
+-----------------------------------------------------------+
|                   Physical Interfaces                     |
|        (LoRa RNodes, TCP Servers, UDP AutoInterface)      |
+-----------------------------+-----------------------------+
                              |
                     +--------v--------+
                     |    gornsd -s    |  (owns network interfaces)
                     +--------+--------+
                              | @rns/default (IPC / Unix domain socket)
      +-----------------------+-----------------------+
      |                       |                       |
+-----v-----+           +-----v-----+           +-----v-----+
|  golxmd   |           |  gorrcd   |           | gonomadnet|
| (LXMF PN) |           | (RRC Hub) |           | (TUI/App) |
+-----------+           +-----------+           +-----------+
```

By connecting to `gornsd -s`, `golxmd` transparently receives announcements and forwards packets without requiring exclusive access to physical radio ports or network sockets.
