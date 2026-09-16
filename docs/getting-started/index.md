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

Download the appropriate binary archive for your system, extract it, and place the executables in your `$PATH` (for example, `/usr/local/bin` or `~/bin`).

### Building from Source

To build from source, ensure you have **Go 1.23+** installed:

```bash
git clone https://github.com/gmlewis/go-reticulum.git
cd go-reticulum

# Install all CLI utilities to $GOPATH/bin:
go install ./cmd/...
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

Output will display the local Reticulum identity hash, uptime, interfaces, and current routing table.

```
Shared Instance[0] is running, pid 14280
Primary Interface   : AutoInterface[Default Interface]
Interface Status    : Up
Received Traffic    : 0 bytes
Sent Traffic        : 0 bytes
```

---

## What's Next?

- Configure interfaces and bootstrap mesh connectivity in [**Bootstrapping Connectivity**](bootstrapping.md).
- Learn about the security and pure-Go design in [**Architecture & Security**](architecture.md).
- Explore available tools in [**Tools Directory**](../tools/index.md).
