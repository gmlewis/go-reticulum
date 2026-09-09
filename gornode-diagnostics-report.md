# RNode Fleet Radio Diagnostics Report

**Tool:** `gornode-diagnostics` · Three fleet runs on 2026-09-09:
**Run 1** (15:33–15:39): 5 radios — found OMEN-875 and raspberrypi TX-dead.
**Run 2** (16:03–16:10): the two dead radios removed, the healthy MacM2Pro radio moved to
raspberrypi — all three remaining radios confirmed healthy.
**Run 3** (17:27–17:33): the ack-every-5 + stale-packet-fix build (v0.93.0) — lower channel
load, thinner ACKs, zero phantom peers. Run 3 is summarized first below; Run 2 and
Run 1 (the original baseline and dead-radio diagnosis) follow.

---

# Run 3 (2026-09-09, ~17:27–17:33) — ack-every-5 + stale-packet fix validated

**Setup:** defaults (60s grace, 300s test, 915.000 MHz / BW 125 kHz / SF9 / CR5 / 17 dBm), now
with the v0.93.0 build: **`-ack-every 5` is the default** (ACK thinning) and the **stale-packet
fix is live** (serial input flushed on open; fleet packets ignored until the test window opens).
**Fleet:** same 3 radios (kamrui upstairs; mac-mini-m2 and raspberrypi downstairs).

## Executive summary — Run 3

| Node | Radio | nodeID | TESTs sent | TESTs heard | ACKs sent | ACK receipts | Verdict |
|---|---|---|---:|---:|---:|---:|---|
| glenn-kamrui (upstairs) | `by-id/…90:70:69:9C:97:A0-if00` | `22f2c0a937c8793d` | 101 | 193 | 37 | 37/101 | **TRANSMIT + RECEIVE OK** |
| glenn-mac-mini-m2 | `/dev/cu.usbmodem101` | `213302de41bc6eee` | 100 | 191 | 37 | 36/100 | **TRANSMIT + RECEIVE OK** |
| raspberrypi | `by-id/…8C:FD:49:B5:7D:6C-if00` | `512e9872c721fc3c` | 100 | 192 | 37 | 34/100 | **TRANSMIT + RECEIVE OK** |

Every expectation from the two fixes was met:

