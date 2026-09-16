# gorrcd — RRC Hub Daemon

`gorrcd` is a standalone, high-performance Reticulum Relay Chat (RRC) hub daemon written in 100% Go. It serves chat rooms over the Reticulum network, provides channel history persistence, and is 100% wire-compatible with Python `rrcd` and NomadNet clients.

---

## Key Features

- **High Concurrency**: Handles dozens of simultaneous connected clients and high message volumes with minimal memory and CPU usage.
- **Persistent Channels**: Retains room message histories across hub restarts.
- **Key-Protected Rooms (`+k`)**: Supports encrypted rooms accessible only to clients with the shared channel key.
- **Direct Notice Delivery (`K_DST`)**: Routes private notices directly between connected clients on the hub.
- **Message of the Day (MOTD)**: Configurable announcements broadcast to users when joining rooms.

---

## Quickstart

Run `gorrcd` for the first time:

```bash
gorrcd
```

On first run, `gorrcd` creates its configuration file at `~/.gorrcd/config.toml` and generates its hub identity before exiting cleanly.

---

## Configuration (`~/.gorrcd/config.toml`)

```toml
# Storage directory for message history
storage_dir = "~/.gorrcd/storage"

# Hub identity private key path
identity_path = "~/.gorrcd/hub_identity"

[hub]
name = "My Reticulum Hub"
motd = "Welcome to Reticulum Relay Chat! Be kind."

# Maximum messages saved per room
history_limit = 100

# Public and key-protected rooms
[[rooms]]
name = "general"
topic = "Public discussion"

[[rooms]]
name = "emergency"
topic = "Emergency coordination"

[[rooms]]
name = "operations"
topic = "Operations team only"
key = "supersecretpassphrase"   # Key-protected room (+k)
```

---

## Interoperability

`gorrcd` is drop-in compatible with:
- **NomadNet** (Python and Go versions)
- **gorrcbot** (Go RRC bot)
- Official Python `rrcd` clients and hubs
