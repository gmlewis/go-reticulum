# gobot — One-Shot RRC Bot CLI

`gobot` is a small, stand-alone command-line client for a live RRC bot. It
connects to an RRC hub over Reticulum, joins the bot's room, privately asks the
bot one question, prints every reply line the bot sends back to **stdout**, and
exits.

It is the scriptable equivalent of joining a hub by hand and typing
`/msg gobot <command>`:

```bash
gobot help buoy
```

```text
buoy — report the sea state from an offshore weather buoy. Usage: buoy <station_id>
<station_id> is a 4 to 6 character buoy id, like 46026 (San
Francisco offshore), 41009 (Cape Canaveral), or 44013 (Boston).
Reports wave height, dominant period and direction, wind, water
temperature, and the pressure trend. The period decides whether
the sea is groundswell or chop, and the answer says which.
gobot buoy 46026
```

Nothing is broadcast to the room: the request travels as a private command to
the hub, and the bot's answer comes back as direct notices addressed only to
this client. `gobot` sends **one** question and exits; it is not a chat client.

---

## Why use it

- **One command, one answer.** No TUI, no hub setup, no room to leave.
- **Zero configuration for the official bot.** The destination is hard-coded to
  the official gonomadnet Public RRC Hub, so `gobot help` reaches the official
  `@gobot` with no flags at all. Point `-dest` at another hub to reach your own.
- **Private by default.** The request and the reply never enter a room, so
  coordinates, check-ins and verbose listings stay off the air.
- **Scriptable.** The reply is plain text on stdout, diagnostics are on stderr,
  and the exit status distinguishes "answered", "failed" and "silent".
- **Identity reuse.** It presents the identity already in your Reticulum
  configuration directory, so the bot sees the same peer every time.
- **No state written.** It loads the identity, connects, asks, prints, and
  cleans up; it never creates an identity or persists anything.

---

## Install

From a checkout of this repository:

```bash
go install ./cmd/gobot/
```

Or straight from the module:

```bash
go install github.com/gmlewis/go-reticulum/cmd/gobot@latest
```

The binary is installed as `gobot` in `$(go env GOPATH)/bin`, which is usually
already in `PATH`.

---

## Requirements

1. **A Reticulum identity that already exists.** `gobot` never generates one.
   The identity it looks for is the persistent transport identity that any
   Reticulum tool creates on first run:

   ```text
   ~/.reticulum/storage/transport_identity
   ```

   If that file is missing, run any Reticulum tool once (for example
   `gornstatus`) and try again, or point `--identity` at an existing key file.
   Generating a fresh identity on the fly would change `gobot`'s identity hash
   on every run, which is exactly what the bot's per-identity cooldown and
   reply routing key on.

2. **A path to the hub.** The default destination is the official gonomadnet
   Public RRC Hub. Reticulum learns and maintains that path itself; a working
   `~/.reticulum/config` with an interface that reaches the hub is all that is
   required (see the [TCP bootstrap interface](../index.md)).

