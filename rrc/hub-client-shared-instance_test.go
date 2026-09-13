// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// This file covers the client half of the fleet topology. On the fleet every
// user-facing program (gonomadnet, gorrcd) attaches to gornsd as a local client
// and owns no interface of its own, so the RRC client's link to a remote hub is
// a link whose packets leave through the shared instance and whose replies must
// come back through it. That reverse path is what the incident report found
// dead, so it is tested here with the hub one interface away from the instance.

// TestClientBehindSharedInstanceReachesRemoteHub dials a remote hub from a
// client that is attached to a shared instance, the way gonomadnet attaches to
// gornsd on the fleet. The client must learn the hub path, receive the WELCOME,
// HELLO and JOIN exactly once, answer the hub's pings, and hold the link.
func TestClientBehindSharedInstanceReachesRemoteHub(t *testing.T) {
	t.Parallel()
	root := tempDir(t)
	instancePort := freeLoopbackPort(t)

	// gornsd: the shared instance that owns every interface and relays.
	instanceDir := filepath.Join(root, "instance-rns")
	writeInstanceConfig(t, instanceDir,
		instanceConfig("rrc-local-instance", instancePort, freeLoopbackPort(t), true, true))
	tsInstance := rns.NewTransportSystem(silentLogger())
	if _, err := rns.NewReticulum(tsInstance, instanceDir); err != nil {
		t.Fatalf("shared instance NewReticulum: %v", err)
	}

	// gonomadnet: a local client of the instance, with no interfaces of its own.
	clientDir := filepath.Join(root, "client-rns")
	writeInstanceConfig(t, clientDir,
		instanceConfig("rrc-local-client", instancePort, freeLoopbackPort(t), true, false))
	tsClient := rns.NewTransportSystem(silentLogger())
	clientReticulum, err := rns.NewReticulum(tsClient, clientDir)
	if err != nil {
		t.Fatalf("client NewReticulum: %v", err)
	}
	if !clientReticulum.IsConnectedToSharedInstance() {
		t.Fatalf("client did not attach to the shared instance (shared=%v standalone=%v)",
			clientReticulum.IsSharedInstance(), clientReticulum.IsStandaloneInstance())
	}

	// The hub node is remote: it reaches the instance over one interface.
	hubDir := filepath.Join(root, "hub-rns")
	writeInstanceConfig(t, hubDir,
		instanceConfig("rrc-remote-hub", freeLoopbackPort(t), freeLoopbackPort(t), false, false))
	tsHub := rns.NewTransportSystem(silentLogger())
	if _, err := rns.NewReticulum(tsHub, hubDir); err != nil {
		t.Fatalf("hub NewReticulum: %v", err)
	}

	pipeInstance := interfaces.NewPipeInterface("instance-to-hub", func(data []byte, iface interfaces.Interface) {
		tsInstance.Inbound(data, iface)
	})
	pipeHub := interfaces.NewPipeInterface("hub-to-instance", func(data []byte, iface interfaces.Interface) {
		tsHub.Inbound(data, iface)
	})
	pipeInstance.SetOther(pipeHub)
	pipeHub.SetOther(pipeInstance)
	tsInstance.RegisterInterface(pipeInstance)
	tsHub.RegisterInterface(pipeHub)
	t.Cleanup(func() {
		_ = pipeInstance.Detach()
		_ = pipeHub.Detach()
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
	cfg.PingIntervalS = 0.3
	cfg.PingTimeoutS = 0.9
	cfg.OverrideRawConfigValue("ping_interval_s", 0.3)
	cfg.OverrideRawConfigValue("ping_timeout_s", 0.9)
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

	hub.AnnounceOnce()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !tsClient.HasPath(hub.DestinationHash()) {
		time.Sleep(10 * time.Millisecond)
	}
	if !tsClient.HasPath(hub.DestinationHash()) {
		t.Fatalf("client never learned the hub path through the shared instance; hub log:\n%v", logs.String())
	}

	client := manager.AddHub(hub.DestinationHash(), "rrc.hub", "Public Hub")
	client.AddRoom("general")
	client.SetAutoReconnect(false, false)
	t.Cleanup(client.Disconnect)
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
	if !welcomed {
		t.Fatalf("client behind the shared instance never received WELCOME; hub log:\n%v", logs.String())
	}
	time.Sleep(time.Second)

	hubLogs := logs.String()
	if got := strings.Count(hubLogs, "HELLO peer="); got != 1 {
		t.Errorf("hub saw %v HELLOs, want exactly 1; hub log:\n%v", got, hubLogs)
	}
	if got := strings.Count(hubLogs, "JOIN peer="); got != 1 {
		t.Errorf("hub saw %v JOINs, want exactly 1; hub log:\n%v", got, hubLogs)
	}
	if got := strings.Count(hubLogs, " t=31 "); got < 2 {
		t.Errorf("hub received %v PONGs from the attached client, want at least 2; hub log:\n%v", got, hubLogs)
	}
	if got := strings.Count(hubLogs, "Ping timeout"); got != 0 {
		t.Errorf("hub logged %v ping timeouts; hub log:\n%v", got, hubLogs)
	}
	if got := strings.Count(hubLogs, "Link closed"); got != 0 {
		t.Errorf("hub logged %v link closes; hub log:\n%v", got, hubLogs)
	}
	client.lock.Lock()
	status, motd := client.Status, client.MOTD
	client.lock.Unlock()
	if status != StatusConnected {
		t.Errorf("client status=%v, want StatusConnected; hub log:\n%v", status, hubLogs)
	}
	if !strings.Contains(motd, "Type /help") {
		t.Errorf("client MOTD=%q, want the greeting delivered through the shared instance; hub log:\n%v",
			motd, hubLogs)
	}
	t.Logf("hub log:\n%v", hubLogs)
}
