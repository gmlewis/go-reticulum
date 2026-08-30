# ASIC Plans — Hardware Offload for Reticulum on go-reticulum

This document captures the analysis (August 2026) of which parts of this
repo (go-reticulum) and gonomadnet are suitable for ASIC/FPGA offload, how
a chip would actually be designed and taped out with today's open tooling,
why TL-Verilog + Makerchip.com is the recommended design entry over raw
SystemVerilog, and how these pieces extend outward into a family of
privacy-first, maker-friendly RISC-V/ESP32 devices built on Reticulum.

The fixed-function hot spots are annotated in the source with
`ASIC suitability:` doc comments:

| Workload | Location | Why it offloads |
|---|---|---|
| LXMF stamp grinding (SHA-256 hashcash) | `go-reticulum/lxmf/stamper.go` (`GenerateStamp`, `StampValue`, `workblockMidstate`) | millions of independent SHA-256 candidates per stamp; no key material on-chip |
| Ed25519 sign/verify | `go-reticulum/rns/crypto/ed25519.go` | every announce, path token, proof, ratchet-file signature |
| X25519 ECDH | `go-reticulum/rns/crypto/x25519.go` | every link establishment and every packet decrypt (one trial per stored ratchet) |
| AES-CBC + HMAC-SHA256 (Fernet-style Token) | `go-reticulum/rns/crypto/token.go` | every encrypted LXMF/link payload |

---

## 1. Why these four (and only these four)

A workload is ASIC-suited when it is:

