// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// This file covers the RRC topology the live fleet actually runs: a shared
// Reticulum instance (gornsd) that owns the interfaces, an RRC hub attached to
// that instance as a local client (gorrcd), and a remote RRC client
// (gonomadnet) that reaches the hub through the shared instance. The hub's
// link is therefore an incoming link whose replies must travel back through
// the shared instance, which is the path the incident report suspected.

// freeLoopbackPort reserves a free loopback TCP port for a test. The listener
// is closed before returning; the shared-instance rig needs distinct ports so
// a test cannot attach to another test's instance (or to the developer's live
// shared instance on the default port).
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return port
}

// instanceConfig renders a per-test RNS configuration. share decides whether
// the node shares an instance (the first node to claim the port becomes the
// shared instance and later ones attach to it) and transport whether it relays
// for others.
func instanceConfig(instance string, instancePort, controlPort int, share, transport bool) string {
	shareValue := "No"
	if share {
		shareValue = "Yes"
	}
	transportValue := "No"
	if transport {
		transportValue = "Yes"
	}
	return fmt.Sprintf(`[reticulum]
instance_name = %v
share_instance = %v
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v
enable_transport = %v

[logging]
loglevel = 4

[interfaces]
`, instance, shareValue, instancePort, controlPort, transportValue)
}

// writeInstanceConfig writes an RNS config file into its own directory.
func writeInstanceConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%v): %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
}

// sharedHubRig is the fleet-shaped RRC topology on loopback only: the shared
// instance, the attached hub, and a remote client two interfaces away.
type sharedHubRig struct {
	t        *testing.T
	hub      *HubService
	logs     *lockedBuffer
	clientTS *rns.TransportSystem
	manager  *RRCManager
	client   *RRCHub
	// blockClientInbound wedges the hub->client direction: the client keeps
	// sending and hears nothing back, while the hub holds its link healthy.
	blockClientInbound *atomic.Bool
}

