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

### Project 4: The Go Reticulum Lifesaver (GRL)

The **Go Reticulum Lifesaver (GRL)** is a sovereign, pocket-sized off-grid survival communicator and field assistant designed around a sealed, low-cost (~$22.50 BOM) hardware puck. It pairs an Espressif ESP32-C5 RISC-V SoC with an SX1262 LoRa transceiver, a multi-constellation GNSS receiver, an electronic compass, and an optional cryptographic ASIC coprocessor.

```
+-----------------------------------------------------------------------------+
|                           Traveler's Smartphone                             |
|          (iPhone / Android running Safari / Chrome in Airplane Mode)        |
+-------------------------------------+---------------------------------------+
                                      |
                                      | 5 GHz Wi-Fi 6 (Captive Portal / HTTP)
                                      v
+-----------------------------------------------------------------------------+
|             The Go Reticulum Lifesaver (GRL) (Sealed, Waterproof, ~$22 BOM) |
|                                                                             |
|  +-----------------------------------------------------------------------+  |
|  | ESP32-C5 Host MCU (RV32IMAC @ 240 MHz, 400KB SRAM + 8MB PSRAM)        |  |
|  |  - Wi-Fi 6 SoftAP ("Reticulum-Lifesaver-[ID]") + HTTP/Micron Portal   |  |
|  |  - Pure-Go RNS Transport + Local gorrcd Chat Hub                      |  |
|  |  - Embedded bot Field Engine (first aid, towers, ephemeris, OLC)      |  |
|  +-------------------+--------------------+--------------------+---------+  |
|                      |                    |                    |            |
|                      | SPI                | UART (9600)        | I2C        |
|                      v                    v                    v            |
|            +------------------+  +-----------------+  +-----------------+   |
|            | Semtech SX1262   |  | ATGM336H GNSS   |  | QMC5883L/LSM303 |   |
|            | LoRa Transceiver |  | Multi-Satellite |  | Digital Compass |   |
|            | (868/915 MHz)    |  | (GPS/BDS/GLO)   |  | (Magnetometer)  |   |
|            +------------------+  +-----------------+  +-----------------+   |
|                      |                    |                    |            |
|  +-------------------+--------------------+--------------------+---------+  |
|  | Power: 18650 Li-Ion (3000 mAh) + TP4056 USB-C Charger (3-5 days idle) |  |
|  | Storage: MicroSD (FAT32: offline survival manuals, topo maps, logs)   |  |
|  +-----------------------------------------------------------------------+  |
+-----------------------------------------------------------------------------+
```

#### The Zero-Hardware UI Paradigm

Rather than burdening the device with fragile screens and tiny keyboards that inflate cost to $80–$150, GRL uses the smartphone already in the traveler's pocket:

- **Automatic Captive Portal**: When the user connects to the device's Wi-Fi 6 SoftAP (`Reticulum-Lifesaver-[ID]`), iOS and Android automatically pop up the captive dashboard without installing any app, creating accounts, or requiring cellular service.
- **In-Process Survival Engine**: Wilderness first aid protocols (`med`), repeater and cell tower catalogs (`tower near`), solar/lunar ephemeris (`sun`, `moon`), geodetics (`whereami`, `geo`), and Plus Codes (`olc`) resolve locally in microseconds with **zero radio airtime and zero network hops**.
- **Radio for True Emergencies**: LoRa radio airtime is reserved exclusively for broadcasting signed emergency SOS distress beacons, coordinating with human rescue parties, peer messaging, and syncing with hubs when reachable.

#### Sensor & Navigation Subsystem

- **Multi-Constellation GNSS**: A high-sensitivity (-162 dBm) GNSS receiver (ATGM336H or Quectel L80) outputs standard NMEA-0183 sentences over UART at 9600 baud, deriving 10-character Plus Codes (Open Location Codes) and Maidenhead grid locators. Coordinates automatically inject into all operational commands.
- **3-Axis Digital Compass (RDF)**: A magnetometer (QMC5883L or LSM303DLHC) on I2C provides stationary 360° heading, unaffected by GPS stationary blindness. Combined with the World Magnetic Model (WMM2025), it calculates True North and delivers relative steering guidance for directional antennas (e.g. `Turn 15° RIGHT · 1 o'clock`).

#### Hardware Bill of Materials (BOM)

