# Go Reticulum Network Stack (RNS)

![gonomadnet Mascot](assets/gonomadnet-mascot.png)

*The Go gopher was designed by Renee French. The design is licensed under the Creative Commons 4.0 Attribution license.*

Welcome to the documentation for **Go Reticulum**, a 100% pure Go rewrite of the [Reticulum Network Stack (RNS)](https://github.com/markqvist/Reticulum), the [LXMF messaging protocol](https://github.com/markqvist/lxmf), and the [RRC (Reticulum Relay Chat)](https://github.com/kc1awv/rrcd) ecosystem.

---

## 🌐 Live Public Hub & Demo Destinations

A full suite of pure-Go Reticulum daemons runs 24/7 on the public hub (`glenn-kamrui`). Anyone connected to the mesh or peered via TCP can immediately interact with these live services:

> [!TIP]
> ### TCP Bootstrap Interface (`~/.reticulum/config`)
> Add this interface to your `~/.reticulum/config` to peer directly with the public hub over TCP:
> ```ini
> [[gonomadnet Public Hub]]
>   type = TCPClientInterface
>   enabled = yes
>   target_host = go-nomadnet.duckdns.org
>   target_port = 4242
> ```

| Service Daemon | Destination Hash | Protocol / URL | Description |
|----------------|------------------|----------------|-------------|
| **gonomadnet Node** | `<c7d0e7bbd883e595f53e14fa6986188c>` | `nomadnetwork://c7d0e7bbd883e595f53e14fa6986188c` | Live Micron pages served over Reticulum. |
| **gorrcd Chat Hub** | `<a012129c10205c0b9441fcd2b755b2a7>` | `rrc://a012129c10205c0b9441fcd2b755b2a7/#general` | Public RRC chat hub. Home of `@gobot`! |
| **gorngit Repos** | `<58a0406047ec2e7ce23e9e9a83b744df>` | `rns://58a0406047ec2e7ce23e9e9a83b744df/<repo>` | Git clone & push over Reticulum mesh links. |
| **gorngit Pages** | `<cb3677a1bb8e37f334096566ed8ff895>` | `nomadnetwork://cb3677a1bb8e37f334096566ed8ff895` | Micron code browser for mesh-hosted repositories. |
| **golxmd Propagation** | `<7acc095f0e83182feb58c888d090a3cc>` | `lxmf.propagation` | Store-and-forward LXMF message propagation node. |

---

## What is Reticulum?

Reticulum is a cryptography-first, self-configuring, delay-tolerant networking stack designed to operate over high-latency, low-bandwidth, and unreliable transport media — including LoRa packet radio, HF/VHF/UHF amateur radio, WiFi, Ethernet, TCP/IP, and serial links.

With Reticulum:

- **No central authorities, DNS, or IP addresses are required.** All destinations are self-sovereign cryptographic keys.
- **End-to-end encryption is mandatory and intrinsic.** Every packet is signed, authenticated, and encrypted at the stack layer.
- **Mesh routing happens autonomously.** Nodes discover and announce paths with no manual routing tables needed.

---

## Why Go Reticulum?

The Go port provides a modern, high-performance, single-binary implementation of Reticulum with strict architectural principles:

- **Standard Library Only**: The root module has **zero external Go dependencies** and **zero Cgo**. All cryptographic primitives (Ed25519, X25519, AES-128, Fernet, SHA-256/512, HKDF) and protocol codecs are implemented natively.
- **High Concurrency & Low Footprint**: Built with Go's lightweight goroutines and channels, providing exceptional throughput and minimal RAM/CPU consumption on low-power devices.
- **Drop-In Interoperability**: Fully wire-compatible with Python Reticulum, LXMF, and RRC hubs and clients.
- **Field-Ready Autonomous Agents**: Includes `gorrcbot`, an autonomous RRC client and field assistant equipped with offline geodesy, Plus Codes, ephemeris, marine telemetry, wilderness medicine cards, and emergency signaling. Its marine, aviation, and navigation station catalogs are embedded, so `search`, `near`, and `list` find an opaque station id with no network at all, and long answers are paginated to the reply budget with `more` / `next`.
- **Hardware & Firmware Management**: Complete device lifecycle tools (`gornodeconf`) for flashing, backing up, and provisioning LoRa RNodes on Linux, macOS, and FreeBSD.

---

## Documentation Roadmap

- [**Getting Started**](getting-started/index.md) — Installation, initial configuration, and joining the mesh.
- [**Tools & Daemons**](tools/index.md) — Overview of all included executables:
    - [**gorrcbot**](tools/gorrcbot.md) — The autonomous RRC chat bot and off-grid field assistant, with offline station discovery (`search`, `near`, `list`) and low-bandwidth pagination (`more`, `next`).
    - [**gobot**](tools/gobot.md) — Ask a live RRC bot one question from the shell and print its reply.
    - [**gorrcd**](tools/gorrcd.md) — Standalone high-performance RRC hub daemon.
    - [**gornodeconf**](tools/gornodeconf.md) — Hardware provisioning and firmware flasher for LoRa RNodes.
    - [**CLI Utilities**](tools/cli-utilities.md) — Diagnostic and operational commands (`gornstatus`, `gornpath`, `gornprobe`, etc.).
- [**Guides**](guides/hardware.md) — Hardware projects, Wasm plugin sandboxing, and radio diagnostics.
- [**Reference**](reference/support-matrix.md) — Certified platform support matrix and cryptographic specifications.
