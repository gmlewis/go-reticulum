# Radio Diagnostics & Fleet Monitoring

Managing LoRa mesh networks requires ongoing radio telemetry and link quality assessment.

---

## Fleet Diagnostics with `gornode-diagnostics`

`gornode-diagnostics` profiles multiple connected RNodes and serial devices across a fleet, reporting hardware revisions, firmware integrity, and RF performance:

```bash
gornode-diagnostics /dev/ttyUSB*
```

---

## Monitoring RF Metrics

When evaluating LoRa link reliability, monitor:

- **RSSI (Received Signal Strength Indication)**:
  - Strong: `-60 dBm` to `-90 dBm`
  - Marginal: `-90 dBm` to `-115 dBm`
  - Limit of sensitivity: `-115 dBm` to `-128 dBm`
- **SNR (Signal-to-Noise Ratio)**:
  - Excellent: `> +5 dB`
  - Acceptable: `0 dB` to `-10 dB`
  - Deep fade / edge: `-10 dB` to `-20 dB` (LoRa can decode below noise floor down to `-20 dB` at SF12)

---

## Reticulum Diagnostics

Use `gornstatus -a` to monitor packet throughput, dropped frames, and interface error rates across all active physical interfaces.
