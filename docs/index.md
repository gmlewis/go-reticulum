# Go Reticulum Network Stack (RNS)

![gonomadnet Mascot](assets/gonomadnet-mascot.png)

*The Go gopher was designed by Renee French. The design is licensed under the Creative Commons 4.0 Attribution license.*

Welcome to the documentation for **Go Reticulum**, a 100% pure Go rewrite of the [Reticulum Network Stack (RNS)](https://github.com/markqvist/Reticulum), the [LXMF messaging protocol](https://github.com/markqvist/lxmf), and the [RRC (Reticulum Relay Chat)](https://github.com/kc1awv/rrcd) ecosystem.

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
- **Field-Ready Autonomous Agents**: Includes `gorrcbot`, an autonomous RRC client and field assistant equipped with offline geodesy, Plus Codes, ephemeris, marine telemetry, wilderness medicine cards, and emergency signaling.
- **Hardware & Firmware Management**: Complete device lifecycle tools (`gornodeconf`) for flashing, backing up, and provisioning LoRa RNodes on Linux, macOS, and FreeBSD.

---

## Documentation Roadmap

- [**Getting Started**](getting-started/index.md) — Installation, initial configuration, and joining the mesh.
- [**Tools & Daemons**](tools/index.md) — Overview of all included executables:
    - [**gorrcbot**](tools/gorrcbot.md) — The autonomous RRC chat bot and off-grid field assistant.
    - [**gorrcd**](tools/gorrcd.md) — Standalone high-performance RRC hub daemon.
    - [**gornodeconf**](tools/gornodeconf.md) — Hardware provisioning and firmware flasher for LoRa RNodes.
    - [**CLI Utilities**](tools/cli-utilities.md) — Diagnostic and operational commands (`gornstatus`, `gornpath`, `gornprobe`, etc.).
- [**Guides**](guides/hardware.md) — Hardware projects, Wasm plugin sandboxing, and radio diagnostics.
- [**Reference**](reference/support-matrix.md) — Certified platform support matrix and cryptographic specifications.
