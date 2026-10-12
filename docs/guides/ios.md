# iOS and iPadOS

The Go Reticulum stack compiles and runs on iOS. What differs from a desktop is the
platform's security model rather than the network stack, and the differences are sharp: an
iOS application may not start another process, and most of the filesystem it can see is not
writable.

This page covers what the stack itself needs to know about iOS. For a step-by-step guide to
running an application on an iPad, see
[go-nomadnet's iOS guide](https://github.com/gmlewis/go-nomadnet/blob/master/docs/iOS.md).

---

## Build target

Build for `ios` with cgo and Apple's clang, as a static archive to link into the application:

```bash
GOOS=ios GOARCH=arm64 CGO_ENABLED=1 \
  CC="$(xcrun --sdk iphoneos -f clang)" \
  CGO_CFLAGS="-isysroot $(xcrun --sdk iphoneos --show-sdk-path) -miphoneos-version-min=16.0" \
  go build -tags ios -buildmode=c-archive -o libstack.a ./cmd/your-program
```

`GOOS=ios` also matches the `darwin` build tag, so the Darwin implementations of each
interface and of the PTY layer are the ones that compile — with the exceptions below.

---

## One process, no daemons

`fork`, `exec` and `posix_spawn` are all unavailable, so there is no way to run `gornsd`,
`gorrcd` or `gorrcbot` as separate programs. The packages are the same: an application links
the transport, the hub and the bot as libraries and runs them as goroutines in its own
process.

`rns`, `rrc` and `bot` are all ordinary importable packages, and nothing in them requires a
subprocess. What an application has to supply is what a command line would have supplied: a
configuration directory, an identity, and a home directory for anything stateful.

---

## The filesystem

An iOS application's home directory is its container root, and **the container root is not
writable**. Only the directories inside it — `Documents`, `Library`, `tmp` — are.

This matters because several tools default a path to `~/.name`. On iOS that mkdir fails with
`operation not permitted`, which reads like a sandbox refusal rather than a wrong default:

| Tool | Default | What to do instead |
|---|---|---|
| `gorrcd` | `~/.rrcd` (`RRCD_HOME`) | set `RRCD_HOME`, or `identity_path` and `configdir`, to a path under `Library/` |
| `gorrcbot` | `~/.gorrcbot` (`GORRCBOT_HOME`) | set `GORRCBOT_HOME`, or the `home` option, to a path under `Library/` |

A path a configuration file leaves unsaid takes the tool's own default, so setting the home
directory is not always enough on its own: state both the home and any path the
configuration can override individually.

---

## Interfaces

| Interface | On iOS | Notes |
|---|---|---|
| `TCPClientInterface` / `TCPServerInterface` | Supported | Loopback works; an app needs no entitlement to open a socket on `127.0.0.1` |
| `TCPClientInterface` to a hub | Supported | The usual way an appliance reaches a hub |
| `RNodeInterface`, `tcp://host:port` | Supported | A network-attached RNode, on the same Wi-Fi or hotspot |
| `RNodeInterface`, a device path | Not usable | There is no serial device to open |
| `RNodeInterface`, `ble://` | Refused | Bluetooth is not reachable from Go on iOS; see below |
| `KISSInterface` | Supported | Over TCP to a KISS TNC |
| `AutoInterface` | Untested | Local multicast is not something an iOS application can rely on |
| `I2PInterface` | Unsupported | It needs a Java I2P router running as a separate process |

### Bluetooth RNodes

An iPad cannot open a USB serial adapter, and Go cannot open a CoreBluetooth connection. The
shape that works keeps Bluetooth entirely in the application and hands the stack an ordinary
socket: the application owns the connection to the RNode's Nordic UART Service and serves a
loopback TCP listener, and the transport is configured with `port = tcp://127.0.0.1:<port>`.

The bridge must be a **transparent byte pump**. KISS framing, the detect and
firmware-version handshake and the radio configuration commands are spoken by the transport
at one end and answered by the firmware at the other; anything that framed, split or merged
bytes in the middle would be a second framer in a conversation it is not a party to.

---

## Sensors

Android feeds a receiver to the stack through FIFOs (`gps.nmea`, `compass.nmea`). An iOS
application has nowhere to put one, so the feed is a loopback TCP server instead and
`gonsensor`'s consumers read NMEA from it over `tcp://127.0.0.1:<port>`.

Both are the same protocol: NMEA-0183 sentences, one sample converted once so that every
subscriber sees identical bytes. A `gps_port` or `compass_port` of the form
`tcp://host:port` is what a bot reads on iOS, and a device path is what it reads on Android.

---

## The shared instance

The shared instance is an ordinary TCP listener on `127.0.0.1`, and the transport, the hub,
the bot and the client all attach to it in-process or over loopback. Nothing about it changes
on iOS; the port is pinned by the application's configuration rather than by the platform.