- **Fixed-function** — the algorithm never changes shape at runtime:
  fixed iteration counts (SHA-256's 64 rounds, the Montgomery ladder's
  255 steps, AES's 10/14 rounds).
- **Data-independent in control flow** — no branches on secret or packet
  data; the datapath is the same every cycle. This is what makes a
  pipelined/datapath design both possible and timing-safe (no
  side-channel surface from branch behavior).
- **Allocation-free** — the current Go implementations do all work in
  fixed-size buffers (signatures, 32-byte field elements, 16-byte AES
  blocks), so the hardware mapping is direct.
- **Embarrassingly parallel** — stamp candidates are independent;
  announces/proofs can be verified as they arrive on independent lanes.

Everything else in the stack (path tables, transport logic, the TUI,
LXMF routing state machines) is branchy, stateful, and low-volume — it
belongs in firmware on a small CPU, not in gates.

### Dominant cost: stamp grinding

On ESP32-class peers, `GenerateStamp` dominates: a target cost of N bits
means on average 2^N SHA-256 compressions, each over a ~4–8 KB workblock.
The Go port already optimizes this with a SHA-256 **midstate** restore
(`workblockMidstate` in `stamper.go`) so only the small suffix is
re-hashed per candidate — that exact trick maps 1:1 onto hardware: load
the workblock midstate once, then stream candidate suffixes through a
deep pipeline. A single SHA-256 pipeline at 200 MHz with one round per
stage is two orders of magnitude faster than software SHA-256 on an
ESP32, and the winning value costs nothing extra to collect.

Stamp grinding is also the *safest* first accelerator: the workblock is
public data, there is no key material on-chip, and a wrong answer is
rejected by the verifier — a fault or bug fails safe.

---

## 2. Chip architecture

A realistic RNode-class device:

```
+----------------------------------------------------------+
|  Host MCU (firmware: RNS Transport, LXMF router, app)    |
|    - key storage, IV generation, packet framing          |
|    - SPI or APB slave interface to the accelerator       |
+-----------------------------+----------------------------+
                              |
+-----------------------------v----------------------------+
|  Accelerator block(s)                                    |
|  +-----------+  +------------+  +----------+  +--------+ |
|  | SHA-256   |  | X25519     |  | Ed25519  |  | AES +  | |
|  | pipeline  |  | Montgomery |  | verify   |  | HMAC-  | |
|  | + lead-0  |  | ladder     |  | (sign    |  | SHA256 | |
|  | counter   |  | core       |  |  shares  |  | token  | |
|  | (stamps)  |  |            |  |  ladder) |  | pipe   | |
|  +-----------+  +------------+  +----------+  +--------+ |
|  Simple FSM + register file + interrupt/done semantics   |
+----------------------------------------------------------+
|  (optionally all behind one PicoRV32-class soft core     |
|   handling job queues, DMA, and clock domain crossing)   |
+----------------------------------------------------------+
```

Notes:

- **X25519 and Ed25519 share curve arithmetic.** The Montgomery ladder
  (one 256-bit word-serial multiplier + dual 256-bit register files,
  ~20–40k gates) serves X25519 key agreement directly; Ed25519 is the
  birational map of the same curve, so signing/verification reuse the
  same field multiplier. Build one field arithmetic unit, wrap two
  protocols around it.
- **The host MCU keeps all secrets.** Key storage, IV generation, and
  RNG stay in firmware; the accelerator is a stateless compute pipe over
  SPI — mirroring how RNode firmware already talks to the LoRa radio.
  This keeps the hardware security boundary small and the ASIC cheap to
  verify.
- **Job model**: one MMIO register file (start, data-in, data-out, done)
  per accelerator; interrupt-driven. Firmware DMA-spools workblocks for
  stamp grinding and small buffers for everything else.

---

## 3. HDL choice: TL-Verilog + Makerchip (recommended)

**Recommended design entry: TL-Verilog (Transaction-Level Verilog),
authored in [Makerchip](https://makerchip.com). NOT raw SystemVerilog.**

TL-Verilog is a strict superset of Verilog that adds *transaction-level*
abstractions: pipelines declared as `|stage` with automatic valid/ready
plumbing, `@1`/`@2` stage-relative signals, and `$when`-scoped state
groups — the pipeline-valid/handshake/latch-enable boilerplate that
dominates and riddles SystemVerilog designs is *generated by the syntax
itself*. For
the accelerators here this is an unusually direct fit:

- The SHA-256 pipeline is literally `|round` stages `@0..@63`, one
  `|stage` per compression round; TL-Verilog generates the
  register-enable and validity logic that would be hundreds of lines of
  error-prone `always_ff` plumbing in SystemVerilog.
- The Montgomery ladder is a small number of conditional transactions
  (`$mul_step`) — the states map naturally, and restructuring the
  schedule (e.g. word-serial vs radix-4) is a small edit instead of a
  datapath rewrite.
- M4 macro pre-processing (Makerchip's native extension mechanism)
  parameterizes the AES key length (128/256) and pipeline depth cleanly.

Why this beats plain SystemVerilog for this project:

| Concern | SystemVerilog | TL-Verilog |
|---|---|---|
| Pipeline handshake/valid plumbing | manual, the #1 source of bugs | generated by `|stage`/`@N` syntax |
| Refactoring a pipeline (insert/drop a stage) | touch every signal | change one number in a stage declaration |
| Learning curve for a software developer porting this repo | steep | gentle — reads like the dataflow it describes |
| Simulation in browser (no local toolchain at all) | no | yes, Makerchip runs in the browser |
| Downstream tools | everything | *everything that accepts Verilog accepts TL-Verilog* — it compiles to plain Verilog |

That last row is the decisive point: **TL-Verilog output is ordinary
Verilog**, so the entire flows below (Verilator, Yosys, OpenLane,
OpenROAD, TinyTapeout) consume it without modification. The open-source
tooling community has largely reached the same conclusion — the
[TL-Verilog](https://makerchip.com) / Redwood EDA ecosystem (Makerchip
and its course toolchains) is now the standard on-ramp for RISC-V
microarchitecture course work and hobby tapeouts, and reference cores
(`picorv32`/`VexRiscv`-class) have published TL-Verilog versions used in
production course material.

**Practical on-ramp**: prototype each core in Makerchip in the browser
(free), visualize waveforms and pipeline stages with Makerchip's built-in
diagram; download the generated Verilog; drop it into the Yosys/OpenLane
flow below. When a core outgrows the browser (long stamp-grind sims),
move the same source to a local Verilator + cocotb testbench.

SystemVerilog remains the right choice where TL-Verilog's model doesn't
reach: clock-domain-crossing blocks, SPI pad rings, chip-level I/O and
constraints (`SDC` files) — those stay SV, but a project doesn't need
much of them: one CDC per accelerator domain and one SPI slave.

## 4. Toolchain (all open source, end to end)

| Stage | Tool |
|---|---|
| Design entry | **TL-Verilog in Makerchip** (browser) or local `tlv` → Verilog |
| Reference sim (fast, C++) | Verilator + cocotb (Python testbench, driven by the Go vectors) |
| Lint | Verilator lint, `svlint` |
| Synthesis (RTL → gates) | **Yosys** (via OpenLane) |
| Floorplan / place / route / CTS | **OpenROAD** (via OpenLane or standalone) |
| DRC / LVS | Magic, Netgen (via OpenLane) |
| PDK | **SkyWater sky130** (fully open, TinyTapeout-compatible, 130 nm — fine for 200 MHz pipelines; ~15 mm² is overkill here, the whole accelerator fits in a few mm²) or **IHP SG13G2** (open 130 nm SiGe BiCMOS, faster transistors, also TinyTapeout-compatible via ChipFoundry) |
| GDS viewer | KLayout, Magic |

Everything above runs on macOS/Linux without licensing: the full
RTL-to-GDS flow is a few shell commands in OpenLane once the Verilog
exists.

### Test vectors straight from the Go implementation

Because these algorithms are already implemented and tested in Go, the
cocotb testbenches should use **actual vectors from the Go code**:
run a small Go helper (e.g. a `cmd/` tool) that prints
`sha256(workblock‖suffix)` triples, X25519 exchanges, Ed25519 signatures
and Token seals in a simple hex format, and feed those to the HDL sim.
That closes the loop "FPGA/ASIC output must match the Go port byte for
byte" with zero manual vector transcription — the same pattern this
repo already uses for Python-vs-Go parity testing.

---

## 5. Tapeout paths

| Path | What you get | Cost ballpark | Fits |
|---|---|---|---|
| **TinyTapeout** (sky130 or IHP SG13G2, via ChipFoundry) | a few hundred gates × small tiles — one accelerator core (e.g. SHA-256 pipeline + leading-zero counter) | ~$300 per tile | prototype / education |
| **ChipFoundry / efabless-style shuttle** | a full multi-mm² die: all four accelerators + PicoRV32 + SPI | low-to-mid four figures per shuttle slot | the real RNode-crypto chip |
| Commercial foundry | production volumes | five figures+ | only if productized |

Recommended progression:

1. **Phase A — Makerchip prototypes** (weeks): SHA-256 stamper, X25519
   ladder, Ed25519 verify, AES/HMAC token, all validated against Go
   vectors in makerchip/Verilator.
2. **Phase B — TinyTapeout tile** (a month or two): the stamp-grind core
   alone on a TT tile. It needs no key material, is fault-tolerant by
   design (the verifier rejects bad stamps), and delivers the biggest
   firmware speedup on ESP32-class peers.
3. **Phase C — integrated accelerator**: all four cores behind one
   register-file job interface + PicoRV32, taped out on a shuttle,
   with the SPI firmware driver added to RNode-class hardware.

Once silicon exists, go-reticulum gains an `Offloader` interface next to
`rns/crypto` (Go side): `Sign`, `Verify`, `Exchange`, `Seal`, `Stamp`
backed either by the in-process implementations (default) or by the
SPI device — firmware changes stay minimal and the software path remains
the fallback.

---

## 6. Beyond the chip: RISC-V/ESP32 "privacy IoT" building blocks for makers

The accelerators in sections 1–5 are the tip of a much bigger opportunity:
turning Reticulum into the *nervous system* of a family of open, DIY-friendly
home devices that hobbyists can assemble, trust, and own outright — the kind
of devices that, today, ship as Ring doorbells, cloud security panels, and
smart-home hubs whose firmware phones home to someone else's servers.

### 6.1 The core insight: identity-first hardware, not app-first hardware

Commercial IoT products get privacy backwards: the device is provisioned
against a vendor account, and everything it knows is stored in the vendor's
cloud by default. A Reticulum-native device inverts this — and RNS gives you
the inversion for free:

- **The device IS an identity.** Each sensor, camera, or actuator is
  provisioned once with a hardware-seeded RNS identity (RNG-seeded keys in
  eFuse / secure element like the ATECC608A, or a factory-flashed seed the
  owner can rotate). Its `lxmf.delivery` / `nomadnetwork.node` destinations
  are self-authenticating addresses.
- **The network is the account system.** Sharing "my front door camera with
  my phone" is copying a 32-hex-character hash into the phone's Directory —
  not creating an account, granting OAuth scopes, or trusting a relay.
  Destination allow-lists replace "households with member accounts."
- **Announces are presence, not telemetry.** A device that never sends its
  data anywhere still participates in the mesh; data flows only along links
  the owner established, end-to-end encrypted, with forward-secrecy ratchets
  already in this codebase.
- **"Cloud" is the owner's hub**, not a vendor's fleet: any always-on node
  (a mini PC, a Jetson, a NAS, even the upstairs hub device) runs a
  propagation node + shared instance, holding LXMF messages, page content,
  and video clips *for a home that owns them*.

This is why the "Zen of Reticulum" maps so directly onto this problem
space: no service providers, no trusted intermediaries, no central
authority, transport-level anonymity options, and crypto that assumes an
adversarial medium. A device built this way literally *cannot* leak your
doorbell video to a breach — it has nowhere to send it.

### 6.2 The hardware family: four building blocks, one firmware stack

Design the family as standard-ish "building blocks" — the ESPHome/Feather
lesson, applied to private networking. Makers should be able to snap a
sensor block onto any other block and have it just work, because identity,
encryption, and discovery are layer-7 properties of Reticulum, not
per-product firmware.

| Block | Silicon | Radio(s) | Role |
|---|---|---|---|
| **Node** (the root of trust) | ESP32-C6 or ESP32-P4 (dual-core RISC-V) | LoRa (SX1262/SX1276, RNode firmware) + WiFi/BLE/802.15.4 | RNode radio + shared instance + propagation node; the house's RNS anchor. Optional: the TinyTapeout accelerator riding along as a stamp/verify coprocessor |
| **Leaf** (sensors & actuators) | CH32V003-class RISC-V (10-cent tier) up to ESP32-C3/C6 | LoRa (sleepy, duty-cycled), optionally built-in 802.15.4/WiFi/BLE | Temperature, humidity, light level, weather, door/window contacts, motion, relays, lighting control. Weeks of battery on slow LoRa announce cadence; in-home Leafs can lean on the radio already on the chip (see 6.3) |
| **Eye** (cameras) | ESP32-P4 (H.264 encoder, dual-core RISC-V) or a Pi/OrangePi-class RISC-V SBC (e.g. Milk-V/StarFive) | Built-in WiFi for in-home streaming, LoRa for wake/alert | Doorbell, motion-triggered captures. See the bandwidth reality in 6.3 |
| **View** (displays & UX) | ESP32 with e-ink; or any Linux SBC running this repo's Go NomadNet | LoRa for alerts, WiFi/LAN for rich UI | Wall panels, room controllers, doorbell screens — NomadNet pages as the UI layer, already designed for exactly this |

Two deliberate software decisions make the family coherent instead of a
pile of boards:

1. **One firmware core, reused everywhere.** The RNode firmware's radio
   driver + RNS framing, extended with per-block sensors, becomes a common
   firmware substrate (ESP-IDF + Arduino-legacy support), so a weather
   Leaf and a doorbell Eye differ only in their driver layer and announce
   app_data payload. go-reticulum serves as the *reference implementation*
   for interoperability testing: every wire format a Leaf emits must parse
   identically in this Go stack, exactly the way the Python SOT gates the
   Go port today.
2. **Linux blocks run this repo natively.** The View/root tiers run
   gonomadnet and the Go RNS stack as-is (it already runs on a Mac Mini,
   a laptop, and Linux SBCs). That gives the whole family a real,
   already-written UI, message store, and browser — no need to invent a
   companion app.

### 6.3 The bandwidth honesty clause (camera reality)

LoRa is extraordinary for battery-year sensors (announce + a few bytes of
telemetry per minute at 868/915 MHz), but it carries ~1.5–19 kbps — it will
*never* stream video, and pretending otherwise poisons the design. The
honest split, which Reticulum's interface-agnostic transport makes trivial:

- **Alerts, commands, sensor frames, arm/disarm, page fetches** → LoRa,
  end-to-end encrypted through the mesh even with no WiFi in the house
  (this is the killer feature: the security system that still works during
  an internet outage, or after an intruder cuts the ISP line).
- **Camera frames/streams** → WiFi/Ethernet between devices that share a
  LAN, again as RNS packets (TCP/UDP/auto interfaces) — still
  end-to-end encrypted and identity-addressed, so the video never transits
  infrastructure just because it transits a router. Motion-triggered
  stills can even *piggyback onto LoRa* as RNS resources (a ~10 KB frame
  is a long-ish but feasible resource transfer at 19 kbps if needed).
- **Recorded video storage** → the house's Node-tier hub: LXMF delivery +
  RNS resources to any destination the owner has authorized, from anywhere
  (phone on cellular included, via the hub's uplink).

#### The free radios already on the chip

A large part of this bandwidth story costs nothing extra, because the
ESP32-class parts that power the family already carry the radios onboard:

- **WiFi (2.4 GHz) is built into ESP32-S3/C6/P4 and most RISC-V-class
  ESP32s.** For in-home video this is the whole answer: an ESP32-P4
  doorbell encoding H.264 can push a viewable stream to the Node hub
  over onboard WiFi as plain RNS TCP/UDP interface traffic — no extra
  hardware, no cloud, no vendor app, only packets between two
  identities the owner controls. The "camera reality" above is
  therefore not aspirational: the streaming leg is a feature of the
  parts hobbyists already buy.
- **BLE is built in on the same dies** and is the natural in-home
  complement to LoRa: an order of magnitude more bandwidth than LoRa at
  a small fraction of the energy for short ranges. For a battery Leaf
  two rooms from the hub, BLE is often the right physics for telemetry
  and even small page/resource transfers — wake, burst, sleep — while
  LoRa remains the long-range, no-WiFi-needed fallback. And because a
  phone speaks BLE, a BLE interface gives owners a wire-free way to
  commission a fresh Leaf ("adopt" it into the house's RNS directory)
  without any infrastructure at all.
- **802.15.4/Zigbee-class radio** (on ESP32-C6) offers a third in-home
  option and bridges to the existing ESPHome/sensor-sensor ecosystem,
  but the RNS-native paths above (WiFi + BLE + LoRa) already cover the
  family's needs; treat 15.4 as an interop nicety, not a dependency.

The design rule that falls out: **use the cheapest radio that reaches,
for each block, and let Reticulum's interface-agnostic routing paper
over the mix** — a house is routinely a Leaf talking BLE to the hub
while a gazebo Leaf talks LoRa across the yard, and both are just
destinations.

### 6.4 BLE support in go-reticulum without CGo: the external-interface trick

Worth capturing explicitly, because it removes a real limitation of this
repo's Go port: BLE was deliberately left out of go-reticulum's interface
set because Go BLE support means CGo bindings and dependencies far
outside the standard library — an ongoing build/portability tax this
repo intentionally avoids. But the family architecture in 6.2 suggests
the Go port can still *speak BLE* without ever touching it:

- The trick is the one RNode already proved: **Reticulum treats an
  external interface device as just another interface.** An ESP32-C6 or
  RISC-V leaf with a BLE module runs a few hundred lines of firmware
  that exposes a GATT transport service (write/notify characteristics,
  roughly MTU-sized framing) and bridges those frames to the host over
  SPI or UART — the exact electrical and framing role the RNode firmware
  plays for LoRa.
- On the Go side this needs **no CGo and nothing beyond the standard
  library**: it is a byte-stream Interface (the same shape as the
  existing TCP/UDP/serial interfaces), fed by SPI or a serial port
  (`os.File`/`iox` reads) or by an SPI device node. All the BLE-specific
  complexity — advertising, GATT, connection intervals, pairing — lives
  in firmware on the remote chip, where it belongs.
- This yields a clean layering: **"BLE RNode"** = external BLE modem
  firmware (the RNode pattern, applied to a different radio), and
  go-reticulum gains one new stdlib-only `Interface` implementation
  (`rns/interfaces/...` on an SPI/serial transport). Phones, laptops
  with native BLE, and ESP32 leaves all reach the house Node over BLE
  while the Go stack stays dependency-shy.
- The same pattern generalizes: any radio whose modem firmware can
  bridge frame-oriented transport over SPI/UART (LoRa RNode, BLE, even
  a future 802.15.4 module) becomes reachable from a pure-Go RNS. The
  repo's job stays "speak Reticulum over a stream"; radio physics stay
  somebody else's well-tested problem.

Practical notes if this is pursued: GATT MTUs are ~244 bytes of usable
payload, so the bridge must do small-frame reassembly (RNS packets are
already framed; this is the same job RNode firmware does for LoRa's
255-byte limit), and connection intervals push real BLE throughput into
the tens-to-hundreds-of-kbps range — squarely in "in-home commands,
telemetry, thumbnails" territory, perfectly complementary to the WiFi
streaming leg above.

### 6.5 What "replace Ring" concretely looks like

A front-door security stack from four blocks, all owner-controlled:

1. A **Leaf** battery door/window sensor announces via LoRa on open/close.
2. The **Eye** doorbell captures H.264 to the **Node** hub over WiFi,
   drops a thumbnail resource on LoRa, and delivers an LXMF notification.
3. The owner's phone (via gonomadnet/NomadNet) gets the notification over
   RNS from the hub — no push service, no app store, no vendor.
4. **View** wall panel shows the door camera page on demand; arming is a
   link to the Leaf's destination, not a cloud API.
5. Everything keeps working through internet outages; nothing is visible
   to any third party; moving house means re-announcing, not re-onboarding
   to a new vendor cloud.

The same four blocks cover weather stations, grow-room monitors, lighting
scenes, presence-based automation, workshop telemetry, greenhouse
controls — which is the point: the *stack* is the product; each
application is a BOM and a `micron` page away.

### 6.6 Where the ASIC fits the family (and where it doesn't)

- **Doesn't yet**: Leaves and Eyes today are fine in pure software —
  X25519 per link + occasional announcements are microseconds-to-milliseconds
  on an ESP32-C6; stamp costs for casual messaging are small.
- **Does, for three cases**: (a) propagation/relay Nodes that grind
  meaningful stamp costs for the mesh's anti-spam tier — the biggest pain
  point on ESP32 today; (b) energy-constrained battery Leaves, where
  compute energy is radio energy; (c) supply-chain trust: a small, fixed,
  auditable crypto block (TL-Verilog, tested against this repo's vectors)
  is *easier* to reason about than a firmware crypto stack on a flashed-and
  -forgotten device. Hence the phased plan in section 5: TinyTapeout the
  stamper first, and let it plug into the Node block as a Feather/HAT
  coprocessor **before** it is ever hard-integrated — the family gets its
  benefits early, and silicon only hardens what has already proven itself
  in maker hands.

### 6.7 Community and credibility path

- **Kit-first, standard connectors** (Qwiic/Stemma QT for peripherals,
  Feather/HAT form factors for the Node) so early adopters extend with
  parts they already own; ESPHome/Home Assistant interop via a bridge
  block gives the existing DIY crowd a zero-throwaway on-ramp.
- **Interop is the moat**: every block speaks stock Reticulum, so it
  coexists with genuine RNodes, Python NomadNet, Meshtastic-adjacent
  hobbyists, rnsh/remote-admin, and rncp file drops from day one — the
  ecosystem is bigger than any one vendor's line, by design.
- **Security story by construction**: reproducible firmware builds,
  signed releases, owner-controlled identity rotation, and the fact that
  the whole protocol stack is auditable open source — "your data isn't
  on our servers" is literally true because there are no servers,
  only *your* hub.

### 6.8 What to build first (opinionated)

1. **Node block as a "super-RNode"**: ESP32-C6 + SX1262 + SD card +
   optional accelerator Feather, running RNode firmware today and the
   propagation/shared-instance role as firmware matures. It is useful
   standalone *tomorrow* (every existing RNS tool works with it).
2. **One battery Leaf** (temp/humidity/door contact) with a publish +
   LXMF-alert firmware template — the template is the deliverable; the
   community will fork the rest.
3. **Eye doorbell** with still-capture to the Node hub — the flagship
   "replaced a commercial product" demo, and the honest demonstration
   of the 6.3 bandwidth split.
4. The ASIC stamper (section 5 Phase B) rides along as optional
   acceleration once Node blocks exist to host it.

---

## 7. Embedded gonomadnet: the whole Go stack as bare-metal firmware

Everything above treats the accelerators as coprocessors riding along with
hosts that already run an OS. This section takes the hypothetical to its
logical end: the shuttle succeeds and a follow-on chip integrates the four
crypto cores **on the same die as a RISC-V (or ESP32-class) CPU** with
enough RAM to be a real node — no Linux, no Darwin, no OS at all. What
would it take for *this* repo and gonomadnet to run as firmware on that
chip?

Both repos were surveyed (August 2026) for their OS-facing surface; every
claim below cites a real file. Headline findings before the detail:

- **Dependency debt: none in go-reticulum.** Its `go.mod` has *zero*
  `require` lines and there is **no cgo anywhere** — the entire library
  is stdlib + intra-repo packages. The only raw syscalls in the tree are
  two termios `ioctl`s in the serial interface layer. This is the single
  most important fact for an embedded port.
- **The seams are narrow but real**: `rns.ParseConfig(io.Reader)` for
  config, the `interfaces.Interface`/`BaseInterface` contract plus
  `NewPipeInterface` for transports, the `Logger` callback seam, an
  injectable RNG in `newResourceWithOptions`, and
  `NewAppWithTransport`/daemon mode on the gonomadnet side.
- **The sprawl is storage**: ~120 direct `os.*` call sites in
  go-reticulum, ~200 in gonomadnet, no filesystem abstraction in either,
  and four independent `tmp`+`rename` atomic-write schemes that assume
  POSIX rename semantics.

### 7.1 The Go-target question comes first, and it gates everything

Stock Go has **no bare-metal target**: the runtime assumes an operating
system (OS threads, mmap-based memory management, OS timers, netpoller).
"A valid Go target" therefore means one of three things:

| Path | Silicon class | How Go runs | RAM floor | Port fidelity |
|---|---|---|---|---|
| **Unikernel / Linux ABI** (pragmatic) | MMU application-class RISC-V core (CVA6/Rocket-class SoC, e.g. a StarFive/Milk-V-class part) | stock Go, `GOOS=linux riscv64`, on a Linux-syscall unikernel (Unikraft-class) or a tiny kernel carrying the syscall ABI | 32–64 MB | **100%** — literally the binaries that run on a Mac Mini today, including the full TUI |
| **TinyGo** (MCU-class) | ESP32-C6 (RV32 @ 160 MHz, 512 KB SRAM), ESP32-P4 (dual RV32 @ 400 MHz, 768 KB SRAM + up to 32 MB PSRAM) | TinyGo runtime: its own scheduler (goroutines work), conservative non-moving GC, per-target `GOOS` (e.g. `//go:build esp32c3`) | 256 KB–8 MB | partial — see below |
| **Custom Go runtime port** | any | port the GC/scheduler/syscall layer yourself | — | research project, not a plan |

The concrete TinyGo risk list falls straight out of the import closure of
the two repos:

- `rns/msgpack` uses **`reflect`** — supported by TinyGo only partially,
  and this is the one library-closure use that is load-bearing on every
  packet. Much of it is `OrderedMap`-specific and could be rewritten as
  static per-type paths, but that is a correctness-sensitive rewrite.
- The non-`cmd` closure also reaches `math/big` (rns) and `image`
  (via `qr`→PNG). Drop the PNG encoder on-device (QR *cells* still
  render fine); audit the `math/big` use.
- TinyGo's GC is conservative and non-moving. This matters because
  `cmd/gonomadnet/memlimit.go` already measured the app: **live heap is
  single-digit MB, with high allocation churn** in the draw path and the
  per-announce persistence path. Single-digit MB live heap fits in PSRAM;
  *high churn under a weak GC* is the actual risk, not the live set.
- Concurrency shape is favorable: ~130 goroutine spawn sites across
  go-reticulum's read/maintenance loops but a single bounded jobs loop in
  gonomadnet and only ~15 raw `go func` sites in its tui+app code —
  goroutine-light, not fan-out, which TinyGo can (carefully) carry.

**Recommendation**: the unikernel build is the reference target (full
parity, proven by the byte-diff harnesses in section 7.7), and TinyGo on
a PSRAM-equipped P4-class part is the Leaf/relay-firmware target.

### 7.2 Hardware endpoints in go-reticulum: the Interface layer is already the seam

The radio/transport work is the *least* changed part of the stack, because
the RNode pattern (section 6.4) already established it and the `Interface`
plug point already exists:

- `rns/interfaces/interfaces.go:63` defines one large `Interface`
  contract; `rns/interfaces/base.go:75` `BaseInterface` implements the
  bulk of it, so a new transport embeds it and overrides
  `Send`/`Detach`/`Status`/`Type`. The repo convention for OS-specific
  files (`-unix.go` implementation + `-other.go` "unsupported" stub)
  means the tree already compiles on non-Linux/Darwin — an `embedded` /
  TinyGo-per-target tag set (TinyGo sets per-target `GOOS` values like
  `esp32c3`, so `//go:build esp32c3` works naturally) extends this
  pattern; no new mechanism is needed.
- **The framing codecs are pure**: `rns/interfaces/hdlc.go`, `kiss.go`,
  `ax25-kiss-*`, and `rnode-radio.go` (all the `RNodeSetFrequency` /
  `RNodeValidateRadioState` / bitrate builders) have zero OS imports.
  `rnode-state.go` is a pure state machine. On the ASIC these run
  unchanged.
- **The on-die radio collapses the RNode protocol, it doesn't remove
  it.** Today `RNodeInterface` exists because the LoRa modem is a
  separate MCU spoken to over serial (`rnode-unix.go`, the only file
  with the termios ioctls). On the ASIC the radio/modem block sits
  behind the same SPI-or-MMIO register interface — same RNode framing,
  same LoRa state machine, minus the serial transport. A
  `rnode-embedded.go` (or `spi-lora.go`) implementing `Interface` on
  on-die SPI is a few hundred lines over pure code that already exists.
- **What gets dropped at compile time**: every TCP/UDP/UNIX/Auto/I2P/
  pipe-subprocess interface is built on `net` (~22 symbols across the
  family) or `os/exec` (`discovery.go` `LocationCmd`/`reachable_on`,
  `pipe-subprocess.go`). Under the embedded tag set, build them out
  (`//go:build !embedded` on those files) and ship **LoRa + KISS/HDLC +
  SPI/UART** as the interface set. An lwIP-backed `UDPInterface`/
  `TCPClientInterface` is the later tier for in-home WiFi — add it when
  the Node-tier product needs it, not for bring-up.
- Bring-up transport for firmware development is `NewPipeInterface`
  (in-memory loopback) plus a UART byte-stream `Interface` — the same
  shape as section 6.4's BLE-over-SPI bridge, which is really the same
  feature with a different radio on the other end.

One governance note: `Transport` (`rns/transport.go:126`) is itself an
interface with `Start(storagePath string)`. A minimal in-memory transport
implementing dispatch + no-op persistence would be the fastest route to a
running (lossy, non-persistent) firmware node for interop testing, before
the storage layer below is finished.

### 7.3 Storage: the real work is not "an SSD", it is atomicity and wear

The intuition "file operations need some other mechanism" is exactly
right, and the survey sharpens it into a design rule: **a raw SD card
(FAT32, backed by the card's own wear-leveling and real rename support)
is the pragmatic storage answer, and choosing it dissolves roughly half
the problem** — because both repos assume POSIX semantics, and FAT
provides the ones they actually use. The alternative path (NOR flash +
littlefs-style log-structured FS) is smaller/faster but has no rename,
which forces a redesign of the atomic-write layer rather than a port.

**go-reticulum inventory** (no choke point — this is the largest
mechanical obstacle in either repo):

- ~120 direct `os.*` file-I/O sites, concentrated in `rns/transport.go`
  (~48), `lxmf/router.go` (~25), `rns/discovery.go` (~17), all composing
  raw string paths via `path/filepath`.
- The storage *root* is already injected — `Transport.Start(storagePath)`
  receives it from `rns/rns.go` — so path redirection is free; the
  *calls themselves* are not injectable.
- Four independent atomic-write implementations (canonical reusable one
  at `lxmf/message.go:1139` `atomicWriteFile`; duplicates in
  `rns/transport.go:4244`, `rns/destination.go:387`,
  `rns/blackhole-updater.go:392`), all `tmp`+`os.Rename`, with
  `os.Getpid()`-derived tmp names.
- Layout assumptions: `rns/rns-config.go` `ensureStartupLayout` creates
  the 7-directory tree (`storage`, `storage/cache{,/announces}`,
  `storage/resources`, `storage/identities`, `storage/blackhole`,
  `interfaces`); LXMF keeps a one-file-per-message propagation store
  (`lxmf/router.go` `writePropagationMessageFile` / reindex walk).
- Seams that already exist and should be preserved/extended:
  `rns.ParseConfig(io.Reader)` (`rns/config.go:51`) — config is fully
  divertible to a flash blob or constant; `rns/logger.go`
  `SetLogDest`/`SetLogCallback` — zero file I/O needed if a callback is
  set; `newResourceWithOptions(..., randRead)` — the RNG seam that maps
  to the hardware entropy peripheral; clock injections in
  `backbone.go`/`blackhole-updater.go`/`lxmf/peer.go`.

The work item that makes both this *and* every future target tractable:

**Create an `rns/storagefs` (or repo-root `fsops`) package** with a small
filesystem interface (Read/Write/List/Stat/Delete/Mkdir) and two
backends:

1. `osfs` — thin pass-through to `os.*`, byte-identical behavior, routed
   behind golden tests (the existing Python-parity discipline applies:
   every hosted build must produce byte-identical on-disk state). All
   ~120 call sites migrate to it; this alone is a large but mechanical
   diff whose behavior change is nil.
2. An embedded backend — either FAT-over-SD (rename works; recommended)
   or a journaling record store over raw flash, where atomicity comes
   from checksummed append-with-replay (littlefs-style) instead of
   rename, and the ~10–20 `os.Rename`/`os.CreateTemp` sites collapse
   into backend-internal transactions.

Also centralized there: path *layout*. The 7-directory RNS layout and
gonomadnet's 9-directory layout (`nomadnet/storage/storage.go`) are
hosted conventions; an embedded build can flatten them behind the
interface without touching callers. The on-disk formats themselves are
fine for flash: both repos' stores are msgpack (`rns/msgpack.OrderedMap`—
already the BIN-key-corrected format), CBOR (`rrc`), and a hand-rolled
INI parser that takes an `io.Reader`-shaped seam.

**Flash-wear items regardless of backend** (all already identified in
hosted profiling, conveniently):

- `nomadnet/directory` persists eagerly on every `Remember` — batch it.
- Per-announce known-destination re-saves (flagged in
  `cmd/gonomadnet/memlimit.go` as an allocation-churn source too).
- The LXMF propagation store's per-message file create/rename churn
  wants a log-structured replacement on any flash backend.

### 7.4 Crypto: the Offloader stops being optional

Section 5 introduced an `Offloader` as a nicety for hosts. On the ASIC
the four accelerators are *the only* crypto engine and the software path
is the bring-up/fallback. Because `rns/crypto` has **no provider pattern
today** — nine files, ~700 lines, free functions and thin struct wrappers
over stdlib — the work item is to put the seams where the hardware is:

- **Packet envelope**: `crypto.Token` (`token.go`) is the single choke
  point for AES-CBC+HMAC — give it a pluggable backend (software default;
  hardware token pipe). Note the repo's AES is **CBC, not GCM** — the
  accelerator must match this (which section 2's token pipe already does).
- **Signatures/ECDH**: `Ed25519`/`X25519` key types in `ed25519.go` /
  `x25519.go` — add `Sign`/`Verify`/`Exchange` dispatch through the
  Offloader, keyed off destination, so per-ratchet packet decrypts hit
  the Montgomery ladder.
- **Stamps**: `lxmf/stamper.go` already restores the SHA-256 midstate —
  route the candidate loop through the Offloader's stamper job interface;
  the `workblockMidstate` optimization maps 1:1 onto the section 2
  pipeline.
- **Entropy**: `crypto/rand` reads are spread wide; centralize behind the
  `randRead` seam (`rns/resource.go:530` is the existing precedent) and
  point it at the hardware RNG.
- On-die, the section 2 job model is unchanged — the SPI driver becomes
  an MMIO register driver, and the Go-generated vectors from section 4
  double as the firmware's own acceptance tests. Software fallback stays
  under a build tag (or a runtime capability flag) both for bring-up and
  for cross-compiling the same firmware image for non-ASIC boards.

### 7.5 gonomadnet on-device: what survives, what doesn't

The structural news from the gonomadnet survey is unexpectedly good:

- **The seam is already cut.** The app-core packages (`nomadnet/app`,
  `nomadnet/browser`, `nomadnet/node`, `nomadnet/directory`,
  `nomadnet/config`, `nomadnet/conversation`, `nomadnet/rrc`,
  `nomadnet/micron`, …) import **zero tview/tcell**. `nomadnet/micron`
  imports only `strings` in non-test code and ships two renderers — one
  tview-destination, one plain-text — so the markup engine is already
  display-agnostic. `cmd/serve-page` (node serving + micron + RNS, no
  app, no config, no TUI) is a working proof of the headless path, and
  `gonomadnet -d` daemon mode runs the real stack with no terminal at
  all (it is force-selected whenever stdin isn't a TTY).
- **One reverse edge to sever**: `nomadnet/app/channels-adapters.go`
  (~60 lines, the only core→TUI import; it mirrors the SendDeps
  injection pattern). Moving the `HubView` interface down into the core
  fully separates the layers.
- **A non-terminal display is a real path, not a rewrite.** tcell's
  `Screen` is an interface (~15 methods) with
  `tcell.NewSimulationScreen` as a working in-memory reference
  implementation, and `tview.Application.SetScreen()` is a public,
  pre-`Run()` injection hook — no tview changes needed. Better: the
  tcell fork's per-cell dirty rendering (unchanged cells skipped in
  `drawCell`) is precisely the behavior a slow e-ink or serial display
  backend wants, and the tview fork's `fullRedraw` mode already avoids
  full-screen clears. An e-ink/LCD `Screen` implementation is a
  board-support-package task, small enough to live next to the drivers.
- **What carries vs. what doesn't in `tui/`** (33.5k LOC, 119 files,
  one flat package, all terminal-parity-specific): the *logic* carries —
  `debouncer.go`, `glyphs.go`, `palette.go`, `borders.go`,
  `formatters.go`, the micron-view renderer wiring. The urwid-port
  widget set (`urwid-button/checkbox/columns`), mouse capture, clipboard
  (`golang.design/x/clipboard` — the one heavyweight dependency chain:
  purego/shiny/mobile/x11), PTY embedded terminal (`creack/pty`,
  `tui/vterm.go`), and every `os/exec` launcher do not, and should be
  compiled out under the embedded tags. Keep `rsc.io/qr` (pure encoder)
  — QR identity exchange is arguably *more* useful on a display than on
  a terminal.
- **The honest scope decision**: a full TUI on a P4-class MCU is the
  stretch goal; the *first* embedded UI should be the View-tier pattern
  from section 6.2 — render micron pages (`view-mu` model: fetch →
  `micron.Parse` → styled-chars/framebuffer draw) with a link cursor and
  a reduced message view, driven by buttons/wheel. That needs `micron`
  + `browser` + a framebuffer — not the 33.5k-line widget library.
  The full `tui/` is the unikernel target's inheritance.
- **Memory reality check** (drives silicon choice, not effort):
  live heap is single-digit MB (`memlimit.go`), plus one bounded jobs
  loop and per-interface read loops — comfortable in a 64 MB unikernel;
  feasible in 8 MB PSRAM only if the draw-path churn is trimmed first
  (the same churn fixes as the flash-wear list); not feasible in
  512 KB-class parts. A relay/propagation Leaf (headless, no TUI) is the
  natural first MCU target since its working set is dominated by the
  transport tables, which section 7.3 already resizes for flash.

### 7.5.1 The go-runtime sub-question for TinyGo

If the MCU-class path is pursued, three library-closure facts become
work items: (a) `rns/msgpack`'s reflect-driven pack/unpack → rewrite
OrderedMap serialization as static per-type code (also a mild
performance win everywhere); (b) drop `image`/PNG out of `qr`'s import
closure on-device (already pure-Go separable); (c) `compress/bzip2` and
the hand-rolled `rns/msgpack` are allocation-heavy — acceptable on
PSRAM-class parts, and stamp workblocks already stream rather than
accumulate. None of these affect the unikernel path.

### 7.6 Checklist, repo by repo

| # | Work item | Repo | Size | Notes |
|---|---|---|---|---|
| 1 | `fsops`/storage VFS package + `osfs` backend; migrate ~120 + ~200 `os.*` call sites behind golden tests | both | large, mechanical | the enabling refactor for *any* non-POSIX storage; behavior-neutral on hosted builds |
| 2 | Embedded storage backend (FAT/SD first; journaling-flash second); collapse rename-based atomicity into backend | both | medium, subtle | new package only; call sites already migrated in #1 |
| 3 | Crypto provider/`Offloader` seams: Token, Ed25519/X25519, stamper, RNG | go-reticulum | small | `rns/crypto` is 9 files; §5's interface design carries over |
| 4 | Hardware `Interface` impls: on-die SPI LoRa + UART/KISS byte-stream (rnode-embedded); build-tag the `net`-family interfaces out | go-reticulum | medium | framing/protocol code already exists and is OS-free |
| 5 | Build-tag scheme: extend the established `-unix`/`-other` pattern with TinyGo per-target tags (`esp32c3`, …) plus a repo-wide `embedded` tag; stub the 3 `os/exec` sites | go-reticulum | small | the tree already compiles with `-other.go` stubs on unsupported platforms |
| 6 | msgpack reflect → static codecs (TinyGo gate); drop PNG from `qr` closure on-device | go-reticulum | medium | nil behavior change on hosted builds |
| 7 | Time/RNG/logging seams: centralize `time.Now` for tick-driven maintenance loops, route entropy + log through injected providers | go-reticulum | medium | logger seam already exists (`SetLogDest`/`LogCallback`) |
| 8 | Sever the one core→TUI edge (`app/channels-adapters.go`, 60 lines) | go-nomadnet | trivial | HubView interface moves down |
| 9 | Flash-wear batching: directory eager-persist, per-announce re-saves, propagation store | go-nomadnet | medium | benefits hosted nodes too |
| 10 | Display `tcell.Screen` backend (e-ink/LCD framebuffer) + input (buttons/serial), injected via `tview.SetScreen` | forks/ firmware | medium | SimulationScreen is the reference; tcell per-cell dirty checking is e-ink-friendly |
| 11 | Firmware substrate / board-support repo: TinyGo target defs, linker scripts, startup, peripheral drivers (SPI radio, SD/FAT or flash FS, display, entropy, RTC) | new | medium | where the "hardware shims" physically live |
| 12 | Reduced micron-first UI for MCU builds (fetch → parse → framebuffer) | go-nomadnet | medium | reuses `micron` + `browser` wholesale |

### 7.7 A phased path that reuses this repo's parity discipline

1. **Phase E1 — seams on hosted builds** (behavior-neutral): storage
   VFS (#1), crypto/Offloader seams (#3), `os/exec` stubs. Golden tests
   prove byte-identical on-disk state and wire bytes. This phase pays
   for itself in testability alone.
2. **Phase E2 — embedded node, headless**: boot the app-core +
   `serve-page`-shaped path on a Linux-ABI RISC-V target (QEMU → real
   board) under the unikernel path; reuse the exact loopback A/B
   comparator (`tooling/parity-ab.sh`, `cmd/serve-page`) to diff a
   firmware node against a desktop node — the /parity tooling was built
   for exactly this class of "same bytes out of both" verification.
3. **Phase E3 — MCU-class firmware**: TinyGo build, FAT/SD storage,
   LoRa `Interface`, micron-first UI, software crypto — a complete
   Leaf/relay node.
4. **Phase E4 — on-die crypto**: flip the provider seams to the
   accelerators; the section 4 Go-vector tooling now serves as the
   firmware bring-up testbench.
5. Every phase keeps the section 6.2 rule: any firmware build must parse
   and interoperate, byte for byte, against both this Go stack and the
   Python SOT — the port is only done when the parity harness says so.

---

## 8. Summary

- Offload targets, in priority order: **stamp grinding** (dominant,
  safest), **X25519/Ed25519** (shared field arithmetic), **AES+HMAC
  token** (library block). Everything else stays in firmware.
- Design entry: **TL-Verilog in Makerchip** — pipelines and handshakes
  are expressed directly instead of hand-plumbed, and the output is
  plain Verilog so nothing downstream changes.
- Flow: Makerchip → Verilator/cocotb (Go-generated vectors) → Yosys +
  OpenLane/OpenROAD on sky130 or IHP SG13G2 → TinyTapeout first, then a
  full shuttle.
- The Go sources in this repo serve double duty as the golden reference
  for the hardware's test vectors — the same philosophy as the Python
  parity work.
- The same crypto cores scale down into a family of maker-friendly
  RISC-V/ESP32 privacy devices (section 6): identity-first hardware
  where the device's RNS identity replaces the vendor account, the
  owner's hub replaces the vendor cloud, and a four-block family
  (Node / Leaf / Eye / View) covers doorbells, security, sensors,
  lighting, weather — all speaking stock Reticulum, all owner-owned by
  construction. Built-in WiFi and BLE on the ESP32-class parts handle
  in-home streaming and low-energy telemetry, and BLE reaches the Go
  port CGo-free as an external SPI/UART interface device (the RNode
  trick, applied to a second radio).
- And if the accelerator ever lands on the same die as the CPU
  (section 7), both repos are firmware-portable with no dependency debt
  and no cgo: the enabling work is a storage VFS layer (the ~320 `os.*`
  call sites are the largest obstacle), crypto provider seams (the
  Offloader), build-tagged hardware `Interface` implementations, and a
  display backend for the TUI — with the unikernel path (stock Go,
  MMU-class RISC-V) as the full-parity reference and TinyGo as the
  MCU-class target.
