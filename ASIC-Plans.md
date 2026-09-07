# ASIC Plans — Hardware Offload for Reticulum on go-reticulum

This document captures the analysis (August–September 2026) of which parts of this
repo (go-reticulum) and gonomadnet are suitable for ASIC/FPGA offload, how
a chip would actually be designed and taped out with today's open tooling,
why SpinalHDL (Scala DSL) is the recommended design entry over raw
SystemVerilog and proprietary TL-Verilog, how the host-to-ASIC interface
is optimized via Quad-SPI (QSPI) and DMA on the ESP32-C5, and how these pieces
extend outward into a family of privacy-first, maker-friendly RISC-V/ESP32
devices built on Reticulum.

The fixed-function hot spots are annotated in the source with
`ASIC suitability:` doc comments:

| Workload | Location | Why it offloads |
|---|---|---|
| LXMF stamp grinding (SHA-256 hashcash) | `go-reticulum/lxmf/stamper.go` (`GenerateStamp`, `StampValue`, `workblockMidstate`) | millions of independent SHA-256 candidates per stamp; no key material on-chip |
| Ed25519 sign/verify | `go-reticulum/rns/crypto/ed25519.go` | every announce, path token, proof, ratchet-file signature |
| X25519 ECDH | `go-reticulum/rns/crypto/x25519.go` | every link establishment and every packet decrypt (one trial per stored ratchet) |
| AES-CBC + HMAC-SHA256 (Fernet-style Token) | `go-reticulum/rns/crypto/token.go` | every encrypted LXMF/link payload and `gorrcd` chat room fanout (burst per-member link encryption) |

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

### Second dominant cost on chat hubs: gorrcd room fanout & link handshakes

Since this document was originally drafted, `gorrcd` (the Reticulum Relay
Chat hub daemon) and client stack have been integrated directly into
`go-reticulum/rrc`. For an RRC chat node, two operational patterns create
sharp CPU spikes that benefit directly from ASIC acceleration:

1. **Room broadcast fanout (burst Token encryption)**: When a member sends a
   message to a channel with $N$ active participants, `gorrcd`'s router
   re-encodes the message and dispatches it across $N$ distinct Reticulum
   `Link` instances. Each link uses an independent ephemeral encryption key
   requiring a distinct `crypto.Token` (AES-128-CBC + HMAC-SHA256). In a room
   with 30 or 50 members, a single incoming chat line causes 30–50 back-to-back
   AES+HMAC operations. On a 240 MHz microcontroller (e.g. ESP32-C5), this
   burst ties up the CPU and causes packet dispatch jitter unless offloaded to a
   pipelined Token accelerator.
2. **Concurrent link handshake storms (X25519 ECDH + Ed25519 verify)**: When
   multiple clients connect or reconnect simultaneously (such as after an
   announce, radio beacon, or network partition heal), each link handshake
   executes one X25519 scalar multiplication and one Ed25519 signature
   verification. In pure software on an RV32 core, point multiplication takes
   tens of thousands of cycles (~10–25 ms each). Multiple concurrent joins
   cause link timeout drops and keepalive churn; offloading the Montgomery
   ladder and Ed25519 verification resolves the negotiation bottleneck.

---

## 2. Chip architecture

A realistic RNode-class or handheld pocket communicator device:

```
+-------------------------------------------------------------------------+
|  Host MCU (e.g. ESP32-C5 / RNode MCU / Linux Host)                       |
|    - RNS Transport, LXMF router, gorrcd chat engine                     |
|    - Keeps all secrets: key storage, IV generation, packet framing      |
|    - General DMA (GDMA) linked to SPI2 (GP-SPI Master)                  |
|    - GPIO Edge-triggered interrupt for asynchronous completion          |
+------------------------------------+------------------------------------+
                                     |
    4-bit QSPI (40–80 MHz)           |  Dedicated Active-Low IRQ Line
    CLK, CS, IO0..IO3 (DMA stream)   |  (non-blocking completion alert)
                                     v
+-------------------------------------------------------------------------+
|  Reticulum Cryptographic ASIC (Authored in SpinalHDL)                   |
|                                                                         |
|  +-------------------------------------------------------------------+  |
|  | QSPI Slave Controller (spinal.lib.com.spi)                        |  |
|  | Command FSM + APB3 / AXI4-Lite Register Map + Stream Deserializer |  |
|  +----------------------------------+--------------------------------+  |
|                                     | Stream[Bits] (valid/ready backpressure)
|  +----------------------------------v--------------------------------+  |
|  | Interconnect & FIFOs: StreamFifo, StreamArbiter, StreamFork       |  |
|  +----+-----------------+--------------------+------------------+----+  |
|       |                 |                    |                  |       |
|  +----v------+   +------v-----+       +------v-----+     +------v----+  |
|  | SHA-256   |   | X25519     |       | Ed25519    |     | AES +     |  |
|  | Pipeline  |   | Montgomery |       | Verify     |     | HMAC-     |  |
|  | + Lead-0  |   | Ladder     |       | (Sign      |     | SHA256    |  |
|  | Counter   |   | Core       |       |  shares    |     | Token     |  |
|  | (stamps)  |   |            |       |  ladder)   |     | Pipeline  |  |
|  +----+------+   +------+-----+       +------+-----+     +------+----+  |
|       |                 |                    |                  |       |
|  +----v-----------------v--------------------v------------------v----+  |
|  | Result Aggregator & Stream Multiplexer                            |  |
|  | Hardware Interrupt Controller -> Asserts External IRQ Pin         |  |
|  +-------------------------------------------------------------------+  |
|                                                                         |
|  (Optionally on-die: integrated VexRiscv/NaxRiscv soft core             |
|   written in SpinalHDL, sharing registers or custom instructions)       |
+-------------------------------------------------------------------------+
```

Notes:

- **Host interface: 4-bit QSPI + GDMA + Hardware IRQ (7 pins total)**.
  Instead of slow 1-bit SPI or pin-exhausting parallel GPIO, the chip uses
  Quad-SPI clocked at 40–80 MHz (20–40 MB/s throughput) linked directly to
  the host MCU's DMA engine (e.g. ESP32-C5 GDMA). An active-low hardware IRQ
  pin signals job completion so the host CPU does *zero* polling and spends
  zero cycles waiting on long crypto operations.
- **Internal pipelining and flow control via SpinalHDL `Stream`**.
  All cryptographic blocks expose standardized `Stream[Bits]` interfaces
  with automatic `valid`/`ready` handshaking and skid buffers. If a core is
  busy, backpressure is propagated to the QSPI FIFO automatically without
  manual buffer plumbing or hazard bugs.
- **X25519 and Ed25519 share curve arithmetic.** The Montgomery ladder
  (one 256-bit word-serial multiplier + dual 256-bit register files,
  ~20–40k gates) serves X25519 key agreement directly; Ed25519 is the
  birational map of the same curve, so signing/verification reuse the
  same field multiplier. Build one field arithmetic unit, wrap two
  protocols around it.