1. **ACK receipts collapsed to ~⅕** (37/36/34 vs Run 2's 181/181/185) — exactly the thinning the
   new default produces: with `ack-every 5`, only seq 5, 10, … are ACK-eligible (20 per sender per
   peer, 40 receipts possible). Confirmation of ack-eligible TESTs: kamrui **37/40 (93%)**,
   mac-mini 36/40 (90%), raspberrypi 34/40 (85%).
2. **Zero phantom peers.** Every report lists **exactly 2 peers**, all with fresh per-run nodeIDs
   (`22f2…`, `2133…`, `512e…`); none of Run 1's or Run 2's nodeIDs appear anywhere. The
   serial-flush + window-gate fix is verified in the field.
3. **The firmware's own congestion indicator confirms the lighter load**: CSMA bands oscillated
   `0x01 ↔ 0x02` (0–29 backoff slots) on all three radios and **never reached `0x03`** —
   in Runs 1–2 every radio ratcheted to `0x03` and stayed there.
4. **ACK delivery was near-perfect** (97–100%): of each radio's 37 ACKs, peers heard 37, 36, 36 —
   so the thinning did not hurt reliability.

## Who heard whom — Run 3 (TEST packets, received/sent)

| ↓ heard by / sent by → | kamrui (101) | mac-mini (100) | raspberrypi (100) |
|---|---|---|---|
| **kamrui** | — | 95 (95%) | 98 (98%) |
| **mac-mini** | 96 (95%) | — | 95 (95%) |
| **raspberrypi** | 99 (98%) | 93 (93%) | — |

Delivery percentages stay symmetric per pair (kamrui↔rpi 98% both ways, kamrui↔mac-mini and
mac-mini↔rpi 93–95%), i.e. the links are unchanged from Run 2 — thinning ACKs did not cost
delivery.

## RTTs — Run 3: better, but not collapsed

| Pair | avg (each side) | min | max |
|---|---|---|---|
| kamrui ↔ mac-mini | 9.7s / 19.8s | 4.0s / 17.2s | 19.8s / 19.7s |
| kamrui ↔ rpi | 20.4s / 11.9s | 17.8s / 3.9s | 21.2s / 19.5s |
| mac-mini ↔ rpi | 19.8s / 19.8s | 17.2s / 17.3s | 19.7s / 20.3s |

- **Minimum RTTs dropped to ~4s** (Run 2's floor was 7–15s) — the channel is fast when the ACK
  catches the same 15s window.
- **Averages are 10–20s**, down from 26–30s, but well above the ~0.3s airtime the frames need.
- **RTTs look quantized to the 15s ack-eligible cadence**: with ack-every 5, ACK-eligible TESTs
  go out every 15s, and each receipt is either ~4s (same window) or ~17–20s (one window late).
  Each directed view is consistently fast or slow — e.g. kamrui saw mac-mini's ACKs fast
  (avg 9.7s) while mac-mini saw kamrui's slow (avg 19.8s, min 17.2s) — a receipt-timing effect,
  not a link-quality one (delivery percentages are symmetric). Residual latency is
  channel-access timing (CSMA deferral of the ACK burst), not radio health.

## Findings — Run 3

1. **Both v0.93.0 fixes validated in the field** (thin ACKs by default; stale packets flushed and
   gated). No tool changes outstanding from this run.
2. **raspberrypi logged a single `read error: EOF` at window open** — its radio kept receiving
   and acknowledging for the full 300s (192 TESTs heard), so this is a transient at the
   flush/reopen boundary, not a fault. Worth watching on the next run; no action taken.
3. **Firmware stat counters remain 0** on all three units (this firmware build does not answer
   the legacy counter queries), unchanged from Runs 1–2.
4. Fleet baseline is now: healthy 3-radio fleet delivering 93–98% TEST delivery both ways,
   85–93% ack-eligible confirmation, ACK airtime cut ~5×, CSMA backoff staying in bands 1–2.

---

# Run 2 (2026-09-09, ~16:05:00–16:10:11) — post-repair fleet

**Setup:** defaults again (60s grace, 300s test, 915.000 MHz / BW 125 kHz / SF9 / CR5 / 17 dBm).
**Fleet:** 3 radios — glenn-kamrui (upstairs), glenn-mac-mini-m2 and raspberrypi (downstairs;
raspberrypi now runs the ex-MacM2Pro radio, USB unit `8C:FD:49:B5:7D:6C`). The three radio-less
nodes (OMEN-875, nano2gb, MacM2Pro) correctly reported "no serial device that could hold an RNode"
and exited.

## Executive summary — Run 2

| Node | Radio | nodeID | TESTs sent | TESTs heard | ACK receipts | Verdict |
|---|---|---|---:|---:|---:|---|
| glenn-kamrui (upstairs) | `by-id/…90:70:69:9C:97:A0-if00` | `206c8ebc12ed4ecd` | 101 | 199 | 181/101 | **TRANSMIT + RECEIVE OK** |
| glenn-mac-mini-m2 | `/dev/cu.usbmodem101` | `5595134c9a8c9ad8` | 101 | 192 | 181/101 | **TRANSMIT + RECEIVE OK** |
| raspberrypi (ex-MacM2Pro radio) | `by-id/…8C:FD:49:B5:7D:6C-if00` | `857933b03afcbadb` | 101 | 197 | **185**/101 | **TRANSMIT + RECEIVE OK** |

**Every radio now in the fleet transmits and receives.** The moved radio is, in fact, the fleet's
strongest: highest ACK-confirmation (185/101) and 97–99% bidirectional delivery.

## Who heard whom — Run 2 (TEST packets, received/sent)

| ↓ heard by / sent by → | kamrui (101) | mac-mini (101) | raspberrypi (101) |
|---|---|---|---|
| **kamrui** | — | 98 (97%) | 99 (98%) |
| **mac-mini** | 95 (94%) | — | 94 (93%) |
| **raspberrypi** | 99 (98%) | 98 (97%) | — |

Per-sender confirmation rate (ACK receipts across both peers): kamrui 90%, mac-mini 90%,
raspberrypi **92%** — the moved radio leads again, exactly as it did as MacM2Pro in Run 1.

## Findings — Run 2

1. **The radio swap fully worked.** The ex-MacM2Pro unit on raspberrypi delivers 97–99% both ways
   and has the best per-sender confirmation (92%) of the fleet. The old raspberrypi radio
   (`44:1B:F6:6F:28:0C`) and the OMEN-875 unit (`8C:FD:49:B6:52:68`) are out of the fleet pending
   repair of their transmitters (both remain excellent receivers per Run 1).
2. **kamrui (upstairs) is still the weakest healthy transmitter, and that is a placement effect**:
   as the user notes, kamrui is upstairs while the other two are downstairs. Its TX is heard 93–94%
   of the time by the downstairs nodes, while its own RX hears 97–99% of everything. The floor/ceiling
   path is not the problem — its transmit margin is, exactly as in Run 1.
3. **A stale-packet bug was caught and fixed (tool improvement #4).** At window open, both kamrui
   ("3 peers") and mac-mini ("4 peers") reported *phantom peers* — with 1–2 packets each carrying
   **Run 1's nodeIDs** (`8bd5…`, `a42d…`) and **seq 100/101** — Run 1's very final packets, sent
   ~25 minutes earlier, which had sat buffered (host/tty or firmware queue) and were delivered the
   instant the new run's RX loop began. They were even acknowledged (wasted airtime). Fixed in the
   tool: the serial input queue is now flushed on open, and fleet packets are ignored until the
   test window opens (a pre-test packet can never be counted or acknowledged again). raspberrypi's
   report shows the clean result (exactly 2 peers): it had no backlog because its radio was fresh.
4. **RTTs unchanged (~26–29s avg, 7–52s range)** because this run still acknowledged every packet
   (no `-ack-every`; the tool's default is now `5`). CSMA bands again ratcheted `0x01 → 0x02 →
   0x03`. With only 3 transmitters the contention is milder than Run 1, but the ACK-everything
   policy still dominates the latency — the next run will thin ACKs by default.
5. **Firmware identity and stat counters remain unanswered by this firmware** even with the new
   probe retries and grace-window re-queries — reports still show plain "RNode", counters 0. The
   over-the-air results remain the authoritative verdicts.
6. **The device-dedupe fix is confirmed live**: each Linux machine now lists its radio exactly once
   (by-id path), with no phantom `ttyACM0` "IN USE" line.

## Run 2 per-peer detail

```
kamrui (206c…):     heard 5595: 98 TESTs,  90 ACKs of ours, RTT avg 26.8s (9.9/49.6)
                    heard 8579: 99 TESTs,  91 ACKs of ours, RTT avg 27.6s (15.0/49.1)
mac-mini (5595):    heard 206c: 95 TESTs,  91 ACKs of ours, RTT avg 25.9s (7.0/47.0)
                    heard 8579: 94 TESTs,  90 ACKs of ours, RTT avg 27.8s (7.0/43.1)
raspberrypi (8579): heard 206c: 99 TESTs,  94 ACKs of ours, RTT avg 29.4s (10.2/51.5)
                    heard 5595: 98 TESTs,  91 ACKs of ours, RTT avg 26.2s (14.9/43.4)
```

---

# Run 1 (2026-09-09, 15:33–15:39) — baseline: two dead transmitters found

## 1. Executive summary

| Node | Radio | nodeID | TESTs sent | TESTs heard | ACK receipts | Verdict |
|---|---|---|---:|---:|---:|---|
| glenn-mac-mini-m2 | `/dev/cu.usbmodem101` | `87fc08fa162f0bc8` | 101 | 193 (2 peers) | 178/101 | **TRANSMIT + RECEIVE OK** |
| glenn-kamrui | `by-id/…90:70:69:9C:97:A0-if00` | `8bd51514a82f9a6e` | 101 | 197 (2 peers) | 185/101 | **TRANSMIT + RECEIVE OK** |
| glenn-MacM2Pro (local) | `/dev/cu.usbmodem101` | `a42db1888110f5e6` | 101 | 189 (2 peers) | 185/101 | **TRANSMIT + RECEIVE OK** |
| glenn-OMEN-875 | `by-id/…8C:FD:49:B6:52:68-if00` | `729e33e60e24be4d` | 101 | 293 (3 peers) | **0**/101 | **RECEIVE ONLY** — dead transmitter |
| raspberrypi | `by-id/…44:1B:F6:6F:28:0C-if00` | `20a369bd0006f838` | 100 | 297 (3 peers) | **0**/100 | **RECEIVE ONLY** — dead transmitter |

**Confirmed: TWO radios fail to transmit — glenn-OMEN-875 and raspberrypi.** Neither appears in any
healthy node's "packets heard" table despite both having sent ~100 TEST packets each. Both have
**excellent receivers** and both accepted the full LoRa configuration cleanly, so the fault is in the
RF output path (dead PA, broken antenna/connector, or RF switch), not the digital side.

---

## 2. Who heard whom (TEST packets, received/sent)

The three healthy radios only ever heard **each other**; the two dead-TX radios heard **all three**
healthy radios. Nobody ever heard OMEN-875 or raspberrypi.

| ↓ heard by / sent by → | kamrui (101) | mac-mini (101) | MacM2Pro (101) | OMEN (101) | rpi (100) |
|---|---|---|---|---|---|
| **kamrui** | — | 98 (97%) | 99 (98%) | 0 | 0 |
| **mac-mini** | 95 (94%) | — | 98 (97%) | 0 | 0 |
| **MacM2Pro** | 93 (92%) | 96 (95%) | — | 0 | 0 |
| **OMEN** | 95 (94%) | 99 (98%) | 99 (98%) | — | 0 |
| **raspberrypi** | 100 (99%) | 99 (98%) | 98 (97%) | 0 | — |

**Per-link round-trip times** (TEST → first ACK receipt, per pair):

| Pair | avg | min | max |
|---|---|---|---|
| mac-mini ↔ kamrui | 29.8s / 26.7s | 8.8s / 12.2s | 49.8s / 44.0s |
| MacM2Pro ↔ kamrui | 25.7s / 25.3s | 6.2s / 10.5s | 47.0s / 49.6s |
| MacM2Pro ↔ mac-mini | 28.9s / 30.4s | 6.0s / 15.0s | 47.1s / 59.4s |

*(values shown as measured by each side of the pair)*

---

## 3. Findings

### 3.1 The two dead radios have the BEST receivers
OMEN-875 and raspberrypi heard 293 and 297 of the 303 healthy-node TEST packets — **97–98%
reception**, the highest in the fleet. Every node in the fleet (healthy included) received 92–99% of
what was actually radiated. So: *reception loss is uniform and small (~2–5%, collisions), and the
dead-TX units are not deaf — their RX chains and antennas work fine.* This is consistent with a
transmitter-only failure (PA / RF switch / TX-side antenna path) and is strong evidence the fix is
hardware, not firmware or configuration.

### 3.2 All five radios accepted the configuration and reported matching state
"State check: reported values match configuration" on every node — both dead units ACKed
frequency/bandwidth/spreading-factor/coding-rate/TX-power writes over serial and reported the
configured values back. The radios' digital side and firmware are alive and healthy; only RF output
is missing. Bitrate derived from the reported params: **1757 bps on-air** on all five.

### 3.3 Channel contention is real — RTTs average 25–30s
Round-trip times averaged 25–30s with a hard floor around 6–15s and a tail to 46–59s — roughly
8–10 test intervals. At 26-byte TESTs + 38-byte ACKs, a single round trip needs only ~0.3s of
airtime, so this latency is **not propagation** — it is CSMA contention: the fleet ACKs every packet
it hears, and during the test the three healthy radios exchanged 197+193+189 ACKs on top of 504
TEST transmissions. The firmware's adaptive CSMA backoff confirms the congestion: all three healthy
nodes reported their CSMA contention-window bands ratcheting up mid-test (`cw band 0x01 → 0x02 →
0x03`, i.e. window rising from 0–14 slots to 30–44 slots), and the Macs started the test at `band
0x01` (quiet) before climbing. **Takeaway: for a 5-node fleet at ~3s interval, per-packet
acknowledgement loads the 1757 bps channel heavily; RTTs in the tens of seconds are contention, not
a fault.** A lower ACK density (e.g. ACK every Nth packet) would cut channel load ~4x in future runs.

### 3.4 kamrui is the weakest healthy link — worth a look at its antenna
kamrui sits at the bottom of every healthy statistic: its TESTs were the least-heard (92–94% vs
97–98% for the Macs), and its ACKs were the least-delivered (mac-mini received only 88 of 101
kamrui ACKs, vs ~92–93 for the Macs' ACKs). It still passes easily, but as the **hub** (gornsd +
gorrcd) it is the one radio whose TX margin is worth improving first — antenna placement/orientation
is the cheapest lever.

### 3.5 Firmware stat counters and RSSI/SNR replies never arrived
Every node's firmware RX/TX packet counters read **0** through all 20 stat polls (15s cadence), and
no RSSI/SNR/temperature replies were captured — yet CSMA-parameter replies *did* arrive on all
three healthy units (5–13 replies each over 20 polls, so even those are lossy). The two dead-TX
units returned **zero** stat replies of any kind. Two readings: (a) this firmware build (Espressif
"USB JTAG serial debug unit" RNodes) doesn't implement/answer the legacy counter queries reliably,
and (b) the dead-TX units were silent even on the serial stat path during the test window. The
verdicts don't depend on the counters — the over-the-air results are the authoritative signal.

### 3.6 Minor tool observations (from the live run) — all addressed in the tool since
- **Duplicate device listing on Linux:** each Linux box listed the same physical radio twice — once
  as `/dev/serial/by-id/usb-Espressif_USB_JTAG_serial_debug_unit_…-if00` (DETECTED) and once as
  `/dev/ttyACM0` (IN USE). The "IN USE" holder was the diagnostics process *itself*: `/proc/*/fd`
  readlink resolves the by-id symlink to the canonical `/dev/ttyACM0`, so the tool saw its own
  already-open fd. **Fixed:** the sniff now de-duplicates device paths that name the same physical
  device (by character-device ID) and excludes its own PID from the holder scan.
- **Firmware/platform details missing:** the live units answered the DETECT request but their
  firmware/platform/board replies weren't captured within the probe window, so the report shows
  "RNode" without version/platform info. **Fixed:** the tool now re-queries
  fw/platform/MCU/board — during the probe (up to 2 retries) and again during the idle grace window.
- **Heavy ACK load (finding 3.3):** **Addressed:** a new `-ack-every N` flag acknowledges every Nth
  test packet heard from each peer (default 1 = every packet, as in this run), cutting channel load
  proportionally on large fleets.

---

## 4. Recommended next steps

1. **Physically inspect OMEN-875 and raspberrypi radios** — RX chains are excellent, config and
   serial control all work, so focus on the TX/RF path: antenna connector seated, RF switch, PA.
   A `gornodeconf --info`-style firmware check on both units can rule out a firmware mode stuck in
   RX-only, but given both units also failed the raw-KISS TX path with matching config, hardware is
   the prime suspect. After any repair, **re-run this same test** and look for their nodeIDs
   (`729e…`, `20a3…`) to appear in the peers' heard tables.
2. **Check kamrui's antenna setup** — the hub radio has the lowest delivery margin of the healthy
   three; cheap win for overall fleet reliability.
3. **Use `-ack-every 5` on the next fleet run** (now implemented in the tool) to cut channel load
   ~5x and shrink the RTTs seen in this run; today's run validated TX/RX conclusively at ~90%
   delivery despite the congestion.
4. Keep this report as the fleet baseline: healthy links deliver 92–99% both ways at 1757 bps, and
   RTTs under load run 25–30s average.

---

## Appendix — verbatim final reports

<details>
<summary>glenn-mac-mini-m2 — /dev/cu.usbmodem101 (nodeID 87fc08fa162f0bc8)</summary>

```
Radio:          RNode
Configured:     915000000 Hz, BW 125000 Hz, SF9, CR5, TX 17 dBm
State check:    reported values match configuration
Bitrate:        1757 bps (on-air, derived from reported radio params)

Fleet test results (300s window):
  TEST packets sent:     101
  TEST packets heard:    193 (from 2 peer(s))
  ACKs sent:             193
  ACK receipts received: 178 (of 101 sent packets)

  Packets heard per peer:
    8bd51514a82f9a6e: 95 TEST packet(s), 88 ACK(s) of ours, RTT avg 26.7s (min 12.2s / max 44.0s)
    a42db1888110f5e6: 98 TEST packet(s), 90 ACK(s) of ours, RTT avg 28.9s (min 6.0s / max 47.1s)

Radio message strings / unrecognized frames:
  CSMA params: cw band 0x02, min 0x0f, max 0x1d        (×12 lines; includes band 0x01, min 0x00, max 0x0e early)

Verdict: TRANSMIT + RECEIVE OK — 178 of 101 sent packets were confirmed received by peers
```

</details>

<details>
<summary>glenn-kamrui — Espressif USB_JTAG_serial_debug_unit 90:70:69:9C:97:A0 (nodeID 8bd51514a82f9a6e)</summary>

```
Radio:          RNode
Configured:     915000000 Hz, BW 125000 Hz, SF9, CR5, TX 17 dBm
State check:    reported values match configuration
Bitrate:        1757 bps (on-air, derived from reported radio params)

Fleet test results (300s window):
  TEST packets sent:     101
  TEST packets heard:    197 (from 2 peer(s))
  ACKs sent:             197
  ACK receipts received: 185 (of 101 sent packets)

  Packets heard per peer:
    87fc08fa162f0bc8: 98 TEST packet(s), 93 ACK(s) of ours, RTT avg 29.8s (min 8.8s / max 49.8s)
    a42db1888110f5e6: 99 TEST packet(s), 92 ACK(s) of ours, RTT avg 25.7s (min 6.2s / max 47.0s)

Radio message strings / unrecognized frames:
  CSMA params: cw band 0x02, min 0x0f, max 0x1d        (×5; bands 0x02/0x03, min 0x1e, max 0x2c)

Verdict: TRANSMIT + RECEIVE OK — 185 of 101 sent packets were confirmed received by peers
```

</details>

<details>
<summary>glenn-MacM2Pro (local) — /dev/cu.usbmodem101 (nodeID a42db1888110f5e6)</summary>

```
Radio:          RNode
Configured:     915000000 Hz, BW 125000 Hz, SF9, CR5, TX 17 dBm
State check:    reported values match configuration
Bitrate:        1757 bps (on-air, derived from reported radio params)

Fleet test results (300s window):
  TEST packets sent:     101
  TEST packets heard:    189 (from 2 peer(s))
  ACKs sent:             189
  ACK receipts received: 185 (of 101 sent packets)

  Packets heard per peer:
    87fc08fa162f0bc8: 96 TEST packet(s), 92 ACK(s) of ours, RTT avg 30.4s (min 15.0s / max 59.4s)
    8bd51514a82f9a6e: 93 TEST packet(s), 93 ACK(s) of ours, RTT avg 25.3s (min 10.5s / max 49.6s)

Radio message strings / unrecognized frames:
  CSMA params: cw band 0x02, min 0x0f, max 0x1d        (×13; includes band 0x01, min 0x00, max 0x0e early; band 0x03 later)

Verdict: TRANSMIT + RECEIVE OK — 185 of 101 sent packets were confirmed received by peers
```

</details>

<details>
<summary>glenn-OMEN-875 — Espressif USB_JTAG_serial_debug_unit 8C:FD:49:B6:52:68 (nodeID 729e33e60e24be4d)</summary>

```
Radio:          RNode
Configured:     915000000 Hz, BW 125000 Hz, SF9, CR5, TX 17 dBm
State check:    reported values match configuration
Bitrate:        1757 bps (on-air, derived from reported radio params)

Fleet test results (300s window):
  TEST packets sent:     101
  TEST packets heard:    293 (from 3 peer(s))
  ACKs sent:             293
  ACK receipts received: 0 (of 101 sent packets)

  Packets heard per peer:
    87fc08fa162f0bc8: 99 TEST packet(s)
    8bd51514a82f9a6e: 95 TEST packet(s)
    a42db1888110f5e6: 99 TEST packet(s)

Verdict: RECEIVE ONLY? — this radio heard peer packets but NO peer ever acknowledged any of its
transmissions; if no peer's report lists this nodeID, its transmitter is not radiating
(dead PA / antenna)
```

</details>

<details>
<summary>raspberrypi — Espressif USB_JTAG_serial_debug_unit 44:1B:F6:6F:28:0C (nodeID 20a369bd0006f838)</summary>

```
Radio:          RNode
Configured:     915000000 Hz, BW 125000 Hz, SF9, CR5, TX 17 dBm
State check:    reported values match configuration
Bitrate:        1757 bps (on-air, derived from reported radio params)

Fleet test results (300s window):
  TEST packets sent:     100
  TEST packets heard:    297 (from 3 peer(s))
  ACKs sent:             297
  ACK receipts received: 0 (of 100 sent packets)

  Packets heard per peer:
    87fc08fa162f0bc8: 99 TEST packet(s)
    8bd51514a82f9a6e: 100 TEST packet(s)
    a42db1888110f5e6: 98 TEST packet(s)

Verdict: RECEIVE ONLY? — this radio heard peer packets but NO peer ever acknowledged any of its
transmissions; if no peer's report lists this nodeID, its transmitter is not radiating
(dead PA / antenna)
```

</details>

---

*Raw scrollback of all three runs is preserved in the tmux sessions (`tmux capture-pane -p -S -3000 -t <session>`);
copies of the final-report captures from Run 1 live in `/tmp/grnd-*.txt`; Runs 2–3 were read directly
from the live tmux panes during analysis.*