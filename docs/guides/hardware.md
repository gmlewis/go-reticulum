# Off-Grid Hardware Projects

Go Reticulum is designed for field resilience and low-power hardware.

For detailed manufacturing instructions, complete bills of materials (BOM), Gerber files, pre-compiled firmware binaries, and one-click in-browser flashing, consult the [**Hardware Projects Guide**](https://github.com/gmlewis/asic-reticulum/tree/master/Hardware-Projects-Guide.md).

---

## Standalone Handheld Devices

### Project 1: The Pocket Linux Terminal

- **Compute**: Raspberry Pi Zero 2W (quad-core 64-bit ARM)
- **Display**: 2.8" ST7789 SPI color LCD (320×240)
- **Input**: I2C M5Stack CardKB full QWERTY keyboard
- **Radio**: Semtech SX1262 LoRa transceiver (868/915 MHz)
- **Power**: 18650 Li-ion cell with integrated TP4056 charging circuit
- **Software**: Linux with Go Reticulum + NomadNet TUI

### Project 2: The Standalone Pocket Communicator

- **Compute**: Espressif ESP32-C5 (single-core RISC-V SoC with native 2.4/5 GHz Wi-Fi 6 and Bluetooth 5)
- **Display**: E-Paper or daylight-readable transflective LCD
- **Radio**: Integrated SPI Semtech SX1262 LoRa transceiver
- **Battery Life**: Multi-day standby runtime with deep-sleep support

### Project 3: The Autonomous Pocket Hub & Repeater

- **Compute**: ESP32-C5 / ESP32-S3
- **Operation**: Headless, solar-powered mesh repeater and RRC relay daemon
- **Connectivity**: Simultaneously bridges LoRa mesh packets to a local Wi-Fi 6 SoftAP access point for nearby phones and laptops

---

## Recommended Off-The-Shelf RNode Hardware

If purchasing ready-to-use hardware:

| Device | Frequency | Architecture | Notes |
|--------|-----------|--------------|-------|
| **LilyGO TTGO T-Beam v1.1 / Supreme** | 868 / 915 MHz | ESP32 / SX1262 | Built-in GPS, 18650 battery holder, OLED display |
| **LilyGO T-Echo** | 868 / 915 MHz | NRF52840 / SX1262 | Ultra-low power, sunlight-readable E-paper display, GPS |
| **Heltec WiFi LoRa 32 (v3)** | 868 / 915 MHz | ESP32-S3 / SX1262 | Compact, integrated 0.96" OLED |
| **RNode (Custom PCB)** | 433 / 868 / 915 MHz | ATmega1284P or ESP32 | Reference hardware designed by Mark Qvist |

All of these devices can be inspected, provisioned, and flashed using [`gornodeconf`](../tools/gornodeconf.md).