3. **A hub that advertises the private-command capability.** `gorrcd` does by
   default (`enable_private_commands = true`). See
   [Private messages between RRC users](https://github.com/gmlewis/go-reticulum#private-messages-between-rrc-users)
   for the protocol. On a hub without it, `gobot` falls back to addressing the
   bot's identity hash directly when it can resolve it.

---

## Usage

```text
usage: gobot [-h] [--version] [-dest DEST] [--to NICK] [--room ROOM]
             [--nick NICK] [--config CONFIG] [--identity IDENTITY]
             [--timeout TIMEOUT] [--quiet QUIET] [--verbose]
             COMMAND [ARGUMENT ...]
```

Every positional argument is joined with single spaces into the command line
sent to the bot, so these are the same request:

```bash
gobot help buoy
gobot "help buoy"
```

The command line is the bot's own syntax, including the field shorthand. A
leading slash is accepted, and any command that takes a location may take none
at all when the bot has a GNSS fix of its own:

```bash
gobot whereami              # the bot's operational location card
gobot /whereami             # the same command, slash form
gobot tower near            # the three closest masts to the bot
gobot tide near             # the three closest tide stations to the bot
gobot sun                   # today's light at the bot's position
```

See [The Go Reticulum Lifesaver](gorrcbot.md#the-go-reticulum-lifesaver-grl) for
the receiver, the `/whereami` card, and the captive portal.

### Options

| Option | Default | Description |
|--------|---------|-------------|
| `-h`, `--help` | — | Show the usage text and exit. |
| `--version` | — | Print the version and exit. |
| `-dest DEST` | `a012129c10205c0b9441fcd2b755b2a7` | The hub's `rrc.hub` destination hash, 32 hex characters. It is **hard-coded to the official gonomadnet Public RRC Hub**, where the official `@gobot` runs, so `gobot` works out of the box; use `-dest` to talk to a bot on another hub. |
| `--to NICK` | `gobot` | The nick of the bot to address. The hub resolves it, so `gobot` never needs the bot's identity hash. |
| `--room ROOM` | `general` | The room to join. A room membership is what lets the bot see this identity and deliver a private reply. |
| `--nick NICK` | `gobot-cli` | The nick `gobot` advertises in `HELLO` and `JOIN`, so the bot's `whoami` and its logs can name the caller. |
| `--config CONFIG` | `~/.reticulum` | The Reticulum configuration directory. It is also where the identity is looked for. |
| `--identity IDENTITY` | the identity under `--config` | An explicit identity file. When given, it is used alone: there is no fallback. |
| `--timeout TIMEOUT` | `90s` | Overall deadline for connecting, joining, and receiving the first reply line. |
| `--quiet QUIET` | `2s` | How long the reply stream must stay silent before it is considered complete. |
| `--verbose` | off | Log Reticulum protocol traffic to stderr. |

### The hard-coded destination

The hub destination is compiled in:

```text
a012129c10205c0b9441fcd2b755b2a7   # the official gonomadnet Public RRC Hub
```

That is the hub the official `@gobot` runs on, so a plain `gobot help` reaches
the official bot with no setup beyond a Reticulum identity and a path to the
hub. Everyone gets the same official bot by default.

To talk to your own bot, override the destination with `-dest`:

```bash
gobot -dest <your-hub-hash> --to <your-bot-nick> help
```

`-dest` accepts the same 32-hex `rrc.hub` destination hash printed by
`gorrcbot --check-config` and `gorrcd` on startup.

---

## Examples

```bash
# The bot's command listing
gobot help

# Help for one command
gobot help buoy

# Live weather for a place
gobot wx Denver

# Find an opaque station id before asking about it: the bot's station catalogs
# are embedded, so search, near, and list answer offline
gobot metar search denver
gobot buoy near 37.8,-122.4
gobot tide list OR

# Convert a Plus Code to every supported coordinate notation
gobot loc 849VCWC8+R9

# The operational location card: Plus Code, coordinates, grid, elevation,
# fix status, local solar time, and the sunset countdown
gobot whereami 37.7553,-122.4527

# With no argument, whereami uses the bot's own GNSS fix (if the node has a
# receiver, or a gps_fix configured), so a field operator types one word
gobot whereami
gobot /whereami

# Great-circle distance and bearings between two grid squares
gobot dist CM87uk CM87wj

# Check that the bot is awake and the link is live
gobot ping

# Identify yourself to the bot
gobot whoami

# Ask a different bot on a different hub
gobot -dest 28c7c1a68c735693aa8e6b8193ed44b2 --to somebot ping
```

Because the whole command line is one private message, an unknown command is
answered by the bot rather than rejected locally:

```console
$ gobot nosuchcommand
unknown command — try @gobot help
```

> [!NOTE]
> The bot remembers the pending page of a `search`, `near`, or `list` answer per
> identity for five minutes, so `gobot tide list CA` followed by `gobot more`
> continues the same walk — both invocations present the same identity, which is
> the one under `--config` unless `--identity` says otherwise. See
> [Low-Bandwidth Pagination](gorrcbot.md#low-bandwidth-pagination-more-next).

---

## How it works

1. **Load the identity** from `~/.reticulum/storage/transport_identity` (or
   `--identity`), and fail if it is missing.
2. **Start Reticulum** with the configuration directory's own interfaces, so the
   path table and any shared instance are reused.
3. **Connect to the hub** as an ordinary RRC client and wait for its `WELCOME`.
4. **Join the room** (`--room`, default `general`) so the hub adds this identity
   to the member set the bot can see. A private reply is only deliverable to a
   peer the bot has observed in a room it joined.
5. **Send one private command** — `/dnotice <nick> <command>` — addressed to the
   hub's own identity (`K_DST`), so the hub resolves the nick and forwards the
   body to the bot alone.
6. **Collect the direct notices** the bot sends back. Each is one reply line.
   The stream ends when no new line arrives for `--quiet` (default two
   seconds), because the bot emits one notice per line back-to-back.
7. **Print every line to stdout**, disconnect, and exit.

The hub confirms delivery to the sender on the same private channel; that
confirmation is not a reply and is not printed.

---

## Output and exit codes

| Stream | Contents |
|--------|----------|
| **stdout** | The bot's reply, one line per direct notice, and nothing else. |
| **stderr** | Errors, and the Reticulum protocol log when `--verbose` is given. |

| Exit code | Meaning |
|-----------|---------|
| `0` | A reply was printed. |
| `1` | Operational failure: no identity, hub unreachable, request rejected, Reticulum could not start. |
| `2` | Usage error: an invalid flag, an invalid `-dest` hash, or no command given. |
| `3` | The request went out but the bot stayed silent before `--timeout` expired. |

Silence means one of: the bot is not connected to that hub, `--to` names a nick
no participant has, or the hub refused the private command. The `gobot` client
cannot tell these apart; the hub's own log and the bot's log can.

### Scripting

stdout carries only the answer, so it composes directly:

```bash
# Fetch one line of a buoy report
if buoy_report=$(gobot buoy 46026); then
  printf '%s\n' "$buoy_report"
else
  case $? in
    3) echo "the bot did not answer" >&2 ;;
    *) echo "gobot failed" >&2 ;;
  esac
fi
```

---

## Troubleshooting

| Symptom | Cause and fix |
|---------|---------------|
| `no Reticulum identity found in ~/.reticulum: ... does not exist` | No identity on this machine yet. Run any Reticulum tool once, or pass `--identity`. |
| `could not load the Reticulum identity ... may be corrupt or truncated` | The identity file exists but is not a 64-byte private key. Restore a backup; do not delete it and let a new one be generated unless you accept a new identity hash. |
| `could not connect to the hub: Hub identity unknown` | No path to the hub. Check the interface in `~/.reticulum/config` and that the hub is announcing (`gornpath <hub-hash>`). |
| `timed out waiting for the hub to connect` | Raise `--timeout`; a multi-hop path can take longer than the default on a slow mesh. |
| `timed out joining room ...` | The hub accepted the link but not the `JOIN`. Confirm the room name exists on the hub. |
| exit `3`, `did not answer before the deadline` | The request reached the hub but no reply arrived. Confirm `--to` matches the bot's nick on that hub, and that the bot is running. |
| `could not send the request to "gobot"` | The hub has no private-command channel and the nick could not be resolved locally. Try again once the member list has arrived, or use a hub with `enable_private_commands = true`. |

Add `--verbose` to see the Reticulum link handshake and the hub's `WELCOME`.

---

## Relationship to the other tools

- [**gorrcbot**](gorrcbot.md) is the bot itself: a long-running daemon that
  joins hubs and answers. `gobot` **talks to** a `gorrcbot`; it is not one.
- [**gorrcd**](gorrcd.md) is the hub that carries the message. Its
  `enable_private_commands` setting is what makes `/msg <nick> <text>` work.
- **`gonomadnet`** is the interactive client for the same exchange. `gobot` is
  the non-interactive, one-shot version of a single `/msg gobot ...` line.
