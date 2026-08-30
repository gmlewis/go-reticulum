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

## 7. Summary

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
