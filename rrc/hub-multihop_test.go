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

// This file covers the relay half of the live topology: the public hub the
// fleet connects to is reached through a TCP gateway and the wider Reticulum
// mesh, so a client's link to the hub can ride a multi-hop path. The client's
// own packets (HELLO, JOIN) travel outward along that path; everything the hub
// sends back (WELCOME, PING) must travel the other way through the same relay
// link entries, which is the direction the incident report found dead.

// multihopConfig renders a per-test RNS configuration for a node on a private
// loopback mesh: transport selects whether the node relays for others.
func multihopConfig(instance string, controlPort int, transport bool) string {
	transportValue := "No"
	if transport {
		transportValue = "Yes"
	}
	return fmt.Sprintf(`[reticulum]
instance_name = %v
share_instance = No
shared_instance_type = tcp
instance_control_port = %v
enable_transport = %v

[logging]
loglevel = 4

[interfaces]
`, instance, controlPort, transportValue)
}

// linkPipe joins two transport systems with an in-memory pipe pair, the way a
// configured interface joins two nodes.
func linkPipe(t *testing.T, a, b *rns.TransportSystem, name string) []*interfaces.PipeInterface {
	t.Helper()
	pipeA := interfaces.NewPipeInterface(name+"-a", func(data []byte, iface interfaces.Interface) {
		a.Inbound(data, iface)
	})
	pipeB := interfaces.NewPipeInterface(name+"-b", func(data []byte, iface interfaces.Interface) {
		b.Inbound(data, iface)
	})
	pipeA.SetOther(pipeB)
	pipeB.SetOther(pipeA)
	a.RegisterInterface(pipeA)
	b.RegisterInterface(pipeB)
	return []*interfaces.PipeInterface{pipeA, pipeB}
}

// multiHopHubRig is the relay topology: a hub, a chain of transport nodes that
// relay for it, and a client at the far end of the chain.
type multiHopHubRig struct {
	t        *testing.T
	hub      *HubService
	logs     *lockedBuffer
	clientTS *rns.TransportSystem
	manager  *RRCManager
	client   *RRCHub
	relays   int
}

// newMultiHopHubRig wires client -> relay[relays-1] -> ... -> relay[0] -> hub
// over loopback pipes only. A larger relay count puts the client's link to the
// hub further from the hub's own interface.
func newMultiHopHubRig(t *testing.T, relays int, pingInterval, pingTimeout float64) *multiHopHubRig {
	t.Helper()
	root := tempDir(t)

	hubDir := filepath.Join(root, "hub-rns")
	writeInstanceConfig(t, hubDir, multihopConfig("rrc-multihop-hub", freeLoopbackPort(t), false))
	tsHub := rns.NewTransportSystem(silentLogger())
	if _, err := rns.NewReticulum(tsHub, hubDir); err != nil {
		t.Fatalf("hub NewReticulum: %v", err)
	}

	// The chain runs hub -> relays[0] -> ... -> relays[n-1] -> client.
	nodes := []*rns.TransportSystem{tsHub}
	for i := range relays {
		dir := filepath.Join(root, fmt.Sprintf("relay-%v-rns", i))
		writeInstanceConfig(t, dir, multihopConfig(fmt.Sprintf("rrc-multihop-relay-%v", i), freeLoopbackPort(t), true))
		tsRelay := rns.NewTransportSystem(silentLogger())
		if _, err := rns.NewReticulum(tsRelay, dir); err != nil {
			t.Fatalf("relay %v NewReticulum: %v", i, err)
		}
		nodes = append(nodes, tsRelay)
	}

	clientDir := filepath.Join(root, "client-rns")
	writeInstanceConfig(t, clientDir, multihopConfig("rrc-multihop-client", freeLoopbackPort(t), false))
	tsClient := rns.NewTransportSystem(silentLogger())
	if _, err := rns.NewReticulum(tsClient, clientDir); err != nil {
		t.Fatalf("client NewReticulum: %v", err)
	}
	nodes = append(nodes, tsClient)

	var pipes []*interfaces.PipeInterface
	for i := 0; i+1 < len(nodes); i++ {
		pipes = append(pipes, linkPipe(t, nodes[i], nodes[i+1], fmt.Sprintf("hop-%v", i))...)
	}
	t.Cleanup(func() {
		for _, p := range pipes {
			_ = p.Detach()
		}
	})

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
	greeting := "Welcome! JOIN #general to discuss go-nomadnet, go-reticulum, and asic-reticulum."
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

	return &multiHopHubRig{
		t:        t,
		hub:      hub,
		logs:     logs,
		clientTS: tsClient,
		manager:  manager,
		relays:   relays,
	}
}

