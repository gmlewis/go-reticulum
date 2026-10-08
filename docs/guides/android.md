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
