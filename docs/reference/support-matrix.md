# Platform Support Matrix

Go Reticulum provides multi-platform binaries. The certified continuous-integration and testing matrix is detailed below.

---

## Certified Platforms

| Platform | Architecture | Core Stack & CLI | `gornodeconf` Serial / Firmware | Release Executables |
|----------|--------------|------------------|----------------------------------|---------------------|
| **Linux** | `amd64`, `arm64`, `armv7`, `armv6`, `riscv64` | **Certified** | **Certified** | Published |
| **macOS (Darwin)** | `arm64` (Apple Silicon), `amd64` (Intel) | **Certified** | **Certified** | Published |
| **FreeBSD** | `amd64`, `arm64` | Community verified | **Supported** | Published |
| **Windows** | `amd64`, `arm64` | Community verified | Unsupported (`not supported on platform`) | Published (CLI only) |
| **Android (Termux)** | `arm64`, `arm`, `amd64`, `386` | Community verified | Unsupported | Build from source |

Android needs platform-specific configuration that the other platforms do not;
see [Android (Termux)](../guides/android.md) for the `targetSdk` requirement, the
missing `/etc/resolv.conf`, and the `AutoInterface` restriction.

---

## Interface Type Support

| Interface Type | Status | Notes |
|----------------|--------|-------|
| `AutoInterface` | Supported | Local link-local UDP multicast (port 29716). **Unavailable on Android** — netlink interface enumeration is denied by SELinux and multicast is filtered without a `WifiManager.MulticastLock`; use a TCP or RNode interface instead |
| `TCPClientInterface` | Supported | Outbound TCP client connectivity |
| `TCPServerInterface` | Supported | Inbound TCP listener |
| `UDPInterface` | Supported | Static point-to-point UDP tunnels |
| `RNodeInterface` | Supported | USB serial LoRa transceivers (Linux, macOS, FreeBSD) |
| `KISSInterface` | Supported | KISS TNCs for packet radio (VHF/UHF) |
| `PipeInterface` | Supported | Unix domain socket / pipe IPC |
| `I2PInterface` | Supported | Invisible Internet Project anonymous tunneling |
