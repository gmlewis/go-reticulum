# Android (Termux)

The Go Reticulum stack builds and runs on Android without modification. Android
is Linux under the hood, so the same `linux` binaries work; the differences that
matter are all in the platform's security model, not in the network stack.

This page covers what the stack itself needs to know about Android. For a
step-by-step guide to running an application (`gonomadnet`) on a device — build,
install, home-screen icon, configuration — see
[go-nomadnet's Android guide](https://github.com/gmlewis/go-nomadnet/blob/master/docs/ANDROID.md).

Android reaches the stack through [Termux](https://termux.dev), which gives an
unprivileged application a PTY, a shell, and a writable home directory. Two
consequences follow, and both are enforced by SELinux and by the absence of
files a desktop Linux provides.

---

## Build target

Build for `linux`, not `android`:

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
```

`GOOS=android` requires cgo and the NDK, and is only needed when linking into a
real Android application through `gomobile`. A Termux-hosted process is an
ordinary Linux process and a `CGO_ENABLED=0` build is statically linked, so it
needs no Termux packages at all.

| Device ABI | `GOARCH` |
| --- | --- |
| `arm64-v8a` | `arm64` |
| `armeabi-v7a` | `arm` (`GOARM=7`) |
| `x86_64` | `amd64` |
| `x86` | `386` |

---

## Interface availability on Android

### `AutoInterface` is unavailable

Two independent platform restrictions apply, either of which is sufficient to
disable it.

**Interface enumeration is denied.** Termux runs in Android's `untrusted_app`
SELinux domain, which cannot open netlink route sockets. `net.Interfaces()` — and
therefore `AutoInterface`'s discovery of usable interfaces — fails outright:

```
Failed to initialize Auto interface Default Interface:
route ip+net: netlinkrib: permission denied
```

**Multicast is filtered.** Android's Wi-Fi driver does not deliver multicast to
applications that fail to hold a `WifiManager.MulticastLock`, and acquiring one
requires an Android `Context` and the `CHANGE_WIFI_MULTICAST_STATE` permission.

This is a known limitation across the Reticulum ecosystem, not specific to this
port. Use `TCPClientInterface`, `TCPServerInterface`, or `RNodeInterface`
(over USB-OTG) instead.

### Interfaces that work

| Interface | On Android | Notes |
| --- | --- | --- |
| `TCPClientInterface` | Supported | IPv4, IPv6 literals, and hostnames subject to the resolver note below |
| `TCPServerInterface` | Supported | Binding an IPv6 literal or the IPv6 wildcard `::` works |
| `UDPInterface` | Supported | Point-to-point; IPv6 literals are bracketed correctly |
| `RNodeInterface` | Supported | USB-OTG serial; Android grants access through Termux |
| `KISSInterface`, `PipeInterface` | Supported | Ordinary device and socket paths |
| `AutoInterface` | **Unavailable** | See above |

---

## Hostname resolution

A `CGO_ENABLED=0` Go binary resolves names with Go's own resolver, which reads
`/etc/resolv.conf`. **Android has no such file** — `/etc` is a read-only symlink
to `/system/etc` — and Android's resolver is reached through `netd` over a Unix
socket that only bionic (the C library) can call. Go's resolver cannot use it.

With the file missing, Go falls back to
`defaultNS = ["127.0.0.1:53", "[::1]:53"]`, so every lookup fails:

```
dial tcp: lookup example.org on [::1]:53: read udp [::1]:36058->[::1]:53:
read: connection refused
```

Note what this does and does not affect:

- **Hostnames are affected.** A `target_host` that is a DNS name will not
  resolve.
- **Literal addresses are unaffected.** An IPv4 or IPv6 literal in `target_host`
  needs no lookup and works directly. This is the simplest fix.
- **Android's own tools are unaffected.** Anything linked against bionic
  (`/system/bin/ping`, `/system/bin/ping6`, toybox `nc`) resolves normally, which
  makes them usable as a resolver of last resort.

To keep a hostname in configuration, resolve it with a bionic binary and feed the
literal to the stack. `/system/bin/ping6 -n` prints a numeric address:

```console
$ /system/bin/ping6 -n -c 1 -W 3 example.org
PING example.org(2603:900b:3300:a::1be9) 56 data bytes
```

A launcher script can do this on every start so the name remains the source of
truth; `go-nomadnet`'s
[Android guide](https://github.com/gmlewis/go-nomadnet/blob/master/docs/ANDROID.md)
shows the complete script.

---

## IPv6

IPv6 literals are supported throughout the TCP and UDP interface paths. Because
Python passes coordinates as `(host, port)` tuples — which is inherently
IPv6-safe — while Go must join them into a single address string, the join must
bracket the literal. `hostPortAddr` in `rns/interfaces` does this and is used by
the TCP client dial, the TCP server bind, and the UDP address resolution:

```go
hostPortAddr("::", 4242)                  // "[::]:4242"
hostPortAddr("2603:900b:3300:a::1be9", 4242) // "[2603:900b:3300:a::1be9]:4242"
```

Joining by hand produces `":::4242"`, which the `net` package rejects with
`too many colons in address` before any I/O occurs.

`TCPInterface`, `TCPServerInterface`, and `BackboneInterface` hash strings
bracket IPv6 literals to match Python's `__str__` methods. `UDPInterface`'s hash
string deliberately does **not** bracket, because Python's does not either; the
hash is a parity artefact, not a dialable address.

---

## Executing external programs

Android's seccomp policy for the `untrusted_app` domain blocks the
`faccessat2(2)` system call and **terminates the process with `SIGSYS`** rather
than returning an error. Go's `os/exec` issues that call while resolving a bare
program name, so `exec.Command("ps")` — or any other unqualified name — kills the
process outright:

```
SIGSYS: bad system call
  syscall.faccessat2 → internal/syscall/unix.Eaccess
  os/exec.LookPath → os/exec.Command
```

This differs from the `shell` domain, which is why the same code can run under
`adb shell` and fail inside Termux. Two rules follow:

- Prefer resolving process and system information without a subprocess.
  `gonomadnet`'s single-instance check reads `/proc/<pid>/cmdline` directly
  instead of shelling out to `ps`, which removes the failure entirely.
- When a subprocess is unavoidable, use an absolute path, which skips
  `LookPath` and therefore the fatal call.

### Test in the right place

Run the daemons **from Termux or from the app**, and grep both the log and
`logcat` for the string above.

Do not test in `/data/local/tmp`. That directory runs as the `shell` uid, a
different SELinux domain with a different seccomp policy, so it **does not
reproduce Termux failures** — a binary that works there tells you nothing about a
binary launched from an app.

`gornsd`, `gorrcd` and `gorrcbot` all run under `untrusted_app` without `SIGSYS`,
provided every subprocess is launched by absolute path. There is no other
adjustment.

---

## Running a bundled binary from the app

An Android app can ship a statically linked Go binary as a shared library and
execute it. Put it in `jniLibs/<abi>/lib<name>.so` and it lands in the app's
`nativeLibraryDir`, which is the one directory the platform both extracts and
marks executable.

**Execute it with `exec` directly.** On the tablet this project targets
(`targetSdk 34`, API 36) that works, and the commonly suggested `linker64`
fallback — invoking `/system/bin/linker64 <path>` — **does not**, because the
kernel rejects a PIE executable that is not also a shared object:

```
linker64: unexpected e_type: 2
```

The practical consequences:

- The binary's on-disk name is `lib<name>.so`; its `argv[0]` is not the name you
  launched it by. A single-instance check that compares process names must
  compare `argv[0]` against what you passed, not against a program name.
- `nativeLibraryDir` is read-only and is replaced on every upgrade, so anything
  the binary must persist belongs in `filesDir`, and must be passed to it.

## One shared instance across two applications

Reticulum can serve one instance to several local clients, and on Android those
clients can be **different applications**. The mechanism is a TCP shared
instance with `require_shared_instance = yes`, and it works across the app
boundary: with the stack owned by one app, a client running in Termux attaches
to it and both hold established connections:

```
tcp 127.0.0.1:37428 (LISTEN)          <- the owning app
tcp 127.0.0.1:37428 (ESTABLISHED)    <- the owning app's own client
tcp 127.0.0.1:37428 (ESTABLISHED)    <- a client in another app
```

Two details make this work:

- Clients need their own configuration directory containing only
  `require_shared_instance = yes` and the shared-instance port. Do **not** point a
  client at the owner's configuration file, which carries the interfaces and the
  transport role.
- `/proc/net/tcp` is not readable from Termux, though it is from `adb shell`. To
  test the port from inside the app, use bash's `/dev/tcp`.

Choose a port that is not already taken. `37428` is Reticulum's shared instance and
`37429` is its control port, so anything else an app needs must go elsewhere.

## Sensors: JSON in, NMEA out

Android hands a program Java objects, not bytes, so the clean seam is a converter.
A small foreground service translates `LocationManager` and `SensorManager`
readings into one JSON object per line, and `gonsensor` turns that into the
NMEA-0183 sentences that a GNSS reader already parses:

```
LocationManager / SensorManager
      -> JSON lines            (the Android side: platform objects in, JSON out)
      -> gonsensor             (this repository: JSON in, NMEA out)
      -> named pipe or socket
      -> gorrcbot or gonomadnet
```

Three things that matter when you build one:

- **Keep the bridge beside the parser.** `gonsensor` lives in the package that owns
  the NMEA parser, so "parse what we emit and get the same fix back" is a test. A
  bridge tested against a second, independently written parser only proves the two
  parsers agree — not that either is right.
- **Hold a named pipe open at both ends for the life of the service.** A reader that
  opens a FIFO `O_RDONLY` **blocks forever** when no process holds the write end, and
  there is no error and no timeout: the bot simply never starts. Open it `O_RDWR` and
  hold it. An `O_RDWR` holder counts as both ends, which is what makes a reader
  restart safe.
- **Use sockets, not pipes, across applications.** A FIFO in the owner's private
  storage cannot be reached by a client in Termux; a loopback socket can.

Sensors on Android generally do **not** keep the device awake, so sampling at a
useful rate needs a foreground service plus a partial wake lock. Otherwise the
readings stop when the screen does, and they stop quietly.

---

## Verifying a deployment

Start the stack and confirm the interface reaches its running state. A
successful TCP client connection logs the bracketed address, which is itself
proof that the IPv6 formatting is correct:

```
Go TCPClientInterface Reticulum Hub connecting to [2603:900b:3300:a::1be9]:4242
Go TCPClientInterface Reticulum Hub connected
```

Then confirm the node participates: `Announce sent` for your own address, and
`announce received` for peers.