- **The host MCU keeps all secrets.** Key storage, IV generation, and
  RNG stay in firmware; the accelerator is a stateless compute pipe over
  QSPI — mirroring how RNode firmware already talks to the LoRa radio.
  This keeps the hardware security boundary small and the ASIC cheap to
  verify.
- **Job model**: one APB3/AXI-Lite register bank (start, control,
  length, status) per accelerator, backed by streaming FIFOs for bulk data.
  Firmware DMA-spools workblock midstates for stamp grinding and packet
  buffers for Token encryption/decryption.
- **The "no host MCU at all" option is native in SpinalHDL** (see §7.1.1):
  Because the preeminent open-source RISC-V cores (**VexRiscv** for RV32
  and **NaxRiscv** for RV64) are **both written in SpinalHDL**, the crypto
  accelerators can be bound directly into the CPU pipeline as custom
  instruction plugins (`Plugin[VexRiscv]`) or memory-mapped AXI/APB bus
  slaves without crossing language or toolchain boundaries. For RV64, this
  core can run bare-metal Go under the TamaGo framework (`GOOS=tamago`) —
  turning the chip into a standalone Go-native Reticulum node without any
  secondary microcontroller.

---

## 3. HDL choice: SpinalHDL (recommended)

**Recommended design entry: SpinalHDL (Scala DSL). NOT raw SystemVerilog, and NOT proprietary TL-Verilog.**

While TL-Verilog initially appeared attractive for its concise pipeline syntax,
SpinalHDL is the superior choice for a production silicon tapeout and for
seamless integration with the Reticulum ecosystem.

SpinalHDL is a modern hardware description language implemented as an open-source
Scala library (LGPL). It is **not** a high-level synthesis (HLS) tool; it is a
direct RTL generator that compiles into clean, readable, synthesis-ready
Verilog-2001 or SystemVerilog.

Why SpinalHDL beats both SystemVerilog and TL-Verilog for this project:

### 1. Toolchain Sovereignty & 100% Free/Open-Source (FOSS)
- **The TL-Verilog trap**: TL-Verilog depends on **SandPiper**, which is a
  proprietary/commercial preprocessor developed by Redwood EDA. Full compilation
  features require commercial licenses or cloud SaaS dependencies.
- **The SpinalHDL advantage**: SpinalHDL is 100% open-source (LGPL). It runs
  completely locally via standard `sbt` and Java/Scala, with zero external cloud
  calls, zero proprietary license servers, and zero vendor lock-in. It integrates
  cleanly into local CI/CD pipelines and aligns with Reticulum's self-sovereign
  philosophy.

### 2. Built-in Stream & Bus Plumbing (`spinal.lib`)
In cryptographic hardware, designing the arithmetic cores is only half the battle;
the other half is **flow control, FIFOs, and bus interfacing**:
- In SystemVerilog or TL-Verilog, implementing robust `valid`/`ready` handshaking
  with backpressure, skid buffers, crossbar arbiters, and bus interfaces requires
  hundreds of lines of manual, bug-prone boilerplate.
- In SpinalHDL, `spinal.lib` provides native, battle-tested hardware abstractions:
  - **`Stream` (`valid` / `ready` handshake)**: Automatically manages backpressure,
    zero-cycle skid buffers, and FIFOs (`StreamFifo`, `StreamArbiter`, `StreamFork`).
    This is ideal for streaming Reticulum packet buffers into the AES-CBC + HMAC
    engine and the IFAC stamp grinder.
  - **Bus standard abstractions out of the box**: Converting an internal crypto
    stream into an APB3 or AXI4-Lite register map takes ~10 lines of SpinalHDL
    (`apbCtrl.drive(nonceReg, address = 0x04)`).
  - **Data width adaptation**: Trivial conversion between 8-bit QSPI streams,
    32-bit APB registers, and 128/256-bit crypto datapaths (`StreamWidthAdapter`).

### 3. Pipelining without the Pain (`spinal.lib.pipeline`)
TL-Verilog's primary selling point was that retiming a pipeline should not require
manual register rewiring:
- SpinalHDL provides the modern **`spinal.lib.pipeline`** API.
- You declare `Stageable` data types (e.g. `val HASH_STATE = Stageable(Bits(256 bits))`),
  define pipeline stages, and route operations between them.
- SpinalHDL inserts the pipeline registers, generates stall/flush logic, and
  manages hazard detection across stages automatically. You get all the retiming
  and conciseness benefits of transaction pipelining without any proprietary syntax.

### 4. Direct Symbiosis with VexRiscv and NaxRiscv (§7 On-Die Endgame)
- The preeminent open-source RISC-V softcores—**VexRiscv** (32-bit, winner of the
  RISC-V SoftCPU contest, standard in LiteX) and **NaxRiscv** (64-bit out-of-order)—are
  **both written in SpinalHDL**.
- When designing on-die RISC-V coprocessors (§7), the CPU and the crypto engine share
  the exact same language and AST. You can attach accelerators as standard APB/AXI bus
  slaves, or build **custom instruction plugins (`Plugin[VexRiscv]`)** that execute
  Reticulum crypto micro-ops directly from the CPU's register file in single clock cycles.

### 5. Parametric Cryptography (Area vs. Throughput Scaling)
Cryptographic cores require trade-offs between silicon area and clock cycles:
- *Example (SHA-256 / IFAC Grinder)*: We can parameterize 1 round per cycle
  (compact for Tiny Tapeout) vs. 2 or 4 unrolled rounds per cycle (high throughput
  for FPGA/shuttle).
- *Example (X25519)*: We can choose between a single word-serial multiplier or
  parallel DSP blocks.
- In SpinalHDL, Scala's full metaprogramming capabilities let you configure round
  unrolling, word widths, and FIFO depths with type safety from a single codebase:
  ```scala
  case class CryptoConfig(
    unrollRounds: Int = 1,
    busWidth: Int = 32,
    useHardMultipliers: Boolean = false
  )
  ```

### 6. High-Performance Bit-Accurate Verification (SpinalSim + Verilator)
- SpinalHDL includes **SpinalSim**, which compiles designs on the fly using
  **Verilator** and runs high-speed C++ testbenches orchestrated directly from Scala.
- It can ingest the exact JSON/binary test vectors produced by `go-reticulum` and
  assert bit-for-bit parity at hundreds of thousands of cycles per second in CI.

### HDL Comparison Matrix

| Concern | SystemVerilog | TL-Verilog | SpinalHDL (Recommended) |
|---|---|---|---|
| **Toolchain Freedom** | Open (Yosys/Verilator) | Proprietary SandPiper / Cloud SaaS | **100% Free & Open-Source (LGPL/Scala)** |
| **Pipeline Handshaking** | Manual, #1 source of bugs | Generated by `\|stage`/`@N` syntax | **Automated via `spinal.lib.pipeline` & `Stream`** |
| **Bus / Protocol Libraries** | Vendor IP or manual | Minimal / manual glue | **Extensive (`spinal.lib`: AXI, APB, FIFOs)** |
| **SoC Integration** | Requires bus glue logic | Manual bridge | **Native symbiosis with VexRiscv & NaxRiscv** |
| **Parametric Generics** | SV parameters (clunky) | M4 / TLV macro preprocessor | **Full Scala OOP / Functional Metaprogramming** |
| **Verification** | UVM (complex) / cocotb | Browser / Verilator | **SpinalSim (Verilator C++) + Scala / cocotb** |
| **Downstream Synthesis** | Yosys / OpenLane | Compiles to Verilog | **Compiles to clean Verilog-2001 / SystemVerilog** |

