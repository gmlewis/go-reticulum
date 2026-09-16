# gornodeconf — RNode Hardware & Firmware Manager

`gornodeconf` is the complete Go utility for inspecting, configuring, provisioning, backing up, and flashing LoRa RNode hardware over USB serial connections.

---

## Supported Platforms

`gornodeconf` live serial workflows are supported on:
- **Linux** (e.g. `/dev/ttyUSB0`, `/dev/ttyACM0`)
- **macOS / Darwin** (e.g. `/dev/cu.usbserial-*`, `/dev/cu.usbmodem*`)
- **FreeBSD** (e.g. `/dev/cuaU0`)

*(Note: Windows is intentionally unsupported for direct serial hardware workflows due to driver variations).*

---

## Common Workflow

### 1. Inspect Device

Check the connected RNode's current EEPROM status, product code, model, and firmware hash:

```bash
gornodeconf -i /dev/ttyUSB0
gornodeconf --public /dev/ttyUSB0
gornodeconf --get-firmware-hash /dev/ttyUSB0
```

### 2. Generate Local Signing Material

Before bootstrapping or signing firmware on fresh devices, generate local signing keys:

```bash
gornodeconf --key
gornodeconf --public
```

This creates `signing.key` and `device.key` under `~/.config/rnodeconf/firmware/`.

### 3. Back Up Device EEPROM

Always take an EEPROM backup before making modifications:

```bash
gornodeconf --eeprom-backup /dev/ttyUSB0
gornodeconf --eeprom-dump /dev/ttyUSB0
```

Timestamped EEPROM binaries are saved under `~/.config/rnodeconf/eeprom/`.

### 4. Bootstrap Fresh Hardware

To provision a newly assembled board or unconfigured ESP32/SX1262 LoRa module:

```bash
gornodeconf --rom --product 03 --model a4 --hwrev 5 /dev/ttyUSB0
```

This writes the cryptographic EEPROM signature and assigns a hardware serial number.

### 5. Flash Firmware

To flash the official RNode firmware image:

```bash
gornodeconf --flash /dev/ttyUSB0
```

To update firmware on an existing provisioned device:

```bash
gornodeconf --update /dev/ttyUSB0
```

---

## Device Recovery

If a device is corrupted or locked:

```bash
# Unlock a write-protected ROM
gornodeconf --unlock-rom /dev/ttyUSB0

# Wipe EEPROM back to factory state
gornodeconf --eeprom-wipe /dev/ttyUSB0
```