| Component | Part / Specification | Approx. Cost | Source / Notes |
|---|---|---|---|
| **Host MCU** | Espressif ESP32-C5-DevKitC-1 | ~$4.00 | Dual-band Wi-Fi 6 (2.4/5 GHz), BLE 5, 240 MHz RV32, 8MB PSRAM |
| **LoRa Radio** | Semtech SX1262 SPI module (+22 dBm, 868/915 MHz) | ~$4.50 | Ebyte E22-900M22S or Ai-Thinker Ra-01SH |
| **GNSS / GPS** | ATGM336H or Quectel L80-M39 (ceramic patch antenna) | ~$3.50 | 3.3V UART, BDS/GPS/GLO, -162 dBm sensitivity |
| **Compass / RDF** | QMC5883L or LSM303DLHC (I2C breakout board) | ~$1.50 | 3.3V I2C, 360° stationary heading, relative antenna pointing |
| **Storage** | MicroSD card slot + 16GB FAT32 card | ~$3.00 | Stores offline survival manuals, topo maps, and logs |
| **Battery & Power** | 18650 Li-Ion cell (3000 mAh) + TP4056 USB-C charger | ~$4.00 | 3–5 days active standby; weeks on duty-cycled sleep |
| **Enclosure** | 3D-printed ruggedized PETG/TPU carabiner case | ~$2.00 | Compact, water-resistant, shock-absorbing |
| **Total BOM** | Complete sovereign off-grid communicator | **~$22.50** | **1/20th the cost of proprietary satellite hardware** |

#### ESP32-C5 Pin Allocation Table (Zero Contention)

| Subsystem | Signal Name | ESP32-C5 Pin | Direction | Description |
|---|---|---|---|---|
| **SX1262 LoRa** | `SCK` | `GPIO 11` | Host $\rightarrow$ Radio | SPI Bus Clock |
| | `MOSI` | `GPIO 12` | Host $\rightarrow$ Radio | SPI Data In |
| | `MISO` | `GPIO 13` | Radio $\rightarrow$ Host | SPI Data Out |
| | `CS#` | `GPIO 14` | Host $\rightarrow$ Radio | Active-low chip select |
| | `RST` | `GPIO 15` | Host $\rightarrow$ Radio | Hardware reset |
| | `BUSY` | `GPIO 16` | Radio $\rightarrow$ Host | Modem busy status line |
| | `DIO1` | `GPIO 17` | Radio $\rightarrow$ Host | Packet received / TX done interrupt |
| **GNSS (GPS)** | `RX1` | `GPIO 9` | GNSS $\rightarrow$ Host | NMEA-0183 serial stream (9600 baud) |
| | `TX1` | `GPIO 10` | Host $\rightarrow$ GNSS | Optional configuration commands |
| **Compass (I2C)** | `SDA` | `GPIO 21` | Bi-directional | I2C Data bus (QMC5883L / LSM303) |
| | `SCL` | `GPIO 22` | Host $\rightarrow$ Compass | I2C Clock bus |
| **MicroSD** | `DAT0` / `CLK` / `CMD` | `GPIO 18, 19, 20` | Bi-directional | Standard SD 1-bit or SPI mode |
| **Power Sense** | `BATT_ADC` | `GPIO 1` (ADC1_CH0) | Analog In | Resistor divider to monitor battery voltage |
| **Crypto ASIC** | `CLK`, `CS`, `IO0..IO3`, `IRQ` | `GPIO 2..8` | Bi-directional | 7-Pin QSPI interconnect + interrupt (optional) |

!!! danger "Safety, Emergency & Medical Disclaimer"
    **GRL is an experimental open-source appliance.** It is **NOT** a certified life-safety device, NOT a certified medical instrument, and NOT connected to official 911/112 dispatch or government search-and-rescue satellites. Transmission over unlicensed LoRa mesh frequencies is best-effort and never guaranteed. Always carry certified primary safety equipment (EPIRB/PLB, paper maps, magnetic compass). See the [**full legal and safety disclaimer**](../tools/grl.md#legal-safety-emergency-and-medical-disclaimer) for complete terms.

For complete software and configuration details, see the [`grl` tool documentation](../tools/grl.md) and [ASIC Plans §6.9–§6.12](https://github.com/gmlewis/go-reticulum/blob/master/ASIC-Plans.md).

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
