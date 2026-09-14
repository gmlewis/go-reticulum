// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"bytes"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// privateCommandClient is one local client of the shared instance: a manager
// with its own identity and nick, attached to the named hub.
type privateCommandClient struct {
	manager *RRCManager
	hub     *RRCHub
	hash    []byte
}

// startPrivateCommandClient attaches one more local client to the instance and
// connects it to the hub.
func startPrivateCommandClient(t *testing.T, root string, instancePort int, name, nick string, hubHash []byte) *privateCommandClient {
	t.Helper()
	dir := filepath.Join(root, name+"-rns")
	writeInstanceConfig(t, dir, instanceConfig("rrc-private-"+name, instancePort, freeLoopbackPort(t), true, false))
	ts := rns.NewTransportSystem(silentLogger())
	if _, err := rns.NewReticulum(ts, dir); err != nil {
		t.Fatalf("%v NewReticulum: %v", name, err)
	}
	manager := NewManager(filepath.Join(root, name+"-storage"), func() []byte { return ts.Identity().Hash })
	manager.SetIdentity(ts.Identity())
	manager.SetNickname(nick)
	manager.SetTransport(ts)

	client := manager.AddHub(hubHash, "rrc.hub", "Private Test Hub")
	client.AddRoom("general")
	client.SetAutoReconnect(false, false)
	t.Cleanup(client.Disconnect)
	client.ConnectAsync()
	return &privateCommandClient{manager: manager, hub: client, hash: ts.Identity().Hash}
}

// waitWelcomed blocks until the client has processed a WELCOME.
func (c *privateCommandClient) waitWelcomed(t *testing.T, logs *lockedBuffer) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		c.hub.lock.Lock()
		welcomed := c.hub.Welcomed
		c.hub.lock.Unlock()
		if welcomed {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("client never received WELCOME; hub log:\n%v", logs.String())
}

// TestPrivateCommandEndToEnd runs the whole feature over a real hub service and
// two real clients: the sender types one private command by NICK, the hub
// resolves it, the named participant alone receives the notice with the
// sender's identity and nick, the sender gets the confirmation, and no room —
// neither participant's — ever carries the private text.
func TestPrivateCommandEndToEnd(t *testing.T) {
	t.Parallel()
	root := tempDir(t)
	instancePort := freeLoopbackPort(t)

	// The shared instance owns every interface and relays.
	instanceDir := filepath.Join(root, "instance-rns")
	writeInstanceConfig(t, instanceDir,
		instanceConfig("rrc-private-instance", instancePort, freeLoopbackPort(t), true, true))
	tsInstance := rns.NewTransportSystem(silentLogger())
	if _, err := rns.NewReticulum(tsInstance, instanceDir); err != nil {
		t.Fatalf("shared instance NewReticulum: %v", err)
	}

	// The hub is a remote node reached over a pipe.
	hubDir := filepath.Join(root, "hub-rns")
	writeInstanceConfig(t, hubDir,
		instanceConfig("rrc-private-hub", freeLoopbackPort(t), freeLoopbackPort(t), false, false))
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
	cfg.PingIntervalS = 0.5
	cfg.PingTimeoutS = 1.5
	cfg.OverrideRawConfigValue("ping_interval_s", 0.5)
	cfg.OverrideRawConfigValue("ping_timeout_s", 1.5)
	cfg.IdentityPath = &identPath
	greeting := "RRC private-command test hub."
	cfg.Greeting = &greeting

	hub := NewHubService(cfg)
	hub.startReticulum = func(string) error {
		hub.ts = tsHub
		return nil
	}
	logs := &lockedBuffer{}
	hub.logSetup.writer = logs
	if err := hub.Start(); err != nil {
		t.Fatalf("hub.Start: %v", err)
	}
	t.Cleanup(hub.Stop)
	hub.AnnounceOnce()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !tsInstance.HasPath(hub.DestinationHash()) {
		time.Sleep(20 * time.Millisecond)
	}
	if !tsInstance.HasPath(hub.DestinationHash()) {
		t.Fatalf("the instance never learned the hub path; hub log:\n%v", logs.String())
	}

	alice := startPrivateCommandClient(t, root, instancePort, "alice", "alice", hub.DestinationHash())
	bob := startPrivateCommandClient(t, root, instancePort, "bob", "minipc", hub.DestinationHash())
	alice.waitWelcomed(t, logs)
	bob.waitWelcomed(t, logs)

	if !alice.hub.HasCapability(CapPrivateCommand) {
		t.Fatalf("the hub did not advertise CAPPrivateCommand in its WELCOME; hub log:\n%v", logs.String())
	}

	const body = "yo dude, whazzup?"
	if err := alice.hub.SendPrivateCommand("/dnotice minipc " + body); err != nil {
		t.Fatalf("SendPrivateCommand: %v", err)
	}

	// The named participant alone receives it, credited to the sender.
	var got *RRCMessage
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && got == nil {
		for _, n := range bob.hub.DirectNotices() {
			if strings.Contains(n.Text, body) {
				got = n
				break
			}
		}
		if got == nil {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if got == nil {
		t.Fatalf("the target never received the private notice; hub log:\n%v", logs.String())
	}
	if !bytes.Equal(got.Src, alice.hash) {
		t.Errorf("private notice src = %v, want the sender %v", hex.EncodeToString(got.Src), hex.EncodeToString(alice.hash))
	}
	if got.Nick != "alice" {
		t.Errorf("private notice nick = %q, want the sender's nick", got.Nick)
	}
	if !got.Direct {
		t.Error("the delivered notice is not marked direct")
	}

	// The sender is told exactly who received it, by full identity hash.
	wantConfirmation := "Direct NOTICE sent to " + hex.EncodeToString(bob.hash)
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		confirmed := false
		for _, text := range roomTexts(alice.hub, "general") {
			if strings.Contains(text, wantConfirmation) {
				confirmed = true
				break
			}
		}
		if confirmed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if texts := roomTexts(alice.hub, "general"); !containsSubstring(texts, wantConfirmation) {
		t.Errorf("sender room view = %v, want the confirmation naming %v; hub log:\n%v",
			texts, hex.EncodeToString(bob.hash), logs.String())
	}

	// No room ever carried the private text.
	for _, client := range []*privateCommandClient{alice, bob} {
		if containsSubstring(roomTexts(client.hub, "general"), body) {
			t.Errorf("the private message leaked into a room view: %v", roomTexts(client.hub, "general"))
		}
	}
}

// containsSubstring reports whether any entry contains sub.
func containsSubstring(items []string, sub string) bool {
	for _, item := range items {
		if strings.Contains(item, sub) {
			return true
		}
	}
	return false
}
