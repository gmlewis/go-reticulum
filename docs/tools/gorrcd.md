# gorrcd — RRC Hub Daemon

`gorrcd` is a standalone, high-performance Reticulum Relay Chat (RRC) hub daemon written in 100% Go. It serves chat rooms over the Reticulum network, provides channel history persistence, and is 100% wire-compatible with Python `rrcd` and NomadNet clients.

---

## Key Features

- **High Concurrency**: Handles dozens of simultaneous connected clients and high message volumes with minimal memory and CPU usage.
- **Persistent Channels**: Retains room message histories across hub restarts.
- **Key-Protected Rooms (`+k`)**: Supports encrypted rooms accessible only to clients with the shared channel key.
- **Direct Notice Delivery (`K_DST`)**: Routes private notices directly between connected clients on the hub.
- **Private Commands (`CAP_PRIVATE_COMMAND`)**: Accepts a command line addressed to the hub's own identity from a client that has not joined a room, and lets a user send a direct message to another participant by nickname. Stock Python `rrcd` has no private-message command; the capability is advertised only when enabled and is ignored by hubs and clients that do not know it.
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

# Accept private command lines addressed to the hub and advertise
# CAP_PRIVATE_COMMAND in the WELCOME. The default is true; set false for a
# WELCOME byte-identical to stock Python rrcd.
enable_private_commands = true

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

## Private Messages Between RRC Users

RRC itself has no private-message command. `gorrcd` adds one without changing
the protocol: when `enable_private_commands = true` (the default) the hub
advertises the capability `CAP_PRIVATE_COMMAND` (`K_CAPS` key `3`) in its
`WELCOME`, and a `NOTICE` whose `K_DST` is the hub's own identity hash — the
hash the `WELCOME` came from, not the hub's destination hash — is read as a
command line and answered on that client's link alone.

Unknown capability keys are ignored by stock `rrcd` and by RRC clients, so no
Python hub or Python client needs any change, and a client only ever sends one
of these commands to a hub that advertises the key.

| Command | Aliases | Effect |
|---------|---------|--------|
| `/dnotice <nick\|hash\|me> <text>` | `/dn`, `/msg` | One direct notice to that participant alone. |
| `/dnoticeme <text>` | — | One direct notice to the sender's own link. |
| `/dnoticecap` | — | Reports whether the hub advertises the capability. |

Targets resolve **exactly** — a full identity hash, a hex prefix of at least six
characters, or a nickname — and a token that matches more than one participant
is reported with nothing sent. There is no fuzzy matching. A nickname
containing spaces must be quoted: `/msg 'gonomadnet on MiniPC' yo dude`. The hub
confirms delivery to the sender with the recipient's full hash and the message
id, or reports `not found`, the ambiguity list, or that the body does not fit
the target's link.

See [Protocol & Markup Extensions](../reference/protocol-extensions.md#1-rrc-capability-cap_private_command-3)
for the envelope layout, hub processing rules, and a Python back-port guide, and
the [go-reticulum README](https://github.com/gmlewis/go-reticulum#private-messages-between-rrc-users)
for the client-side behaviour in `gonomadnet`.

---

## Interoperability

`gorrcd` is drop-in compatible with:
- **NomadNet** (Python and Go versions)
- **gorrcbot** (Go RRC bot)
- Official Python `rrcd` clients and hubs
