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

Once connected to `go-nomadnet.duckdns.org:4242`, all public services hosted on the hub are immediately reachable over the mesh:

| Service Daemon | Destination Hash | Protocol / URL | Description |
|----------------|------------------|----------------|-------------|
| **gonomadnet Node** | `<c7d0e7bbd883e595f53e14fa6986188c>` | `nomadnetwork://c7d0e7bbd883e595f53e14fa6986188c` | Live Micron pages served over Reticulum. |
| [**gorrcd Chat Hub**](../tools/gorrcd.md) | `<a012129c10205c0b9441fcd2b755b2a7>` | `rrc://a012129c10205c0b9441fcd2b755b2a7/#general` | Public RRC chat hub. Home of `@gobot`! |
| **gorngit Repos** | `<58a0406047ec2e7ce23e9e9a83b744df>` | `rns://58a0406047ec2e7ce23e9e9a83b744df/<repo>` | Git clone & push over Reticulum mesh links. |
| **gorngit Pages** | `<cb3677a1bb8e37f334096566ed8ff895>` | `nomadnetwork://cb3677a1bb8e37f334096566ed8ff895` | Micron code browser for mesh-hosted repositories. |
| [**golxmd Propagation**](../tools/golxmd.md) | `<7acc095f0e83182feb58c888d090a3cc>` | `lxmf.propagation` | Store-and-forward LXMF message propagation node. |


Popular public bootstrap and testnet nodes include:

| Hub / Location | Target Host | Port | Notes |
|---|---|---|---|
| **Go Reticulum Public Hub** | `go-nomadnet.duckdns.org` | `4242` | Official Go Reticulum public hub |
| **Between The Borders (US)** | `reticulum.betweentheborders.com` | `4242` | Community testnet node |

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
