// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// This file covers the whole fleet topology at once: a shared Reticulum
// instance on the client's machine (gornsd) with gonomadnet attached to it, a
// second shared instance on the hub's machine with gorrcd attached, and a
// network hop between them. The live incident happened on exactly this shape
// but over the internet, where the hub measured an RTT of 1.45 s, so the hop is
// a real TCP link through a transport node that can delay every frame to
// reproduce production timing on loopback.

// fleetInstanceConfig renders an RNS configuration whose [interfaces] section
// declares the network hop between the two instances, matching the fleet's TCP
// wiring (one machine listens, the other dials in).
func fleetInstanceConfig(instance string, instancePort, controlPort int, share, transport bool, interfacesSection string) string {
	return instanceConfig(instance, instancePort, controlPort, share, transport) + interfacesSection
}

// delayedFrame is one queued interface frame awaiting delayed delivery.
type delayedFrame struct {
	data  []byte
	iface interfaces.Interface
	due   time.Time
}

// delayedInbound returns an inbound handler that delivers every frame to ts
// after oneWay, in arrival order. Each frame is delayed independently, so a
// burst of frames keeps its shape and only shifts in time, the way a link with
// latency (but normal bandwidth) behaves.
func delayedInbound(ts *rns.TransportSystem, oneWay time.Duration) interfaces.InboundHandler {
	queue := make(chan delayedFrame, 4096)
	go func() {
		for frame := range queue {
			if wait := time.Until(frame.due); wait > 0 {
				time.Sleep(wait)
			}
			ts.Inbound(frame.data, frame.iface)
		}
	}()
	return func(data []byte, iface interfaces.Interface) {
		select {
		case queue <- delayedFrame{data: append([]byte(nil), data...), iface: iface, due: time.Now().Add(oneWay)}:
		default:
		}
	}
}

// fleetRig is two shared instances joined through a transport node, with an RRC
// client attached to one instance and an RRC hub attached to the other.
type fleetRig struct {
	t        *testing.T
	hub      *HubService
	logs     *lockedBuffer
	clientTS *rns.TransportSystem
	manager  *RRCManager
	client   *RRCHub
}