// connect dials the hub through the relay chain and reports whether the
// WELCOME arrived.
func (r *multiHopHubRig) connect() bool {
	r.t.Helper()
	r.hub.AnnounceOnce()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !r.clientTS.HasPath(r.hub.DestinationHash()) {
		time.Sleep(10 * time.Millisecond)
	}
	hops := r.clientTS.HopsTo(r.hub.DestinationHash())
	hasPath := r.clientTS.HasPath(r.hub.DestinationHash())

	client := r.manager.AddHub(r.hub.DestinationHash(), "rrc.hub", "Public Hub")
	client.AddRoom("general")
	client.SetAutoReconnect(false, false)
	r.client = client
	r.t.Cleanup(client.Disconnect)
	client.ConnectAsync()

	welcomed := false
	welcomeDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(welcomeDeadline) {
		client.lock.Lock()
		welcomed = client.Welcomed
		client.lock.Unlock()
		if welcomed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.t.Logf("relays=%v hasPath=%v hops=%v welcomed=%v", r.relays, hasPath, hops, welcomed)
	return welcomed
}

// TestMultiHopHubDeliversWelcome walks the relay chain length from a single
// relay outward: for each length the client must be welcomed (a WELCOME that
// fits the link MTU and is forwarded back along the relay link entries), HELLO
// and JOIN exactly once, and the link must survive the hub's ping loop.
func TestMultiHopHubDeliversWelcome(t *testing.T) {
	t.Parallel()
	for _, relays := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("relays=%v", relays), func(t *testing.T) {
			t.Parallel()
			const (
				pingInterval = 0.3
				pingTimeout  = 0.9
			)
			rig := newMultiHopHubRig(t, relays, pingInterval, pingTimeout)

			if !rig.connect() {
				t.Fatalf("client never received WELCOME through %v relay(s); hub log:\n%v",
					relays, rig.logs.String())
			}
			time.Sleep(time.Second)

			logs := rig.logs.String()
			if got := strings.Count(logs, "HELLO peer="); got != 1 {
				t.Errorf("hub saw %v HELLOs, want exactly 1; hub log:\n%v", got, logs)
			}
			if got := strings.Count(logs, "JOIN peer="); got != 1 {
				t.Errorf("hub saw %v JOINs, want exactly 1; hub log:\n%v", got, logs)
			}
			if got := strings.Count(logs, " t=31 "); got < 2 {
				t.Errorf("hub received %v PONGs through %v relay(s), want at least 2; hub log:\n%v",
					got, relays, logs)
			}
			if got := strings.Count(logs, "Ping timeout"); got != 0 {
				t.Errorf("hub logged %v ping timeouts through %v relay(s); hub log:\n%v", got, relays, logs)
			}
			if got := strings.Count(logs, "Link closed"); got != 0 {
				t.Errorf("hub logged %v link closes through %v relay(s); hub log:\n%v", got, relays, logs)
			}
			rig.client.lock.Lock()
			status, motd := rig.client.Status, rig.client.MOTD
			rig.client.lock.Unlock()
			if status != StatusConnected {
				t.Errorf("client status=%v, want StatusConnected; hub log:\n%v", status, logs)
			}
			if !strings.Contains(motd, "JOIN #general") {
				t.Errorf("client MOTD=%q, want the greeting delivered through %v relay(s); hub log:\n%v",
					motd, relays, logs)
			}
			t.Logf("hub log:\n%v", logs)
		})
	}
}
