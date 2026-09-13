// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// hubFlapRig is a local two-node RRC rig: a real HubService (the rrcd hub
// role) and a real RRCHub client (the gonomadnet role) joined by an in-memory
// PipeInterface pair. It reproduces the live hub/client link without touching
// the network.
type hubFlapRig struct {
	t        *testing.T
	hub      *HubService
	hubDest  *rns.Destination
	logs     *lockedBuffer
	client   *RRCHub
	manager  *RRCManager
	clientTS *rns.TransportSystem
}

// lockedBuffer is a concurrency-safe log sink for the hub's log setup.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns everything written so far.
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// writeRRCTestRNSConfig writes a standalone RNS config (no shared instance)
// into the given directory.
func writeRRCTestRNSConfig(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%v): %v", dir, err)
	}
	content := `[reticulum]
share_instance = No

[logging]
loglevel = 4
`
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
}

// newStartedRRCTS brings up a standalone TransportSystem in its own config
// directory.
func newStartedRRCTS(t *testing.T, dir string) *rns.TransportSystem {
	t.Helper()
	writeRRCTestRNSConfig(t, dir)
	ts := rns.NewTransportSystem(silentLogger())
	if _, err := rns.NewReticulum(ts, dir); err != nil {
		t.Fatalf("NewReticulum(%v): %v", dir, err)
	}
	return ts
}

// newHubFlapRig wires a real hub and a real RRC client over a pipe pair, with
// a fast ping interval so the keepalive path is exercised in milliseconds.
func newHubFlapRig(t *testing.T, pingInterval, pingTimeout float64) *hubFlapRig {
	t.Helper()
	root := tempDir(t)

	tsHub := newStartedRRCTS(t, filepath.Join(root, "hub-rns"))
	tsClient := newStartedRRCTS(t, filepath.Join(root, "client-rns"))

	pipeHub := interfaces.NewPipeInterface("hub", func(data []byte, iface interfaces.Interface) {
		tsHub.Inbound(data, iface)
	})
	pipeClient := interfaces.NewPipeInterface("client", func(data []byte, iface interfaces.Interface) {
		tsClient.Inbound(data, iface)
	})
	pipeHub.SetOther(pipeClient)
	pipeClient.SetOther(pipeHub)
	tsHub.RegisterInterface(pipeHub)
	tsClient.RegisterInterface(pipeClient)
	t.Cleanup(func() {
		_ = pipeHub.Detach()
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
	cfg.AnnounceOnStart = false
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

	return &hubFlapRig{
		t:        t,
		hub:      hub,
		hubDest:  hub.destination,
		logs:     logs,
		manager:  manager,
		clientTS: tsClient,
	}
}

// connect starts the client's link to the hub destination.
func (r *hubFlapRig) connect() *RRCHub {
	r.t.Helper()
	client := r.manager.AddHub(r.hub.DestinationHash(), "rrc.hub", "Public Hub")
	client.SetAutoReconnect(false, false)
	if err := client.Connect(r.clientTS, r.hubDest); err != nil {
		r.t.Fatalf("client Connect: %v", err)
	}
	r.client = client
	r.t.Cleanup(client.Disconnect)
	return client
}

// waitWelcomed blocks until the client's hub reports the WELCOME, or fails.
func (r *hubFlapRig) waitWelcomed(timeout time.Duration) bool {
	r.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.client.lock.Lock()
		welcomed := r.client.Welcomed
		r.client.lock.Unlock()
		if welcomed {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestHubLinkWelcomeDeliveredAndSurvivesPingLoop reproduces the live RRC hub
// symptoms: the hub queues a WELCOME the client never receives (so the client
// re-HELLOs and re-joins), and the hub's ping loop then tears the healthy link
// down on its ping timeout.
func TestHubLinkWelcomeDeliveredAndSurvivesPingLoop(t *testing.T) {
	t.Parallel()
	const (
		pingInterval = 0.15
		pingTimeout  = 0.45
	)
	rig := newHubFlapRig(t, pingInterval, pingTimeout)
	client := rig.connect()

	if !rig.waitWelcomed(3 * time.Second) {
		t.Fatalf("client never received WELCOME; hub log:\n%v", rig.logs.String())
	}

	if got := strings.Count(rig.logs.String(), "HELLO peer="); got != 1 {
		t.Errorf("hub saw %v HELLOs, want exactly 1; hub log:\n%v", got, rig.logs.String())
	}

	// Hold the link idle past the hub's ping timeout window: the hub must see
	// its PONGs and leave the healthy link alone.
	time.Sleep(time.Duration(4*(pingInterval+pingTimeout)*float64(time.Second)) + 500*time.Millisecond)

	logs := rig.logs.String()
	if got := strings.Count(logs, "Ping timeout"); got != 0 {
		t.Errorf("hub logged %v ping timeouts on a healthy link; hub log:\n%v", got, logs)
	}
	if got := strings.Count(logs, "Link closed"); got != 0 {
		t.Errorf("hub logged %v link closes on a healthy link; hub log:\n%v", got, logs)
	}
	client.lock.Lock()
	status := client.Status
	welcomed := client.Welcomed
	client.lock.Unlock()
	if !welcomed || status != StatusConnected {
		t.Errorf("client status=%v welcomed=%v, want StatusConnected; hub log:\n%v",
			status, welcomed, logs)
	}
	t.Logf("hub log:\n%v", logs)
}