Downstream tools (Verilator, Yosys, OpenLane, OpenROAD, TinyTapeout) consume the
generated Verilog without modification.

## 4. Toolchain (all open source, end to end)

| Stage | Tool |
|---|---|
| Design entry | **SpinalHDL (Scala / sbt)** → generated Verilog-2001 / SystemVerilog |
| Reference sim (fast, C++) | **SpinalSim (Verilator backend)** or **cocotb** (driven by Go vectors) |
| Lint | Verilator lint, `svlint` |
| Synthesis (RTL → gates) | **Yosys** (via OpenLane) |
| Floorplan / place / route / CTS | **OpenROAD** (via OpenLane or standalone) |
| DRC / LVS | Magic, Netgen (via OpenLane) |
| PDK | **SkyWater sky130** (fully open, TinyTapeout-compatible, 130 nm — fine for 200 MHz pipelines; ~15 mm² is overkill here, the whole accelerator fits in a few mm²) or **IHP SG13G2** (open 130 nm SiGe BiCMOS, faster transistors, also TinyTapeout-compatible via ChipFoundry) |
| GDS viewer | KLayout, Magic |

Everything above runs locally on macOS/Linux without licensing: the full
RTL-to-GDS flow is a few shell commands in OpenLane once the Verilog
is generated.

### Test vectors straight from the Go implementation

Because these algorithms are already implemented and tested in Go, the
SpinalSim / cocotb testbenches should use **actual vectors from the Go code**:
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
| **ChipFoundry / efabless-style shuttle** | a full multi-mm² die: all four accelerators + PicoRV32/VexRiscv + QSPI | low-to-mid four figures per shuttle slot | the real RNode-crypto chip |
| Commercial foundry | production volumes | five figures+ | only if productized |

Recommended progression:

1. **Phase A — SpinalHDL prototypes** (weeks): SHA-256 stamper, X25519
   ladder, Ed25519 verify, AES/HMAC token, all validated against Go
   vectors via SpinalSim / Verilator.
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
| **Communicator / Pocket Hub** (handheld chat node) | **ESP32-C5** (single-core RISC-V @ 240 MHz, 400 KB SRAM + 8–16 MB OPI PSRAM) | Dual-band Wi-Fi 6 (2.4 GHz + 5 GHz) + BLE 5 + 802.15.4 + LoRa (SX1262 SPI) | Handheld pocket RRC relay chat hub (`gorrcd`) and optional portable NomadNet terminal. Dual-band Wi-Fi 6 enables clean 5 GHz SoftAP for nearby peers to join local chat without 2.4 GHz interference; ASIC coprocessor offloads burst AES+HMAC token encryption during room fanout and X25519/Ed25519 link handshakes |

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
   companion app. And there is a third, OS-free tier now on the table:
   RISC-V64 blocks can run the very same Go stack as *bare-metal firmware*
   under the TamaGo framework — no kernel, no Linux, full stdlib — which
   is surveyed as a strong possibility in §7.1.1 for the Node/relay role.

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
  auditable crypto block (SpinalHDL, tested against this repo's vectors)
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
- **A bare-metal Go target already exists: TamaGo.** The usbarmory
  [TamaGo](https://github.com/usbarmory/tamago) framework (surveyed at
  `~/src/github.com/usbarmory/tamago`, master `b5e0153`, 2026-08-25,
  tracking go 1.27) compiles *stock Go* — stock compiler, stock GC, full
  standard library — to a `GOOS=tamago` bare-metal target and already
  boots on RISC-V64 SoCs. It lands exactly between the unikernel and
  TinyGo paths below; see new §7.1.1.

### 7.1 The Go-target question comes first, and it gates everything

Stock Go has **no bare-metal target**: the runtime assumes an operating
system (OS threads, mmap-based memory management, OS timers, netpoller).
"A valid Go target" therefore means one of four things:

| Path | Silicon class | How Go runs | RAM floor | Port fidelity |
|---|---|---|---|---|
| **Unikernel / Linux ABI** (pragmatic) | MMU application-class RISC-V core (CVA6/Rocket-class SoC, e.g. a StarFive/Milk-V-class part) | stock Go, `GOOS=linux riscv64`, on a Linux-syscall unikernel (Unikraft-class) or a tiny kernel carrying the syscall ABI | 32–64 MB | **100%** — literally the binaries that run on a Mac Mini today, including the full TUI |
| **TamaGo** (bare metal, strong possibility) | SoC-class RISC-V64 (Nuclei UX600-class, SiFive FU540-class, i.MX6UL/8MP-class); **RV64 only — no ESP32/Xtensa** | [TamaGo](https://github.com/usbarmory/tamago) modified Go distribution: `GOOS=tamago` + `GOOSPKG` runtime overlay, one board-package import; stock compiler, stock concurrent GC, complete stdlib | 6–16 MB proven (kotama `tiny`/`GOSOFT=1`), tens of MB comfortable | **near-100%** — the real compiler, runtime, and stdlib; caveats: no async preemption, riscv64 IRQ support still maturing (see 7.1.1) |
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
parity, proven by the byte-diff harnesses in section 7.7), and **TamaGo
is the strong bare-metal target** for SoC-class RISC-V64 — which extends
all the way to running both repos directly on the section 5 ASIC's own
die. TinyGo on a PSRAM-equipped P4-class part remains the ESP32
Leaf/relay-firmware target, where TamaGo cannot go (see 7.1.1).

### 7.1.1 TamaGo: bare-metal stock Go on RISC-V64 — the fit analysis

[**TamaGo**](https://github.com/usbarmory/tamago) (usbarmory; local
checkout `~/src/github.com/usbarmory/tamago`, master `b5e0153`,
2026-08-25, module targeting go 1.27) is a framework for running
*unencumbered* Go programs on bare metal: a minimally modified Go
distribution adds `GOOS=tamago` through a `runtime/goos` overlay
(selected by the `GOOSPKG` variable), plus Go packages for SoC and board
support. This is the mirror image of TinyGo: instead of a new
LLVM-based compiler with a re-implemented runtime and partial language
support, TamaGo keeps **the actual Go compiler, the actual scheduler,
the actual concurrent GC, and complete standard library support** (the
standard distribution test suite runs in CI), and changes exactly three
things: an import (the board package), several `GOOSPKG` environment
variables, and a handful of `runtime/goos` hooks — `RamStart`/`RamSize`,
`CPUinit`/`Hwinit0`, `InitRNG`/`GetRandomData`, `Nanotime`, `Printk`,
and optionally an `InitTime`-style date source — the "Rosetta Stone"
for embedding the Go runtime on bare silicon.

Why this matters specifically for this repo, point by point against the
TinyGo risk list above:

- **`rns/msgpack`'s `reflect` compiles unchanged.** TamaGo's stock
  compiler and full stdlib mean the load-bearing reflect paths simply
  work. Checklist item #6 (the msgpack static-codec rewrite, flagged as
  a correctness-sensitive rewrite) is a **TinyGo-only** gate; a TamaGo
  build needs none of it. Same for `math/big` and `image`/PNG in the
  `qr` closure — the QR identity path survives intact, PNG encoder
  included.
