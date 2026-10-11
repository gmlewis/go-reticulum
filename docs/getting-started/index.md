# Getting Started with Go Reticulum

This guide covers installing Go Reticulum binaries, generating your first configuration, and verifying stack connectivity.

---

## Installation

### Pre-Compiled Binaries

Static, standalone release executables are automatically built and published on the [GitHub Releases](https://github.com/gmlewis/go-reticulum/releases) page for:

- **Linux** (`amd64`, `arm64`, `armv7`, `armv6`, `riscv64`)
- **macOS / Darwin** (`arm64` Apple Silicon, `amd64` Intel)
- **FreeBSD** (`amd64`, `arm64`)
- **Windows** (`amd64`, `arm64`)

Download the appropriate binary archive for your system, extract it, and place the executables in your `$PATH` (for example, `/usr/local/bin` or `~/go/bin`).

### Building from Source

To build from source, ensure you have **Go 1.26+** installed:

```bash
git clone https://github.com/gmlewis/go-reticulum.git
cd go-reticulum

# Install all CLI utilities to $GOPATH/bin:
go install ./cmd/go*
```

To build a specific daemon or tool (for example, `gorrcbot` or `gorrcd`):

```bash
go build -o bin/gorrcbot ./cmd/gorrcbot
go build -o bin/gorrcd ./cmd/gorrcd
```

---

## First Run and Configuration

Reticulum stores its system configuration and identity keys in `~/.reticulum/` (or the directory specified by `--config`).

On first run of any Reticulum tool (such as `gornstatus`), if no configuration exists, Reticulum creates an initial default configuration file at `~/.reticulum/config` and generates a primary system identity.

Run `gornstatus` to verify:

```bash
gornstatus
```

It prints the running transport instance and its uptime, then a block per
interface — its name, its `Status` (`Up` or `Down`), and its traffic counters.

---

## What's Next?

- Configure interfaces and bootstrap mesh connectivity in [**Bootstrapping Connectivity**](bootstrapping.md).
- Orchestrate a full Reticulum node (transport, chat, bot, propagation, web) in [**Running a Node**](../guides/running-a-node.md).
- Learn about the security and pure-Go design in [**Architecture & Security**](architecture.md).
- Explore available tools in [**Tools Directory**](../tools/index.md).