// newSharedHubRig builds the shared-instance topology with a fast ping
// interval so several keepalive cycles run inside the test.
func newSharedHubRig(t *testing.T, pingInterval, pingTimeout float64) *sharedHubRig {
	t.Helper()
	root := tempDir(t)
	instancePort := freeLoopbackPort(t)
	controlPort := freeLoopbackPort(t)

	// The shared instance owns the network interfaces and relays.
	instanceDir := filepath.Join(root, "instance-rns")
	writeInstanceConfig(t, instanceDir,
		instanceConfig("rrc-flap-instance", instancePort, controlPort, true, true))
	tsInstance := rns.NewTransportSystem(silentLogger())
	if _, err := rns.NewReticulum(tsInstance, instanceDir); err != nil {
		t.Fatalf("shared instance NewReticulum: %v", err)
	}

	// The hub attaches to the running instance instead of owning interfaces.
	hubDir := filepath.Join(root, "hub-rns")
	writeInstanceConfig(t, hubDir,
		instanceConfig("rrc-flap-hub", instancePort, controlPort, true, false))
	tsHub := rns.NewTransportSystem(silentLogger())
	hubReticulum, err := rns.NewReticulum(tsHub, hubDir)
	if err != nil {
		t.Fatalf("hub NewReticulum: %v", err)
	}
	if !hubReticulum.IsConnectedToSharedInstance() {
		t.Fatalf("hub did not attach to the shared instance (shared=%v standalone=%v)",
			hubReticulum.IsSharedInstance(), hubReticulum.IsStandaloneInstance())
	}

	// The remote client node reaches the hub through the instance.
	clientDir := filepath.Join(root, "client-rns")
	writeInstanceConfig(t, clientDir,
		instanceConfig("rrc-flap-client", freeLoopbackPort(t), freeLoopbackPort(t), false, false))
	tsClient := rns.NewTransportSystem(silentLogger())
	if _, err := rns.NewReticulum(tsClient, clientDir); err != nil {
		t.Fatalf("client NewReticulum: %v", err)
	}

	pipeInstance := interfaces.NewPipeInterface("instance-to-client", func(data []byte, iface interfaces.Interface) {
		tsInstance.Inbound(data, iface)
	})
	blockClientInbound := &atomic.Bool{}
	pipeClient := interfaces.NewPipeInterface("client-to-instance", func(data []byte, iface interfaces.Interface) {
		if blockClientInbound.Load() {
			return
		}
		tsClient.Inbound(data, iface)
	})
	pipeInstance.SetOther(pipeClient)
	pipeClient.SetOther(pipeInstance)
	tsInstance.RegisterInterface(pipeInstance)
	tsClient.RegisterInterface(pipeClient)
	t.Cleanup(func() {
		_ = pipeInstance.Detach()
		_ = pipeClient.Detach()
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

	return &sharedHubRig{
		t:                  t,
		hub:                hub,
		logs:               logs,
		blockClientInbound: blockClientInbound,
		clientTS:           tsClient,
		manager:            manager,
	}
}

// connect announces the hub, waits for the remote client to learn the path,
// then dials the hub with the production connect worker. Any rooms listed are
// stored first, exactly as a client that had joined them in an earlier
// session would have them stored.
func (r *sharedHubRig) connect(rooms ...string) *RRCHub {
	r.t.Helper()
	r.hub.AnnounceOnce()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !r.clientTS.HasPath(r.hub.DestinationHash()) {
		time.Sleep(10 * time.Millisecond)
	}
	if !r.clientTS.HasPath(r.hub.DestinationHash()) {
		r.t.Fatalf("client never learned the hub path; hub log:\n%v", r.logs.String())
	}

	client := r.manager.AddHub(r.hub.DestinationHash(), "rrc.hub", "Public Hub")
	for _, room := range rooms {
		client.AddRoom(room)
	}
	client.SetAutoReconnect(false, false)
	r.client = client
	r.t.Cleanup(client.Disconnect)
	client.ConnectAsync()

	if !r.waitWelcomed(10 * time.Second) {
		r.t.Fatalf("client never received WELCOME through the shared instance; hub log:\n%v", r.logs.String())
	}
	return client
}

// waitWelcomed blocks until the client reports the WELCOME.
func (r *sharedHubRig) waitWelcomed(timeout time.Duration) bool {
	r.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.client.lock.Lock()
		welcomed := r.client.Welcomed
		r.client.lock.Unlock()
		if welcomed {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// waitNotWelcomed blocks until the client has dropped its WELCOME state, which
// onClosedWithReason does as part of the link-close handling. Because that
// callback is dispatched on its own goroutine, a link that already reads
// LinkClosed can still report Welcomed — waiting on the flag alone would
// sample the dead session. Observing the clear also means the close handling
// ran, so the reconnect it schedules is armed.
func (r *sharedHubRig) waitNotWelcomed(timeout time.Duration) bool {
	r.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.client.lock.Lock()
		welcomed := r.client.Welcomed
		r.client.lock.Unlock()
		if !welcomed {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// waitWelcomeCount blocks until the hub has queued at least n WELCOMEs. The
// client's own Welcomed flag cannot be used to detect a *reconnect*: rns.Link
// dispatches its link-closed callback on a fresh goroutine after the status
// already reads LinkClosed, so for a moment after a teardown the flag still
// reports the dead session's WELCOME. The hub's log is the unambiguous signal
// — it queues a WELCOME only for a HELLO arriving on a new link.
func (r *sharedHubRig) waitWelcomeCount(n int, timeout time.Duration) bool {
	r.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Count(r.logs.String(), "Queued WELCOME") >= n {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestSharedInstanceHubWelcomesClientAndHoldsLink covers the three live
// symptoms in the fleet topology: the hub must deliver its WELCOME (and the
// greeting that follows it) through the shared instance, the client must
// HELLO and JOIN once per stored room, and the link must survive well past
// ping_interval_s + ping_timeout_s with the hub answering every PONG.
func TestSharedInstanceHubWelcomesClientAndHoldsLink(t *testing.T) {
	t.Parallel()
	const (
		pingInterval = 0.2
		pingTimeout  = 0.6
	)
	rig := newSharedHubRig(t, pingInterval, pingTimeout)
	client := rig.connect("general", "other")

	// Hold the link idle across several keepalive cycles.
	time.Sleep(2 * time.Second)

	logs := rig.logs.String()
	if got := strings.Count(logs, "HELLO peer="); got != 1 {
		t.Errorf("hub saw %v HELLOs, want exactly 1; hub log:\n%v", got, logs)
	}
	if got := strings.Count(logs, "Re-HELLO peer="); got != 0 {
		t.Errorf("hub saw %v re-HELLOs, want 0; hub log:\n%v", got, logs)
	}
	if got := strings.Count(logs, "Queued WELCOME"); got != 1 {
		t.Errorf("hub queued %v WELCOMEs, want 1; hub log:\n%v", got, logs)
	}
	if got := strings.Count(logs, "JOIN peer="); got != 2 {
		t.Errorf("hub saw %v JOINs for 2 stored rooms, want 2; hub log:\n%v", got, logs)
	}
	// The greeting rides the immediate NOTICE-chunk path the incident log
	// shows as "Falling back to chunking ... outgoing_is_none=true".
	if got := strings.Count(logs, "Falling back to chunking"); got != 1 {
		t.Errorf("hub chunked the greeting %v times, want 1; hub log:\n%v", got, logs)
	}
	if got := strings.Count(logs, " t=31 "); got < 3 {
		t.Errorf("hub received %v PONGs, want at least 3; hub log:\n%v", got, logs)
	}
	if got := strings.Count(logs, "Ping sent"); got < 3 {
		t.Errorf("hub sent %v PINGs, want at least 3; hub log:\n%v", got, logs)
	}
	if got := strings.Count(logs, "Ping timeout"); got != 0 {
		t.Errorf("hub logged %v ping timeouts on a healthy link; hub log:\n%v", got, logs)
	}
	if got := strings.Count(logs, "Link closed"); got != 0 {
		t.Errorf("hub logged %v link closes on a healthy link; hub log:\n%v", got, logs)
	}

	client.lock.Lock()
	status, welcomed, motd := client.Status, client.Welcomed, client.MOTD
	client.lock.Unlock()
	if !welcomed || status != StatusConnected {
		t.Errorf("client status=%v welcomed=%v, want StatusConnected; hub log:\n%v", status, welcomed, logs)
	}
	if !strings.Contains(motd, "Welcome to the RRC Public Hub") {
		t.Errorf("client MOTD=%q, want the hub greeting delivered through the shared instance", motd)
	}
	if got := client.JoinedRoomList(); len(got) != 2 {
		t.Errorf("client joined rooms=%v, want both stored rooms", got)
	}
	t.Logf("hub log:\n%v", logs)
}
