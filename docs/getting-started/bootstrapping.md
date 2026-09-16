# Bootstrapping Connectivity

Reticulum can operate completely offline over local radios or ethernet multicast, but it can also connect to the global Reticulum mesh over TCP, UDP, or I2P.

---

## Configuring Interfaces

Open `~/.reticulum/config` in your text editor. Interface definitions reside under the `[interfaces]` section.

### Local Network Auto-Discovery (AutoInterface)

To discover and connect automatically to any other Reticulum nodes on your local Wi-Fi or Ethernet LAN:

```ini
[[Default Interface]]
  type = AutoInterface
  enabled = yes
```

`AutoInterface` uses IPv4/IPv6 link-local multicast (UDP port `29716`) to peer with neighboring nodes without manual configuration.

---

### Connecting to an Existing Node over TCP (TCPClientInterface)

To connect to a remote Reticulum node or hub over the internet:

```ini
[[gonomadnet Public RRC Hub]]
  type = TCPClientInterface
  enabled = yes
  target_host = go-nomadnet.duckdns.org
  target_port = 4242
```

Popular public testnet bootstrap nodes include:

| Location | Target Host | Port |
|----------|-------------|------|
| **Dublin, Ireland** | `dublin.connect.reticulum.network` | `4965` |
| **Frankfurt, Germany** | `frankfurt.connect.reticulum.network` | `4965` |
| **Washington DC, USA** | `between加.connect.reticulum.network` | `4965` |

---

### LoRa Radio Interface (RNodeInterface)

To connect a LoRa radio flashed with RNode firmware over USB serial:

```ini
[[LoRa RNode]]
  type = RNodeInterface
  enabled = yes
  port = /dev/ttyUSB0
  frequency = 915000000       # e.g., 915000000 for US, 868000000 for EU
  bandwidth = 125000
  txpower = 17
  spreadingfactor = 9
  codingrate = 5
```

Use `gornodeconf` to inspect, provision, and test the connected RNode.

---

## Verifying Mesh Connectivity

Once interfaces are configured and Reticulum is running:

### 1. Check Interface Status

```bash
gornstatus
```

Ensure the configured interface shows `Status: Up` and that traffic counters increment.

### 2. Probe Reachability

Query paths to an announced destination hash:

```bash
gornpath <destination_hash>
```

If a path is known, it outputs the hop count, next hop interface, and path metric.

To ping a remote destination:

```bash
gornprobe <destination_hash>
```

Output:
```
Sent 16 byte probe to <destination_hash>
Valid reply received in 42.18 ms (1 hop)
```
