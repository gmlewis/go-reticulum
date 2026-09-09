# RNode Fleet Radio Diagnostics Report

**Date:** 2026-09-09 · **Tool:** `gornode-diagnostics` (first real fleet run) · **Window:** 15:33:47–15:39:55 local
**Setup:** defaults — 60s grace, 300s test, 915.000 MHz / BW 125 kHz / SF9 / CR5 / TX 17 dBm, ~1 TEST packet every 3s
**Fleet:** 5 RNodes on 5 machines (`glenn-nano2gb` has no RNode — its run correctly reported "no RNode found")

---

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

### 3.6 Minor tool observations (from the live run, worth a small future tweak)
- **Duplicate device listing on Linux:** each Linux box listed the same physical radio twice — once
  as `/dev/serial/by-id/usb-Espressif_USB_JTAG_serial_debug_unit_…-if00` (DETECTED) and once as
  `/dev/ttyACM0` (IN USE). The "IN USE" holder was the diagnostics process *itself*: `/proc/*/fd`
  readlink resolves the by-id symlink to the canonical `/dev/ttyACM0`, so the tool saw its own
  already-open fd. The outcome was correct (the radio was tested once), but excluding the tool's own
  PID from the holder scan and de-duplicating device nodes that share a USB serial number would make
  the sniff output cleaner.
- **Firmware/platform details missing:** the live units answered the DETECT request but their
  firmware/platform/board replies weren't captured within the probe window, so the report shows
  "RNode" without version/platform info. A slightly longer probe window (or retry of the
  fw/platform/mcu/board queries) would fill that in.

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
3. **Consider an ACK-thinning option in `gornode-diagnostics`** (e.g. `-ack-every N`) to reduce
   channel load on the next run and shrink RTTs; today's run validated TX/RX conclusively at ~90%
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

*Raw scrollback of all five runs is preserved in the tmux sessions (`tmux capture-pane -p -S -3000 -t <session>`);
copies of the five final-report captures from this analysis live in `/tmp/grnd-*.txt`.*