- **The GC objection dissolves.** TamaGo runs the stock Go concurrent
  mark-sweep collector as-is. The section 7.1 finding that "high churn
  under a weak GC is the actual risk, not the live set" is a TinyGo
  objection; TamaGo's GC is *the* GC that already carried this app on
  hosted builds.
- **No cgo, ever — and this repo needs none.** TamaGo forbids cgo
  outright, which go-reticulum and gonomadnet already satisfy
  (§7's headline finding). The two termios ioctls in the serial layer
  are the only syscalls in the tree, and on bare metal the SoC UART
  driver replaces them wholesale (see below).
- **Bare-metal semantics match bare-metal reality.** No OS, no signals,
  no environment variables — which this stack barely notices: config
  already flows through `rns.ParseConfig(io.Reader)` (§7.3's seam),
  logging routes through the `SetLogCallback` seam, and entropy maps to
  `InitRNG`/`GetRandomData` over the SoC RNG peripheral (the same RNG
  seam §7.4 wants for hardware entropy). `rns` needs no users, no
  signals, and no environment.

Silicon actually supported in-tree (master @ `b5e0153`):

| Arch | SoC (in-tree package) | Representative board | Drivers today |
|---|---|---|---|
| riscv64 | Fisilink FSL91030 (Nuclei UX600, rv64imafdc, sv39, 400 MHz) | Milk-V Vega, Nuclei QEMU `eval_soc` | CLINT timer, UART, GPIO, WDT, HW RNG, `nanotime` |
| riscv64 | SiFive FU540 | QEMU `sifive_u` | kotama boots it in **6 MB** RAM |
| riscv64 | AI Foundry Erbium / ET-SoC-1 (`GOSOFT=1` soft-float, rv64imfc) | `erbium_emu`, `sys_emu` board packages | soft-float branch — single-threaded, `tiny`-tagged |
| arm/arm64 | NXP i.MX6UL (USB armory Mk II), i.MX8M Plus | production boards | **SPI (`ecspi`), ENET, USDHC/SD, USB, GPIO, I2C, CAAM/DCP crypto, RNGB**, plus VirtIO/KVM/UEFI amd64 boards, go-net NIC drivers |

The decisive datapoint for this repo's plans is
[kotama](https://github.com/usbarmory/kotama), the "tiny RISC-V target"
demonstrator: a TamaGo unikernel with a Go shell, post-quantum KEM
benchmarking and an in-memory filesystem running on **16 MB** (AI Foundry
Erbium/ET-SoC-1) and **6 MB** (FU540/QEMU) with an experimental
soft-float compiler branch, at ~1 MiB of text+data runtime overhead.
That is the exact firmware envelope this document's section 7 hypothesized
for the relay/propagation Leaf — demonstrated, not projected, by a
production project ([ArmoredWitness](https://github.com/transparency-dev/armored-witness),
GoKey, go-boot, armory-drive are all TamaGo-based and in the field).

What TamaGo does *not* solve, honestly:

- **No ESP32, ever, and no RV32.** TamaGo targets amd64/arm/arm64/
  riscv64/loong64. The ESP32 family is Xtensa (no Go backend exists) or
  RV32 (outside TamaGo), so the section 6.2 Leaf/Eye tiers remain
  TinyGo's territory. TamaGo and TinyGo are complementary tiers, not
  competitors: TamaGo = Node/relay/ASIC-die-class RV64 silicon;
  TinyGo = sub-1 MB MCU Leaves.
- **riscv64 interrupt support is the youngest leg** (the FAQ documents
  amd64/arm/arm64): CLINT timer and `nanotime` — everything the RNS
  maintenance and tick loops actually need — are established; broad IRQ
  plumbing is still landing. Checklist item #7 (seam-centralized ticks)
  covers the gap either way.
- **Storage, radio, and display drivers are board work** in both plans:
  TamaGo ships peripheral register drivers (SPI `ecspi`, USDHC/SD card,
  USB, ENET on NXP; CLINT/UART on RISC-V) but no FAT/littlefs and no
  LoRa modem — so the `fsops` backends (#2), the SPI/UART LoRa
  `Interface` (#4), and the display backend (#10) are **shared work,
  not duplicated** across the TamaGo and TinyGo tracks.
- **RAM floor is real**: ~6 MB proven minimum, tens of MB comfortable —
  it serves the Node/relay/View tiers and the on-die endgame in §7's
  introduction, and rules nothing in for 10-cent CH32V003-class Leaves,
  which §6.2 never asked to run the full stack anyway.

**Verdict: TamaGo is a good fit and earns a "strong possibility" here** —
it keeps both non-negotiables of this repo (stdlib-only, no cgo) *and*
full Go semantics on bare metal, deletes the two riskiest MCU-path items
(#6's msgpack rewrite, the GC-churn exposure), and is the natural
firmware substrate for the §2/§5 endgame where the four crypto cores
share a die with an RV64 CPU: TamaGo's `cpuinit`/`goos` overlay is
exactly the work "port the Go runtime to our SoC" sounds like, already
done — kotama on the AI Foundry erbium/et-SoC-1 emulated SoCs is that
precise precedent. The concrete entry point is checklist item **#13**:
a headless relay/propagation node built `GOOS=tamago` for the Nuclei
QEMU + Milk-V Vega RV64 target, validated by the same §7.7 A/B parity
harness as everything else.

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

If the MCU-class (TinyGo) path is pursued, three library-closure facts
become work items: (a) `rns/msgpack`'s reflect-driven pack/unpack →
rewrite OrderedMap serialization as static per-type code (also a mild
performance win everywhere); (b) drop `image`/PNG out of `qr`'s import
closure on-device (already pure-Go separable); (c) `compress/bzip2` and
the hand-rolled `rns/msgpack` are allocation-heavy — acceptable on
PSRAM-class parts, and stamp workblocks already stream rather than
accumulate. None of these affect the unikernel path — and none of them
affect the TamaGo path either (§7.1.1): with the stock compiler and full
stdlib, `reflect`, `math/big`, and `image` all compile as-is, making
TamaGo the parity-faithful firmware route if a bare-metal node is wanted
before any of these work items are done.

### 7.5.2 The Handheld ESP32-C5 Pocket Hub: Running gorrcd (and optionally gonomadnet) natively

A particularly compelling hardware realization of the Communicator tier (§6.2) is a
**handheld, battery-powered pocket device powered by the ESP32-C5** running `gorrcd`
(and optionally a lightweight `gonomadnet` client) assisted by the crypto ASIC.

The **ESP32-C5** is Espressif's first dual-band Wi-Fi 6 (2.4 & 5 GHz) + BLE 5 +
802.15.4 RISC-V SoC (single-core RV32IMAC @ 240 MHz). It changes the economics and
physics of an RRC chat node in three fundamental ways:

1. **Dual-Band Wi-Fi 6 (5 GHz / 2.4 GHz) for Congestion-Free Local Chat**:
   In urban environments, field deployments, emergency shelters, or crowded
   conventions, the 2.4 GHz band is plagued by severe RF saturation and packet
   loss. The ESP32-C5 can spin up a clean **5 GHz SoftAP or Wi-Fi 6 ad-hoc mesh
   link**, allowing nearby phones, laptops, or peer handhelds to join the local
   `gorrcd` hub with minimal latency and zero 2.4 GHz channel contention.
2. **Built-in Crypto Engine as a Tier-1 Baseline Accelerator**:
   The ESP32-C5 includes onboard hardware blocks for AES-128/256, SHA-256, RSA, ECC,
   HMAC, and a True Random Number Generator (TRNG). Under the `Offloader` pattern
   (§7.4), firmware can dispatch standard AES/SHA/HMAC operations to the ESP32-C5's
   internal peripheral immediately, providing a baseline speedup in software before
   the custom ASIC coprocessor is attached.
3. **Zero-Dependency Footprint of `gorrcd`**:
   The recent consolidation of RRC into `go-reticulum/rrc` removed all external
   dependencies (eliminating `github.com/fxamacker/cbor/v2` and `golang.org/x/text`
   in favor of standard library decoding and in-tree `rrc/cbor`). Unlike the full
   `gonomadnet` TUI (which carries tview/tcell and clipboard bindings), `gorrcd` is a
   **pure network daemon**: it needs only link buffers, session tables, and the
   room registry. It has no terminal dependencies, no cgo, and a compact working set
   that fits comfortably within external Octal PSRAM (e.g. 8 MB or 16 MB OPI PSRAM).

#### The Workload: Where ASIC Assistance is Essential on ESP32-C5

While the ESP32-C5's 240 MHz RV32 core is fast for general control flow, running an
active RRC chat hub exposes three cryptographic bottlenecks:

- **The Room Fanout Spike (Burst Token Encryption)**:
  When a message arrives in an active room with $N$ joined members (such as the ~64 active
  members on the standard "RNS Community Hub"), `gorrcd` loops through all members and
  transmits the message over each member's individual `Link`. Because Reticulum links use
  ephemeral per-link keys, the message cannot simply be broadcast as raw ciphertext: the hub
  must generate $N$ unique `crypto.Token` envelopes (AES-128-CBC encryption + HMAC-SHA256
  authentication).
  - *The problem*: For a room of 64 users, 1 message requires 64 distinct AES-CBC
    runs and 64 HMAC-SHA256 computations in rapid succession. In software on an
    ESP32-C5 (240 MHz RV32), software AES-CBC + HMAC-SHA256 takes ~1,000 µs (1 ms) per user,
    stalling the CPU for **64 full milliseconds**. During this burst CPU freeze, incoming
    radio packets (Wi-Fi, BLE, LoRa) encounter buffer overflows and drop.
  - *The ASIC solution & cycle analysis*:
    - For a standard ~128-byte RRC chat message, PKCS#7 padding adds 16 bytes (144 bytes = 9 blocks).
    - An iterative 1-round/cycle AES-128 core processes 9 blocks in **90 clock cycles**.
    - HMAC-SHA256 over the $IV \parallel Ciphertext$ (160 bytes) processes 4 SHA-256 blocks in
      the pipelined `Sha256Pipe` in **~120 clock cycles**.
    - Total computation per user is only $\sim 210$ clock cycles ($\mathbf{4.2 \ \mu\text{s}}$ at 50 MHz).
    - For **64 concurrent users**, the entire burst finishes in:
      $$64 \times 4.2 \ \mu\text{s} = \mathbf{268 \ \mu\text{s}} \ (\mathbf{0.27 \text{ ms}})$$
      yielding a **>230x speedup** over firmware and reducing CPU load to near 0%.
  - *Bus bandwidth vs compute latency (Amdahl's law on QSPI)*:
    - Transferring 64 separate user payloads ($64 \times 176\text{B} \approx 11.2\text{ KB}$) over
      a 4-bit QSPI bus at 40 MHz (20 MB/s wire speed) takes $\mathbf{560 \ \mu\text{s}}$.
    - Because hardware computation ($268\ \mu\text{s}$) is already *twice as fast as the physical
      bus wires*, duplicating the encryption core $N$ times on silicon would yield zero end-to-end
      speedup if packets must queue through the same QSPI bus.
  - *Architectural decisions & Tiny Tapeout tradeoffs*:
    1. **Silicon density**: A single iterative AES-128 core consumes ~2,500 standard cells;
       SHA-256 consumes ~3,500 cells. Retaining a single Token pipe allows the complete ASIC
       (Stamper + X25519 + Token + QSPI) to fit cleanly in silicon without exceeding Tiny Tapeout
       or small shuttle area budgets.
    2. **Broadcast Envelope Mode (`OP_TOKEN_BROADCAST`)**: Because the plaintext message is
       identical across all 64 recipients, the host can send the plaintext payload *once*,
       followed by an array of $N$ recipient link keys. The ASIC encrypts the plaintext once,
       derives the $N$ per-user envelopes sequentially in 0.27 ms, and streams them back via DMA,
       eliminating redundant QSPI bus transfers.
    3. **SpinalHDL Parameterized Parallelism**: The core is authored as
       `case class TokenEngine(numEngines: Int = 1)`. For Tiny Tapeout and low-power IoT nodes,
       `numEngines = 1` provides optimal silicon density. For dedicated full-die shuttles
       (ChipIgnite, IHP SG13G2) or FPGA hub coprocessors, setting `numEngines = 4` or `8`
       instantiates parallel pipes via a single parameter change.
- **Concurrent Link Handshake Storms (X25519 ECDH + Ed25519 Verify)**:
  Every client connecting to the pocket hub performs a full Reticulum link establishment:
  an X25519 Diffie-Hellman exchange and an Ed25519 signature verification.
  - *The problem*: On a 32-bit RISC-V core without 64-bit SIMD or dedicated curve
    instructions, X25519 scalar multiplication takes ~10–25 ms and Ed25519 verify
    takes ~15–30 ms. If 10 local users discover the pocket hub's announce and join
    simultaneously, the MCU stalls for nearly half a second verifying proofs.
  - *The ASIC solution*: The shared Montgomery ladder core (§2) executes field
    arithmetic in ~20–40k gates, computing scalar multiplications in microseconds
    and keeping the hub responsive during connection storms.
- **LXMF Stamp Grinding (when running NomadNet alongside gorrcd)**:
  If the handheld also runs as a client composing outbound LXMF messages or
  propagation notices, the SHA-256 stamp grinding pipeline with midstate caching
  (§1) reduces computation time from minutes to milliseconds, dramatically
  preserving battery life.

#### Host-to-ASIC Interconnect: Why QSPI + ESP32 GDMA + Hardware IRQ Beats Parallel GPIO & Standard SPI

A critical architectural decision is how the host MCU (ESP32-C5) communicates with the
crypto ASIC. While one might initially suspect that SPI would be a bottleneck for
Reticulum packet throughput—prompting consideration of an 8-bit parallel GPIO bus—a
quantitative analysis of Reticulum packet physics, ESP32-C5 silicon constraints, and
DMA mechanics demonstrates that **Quad-SPI (QSPI) with General DMA (GDMA) and a hardware
IRQ pin is the optimal interface**.

##### 1. The Reticulum Math: Wire Throughput vs. Packet Size
- **Reticulum MTU**: Reticulum's maximum packet size is **507 bytes** (typically
  100–300 bytes for messages, and < 50 bytes for announces/proofs).
- **Cryptographic Payload Traffic**:
  - *IFAC Stamp Grinding*: The host sends ~64–128 bytes (packet header + midstate +
    difficulty target) *once*. The ASIC grinds millions of candidate hashes internally
    without generating any bus traffic. When finished, it returns an **8-byte nonce**.
    Total bus round-trip: **< 150 bytes**.
  - *X25519 Key Agreement / Ed25519 Verify*: Host sends 64 bytes (32-byte scalar +
    32-byte point); ASIC returns 32 bytes (point) or 64 bytes (signature).
    Total bus round-trip: **96–128 bytes**.
  - *AES-128-CBC + HMAC-SHA256 (Token)*: Host sends 32 bytes (key + IV) + up to 500 bytes
    payload; ASIC returns 500 bytes ciphertext + 32-byte HMAC.
    Total bus round-trip: **~1,050 bytes**.
- **Wire Speed vs. Transfer Time**:
  - Standard 1-bit SPI @ 40 MHz (5 MB/s): Transmitting a full 507-byte packet takes **~101 µs**.
  - 4-bit QSPI @ 40 MHz (20 MB/s): Transmitting a full 507-byte packet takes **~25 µs**.
  - 4-bit QSPI @ 80 MHz (40 MB/s): Transmitting a full 507-byte packet takes **~12.5 µs**.
- **Burst Fanout Reality**: Even during a severe `gorrcd` room fanout where an incoming
  message is encrypted for 20 active participants (20 × 1,050 bytes = 21,000 bytes total),
  the entire 20-client batch moves across an 80 MHz QSPI bus in **0.52 milliseconds**.
  Compared to radio transmission times (hundreds of milliseconds over LoRa, or tens of
  milliseconds over Wi-Fi), wire transfer time is completely negligible (< 1% of radio latency).
  **Raw bus bandwidth is not the bottleneck.**

##### 2. Pin-Budget Reality of the ESP32-C5
The ESP32-C5 is packaged in QFN32 or QFN40, providing only **~24 to 28 usable GPIO pins**.
A complete handheld Communicator/Hub already requires:
- **SX1262 LoRa SPI**: `SCK`, `MOSI`, `MISO`, `CS`, `RST`, `BUSY`, `DIO1` = **7 pins**
- **2.8" Display (SPI)**: `SCK`, `MOSI`, `CS`, `DC`, `RST` = **5 pins**
- **MicroSD slot (FAT32)**: `CLK`, `CMD`, `DAT0` (or SPI mode) = **4 to 6 pins**
- **I2C Keyboard / Trackball**: `SDA`, `SCL`, `INT` = **3 pins**
- **Battery ADC, Charging Status, System LEDs**: = **3 pins**
- **Subtotal for existing peripherals**: **22 to 24 GPIO pins**.

An 8-bit parallel bus (8 data lines + `WR` + `RD` + `CS` + `RS`/`ALE`) requires
**11 to 12 GPIO pins**. On an ESP32-C5, **a parallel bus causes immediate, fatal pin
exhaustion**. Using an external I/O expander would negate the latency advantages of parallel
GPIO while increasing PCB complexity and power draw.

##### 3. Silicon Pad Economics ("Pad-Limited" ASIC Dies)
In open-source silicon manufacturing (Tiny Tapeout, SkyWater sky130, GF180):
- Silicon I/O bonding pads are physically massive (~60×60 µm to 80×80 µm each, plus
  electrostatic discharge [ESD] rings and power rails).
- Small crypto accelerators are almost always **pad-limited**: the silicon die size (and
  manufacturing cost) is dictated by the perimeter needed to place the bond pads, not by
  the internal logic gates.
- Quad-SPI requires only **6 pads** (`CS`, `CLK`, `IO0`, `IO1`, `IO2`, `IO3`).
- An 8-bit parallel bus requires **12 to 16 pads**, which nearly doubles the silicon
  die perimeter and package pin count, drastically increasing unit cost.

##### 4. The Real Bottleneck: Transaction Latency & CPU Context Switching
The true bottleneck in accelerator offload is not wire clock speed; it is **software
overhead**:
- If the single-core RV32 CPU has to prepare a packet in software, manually toggle `CS`,
  push words to a FIFO, wait in a `while (!ready)` polling loop, and toggle `CS` again,
  driver overhead (15–40 µs) can dwarf the hardware computation time.
- Polling while waiting for an X25519 scalar multiplication (~50 µs) completely starves
  the CPU from processing background network packets.

##### 5. The 7-Pin Architecture: 4-Bit QSPI + ESP32 GDMA + Dedicated Hardware IRQ
The optimal host-to-ASIC interconnect solves software latency and CPU starvation using
exactly **7 pins**:
- **Pinout**: 4-bit QSPI (`CLK`, `CS`, `IO0`, `IO1`, `IO2`, `IO3`) + 1 active-low `IRQ` line.
- **Zero-CPU Transfer via ESP32 General DMA (GDMA)**: The ESP32-C5 GP-SPI master (SPI2)
  is coupled directly to the onboard GDMA engine. Firmware configures chained DMA
  descriptors in SRAM. The GDMA controller streams packet buffers directly into the SPI
  FIFO without burning CPU cycles.
- **Asynchronous Completion via Dedicated Hardware IRQ**:
  1. The host dispatches a job (e.g. stamp grind or X25519 point multiplication) via a
     single non-blocking DMA write and immediately yields to other FreeRTOS tasks.
  2. When the ASIC pipeline finishes the operation, its result aggregator pulls the
     `IRQ` line low.
  3. The falling edge triggers an edge-sensitive GPIO interrupt on the ESP32-C5,
     firing a non-blocking DMA read to pull the computed result back into SRAM.
  4. The host CPU spends **zero cycles polling** and performs **zero buffer copies**.

##### 6. Implementation in SpinalHDL
In SpinalHDL, this architecture is modeled with high fidelity:
- A QSPI slave receiver module (`spinal.lib.com.spi`) deserializes 4-bit nibbles into
  a standard `Stream[Bits]`.
- Backpressure is propagated naturally: if the AES or SHA-256 core is busy, the
  `Stream.ready` signal drops, and the QSPI controller holds off the FIFO.
- A concise Command FSM parses the 1-byte opcode (`OP_STAMP_GRIND`, `OP_X25519_MULT`,
  `OP_TOKEN_SEAL`, `OP_TOKEN_OPEN`), 2-byte payload length, and dispatches the
  payload stream to the target cryptographic core via `StreamFork` or `StreamDemux`.

#### Hardware Architecture of the Handheld Pocket Hub

A concrete bill of materials for an open, handheld pocket communicator:

```
+-------------------------------------------------------------------------+
|                       Handheld Pocket Communicator                      |
|                                                                         |
|  +---------------------+   4-bit QSPI @ 80 MHz      +----------------+  |
|  | ESP32-C5 MCU        |<==========================>| Crypto ASIC    |  |
|  | - RV32IMAC @ 240 MHz|   CLK, CS, IO[0..3] (GDMA) | (SpinalHDL)    |  |
|  | - 400 KB SRAM       |                            | - QSPI Slave   |  |
|  | - 8–16 MB OPI PSRAM |   Active-Low IRQ Line      | - SHA-256 pipe |  |
|  | - 16 MB Flash       |<---------------------------| - X25519 ladder|  |
|  +----------+----------+   (Interrupt on Done)      | - Ed25519 sign |  |
|             |                                       | - AES+HMAC tok |  |
|             | SPI                                   +----------------+  |
|             v                                                           |
|  +---------------------+                                                |
|  | Semtech SX1262 LoRa |                                                |
|  | (868/915 MHz Mesh)  |                                                |
|  +---------------------+                                                |
|                                                                         |
|  Built-in Radios:                                                       |
|  - Dual-Band Wi-Fi 6 (2.4 GHz mesh + 5 GHz local SoftAP)                |
|  - Bluetooth 5 (LE) for phone commissioning                             |
|                                                                         |
|  Peripherals:                                                           |
|  - MicroSD slot (FAT32 for rooms.toml, hub_identity, message logs)     |
|  - 2.8" SPI color LCD (320x240) or low-power e-ink display             |
|  - I2C QWERTY keyboard (BB Q10 / CardKB) or navigation trackball       |
|  - LiPo battery (2000–3000 mAh) with USB-C charging                     |
+-------------------------------------------------------------------------+
```

#### Operational Modes

The handheld device supports two primary operating modes:

1. **Autonomous Pocket Hub (Headless / Backpack Mode)**:
   - Device rests in a pocket, backpack, or vehicle.
   - Runs `gorrcd` as a permanent local chat hub.
   - Radios: Listens on LoRa for incoming long-range mesh packets and announces;
     broadcasts a local 5 GHz Wi-Fi 6 AP and BLE service.
   - Anyone nearby (friends, field team) connects their laptop or phone to the
     5 GHz Wi-Fi network and opens NomadNet, immediately accessing the local RRC
     rooms hosted right on the handheld device.
2. **Stand-Alone Pocket Communicator (TUI / Micron Mode)**:
   - Runs a trimmed, micron-first client UI (§7.5) on the integrated LCD/e-ink
     screen with keyboard input.
   - Operators can read/post to local `gorrcd` channels, send point-to-point LXMF
     messages, and browse NomadNet Micron pages directly from the palm of their hand
     without requiring an external phone or computer.

#### Firmware Compilation Path on ESP32-C5 (TinyGo vs. C-Host)

Because the ESP32-C5 is 32-bit RISC-V (RV32), TamaGo (which is RV64-only, §7.1.1)
cannot be used directly. The two viable firmware paths are:

- **TinyGo + PSRAM Target (Pure Go Firmware)**:
  - Target: `//go:build esp32c5` (extending TinyGo's ESP32-C series target).
  - External 8 MB PSRAM provides the necessary heap room for `gorrcd` and `rns`.
  - The zero-dependency cleanup of `go-reticulum/rrc` (dropping `fxamacker/cbor`
    and `golang.org/x/text`) enables compilation without external module friction.
- **Hybrid ESP-IDF + Go Library (CGo/Static Archive)**:
  - Compile the pure-Go core (`rrc`, `rns`, `lxmf`) into a static library via
    `tinygo build -target=esp32c5 -o librrc.a`.
  - ESP-IDF provides the low-level dual-band Wi-Fi 6 stack, BLE GATT service,
    display/keyboard drivers, and FreeRTOS tasks, calling into Go for packet
    routing, envelope decoding, and chat hub management.

### 7.6 Checklist, repo by repo

| # | Work item | Repo | Size | Notes |
|---|---|---|---|---|
| 1 | `fsops`/storage VFS package + `osfs` backend; migrate ~120 + ~200 `os.*` call sites behind golden tests | both | large, mechanical | the enabling refactor for *any* non-POSIX storage; behavior-neutral on hosted builds |
| 2 | Embedded storage backend (FAT/SD first; journaling-flash second); collapse rename-based atomicity into backend | both | medium, subtle | new package only; call sites already migrated in #1 |
| 3 | Crypto provider/`Offloader` seams: Token, Ed25519/X25519, stamper, RNG | go-reticulum | small | `rns/crypto` is 9 files; §5's interface design carries over |
| 4 | Hardware `Interface` impls: on-die SPI LoRa + UART/KISS byte-stream (rnode-embedded); build-tag the `net`-family interfaces out | go-reticulum | medium | framing/protocol code already exists and is OS-free |
| 5 | Build-tag scheme: extend the established `-unix`/`-other` pattern with TinyGo per-target tags (`esp32c3`, …) plus a repo-wide `embedded` tag (TamaGo needs nothing beyond its board-package import, so the same `embedded` tag set covers both tracks); stub the 3 `os/exec` sites | go-reticulum | small | the tree already compiles with `-other.go` stubs on unsupported platforms |
| 6 | msgpack reflect → static codecs (**TinyGo-track only** — `reflect` stays stock on TamaGo/unikernel builds); drop PNG from `qr` closure on-device | go-reticulum | medium | nil behavior change on hosted builds |
| 7 | Time/RNG/logging seams: centralize `time.Now` for tick-driven maintenance loops, route entropy + log through injected providers | go-reticulum | medium | logger seam already exists (`SetLogDest`/`LogCallback`) |
| 8 | Sever the one core→TUI edge (`app/channels-adapters.go`, 60 lines) | go-nomadnet | trivial | HubView interface moves down |
| 9 | Flash-wear batching: directory eager-persist, per-announce re-saves, propagation store | go-nomadnet | medium | benefits hosted nodes too |
| 10 | Display `tcell.Screen` backend (e-ink/LCD framebuffer) + input (buttons/serial), injected via `tview.SetScreen` | forks/ firmware | medium | SimulationScreen is the reference; tcell per-cell dirty checking is e-ink-friendly |
| 11 | Firmware substrate / board-support repo: TinyGo target defs **and TamaGo `soc/<vendor>/<SoC>` + `board/…` packages** (the `runtime/goos` overlay: `ramStart`/`ramSize`, `cpuinit`, `InitRNG`/`GetRandomData`, `Nanotime`, `Printk`), linker scripts, startup, peripheral drivers (SPI radio, SD/FAT or flash FS, display, entropy, RTC) | new | medium | where the "hardware shims" physically live; TamaGo's in-tree SoC packages (SPI/ecspi, USDHC, CLINT/UART, RNG) are the driver template |
| 12 | Reduced micron-first UI for MCU builds (fetch → parse → framebuffer) | go-nomadnet | medium | reuses `micron` + `browser` wholesale |
| 13 | TamaGo bring-up: headless RNS relay/propagation node, `GOOS=tamago` on RISC-V64 (Nuclei QEMU + Milk-V Vega first), LoRa-over-SPI `Interface` + storage through #1/#2 | both | medium | the §7.1.1 entry point; same §7.7 parity A/B gate as every other phase |
| 14 | `gorrcd` embedded daemon target: headless build tag (`//go:build embedded`), in-memory/SD room registry, session tables in PSRAM | go-reticulum | medium | zero-dependency `rrc` already compiles with stdlib only; enables pocket hub |
| 15 | ESP32-C5 board support & dual-band AP bridge: TinyGo target `esp32c5`, dual-band Wi-Fi 6 AP `Interface` + BLE GATT + SX1262 LoRa SPI driver + QSPI GDMA host driver for crypto ASIC (§7.5.2) | new / both | medium | enables the standalone Pocket Hub (§7.5.2) |
| 16 | SpinalHDL Crypto ASIC cores: SHA-256 stamper, X25519/Ed25519 Montgomery ladder, AES+HMAC Token engine, QSPI slave with `Stream` interface (§2, §3) | new | large | hardware accelerator targeting TinyTapeout and full shuttles |

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
4. **Phase E3b — TamaGo bare-metal node** (new, §7.1.1): the same
   headless relay/propagation node compiled `GOOS=tamago` against an
   RV64 target — Nuclei QEMU emulator first, then a Milk-V Vega-class
   board — stock-Go crypto, `fsops` storage on SD via the SoC's USDHC,
   a TamaGo UART/SPI `Interface` for bring-up, software crypto. It
   carries *zero* of the §7.5.1 TinyGo work items, so it is the
   parity-faithful firmware node and the dress rehearsal for the §5
   on-die endgame.
5. **Phase E3c — ESP32-C5 Handheld Pocket Hub (`gorrcd` + dual-band Wi-Fi 6 + ASIC)**:
   Deploy `gorrcd` on an ESP32-C5 with 8–16 MB OPI PSRAM; bring up the 5 GHz SoftAP
   and SX1262 LoRa interfaces; hook the `Offloader` into the QSPI ASIC (SpinalHDL
   cores) via GDMA and the active-low hardware IRQ line for burst AES+HMAC room
   fanout and X25519 link handshake acceleration. Verify multi-client chat fanout
   against desktop `gorrcd` with the parity harness.
6. **Phase E4 — on-die crypto**: flip the provider seams to the
   accelerators; the section 4 Go-vector tooling now serves as the
   firmware bring-up testbench.
7. Every phase keeps the section 6.2 rule: any firmware build must parse
   and interoperate, byte for byte, against both this Go stack and the
   Python SOT — the port is only done when the parity harness says so.

---

## 8. Summary

- Offload targets, in priority order: **stamp grinding** (dominant,
  safest), **X25519/Ed25519** (shared field arithmetic, link handshake
  storms), **AES+HMAC token** (library block, `gorrcd` room fanout burst
  encryption). Everything else stays in firmware.
- Design entry: **SpinalHDL (Scala DSL)** — 100% free and open-source (LGPL),
  provides first-class `Stream` abstractions (valid/ready handshakes),
  `spinal.lib.pipeline` stage retiming, parametric crypto configuration,
  built-in AXI/APB bus shims, and native symbiosis with VexRiscv/NaxRiscv.
  Compiles to clean Verilog-2001/SystemVerilog with zero proprietary tool dependencies.
- Interconnect: **4-bit QSPI @ 40–80 MHz + ESP32 GDMA + Dedicated Hardware IRQ (7 pins total)**.
  Moves 20–40 MB/s, transferring 507B packets in 12–25 µs with zero CPU polling
  and zero memory copy overhead, perfectly fitting the ESP32-C5 pin budget and pad-limited ASIC dies.
- Flow: SpinalHDL → SpinalSim/Verilator (Go-generated golden vectors) → Yosys +
  OpenLane/OpenROAD on sky130 or IHP SG13G2 → TinyTapeout first, then a
  full shuttle.
- The Go sources in this repo serve double duty as the golden reference
  for the hardware's test vectors — the same philosophy as the Python
  parity work.
- The same crypto cores scale down into a family of maker-friendly
  RISC-V/ESP32 privacy devices (section 6): identity-first hardware
  where the device's RNS identity replaces the vendor account, the
  owner's hub replaces the vendor cloud, and a five-block family
  (Node / Leaf / Eye / View / **Communicator**) covers doorbells, security,
  sensors, lighting, weather, and **handheld pocket chat hubs (`gorrcd` on
  ESP32-C5)** — all speaking stock Reticulum, all owner-owned by
  construction. Built-in dual-band Wi-Fi 6 (2.4 & 5 GHz) and BLE on the
  ESP32-C5 provide clean, congestion-free local AP connectivity and
  low-energy telemetry, while BLE reaches the Go port CGo-free as an
  external SPI/UART interface device (the RNode trick, applied to a second radio).
- And if the accelerator ever lands on the same die as the CPU
  (section 7), both repos are firmware-portable with no dependency debt
  and no cgo: the enabling work is a storage VFS layer (the ~320 `os.*`
  call sites are the largest obstacle), crypto provider seams (the
  Offloader), build-tagged hardware `Interface` implementations, and a
  display backend for the TUI. For the "no OS at all" tier three Go
  targets exist, on a spectrum of fidelity: the unikernel path (stock Go,
  MMU-class RISC-V) as the full-parity reference, **TamaGo** as the
  bare-metal stock-Go target on RISC-V64 SoCs — near-full parity, proven
  in the field (ArmoredWitness) and at 6–16 MB RAM (kotama), and the
  natural firmware for an ASIC with an RV64 core sharing the die with
  the crypto cores (§2, §7.1.1, checklist #13, phase E3b) — and TinyGo
  for the sub-1 MB ESP32-class Leaf tier and the **ESP32-C5 handheld
  Communicator tier (§7.5.2)**, where the zero-dependency consolidation of
  `gorrcd` into `go-reticulum/rrc` with external PSRAM makes a pocket chat
  node an immediate, practical reality.
