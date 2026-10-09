// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// The shared-instance RPC is how a client asks the transport that owns the network what is
// actually going on: which interfaces are up, what the path table holds, how many links there
// are. Its key is derived from a transport identity, and *which* identity is the whole of the
// question here.
//
// Python derives it from `Transport.internal_identity()` (Reticulum.py:355-356), the identity
// saved in the machine's storage, and not from the operative identity — which a process that
// does not own the transport replaces with an ephemeral one of its own (Transport.py:234-237).
// On a desktop both processes share one storage directory, so both derive the same key and the
// RPC works. A port that keyed off the operative identity would have the client present a key
// the instance never published, and every call would come back "unauthorized" — an appliance
// that is connected, reported as though it were not.

// rpcFreePort returns a port nothing is listening on, for a test's control listener.
func rpcFreePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

// rpcConfig writes an RNS configuration that shares an instance on the given ports, with the
// given extra lines under [reticulum] and interfaces.
func rpcConfig(t *testing.T, dir string, instancePort, controlPort int, transport bool, extra string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%v): %v", dir, err)
	}
	transportValue := "No"
	if transport {
		transportValue = "Yes"
	}
	config := fmt.Sprintf(`[reticulum]
instance_name = rpc-key-test
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v
enable_transport = %v
%v
[logging]
loglevel = 4

[interfaces]
`, instancePort, controlPort, transportValue, extra)
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
}

// rpcTestInterface is an interface the transport owns, so that the stats a client asks for
// have something in them: an empty answer and a refused one would otherwise look the same
// from the client's side.
func rpcTestInterface(ts *TransportSystem) {
	ts.RegisterInterface(&dummyInterface{name: "Home Hub"})
}

// TestAttachedClientSharesTheInstanceRPCKey covers the desktop shape: one configuration
// directory, used by the instance and by everything that attaches to it. The client's
// operative identity is ephemeral and the instance's is not, so a key taken from the operative
// identity would never match — and no RPC from any client would ever be answered.
func TestAttachedClientSharesTheInstanceRPCKey(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(tempDir(t), "reticulum")
	instancePort := rpcFreePort(t)
	controlPort := rpcFreePort(t)

	// The instance, which owns the transport and the network.
	instanceTS := NewTransportSystem(NewLogger())
	rpcConfig(t, dir, instancePort, controlPort, true, "")
	instance, err := NewReticulum(instanceTS, dir)
	if err != nil {
		t.Fatalf("the instance: %v", err)
	}
	if !instance.IsSharedInstance() {
		t.Fatal("the first process to start should own the shared instance")
	}
	rpcTestInterface(instanceTS)
	t.Cleanup(func() { _ = instance.Close() })

	// The client, with the same configuration: it attaches rather than owns.
	clientTS := NewTransportSystem(NewLogger())
	client, err := NewReticulum(clientTS, dir)
	if err != nil {
		t.Fatalf("the client: %v", err)
	}
	if !client.IsConnectedToSharedInstance() {
		t.Fatalf("the client did not attach (shared=%v standalone=%v)",
			client.IsSharedInstance(), client.IsStandaloneInstance())
	}
	t.Cleanup(func() { _ = client.Close() })

	snapshot, err := client.InterfaceStats()
	if err != nil {
		t.Fatalf("the client's interface stats were refused: %v", err)
	}
	// The instance's own interfaces, which is what the client cannot know by itself: it owns
	// none, so an empty answer here is the appliance showing an Interfaces page that lists a
	// network it is not actually on.
	if !rpcHasInterface(snapshot, "Home Hub") {
		t.Errorf("the client does not see the interface the instance owns: %v", rpcInterfaceNames(snapshot))
	}
}

// TestSeparateConfigDirectoriesNeedAStatedRPCKey covers the shape an appliance has: the client
// must have a configuration of its own, or it races the transport for ownership of the shared
// instance. Two directories mean two transport identities and therefore two derived keys, so
// an appliance that wants its client's RPC answered states the key on both sides — the
// supported way to run them apart (`[reticulum] rpc_key`, Reticulum.py:494-500).
func TestSeparateConfigDirectoriesNeedAStatedRPCKey(t *testing.T) {
	t.Parallel()

	root := tempDir(t)
	instancePort := rpcFreePort(t)
	controlPort := rpcFreePort(t)
	key := "3f1c0d5a9b2e48766123456789abcdef0123456789abcdef0123456789abcdef"

	instanceTS := NewTransportSystem(NewLogger())
	rpcConfig(t, filepath.Join(root, "instance"), instancePort, controlPort, true, "rpc_key = "+key)
	instance, err := NewReticulum(instanceTS, filepath.Join(root, "instance"))
	if err != nil {
		t.Fatalf("the instance: %v", err)
	}
	rpcTestInterface(instanceTS)
	t.Cleanup(func() { _ = instance.Close() })

	clientTS := NewTransportSystem(NewLogger())
	rpcConfig(t, filepath.Join(root, "client"), instancePort, controlPort, false,
		"require_shared_instance = Yes\nrpc_key = "+key)
	client, err := NewReticulum(clientTS, filepath.Join(root, "client"))
	if err != nil {
		t.Fatalf("the client: %v", err)
	}
	if !client.IsConnectedToSharedInstance() {
		t.Fatalf("the client did not attach (shared=%v standalone=%v)",
			client.IsSharedInstance(), client.IsStandaloneInstance())
	}
	t.Cleanup(func() { _ = client.Close() })

	snapshot, err := client.InterfaceStats()
	if err != nil {
		t.Fatalf("the client's interface stats were refused: %v", err)
	}
	if !rpcHasInterface(snapshot, "Home Hub") {
		t.Errorf("the client does not see the interface the instance owns: %v", rpcInterfaceNames(snapshot))
	}
}

// rpcHasInterface reports whether a stats snapshot names an interface.
func rpcHasInterface(snapshot *InterfaceStatsSnapshot, name string) bool {
	for _, iface := range snapshot.Interfaces {
		if iface.Name == name {
			return true
		}
	}
	return false
}

// rpcInterfaceNames lists what a snapshot holds, for a failure that says what it saw.
func rpcInterfaceNames(snapshot *InterfaceStatsSnapshot) []string {
	names := make([]string, 0, len(snapshot.Interfaces))
	for _, iface := range snapshot.Interfaces {
		names = append(names, iface.Name)
	}
	return names
}

// TestAClientWithNoStatedKeyIsRefused covers the failure this all exists to prevent, from the
// other side: two directories and no stated key is two different keys, and the client is told
// so rather than being handed an empty interface list that looks like a transport with nothing
// configured.
func TestAClientWithNoStatedKeyIsRefused(t *testing.T) {
	t.Parallel()

	root := tempDir(t)
	instancePort := rpcFreePort(t)
	controlPort := rpcFreePort(t)

	instanceTS := NewTransportSystem(NewLogger())
	rpcConfig(t, filepath.Join(root, "instance"), instancePort, controlPort, true, "")
	instance, err := NewReticulum(instanceTS, filepath.Join(root, "instance"))
	if err != nil {
		t.Fatalf("the instance: %v", err)
	}
	t.Cleanup(func() { _ = instance.Close() })

	clientTS := NewTransportSystem(NewLogger())
	rpcConfig(t, filepath.Join(root, "client"), instancePort, controlPort, false, "require_shared_instance = Yes")
	client, err := NewReticulum(clientTS, filepath.Join(root, "client"))
	if err != nil {
		t.Fatalf("the client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if _, err := client.InterfaceStats(); err == nil {
		t.Fatal("a client with no key of its own was answered, which means the two sides agree by accident")
	}
}