// newFleetRig builds the two-instance fleet topology. oneWay is the delay
// applied to every frame crossing the transport node between the instances.
func newFleetRig(t *testing.T, oneWay time.Duration, pingInterval, pingTimeout float64) *fleetRig {
	t.Helper()
	root := tempDir(t)
	clientInstancePort := freeLoopbackPort(t)
	clientFleetPort := freeLoopbackPort(t)
	hubInstancePort := freeLoopbackPort(t)
	relayListenPort := freeLoopbackPort(t)

	// gornsd on the client's machine: the shared instance that owns the network
	// interface and relays for its local clients.
	clientInstanceDir := filepath.Join(root, "client-instance-rns")
	writeInstanceConfig(t, clientInstanceDir,
		fleetInstanceConfig("rrc-client-instance", clientInstancePort, freeLoopbackPort(t), true, true,
			fmt.Sprintf(`
  [[Fleet Dial-Ins]]
    type = TCPServerInterface
    enabled = yes
    listen_ip = 127.0.0.1
    listen_port = %v
`, clientFleetPort)))
	tsClientInstance := rns.NewTransportSystem(silentLogger())
	if _, err := rns.NewReticulum(tsClientInstance, clientInstanceDir); err != nil {
		t.Fatalf("client instance NewReticulum: %v", err)
	}

	// The transport node between the two machines. Its TCP client dials the
	// client instance and its TCP server accepts the hub instance's uplink,
	// delaying every frame in both directions.
	relayDir := filepath.Join(root, "relay-rns")
	writeInstanceConfig(t, relayDir,
		instanceConfig("rrc-fleet-relay", freeLoopbackPort(t), freeLoopbackPort(t), false, true))
	tsRelay := rns.NewTransportSystem(silentLogger())
	if _, err := rns.NewReticulum(tsRelay, relayDir); err != nil {
		t.Fatalf("relay NewReticulum: %v", err)
	}
	relayServer, err := interfaces.NewTCPServerInterface("relay-from-hub", "127.0.0.1", relayListenPort,
		delayedInbound(tsRelay, oneWay),
		func(iface interfaces.Interface) { tsRelay.RegisterInterface(iface) })
	if err != nil {
		t.Fatalf("relay TCP server: %v", err)
	}
	relayClient, err := interfaces.NewTCPClientInterface("relay-to-client-instance", "127.0.0.1", clientFleetPort,
		false, delayedInbound(tsRelay, oneWay))
	if err != nil {
		t.Fatalf("relay TCP client: %v", err)
	}
	tsRelay.RegisterInterface(relayServer)
	tsRelay.RegisterInterface(relayClient)
	t.Cleanup(func() {
		_ = relayServer.Detach()
		_ = relayClient.Detach()
	})

	// gornsd on the hub's machine: a second shared instance, dialing the
	// transport node between the two machines.
	hubInstanceDir := filepath.Join(root, "hub-instance-rns")
	writeInstanceConfig(t, hubInstanceDir,
		fleetInstanceConfig("rrc-hub-instance", hubInstancePort, freeLoopbackPort(t), true, true,
			fmt.Sprintf(`
  [[Fleet Uplink]]
    type = TCPClientInterface
    enabled = yes
    target_host = 127.0.0.1
    target_port = %v
`, relayListenPort)))
	tsHubInstance := rns.NewTransportSystem(silentLogger())
	if _, err := rns.NewReticulum(tsHubInstance, hubInstanceDir); err != nil {
		t.Fatalf("hub instance NewReticulum: %v", err)
	}

	// gonomadnet: attached to the client's instance, owning no interface.
	clientDir := filepath.Join(root, "client-rns")
	writeInstanceConfig(t, clientDir,
		instanceConfig("rrc-fleet-client", clientInstancePort, freeLoopbackPort(t), true, false))
	tsClient := rns.NewTransportSystem(silentLogger())
	clientReticulum, err := rns.NewReticulum(tsClient, clientDir)
	if err != nil {
		t.Fatalf("client NewReticulum: %v", err)
	}
	if !clientReticulum.IsConnectedToSharedInstance() {
		t.Fatalf("client did not attach to its shared instance")
	}

	// gorrcd: attached to the hub's instance, owning no interface.
	hubDir := filepath.Join(root, "hub-rns")
	writeInstanceConfig(t, hubDir,
		instanceConfig("rrc-fleet-hub", hubInstancePort, freeLoopbackPort(t), true, false))
	tsHub := rns.NewTransportSystem(silentLogger())
	hubReticulum, err := rns.NewReticulum(tsHub, hubDir)
	if err != nil {
		t.Fatalf("hub NewReticulum: %v", err)
	}
	if !hubReticulum.IsConnectedToSharedInstance() {
		t.Fatalf("hub did not attach to its shared instance")
	}

	ident, err := rns.NewIdentity(true, silentLogger())
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	identPath := filepath.Join(root, "hub-identity")
	if err := ident.ToFile(identPath); err != nil {
		t.Fatalf("ToFile(%v): %v", identPath, err)
	}

	cfg := DefaultHubConfig()
	cfg.AnnounceOnStart = true
	cfg.PingIntervalS = pingInterval
	cfg.PingTimeoutS = pingTimeout
	cfg.OverrideRawConfigValue("ping_interval_s", pingInterval)
	cfg.OverrideRawConfigValue("ping_timeout_s", pingTimeout)
	cfg.IdentityPath = &identPath
	greeting := "Welcome to the RRC Public Hub! Type /help for commands."
	cfg.Greeting = &greeting

	hub := NewHubService(cfg)
	hub.startReticulum = func(string) error {
		hub.ts = tsHub
		return nil
	}
	logs := &lockedBuffer{}
	hub.logSetup.writer = logs
	hub.logSetup.Level = slog.LevelDebug
	if err := hub.Start(); err != nil {
		t.Fatalf("hub.Start: %v", err)
	}
	t.Cleanup(hub.Stop)

	manager := NewManager(filepath.Join(root, "client-storage"), func() []byte {
		return tsClient.Identity().Hash
	})
	manager.SetIdentity(tsClient.Identity())
	manager.SetNickname("gonomadnet on RaspPi")
	manager.SetTransport(tsClient)

	return &fleetRig{t: t, hub: hub, logs: logs, clientTS: tsClient, manager: manager}
}

