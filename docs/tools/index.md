# Included Tools & Daemons

Go Reticulum includes a comprehensive suite of command-line tools and standalone daemons.

---

## Chat & Messaging Daemons

| Tool | Purpose | Description |
|------|---------|-------------|
| [**gorrcbot**](gorrcbot.md) | Autonomous Chat Bot & Field Assistant | Headless, always-on RRC client that joins multiple hubs, manages room catchups, and provides 30+ offline & online field tools, including embedded station catalogs (`search`, `near`, `list`), an offline cell/repeater finder (`tower`, `repeater`, `cell`) with WGS-84 ↔ GCJ-02 conversion, and low-bandwidth pagination (`more`, `next`). |
| [**grl**](grl.md) | Go Reticulum Lifesaver (GRL) Appliance | The whole off-grid survival appliance in one executable: local Reticulum stack, NMEA-0183 GNSS receiver and electronic compass (or static fallbacks), the shared zero-hop field assistant, and the captive survival dashboard a smartphone reads with no app installed. |
| [**gobot**](gobot.md) | One-Shot Bot CLI | Connects to a hub, privately asks a live bot one question (`/msg gobot ...`), prints the reply to stdout, and exits. |
| [**gorrcd**](gorrcd.md) | Reticulum Relay Chat (RRC) Hub Daemon | Standalone chat server supporting persistent rooms, key-protected channels (`+k`), direct client notices, and MOTD broadcasts. |
| [**golxmd**](golxmd.md) | LXMF Propagation Daemon | Standalone LXMF message routing and store-and-forward propagation node. |

---

## Device & Hardware Management

| Tool | Purpose | Description |
|------|---------|-------------|
| [**gornodeconf**](gornodeconf.md) | RNode Hardware Manager | Configure, bootstrap, sign, backup, and flash LoRa RNode devices over serial ports. |
| [**update-offline-data**](update-offline-data.md) | Embedded Data Refresher | Keeps the embedded public catalogs (NDBC buoys, NOAA tide stations, OurAirports airfields, the WMM notice) in step with their live sources, with an idempotent check that names the stations that changed. |
| [**gornode-diagnostics**](../guides/diagnostics.md) | Fleet Radio Diagnostics | Inspect and profile fleets of RNodes, reporting RSSI, SNR, hardware revisions, and firmware statuses. |

---

## Core Network Diagnostics & CLI

| Tool | Command | Description |
|------|---------|-------------|
| [**gornsd**](cli-utilities.md#gornsd) | `gornsd` | Core Reticulum transport daemon; manages physical interfaces and shared IPC instance. |
| [**gornstatus**](cli-utilities.md#gornstatus) | `gornstatus` | View interface traffic statistics, routing tables, and interface health. |
| [**gornpath**](cli-utilities.md#gornpath) | `gornpath <dest>` | Discover and display the active hop count and route to a destination. |
| [**gornprobe**](cli-utilities.md#gornprobe) | `gornprobe <dest>` | Probe reachability and calculate round-trip latency to a destination. |
| [**gornid**](cli-utilities.md#gornid) | `gornid` | Generate, import, export, and inspect 64-byte Reticulum cryptographic identities. |
| [**gornx**](cli-utilities.md#gornx) | `gornx` | Authenticated, encrypted remote command execution over Reticulum (`rnx`). |
| [**gorncp**](cli-utilities.md#gorncp) | `gorncp` | Secure peer-to-peer file transfer over Reticulum. |
| [**gornsh**](cli-utilities.md#gornsh) | `gornsh` | Authenticated, encrypted remote shell over Reticulum. |
| [**gorngit**](cli-utilities.md#gorngit) | `gorngit` | Git transport helper (`gogit-remote-rns`) allowing git clones and pushes over mesh links. |
| [**gorngcs**](cli-utilities.md#gorngcs) | `gorngcs` | Reticulum Signed Git (RSG) commit signature signer and validator (`rngcs`). |
| [**gornpkg**](cli-utilities.md#gornpkg) | `gornpkg` | Package manager for distribution of files and software bundles over Reticulum. |
| [**gornir**](cli-utilities.md#gornir) | `gornir` | Reticulum Distributed Identity Resolver daemon (`rnir`). |