// connect dials the hub across the two-instance fleet hop and reports whether
// the WELCOME arrived and how long it took.
func (r *fleetRig) connect(rooms ...string) (bool, time.Duration) {
	r.t.Helper()
	r.hub.AnnounceOnce()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && !r.clientTS.HasPath(r.hub.DestinationHash()) {
		time.Sleep(10 * time.Millisecond)
	}
	if !r.clientTS.HasPath(r.hub.DestinationHash()) {
		r.t.Fatalf("client never learned the hub path across the fleet hop; hub log:\n%v", r.logs.String())
	}

	client := r.manager.AddHub(r.hub.DestinationHash(), "rrc.hub", "Public Hub")
	for _, room := range rooms {
		client.AddRoom(room)
	}
	client.SetAutoReconnect(false, false)
	r.client = client
	r.t.Cleanup(client.Disconnect)

	start := time.Now()
	client.ConnectAsync()
	welcomeDeadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(welcomeDeadline) {
		client.lock.Lock()
		welcomed := client.Welcomed
		client.lock.Unlock()
		if welcomed {
			return true, time.Since(start)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false, time.Since(start)
}

// TestFleetTwoInstancesDeliverWelcome runs the fleet's two-instance topology
// with the hub's keepalive loop enabled and a slow hop between the instances:
// the client must be welcomed inside its HELLO retry window, HELLO and JOIN
// exactly once, answer every ping, and hold the link for several ping cycles.
func TestFleetTwoInstancesDeliverWelcome(t *testing.T) {
	t.Parallel()
	for _, oneWay := range []time.Duration{0, 350 * time.Millisecond} {
		t.Run(fmt.Sprintf("oneway=%v", oneWay), func(t *testing.T) {
			t.Parallel()
			const (
				pingInterval = 0.3
				pingTimeout  = 2.0
			)
			rig := newFleetRig(t, oneWay, pingInterval, pingTimeout)
			welcomed, elapsed := rig.connect("general")
			if !welcomed {
				t.Fatalf("client behind the shared instance never received WELCOME after %v; hub log:\n%v",
					elapsed.Round(time.Millisecond), rig.logs.String())
			}
			if elapsed > 3*time.Second {
				t.Errorf("WELCOME took %v, past the client's 3s HELLO retry window; hub log:\n%v",
					elapsed.Round(time.Millisecond), rig.logs.String())
			}

			// Hold the link across several cycles of the hub's keepalive loop.
			time.Sleep(2 * time.Second)
			logs := rig.logs.String()
			if got := strings.Count(logs, "HELLO peer="); got != 1 {
				t.Errorf("hub saw %v HELLOs, want exactly 1; hub log:\n%v", got, logs)
			}
			if got := strings.Count(logs, "Re-HELLO peer="); got != 0 {
				t.Errorf("hub saw %v Re-HELLOs, want 0; hub log:\n%v", got, logs)
			}
			if got := strings.Count(logs, "JOIN peer="); got != 1 {
				t.Errorf("hub saw %v JOINs, want exactly 1; hub log:\n%v", got, logs)
			}
			// The hub pings again only after each pong clears the marker, so
			// the pong rate is bounded by the round trip: a 0.7 s RTT yields
			// roughly one answered ping per second.
			if got := strings.Count(logs, " t=31 "); got < 2 {
				t.Errorf("hub received %v PONGs, want at least 2; hub log:\n%v", got, logs)
			}
			if got := strings.Count(logs, "Ping timeout"); got != 0 {
				t.Errorf("hub logged %v ping timeouts, want 0; hub log:\n%v", got, logs)
			}
			if got := strings.Count(logs, "Link closed"); got != 0 {
				t.Errorf("hub logged %v link closes, want 0; hub log:\n%v", got, logs)
			}
			rig.client.lock.Lock()
			status, motd := rig.client.Status, rig.client.MOTD
			rig.client.lock.Unlock()
			if status != StatusConnected {
				t.Errorf("client status=%v, want StatusConnected; hub log:\n%v", status, logs)
			}
			if !strings.Contains(motd, "Type /help") {
				t.Errorf("client MOTD=%q, want the hub greeting; hub log:\n%v", motd, logs)
			}
			t.Logf("oneway=%v welcomed_after=%v", oneWay, elapsed.Round(time.Millisecond))
		})
	}
}
