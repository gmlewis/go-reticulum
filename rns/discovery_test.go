// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	_ "embed"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

//go:embed testdata/discovery_test.data
var discoveryTestData []byte

func reserveUDPPort(t *testing.T) int {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ResolveUDPAddr() error = %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("ListenUDP() error = %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

func startSocatLinkedPTYPair(t *testing.T) (string, string) {
	t.Helper()

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("serial bootstrap test not supported on %v", runtime.GOOS)
	}
	if _, err := exec.LookPath("socat"); err != nil {
		t.Skip("socat not installed")
	}

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-pty-")
	t.Cleanup(cleanup)

	left := filepath.Join(tmpDir, "pty0")
	right := filepath.Join(tmpDir, "pty1")
	cmd := exec.Command("socat", "-d", "-d",
		"PTY,link="+left+",raw,echo=0",
		"PTY,link="+right+",raw,echo=0")
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start socat: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(left); err == nil {
			if _, err := os.Stat(right); err == nil {
				return left, right
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for socat PTY links: %s", stderr.String())
	return "", ""
}

type targetHostTestInterface struct {
	*interfaces.BaseInterface
	ifaceType   string
	targetHost  string
	targetPort  int
	detachError error
}

func newTargetHostTestInterface(name, ifaceType, targetHost string, targetPort int) *targetHostTestInterface {
	return &targetHostTestInterface{
		BaseInterface: interfaces.NewBaseInterface(name, interfaces.ModeFull, 0),
		ifaceType:     ifaceType,
		targetHost:    targetHost,
		targetPort:    targetPort,
	}
}

func (i *targetHostTestInterface) Type() string      { return i.ifaceType }
func (i *targetHostTestInterface) Status() bool      { return true }
func (i *targetHostTestInterface) IsOut() bool       { return true }
func (i *targetHostTestInterface) Send([]byte) error { return nil }
func (i *targetHostTestInterface) Detach() error {
	i.SetDetached(true)
	return i.detachError
}
func (i *targetHostTestInterface) TargetHost() string { return i.targetHost }
func (i *targetHostTestInterface) TargetPort() int    { return i.targetPort }

type b32TestInterface struct {
	*interfaces.BaseInterface
	ifaceType string
	b32       string
}

func newB32TestInterface(name, ifaceType, b32 string) *b32TestInterface {
	return &b32TestInterface{
		BaseInterface: interfaces.NewBaseInterface(name, interfaces.ModeFull, 0),
		ifaceType:     ifaceType,
		b32:           b32,
	}
}

func (i *b32TestInterface) Type() string      { return i.ifaceType }
func (i *b32TestInterface) Status() bool      { return true }
func (i *b32TestInterface) IsOut() bool       { return true }
func (i *b32TestInterface) Send([]byte) error { return nil }
func (i *b32TestInterface) Detach() error {
	i.SetDetached(true)
	return nil
}
func (i *b32TestInterface) B32() string { return i.b32 }

type panicTargetHostTestInterface struct {
	*interfaces.BaseInterface
}

func newPanicTargetHostTestInterface(name string) *panicTargetHostTestInterface {
	return &panicTargetHostTestInterface{
		BaseInterface: interfaces.NewBaseInterface(name, interfaces.ModeFull, 0),
	}
}

func (i *panicTargetHostTestInterface) Type() string      { return "panic-target-host" }
func (i *panicTargetHostTestInterface) Status() bool      { return true }
func (i *panicTargetHostTestInterface) IsOut() bool       { return true }
func (i *panicTargetHostTestInterface) Send([]byte) error { return nil }
func (i *panicTargetHostTestInterface) Detach() error {
	i.SetDetached(true)
	return nil
}
func (i *panicTargetHostTestInterface) TargetHost() string { panic("boom") }
func (i *panicTargetHostTestInterface) TargetPort() int    { return 0 }

func TestListDiscoveredInterfaces(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	fixture := normalizeDiscoveryFixtureTimes(t, discoveryTestData)

	// Write data generated by Python to the temporary storage path.
	if err := os.WriteFile(filepath.Join(storagePath, "test_interface.data"), fixture, 0o644); err != nil {
		t.Fatalf("failed to write test data: %v", err)
	}

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}

	if len(discovered) != 1 {
		t.Fatalf("expected 1 discovered interface, got %v", len(discovered))
	}

	di := discovered[0]
	if di.Name != "Test Interface" {
		t.Errorf("expected name %q, got %q", "Test Interface", di.Name)
	}
	if di.Type != "UDPInterface" {
		t.Errorf("expected type %q, got %q", "UDPInterface", di.Type)
	}
	if di.Hops != 1 {
		t.Errorf("expected 1 hop, got %v", di.Hops)
	}
	if !di.Transport {
		t.Errorf("expected transport enabled")
	}
	if di.Latitude == nil || *di.Latitude != 34.0522 {
		t.Errorf("expected latitude 34.0522, got %v", di.Latitude)
	}
	if di.Longitude == nil || *di.Longitude != -118.2437 {
		t.Errorf("expected longitude -118.2437, got %v", di.Longitude)
	}
	if di.Height == nil || *di.Height != 100 {
		t.Errorf("expected height 100, got %v", di.Height)
	}
	if di.Value != 1000 {
		t.Errorf("expected value 1000, got %v", di.Value)
	}
	// Note that `Status` depends on the current time.
}

func normalizeDiscoveryFixtureTimes(t *testing.T, data []byte) []byte {
	t.Helper()

	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("failed to unpack discovery fixture: %v", err)
	}
	m := asAnyMap(unpacked)
	if m == nil {
		t.Fatalf("unexpected discovery fixture type %T", unpacked)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	m["discovered"] = now - 7200
	m["last_heard"] = now - 3600
	return mustMsgpackPack(m)
}

func TestListDiscoveredInterfaces_StatusThresholds(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-status-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9

	// Available: heard 1 hour ago
	availableData := map[string]any{
		"name":       "Available",
		"last_heard": now - 3600,
	}
	// Unknown: heard 2 days ago
	unknownData := map[string]any{
		"name":       "Unknown",
		"last_heard": now - (ThresholdUnknown + 3600),
	}
	// Stale: heard 4 days ago
	staleData := map[string]any{
		"name":       "Stale",
		"last_heard": now - (ThresholdStale + 3600),
	}
	// Expired: heard 8 days ago (should be removed)
	expiredData := map[string]any{
		"name":       "Expired",
		"last_heard": now - (ThresholdRemove + 3600),
	}

	writeData := func(name string, m map[string]any) {
		path := filepath.Join(storagePath, name+".data")
		data := mustMsgpackPack(m)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("failed to write %v data: %v", name, err)
		}
	}

	writeData("available", availableData)
	writeData("unknown", unknownData)
	writeData("stale", staleData)
	writeData("expired", expiredData)

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}

	if len(discovered) != 3 {
		t.Fatalf("expected 3 discovered interfaces (1 removed), got %v", len(discovered))
	}

	statusMap := make(map[string]string)
	for _, di := range discovered {
		statusMap[di.Name] = di.Status
	}

	if statusMap["Available"] != "available" {
		t.Errorf("Available: expected status available, got %q", statusMap["Available"])
	}
	if statusMap["Unknown"] != "unknown" {
		t.Errorf("Unknown: expected status unknown, got %q", statusMap["Unknown"])
	}
	if statusMap["Stale"] != "stale" {
		t.Errorf("Stale: expected status stale, got %q", statusMap["Stale"])
	}

	// Verify expired file was removed
	if _, err := os.Stat(filepath.Join(storagePath, "expired.data")); !os.IsNotExist(err) {
		t.Errorf("expected expired file to be removed")
	}
}

func TestListDiscoveredInterfaces_SourceAndReachableFiltering(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-filter-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	sourceHex := "0102030405060708090a0b0c0d0e0f10"
	sourceBytes, err := hex.DecodeString(sourceHex)
	if err != nil {
		t.Fatalf("DecodeString(sourceHex) error = %v", err)
	}

	writeData := func(name string, m map[string]any) {
		path := filepath.Join(storagePath, name+".data")
		if err := os.WriteFile(path, mustMsgpackPack(m), 0o644); err != nil {
			t.Fatalf("failed to write %v data: %v", name, err)
		}
	}

	writeData("matching", map[string]any{
		"name":         "Matching",
		"last_heard":   now - 3600,
		"network_id":   sourceHex,
		"reachable_on": "discovery.example.net",
	})
	writeData("missing-network-id", map[string]any{
		"name":       "MissingNetworkID",
		"last_heard": now - 3600,
	})
	writeData("wrong-network-id", map[string]any{
		"name":       "WrongNetworkID",
		"last_heard": now - 3600,
		"network_id": "ffeeddccbbaa00998877665544332211",
	})
	writeData("invalid-reachable", map[string]any{
		"name":         "InvalidReachable",
		"last_heard":   now - 3600,
		"network_id":   sourceHex,
		"reachable_on": "not a hostname!",
	})

	r := &Reticulum{
		configDir:        tmpDir,
		interfaceSources: [][]byte{sourceBytes},
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 discovered interface after filtering, got %v", len(discovered))
	}
	if discovered[0].Name != "Matching" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}

	for _, name := range []string{"missing-network-id.data", "wrong-network-id.data", "invalid-reachable.data"} {
		if _, err := os.Stat(filepath.Join(storagePath, name)); !os.IsNotExist(err) {
			t.Fatalf("expected filtered discovery file %q to be removed", name)
		}
	}
}

func TestListDiscoveredInterfaces_SortsLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-sort-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	writeData := func(name string, m map[string]any) {
		path := filepath.Join(storagePath, name+".data")
		if err := os.WriteFile(path, mustMsgpackPack(m), 0o644); err != nil {
			t.Fatalf("failed to write %v data: %v", name, err)
		}
	}

	writeData("stale-high-value", map[string]any{
		"name":       "StaleHighValue",
		"last_heard": now - (ThresholdStale + 3600),
		"value":      5000,
	})
	writeData("available-lower-value", map[string]any{
		"name":       "AvailableLowerValue",
		"last_heard": now - 3600,
		"value":      1000,
	})
	writeData("available-higher-value", map[string]any{
		"name":       "AvailableHigherValue",
		"last_heard": now - 1800,
		"value":      2000,
	})
	writeData("available-same-value-older", map[string]any{
		"name":       "AvailableSameValueOlder",
		"last_heard": now - 2400,
		"value":      2000,
	})
	writeData("unknown-high-value", map[string]any{
		"name":       "UnknownHighValue",
		"last_heard": now - (ThresholdUnknown + 3600),
		"value":      8000,
	})

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}

	if got, want := len(discovered), 5; got != want {
		t.Fatalf("expected %v discovered interfaces, got %v", want, got)
	}

	gotNames := []string{
		discovered[0].Name,
		discovered[1].Name,
		discovered[2].Name,
		discovered[3].Name,
		discovered[4].Name,
	}
	wantNames := []string{
		"AvailableHigherValue",
		"AvailableSameValueOlder",
		"AvailableLowerValue",
		"UnknownHighValue",
		"StaleHighValue",
	}
	for i, want := range wantNames {
		if gotNames[i] != want {
			t.Fatalf("discovered[%v] name = %q, want %q (full order %v)", i, gotNames[i], want, gotNames)
		}
	}
}

func TestPersistDiscoveredInterface_NewEntry(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-new-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	receivedAt := float64(time.Now().UnixNano()) / 1e9
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       receivedAt,
		"discovery_hash": []byte{0xaa, 0xbb, 0xcc},
		"value":          1234,
	}

	if err := discovery.persistDiscoveredInterface(info); err != nil {
		t.Fatalf("persistDiscoveredInterface failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", "aabbcc.data"))
	if err != nil {
		t.Fatalf("failed to read persisted discovery file: %v", err)
	}
	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("failed to unpack persisted discovery file: %v", err)
	}
	m := asAnyMap(unpacked)
	if m == nil {
		t.Fatalf("unexpected persisted discovery type %T", unpacked)
	}

	if got := asFloat64(lookupAnyValue(m, "discovered")); got != receivedAt {
		t.Fatalf("discovered = %v, want %v", got, receivedAt)
	}
	if got := asFloat64(lookupAnyValue(m, "last_heard")); got != receivedAt {
		t.Fatalf("last_heard = %v, want %v", got, receivedAt)
	}
	if got := asInt(lookupAnyValue(m, "heard_count")); got != 0 {
		t.Fatalf("heard_count = %v, want 0", got)
	}
}

func TestPersistDiscoveredInterface_ExistingEntryPreservesDiscoveryTime(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-existing-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	initialReceived := float64(time.Now().UnixNano()) / 1e9
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       initialReceived,
		"discovery_hash": "aabbcc",
		"value":          1234,
	}

	if err := discovery.persistDiscoveredInterface(info); err != nil {
		t.Fatalf("first persistDiscoveredInterface failed: %v", err)
	}

	secondReceived := initialReceived + 60
	if err := discovery.persistDiscoveredInterface(map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       secondReceived,
		"discovery_hash": "aabbcc",
		"value":          5678,
	}); err != nil {
		t.Fatalf("second persistDiscoveredInterface failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", "aabbcc.data"))
	if err != nil {
		t.Fatalf("failed to read persisted discovery file: %v", err)
	}
	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("failed to unpack persisted discovery file: %v", err)
	}
	m := asAnyMap(unpacked)
	if m == nil {
		t.Fatalf("unexpected persisted discovery type %T", unpacked)
	}

	if got := asFloat64(lookupAnyValue(m, "discovered")); got != initialReceived {
		t.Fatalf("discovered = %v, want %v", got, initialReceived)
	}
	if got := asFloat64(lookupAnyValue(m, "last_heard")); got != secondReceived {
		t.Fatalf("last_heard = %v, want %v", got, secondReceived)
	}
	if got := asInt(lookupAnyValue(m, "heard_count")); got != 1 {
		t.Fatalf("heard_count = %v, want 1", got)
	}
	if got := asInt(lookupAnyValue(m, "value")); got != 5678 {
		t.Fatalf("value = %v, want 5678", got)
	}
}

func TestInterfaceDiscoveryReceiveAndPersist(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-")
	defer cleanup()

	ts := NewTransportSystem(nil)
	destinationHash := []byte("discovery-destination")
	ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 2, Expires: time.Now().Add(time.Hour)}

	r := &Reticulum{
		configDir: tmpDir,
		transport: ts,
		logger:    NewLogger(),
	}
	discovery := NewInterfaceDiscovery(r)

	sourceIdentity := mustTestNewIdentity(t, true)
	transportID := []byte{0xde, 0xad, 0xbe, 0xef}
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   transportID,
		discoveryFieldName:          "Discovered TCP",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
		discoveryFieldIFACNetname:   "mesh",
		discoveryFieldIFACNetkey:    "secret",
	}, 2)

	var callbackErr error
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackErr = discovery.persistDiscoveredInterface(info)
	})

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)
	if callbackErr != nil {
		t.Fatalf("persist callback failed: %v", callbackErr)
	}

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if got, want := len(discovered), 1; got != want {
		t.Fatalf("expected %v discovered interface, got %v", want, got)
	}

	got := discovered[0]
	if got.Name != "Discovered TCP" {
		t.Fatalf("Name = %q, want %q", got.Name, "Discovered TCP")
	}
	if got.Type != "TCPServerInterface" {
		t.Fatalf("Type = %q, want %q", got.Type, "TCPServerInterface")
	}
	if got.Hops != 2 {
		t.Fatalf("Hops = %v, want 2", got.Hops)
	}
	if got.TransportID != "deadbeef" {
		t.Fatalf("TransportID = %q, want %q", got.TransportID, "deadbeef")
	}
	if got.NetworkID != sourceIdentity.HexHash {
		t.Fatalf("NetworkID = %q, want %q", got.NetworkID, sourceIdentity.HexHash)
	}
	if got.ReachableOn != "discovery.example.net" {
		t.Fatalf("ReachableOn = %q, want %q", got.ReachableOn, "discovery.example.net")
	}
	if got.Port == nil || *got.Port != 4242 {
		t.Fatalf("Port = %v, want 4242", got.Port)
	}
	if got.Value < 2 {
		t.Fatalf("Value = %v, want >= 2", got.Value)
	}

	connectionType := "BackboneInterface"
	remoteKey := "remote"
	if runtime.GOOS == "windows" {
		connectionType = "TCPClientInterface"
		remoteKey = "target_host"
	}
	wantConfigEntry := "[[Discovered TCP]]\n  type = " + connectionType +
		"\n  enabled = yes\n  " + remoteKey + " = discovery.example.net\n  target_port = 4242" +
		"\n  transport_identity = deadbeef\n  network_name = mesh\n  passphrase = secret"
	if got.ConfigEntry != wantConfigEntry {
		t.Fatalf("ConfigEntry = %q, want %q", got.ConfigEntry, wantConfigEntry)
	}
}

func TestInterfaceDiscoveryReceiveAndPersistRejectsInsufficientStampValue(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-invalid-")
	defer cleanup()

	ts := NewTransportSystem(nil)
	r := &Reticulum{
		configDir: tmpDir,
		transport: ts,
		logger:    NewLogger(),
	}
	discovery := NewInterfaceDiscovery(r)

	callbackCalled := false
	handler := NewInterfaceAnnounceHandler(r, 20, func(info map[string]any) {
		callbackCalled = true
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Broken",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
	}, 2)

	handler.receivedAnnounce([]byte("discovery-destination"), sourceIdentity, appData)
	if callbackCalled {
		t.Fatal("expected insufficient-value stamp announce to be ignored")
	}

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("expected no persisted discovered interfaces, got %v", len(discovered))
	}
}

func TestInterfaceDiscoveryReceiveAndPersistEncryptedWithTransportNetworkIdentity(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-encrypted-")
	defer cleanup()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	destinationHash := []byte("discovery-destination")
	ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 2, Expires: time.Now().Add(time.Hour)}

	networkIdentity := mustTestNewIdentity(t, true)
	ts.SetNetworkIdentity(networkIdentity)

	r := &Reticulum{
		configDir: tmpDir,
		transport: ts,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	sourceIdentity := mustTestNewIdentity(t, true)
	plain := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Encrypted TCP",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
	}, 2)

	encryptedPayload, err := networkIdentity.Encrypt(plain[1:], nil)
	if err != nil {
		t.Fatalf("networkIdentity.Encrypt() error = %v", err)
	}
	appData := append([]byte{discoveryFlagEncrypted}, encryptedPayload...)

	var callbackErr error
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackErr = discovery.persistDiscoveredInterface(info)
	})

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)
	if callbackErr != nil {
		t.Fatalf("persist callback failed: %v", callbackErr)
	}

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if got, want := len(discovered), 1; got != want {
		t.Fatalf("expected %v discovered interface, got %v", want, got)
	}
	if got := discovered[0].Name; got != "Encrypted TCP" {
		t.Fatalf("Name = %q, want %q", got, "Encrypted TCP")
	}
}

func TestInterfaceAnnounceHandlerRecoversCallbackPanic(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-handler-panic-")
	defer cleanup()

	ts := NewTransportSystem(NewLogger())
	destinationHash := []byte("discovery-destination")
	ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 2, Expires: time.Now().Add(time.Hour)}

	r := &Reticulum{
		configDir: tmpDir,
		transport: ts,
		logger:    NewLogger(),
	}

	handler := NewInterfaceAnnounceHandler(r, 2, func(map[string]any) {
		panic("boom")
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Callback Boom",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
	}, 2)

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("receivedAnnounce() propagated callback panic: %v", recovered)
			}
		}()
		handler.receivedAnnounce(destinationHash, sourceIdentity, appData)
	}()
}

func TestInterfaceDiscoveryReceiveAndPersistRejectsMissingRequiredFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload map[any]any
	}{
		{
			name: "missing-transport",
			payload: map[any]any{
				discoveryFieldInterfaceType: "TCPServerInterface",
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:          "Broken TCP",
				discoveryFieldReachableOn:   "discovery.example.net",
				discoveryFieldPort:          4242,
			},
		},
		{
			name: "missing-name",
			payload: map[any]any{
				discoveryFieldInterfaceType: "TCPServerInterface",
				discoveryFieldTransport:     true,
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldReachableOn:   "discovery.example.net",
				discoveryFieldPort:          4242,
			},
		},
		{
			name: "tcp-missing-port",
			payload: map[any]any{
				discoveryFieldInterfaceType: "TCPServerInterface",
				discoveryFieldTransport:     true,
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:          "Broken TCP",
				discoveryFieldReachableOn:   "discovery.example.net",
			},
		},
		{
			name: "tcp-missing-reachable-on",
			payload: map[any]any{
				discoveryFieldInterfaceType: "TCPServerInterface",
				discoveryFieldTransport:     true,
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:          "Broken TCP",
				discoveryFieldPort:          4242,
			},
		},
		{
			name: "i2p-missing-reachable-on",
			payload: map[any]any{
				discoveryFieldInterfaceType: "I2PInterface",
				discoveryFieldTransport:     true,
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:          "Broken I2P",
			},
		},
		{
			name: "rnode-missing-frequency",
			payload: map[any]any{
				discoveryFieldInterfaceType:   "RNodeInterface",
				discoveryFieldTransport:       true,
				discoveryFieldTransportID:     []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:            "Broken RNode",
				discoveryFieldReachableOn:     "rnode.example.net",
				discoveryFieldBandwidth:       125000,
				discoveryFieldSpreadingFactor: 7,
				discoveryFieldCodingRate:      5,
			},
		},
		{
			name: "weave-missing-channel",
			payload: map[any]any{
				discoveryFieldInterfaceType: "WeaveInterface",
				discoveryFieldTransport:     true,
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:          "Broken Weave",
				discoveryFieldReachableOn:   "weave.example.net",
				discoveryFieldFrequency:     2450000000,
				discoveryFieldBandwidth:     2000000,
				discoveryFieldModulation:    "gmsk",
			},
		},
		{
			name: "kiss-missing-modulation",
			payload: map[any]any{
				discoveryFieldInterfaceType: "KISSInterface",
				discoveryFieldTransport:     true,
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:          "Broken KISS",
				discoveryFieldReachableOn:   "kiss.example.net",
				discoveryFieldFrequency:     433920000,
				discoveryFieldBandwidth:     12500,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-missing-")
			defer cleanup()

			ts := NewTransportSystem(nil)
			destinationHash := []byte("discovery-destination")
			ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 2, Expires: time.Now().Add(time.Hour)}

			r := &Reticulum{
				configDir: tmpDir,
				transport: ts,
				logger:    NewLogger(),
			}
			discovery := NewInterfaceDiscovery(r)

			callbackCalled := false
			handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
				callbackCalled = true
			})

			sourceIdentity := mustTestNewIdentity(t, true)
			appData := mustDiscoveryAnnounceAppData(t, tt.payload, 2)

			handler.receivedAnnounce(destinationHash, sourceIdentity, appData)
			if callbackCalled {
				t.Fatal("expected malformed discovery announce to be ignored")
			}

			discovered, err := discovery.ListDiscoveredInterfaces(false, false)
			if err != nil {
				t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
			}
			if len(discovered) != 0 {
				t.Fatalf("expected no persisted discovered interfaces, got %v", len(discovered))
			}
		})
	}
}

func TestInterfaceDiscoveryReceiveAndPersistAdditionalTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		payload         map[any]any
		wantType        string
		wantReachableOn string
		wantFrequency   int
		wantBandwidth   int
		wantSF          int
		wantCR          int
		wantModulation  string
		wantConfigEntry string
	}{
		{
			name: "i2p",
			payload: map[any]any{
				discoveryFieldInterfaceType: "I2PInterface",
				discoveryFieldTransport:     true,
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:          "Discovered I2P",
				discoveryFieldReachableOn:   "exampleabcdefghijklmnopqrstuvwxyz.b32.i2p",
			},
			wantType:        "I2PInterface",
			wantReachableOn: "exampleabcdefghijklmnopqrstuvwxyz.b32.i2p",
			wantConfigEntry: "[[Discovered I2P]]\n  type = I2PInterface\n  enabled = yes\n  peers = exampleabcdefghijklmnopqrstuvwxyz.b32.i2p\n  transport_identity = deadbeef",
		},
		{
			name: "rnode",
			payload: map[any]any{
				discoveryFieldInterfaceType:   "RNodeInterface",
				discoveryFieldTransport:       true,
				discoveryFieldTransportID:     []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:            "Discovered RNode",
				discoveryFieldReachableOn:     "rnode.example.net",
				discoveryFieldFrequency:       915000000,
				discoveryFieldBandwidth:       125000,
				discoveryFieldSpreadingFactor: 7,
				discoveryFieldCodingRate:      5,
			},
			wantType:        "RNodeInterface",
			wantReachableOn: "rnode.example.net",
			wantFrequency:   915000000,
			wantBandwidth:   125000,
			wantSF:          7,
			wantCR:          5,
			wantConfigEntry: "[[Discovered RNode]]\n  type = RNodeInterface\n  enabled = yes\n  port = \n  frequency = 915000000\n  bandwidth = 125000\n  spreadingfactor = 7\n  codingrate = 5\n  txpower = ",
		},
		{
			name: "weave",
			payload: map[any]any{
				discoveryFieldInterfaceType: "WeaveInterface",
				discoveryFieldTransport:     true,
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:          "Discovered Weave",
				discoveryFieldReachableOn:   "weave.example.net",
				discoveryFieldFrequency:     2450000000,
				discoveryFieldBandwidth:     2000000,
				discoveryFieldChannel:       11,
				discoveryFieldModulation:    "gmsk",
			},
			wantType:        "WeaveInterface",
			wantReachableOn: "weave.example.net",
			wantFrequency:   2450000000,
			wantBandwidth:   2000000,
			wantModulation:  "gmsk",
			wantConfigEntry: "[[Discovered Weave]]\n  type = WeaveInterface\n  enabled = yes\n  port = ",
		},
		{
			name: "kiss",
			payload: map[any]any{
				discoveryFieldInterfaceType: "KISSInterface",
				discoveryFieldTransport:     true,
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:          "Discovered KISS",
				discoveryFieldReachableOn:   "kiss.example.net",
				discoveryFieldFrequency:     433920000,
				discoveryFieldBandwidth:     12500,
				discoveryFieldModulation:    "afsk",
			},
			wantType:        "KISSInterface",
			wantReachableOn: "kiss.example.net",
			wantFrequency:   433920000,
			wantBandwidth:   12500,
			wantModulation:  "afsk",
			wantConfigEntry: "[[Discovered KISS]]\n  type = KISSInterface\n  enabled = yes\n  port = \n  # Frequency: 433920000\n  # Bandwidth: 12500\n  # Modulation: afsk\n  transport_identity = deadbeef",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-extra-")
			defer cleanup()

			ts := NewTransportSystem(nil)
			destinationHash := []byte("discovery-destination")
			ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 2, Expires: time.Now().Add(time.Hour)}

			r := &Reticulum{
				configDir: tmpDir,
				transport: ts,
				logger:    NewLogger(),
			}
			discovery := NewInterfaceDiscovery(r)

			sourceIdentity := mustTestNewIdentity(t, true)
			appData := mustDiscoveryAnnounceAppData(t, tt.payload, 2)

			var callbackErr error
			handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
				callbackErr = discovery.persistDiscoveredInterface(info)
			})

			handler.receivedAnnounce(destinationHash, sourceIdentity, appData)
			if callbackErr != nil {
				t.Fatalf("persist callback failed: %v", callbackErr)
			}

			discovered, err := discovery.ListDiscoveredInterfaces(false, false)
			if err != nil {
				t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
			}
			if got, want := len(discovered), 1; got != want {
				t.Fatalf("expected %v discovered interface, got %v", want, got)
			}

			got := discovered[0]
			if got.Type != tt.wantType {
				t.Fatalf("Type = %q, want %q", got.Type, tt.wantType)
			}
			if got.ReachableOn != tt.wantReachableOn {
				t.Fatalf("ReachableOn = %q, want %q", got.ReachableOn, tt.wantReachableOn)
			}
			if tt.wantFrequency != 0 && (got.Frequency == nil || *got.Frequency != tt.wantFrequency) {
				t.Fatalf("Frequency = %v, want %v", got.Frequency, tt.wantFrequency)
			}
			if tt.wantBandwidth != 0 && (got.Bandwidth == nil || *got.Bandwidth != tt.wantBandwidth) {
				t.Fatalf("Bandwidth = %v, want %v", got.Bandwidth, tt.wantBandwidth)
			}
			if tt.wantSF != 0 && (got.SF == nil || *got.SF != tt.wantSF) {
				t.Fatalf("SF = %v, want %v", got.SF, tt.wantSF)
			}
			if tt.wantCR != 0 && (got.CR == nil || *got.CR != tt.wantCR) {
				t.Fatalf("CR = %v, want %v", got.CR, tt.wantCR)
			}
			if tt.wantModulation != "" && got.Modulation != tt.wantModulation {
				t.Fatalf("Modulation = %q, want %q", got.Modulation, tt.wantModulation)
			}
			if got.ConfigEntry != tt.wantConfigEntry {
				t.Fatalf("ConfigEntry = %q, want %q", got.ConfigEntry, tt.wantConfigEntry)
			}
		})
	}
}

func mustDiscoveryAnnounceAppData(t *testing.T, payload map[any]any, targetCost int) []byte {
	t.Helper()

	packed, err := msgpack.Pack(payload)
	if err != nil {
		t.Fatalf("msgpack.Pack(payload) error = %v", err)
	}
	stamp, _, err := generateDiscoveryStamp(FullHash(packed), targetCost)
	if err != nil {
		t.Fatalf("generateDiscoveryStamp error = %v", err)
	}

	appData := make([]byte, 1, 1+len(packed)+len(stamp))
	appData[0] = 0
	appData = append(appData, packed...)
	appData = append(appData, stamp...)
	return appData
}

func TestInterfaceDiscoveryStartReconnectsCachedBackbone(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-reconnect-")
	defer cleanup()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen error = %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("listener.Close() error = %v", err)
		}
	})

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "cached-backbone.data"), mustMsgpackPack(map[string]any{
		"name":         "Cached Backbone",
		"type":         "BackboneInterface",
		"transport":    true,
		"last_heard":   now - 60,
		"discovered":   now - 120,
		"reachable_on": "127.0.0.1",
		"port":         port,
		"network_id":   "01020304",
		"ifac_netname": "mesh",
		"ifac_netkey":  "secret",
	}), 0o644); err != nil {
		t.Fatalf("failed to write cached discovery file: %v", err)
	}

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)

	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	ts.SetDiscoverInterfacesHook(discovery.connectDiscovered)
	ts.DiscoverInterfaces()

	var acceptedConn net.Conn
	select {
	case acceptedConn = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for auto-connected backbone client")
	}
	t.Cleanup(func() {
		if acceptedConn != nil {
			if err := acceptedConn.Close(); err != nil {
				t.Errorf("acceptedConn.Close() error = %v", err)
			}
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for len(ts.GetInterfaces()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", got)
	}
	if got := len(ts.AnnounceHandlers()); got != 1 {
		t.Fatalf("expected 1 announce handler, got %v", got)
	}

	iface := ts.GetInterfaces()[0]
	if iface.Type() != "BackboneClientInterface" {
		t.Fatalf("Type() = %q, want %q", iface.Type(), "BackboneClientInterface")
	}
	meta, ok := iface.(interface {
		AutoconnectHash() []byte
		AutoconnectSource() string
		IFACConfig() interfaces.IFACConfig
		TargetHost() string
		TargetPort() int
	})
	if !ok {
		t.Fatalf("auto-connected interface does not expose expected metadata")
	}
	if len(meta.AutoconnectHash()) == 0 {
		t.Fatalf("expected autoconnect hash to be set")
	}
	if got := meta.AutoconnectSource(); got != "01020304" {
		t.Fatalf("AutoconnectSource() = %q, want %q", got, "01020304")
	}
	if got := meta.TargetHost(); got != "127.0.0.1" {
		t.Fatalf("TargetHost() = %q, want %q", got, "127.0.0.1")
	}
	if got := meta.TargetPort(); got != port {
		t.Fatalf("TargetPort() = %v, want %v", got, port)
	}
	if got := meta.IFACConfig(); !got.Enabled || got.NetName != "mesh" || got.NetKey != "secret" || got.Size != 16 {
		t.Fatalf("IFACConfig() = %+v, want enabled mesh/secret size 16", got)
	}
	if got := iface.Bitrate(); got != 5_000_000 {
		t.Fatalf("Bitrate() = %v, want %v", got, 5_000_000)
	}

	discovery.connectDiscovered()
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected cached autoconnect dedupe to keep 1 interface, got %v", got)
	}

	_ = iface.Detach()
}

func TestInterfaceDiscoveryConnectDiscoveredSkipsWhenAutoconnectDisabled(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-disabled-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "cached-backbone.data"), mustMsgpackPack(map[string]any{
		"name":         "Cached Backbone",
		"type":         "BackboneInterface",
		"transport":    true,
		"last_heard":   now - 60,
		"discovered":   now - 120,
		"reachable_on": "127.0.0.1",
		"port":         4242,
		"network_id":   "01020304",
	}), 0o644); err != nil {
		t.Fatalf("failed to write cached discovery file: %v", err)
	}

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 0,
	}
	discovery := NewInterfaceDiscovery(r)

	discovery.connectDiscovered()

	if discovery.initialAutoconnectRan {
		t.Fatal("expected initialAutoconnectRan to remain false when autoconnect is disabled")
	}
	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected no auto-connected interfaces when autoconnect is disabled, got %v", got)
	}
}

func TestInterfaceDiscoveryAutoconnectCountIncludesEmptyAutoconnectHash(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(NewLogger())
	ts.RegisterInterface(&autoconnectCountTestInterface{
		BaseInterface: interfaces.NewBaseInterface("autoconnect-capable", interfaces.ModeFull, 1000),
	})

	discovery := NewInterfaceDiscovery(&Reticulum{
		transport: ts,
		logger:    NewLogger(),
	})

	if got := discovery.autoconnectCount(); got != 1 {
		t.Fatalf("autoconnectCount() = %v, want 1", got)
	}
}

func TestInterfaceDiscoveryAutoconnectRecoversInterfaceExistsPanic(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	ts.RegisterInterface(newPanicTargetHostTestInterface("panic-host"))

	discovery := NewInterfaceDiscovery(&Reticulum{
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 2,
	})

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("autoconnect() propagated panic: %v", recovered)
		}
	}()

	if err := discovery.autoconnect(DiscoveredInterface{
		Name:        "Candidate",
		Type:        "BackboneInterface",
		ReachableOn: "127.0.0.1",
		Port:        intPtr(4242),
	}); err != nil {
		t.Fatalf("autoconnect() error = %v, want nil after panic recovery", err)
	}
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected autoconnect panic recovery to leave transport unchanged, got %v interfaces", got)
	}
}

func TestInterfaceDiscoveryInterfaceExistsMatchesHostWithoutPort(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	existing := newTargetHostTestInterface(
		"Existing I2P",
		"I2PInterfacePeer",
		"exampleabcdefghijklmnopqrstuvwxyz.b32.i2p",
		1234,
	)
	ts.RegisterInterface(existing)

	if !discovery.interfaceExists(DiscoveredInterface{
		Type:        "I2PInterface",
		ReachableOn: "exampleabcdefghijklmnopqrstuvwxyz.b32.i2p",
	}) {
		t.Fatal("expected host-only discovered interface to match existing target host")
	}
}

func TestInterfaceDiscoveryInterfaceExistsMatchesI2PB32(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	existing := newB32TestInterface(
		"Existing I2P B32",
		"I2PInterfacePeer",
		"exampleabcdefghijklmnopqrstuvwxyz.b32.i2p",
	)
	ts.RegisterInterface(existing)

	if !discovery.interfaceExists(DiscoveredInterface{
		Type:        "I2PInterface",
		ReachableOn: "exampleabcdefghijklmnopqrstuvwxyz.b32.i2p",
	}) {
		t.Fatal("expected discovered I2P interface to match existing b32 address")
	}
}

func TestInterfaceDiscoveryStartAutoconnectsReceivedBackboneAnnounce(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-live-autoconnect-")
	defer cleanup()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen error = %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("listener.Close() error = %v", err)
		}
	})

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	destinationHash := []byte("autoconnect-destination")
	ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 3, Expires: time.Now().Add(time.Hour)}

	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	sourceIdentity := mustTestNewIdentity(t, true)
	port := listener.Addr().(*net.TCPAddr).Port
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "BackboneInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Live Backbone",
		discoveryFieldReachableOn:   "127.0.0.1",
		discoveryFieldPort:          port,
		discoveryFieldIFACNetname:   "mesh",
		discoveryFieldIFACNetkey:    "secret",
	}, 2)

	discovery.handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	var acceptedConn net.Conn
	select {
	case acceptedConn = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for auto-connected announce listener")
	}
	t.Cleanup(func() {
		if acceptedConn != nil {
			if err := acceptedConn.Close(); err != nil {
				t.Errorf("acceptedConn.Close() error = %v", err)
			}
		}
	})

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 auto-connected interface from announce, got %v", got)
	}
	iface := ts.GetInterfaces()[0]
	if iface.Type() != "BackboneClientInterface" {
		t.Fatalf("Type() = %q, want %q", iface.Type(), "BackboneClientInterface")
	}

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if got := len(discovered); got != 1 {
		t.Fatalf("expected 1 persisted discovered interface, got %v", got)
	}
	if discovered[0].Name != "Live Backbone" {
		t.Fatalf("discovered[0].Name = %q, want %q", discovered[0].Name, "Live Backbone")
	}

	_ = iface.Detach()
}

func TestInterfaceDiscoveryStartDoesNotAutoconnectReceivedTCPServerAnnounce(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-live-no-autoconnect-tcp-")
	defer cleanup()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen error = %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("listener.Close() error = %v", err)
		}
	})

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	destinationHash := []byte("no-autoconnect-tcp-destination")
	ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 3, Expires: time.Now().Add(time.Hour)}

	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	sourceIdentity := mustTestNewIdentity(t, true)
	port := listener.Addr().(*net.TCPAddr).Port
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Live TCP Server",
		discoveryFieldReachableOn:   "127.0.0.1",
		discoveryFieldPort:          port,
		discoveryFieldIFACNetname:   "mesh",
		discoveryFieldIFACNetkey:    "secret",
	}, 2)

	discovery.handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	select {
	case conn := <-accepted:
		_ = conn.Close()
		t.Fatal("unexpected auto-connect for discovered TCPServerInterface")
	case <-time.After(300 * time.Millisecond):
	}

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected no auto-connected interfaces from tcp-server announce, got %v", got)
	}

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if got := len(discovered); got != 1 {
		t.Fatalf("expected 1 persisted discovered interface, got %v", got)
	}
	if discovered[0].Type != "TCPServerInterface" {
		t.Fatalf("discovered[0].Type = %q, want %q", discovered[0].Type, "TCPServerInterface")
	}
}

func TestInterfaceDiscoveryStartInvokesDiscoveryCallbackAfterAutoconnect(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-live-callback-")
	defer cleanup()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen error = %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("listener.Close() error = %v", err)
		}
	})

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	destinationHash := []byte("callback-destination")
	ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 3, Expires: time.Now().Add(time.Hour)}

	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)

	type callbackResult struct {
		name            string
		interfacesCount int
		discoveredCount int
		err             error
	}
	callbackCh := make(chan callbackResult, 1)
	discovery.SetDiscoveryCallback(func(info map[string]any) {
		discovered, err := discovery.ListDiscoveredInterfaces(false, false)
		callbackCh <- callbackResult{
			name:            asString(info["name"]),
			interfacesCount: len(ts.GetInterfaces()),
			discoveredCount: len(discovered),
			err:             err,
		}
	})
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	sourceIdentity := mustTestNewIdentity(t, true)
	port := listener.Addr().(*net.TCPAddr).Port
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "BackboneInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Callback Backbone",
		discoveryFieldReachableOn:   "127.0.0.1",
		discoveryFieldPort:          port,
	}, 2)

	discovery.handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	var result callbackResult
	select {
	case result = <-callbackCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for discovery callback")
	}
	if result.err != nil {
		t.Fatalf("callback ListDiscoveredInterfaces() error = %v", result.err)
	}
	if result.name != "Callback Backbone" {
		t.Fatalf("callback name = %q, want %q", result.name, "Callback Backbone")
	}
	if result.interfacesCount != 1 {
		t.Fatalf("callback interfaces count = %v, want 1", result.interfacesCount)
	}
	if result.discoveredCount != 1 {
		t.Fatalf("callback discovered count = %v, want 1", result.discoveredCount)
	}

	select {
	case conn := <-accepted:
		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Errorf("accepted conn.Close() error = %v", err)
			}
		})
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback auto-connected listener")
	}

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", got)
	}
}

func TestInterfaceDiscoveryStartRecoversDiscoveryCallbackPanic(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-live-callback-panic-")
	defer cleanup()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen error = %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("listener.Close() error = %v", err)
		}
	})

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	destinationHash := []byte("callback-panic-destination")
	ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 3, Expires: time.Now().Add(time.Hour)}

	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)
	discovery.SetDiscoveryCallback(func(map[string]any) {
		panic("boom")
	})
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	sourceIdentity := mustTestNewIdentity(t, true)
	port := listener.Addr().(*net.TCPAddr).Port
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "BackboneInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Callback Panic Backbone",
		discoveryFieldReachableOn:   "127.0.0.1",
		discoveryFieldPort:          port,
	}, 2)

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("receivedAnnounce() propagated callback panic: %v", recovered)
			}
		}()
		discovery.handler.receivedAnnounce(destinationHash, sourceIdentity, appData)
	}()

	select {
	case conn := <-accepted:
		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Errorf("accepted conn.Close() error = %v", err)
			}
		})
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for auto-connect after callback panic")
	}

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if got := len(discovered); got != 1 {
		t.Fatalf("expected 1 persisted discovered interface, got %v", got)
	}
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", got)
	}
}

func TestInterfaceDiscoveryStartSkipsAutoconnectWhenPersistFails(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-live-autoconnect-fail-")
	defer cleanup()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen error = %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("listener.Close() error = %v", err)
		}
	})

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	destinationHash := []byte("autoconnect-destination")
	ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 3, Expires: time.Now().Add(time.Hour)}

	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.RemoveAll(storagePath); err != nil {
		t.Fatalf("RemoveAll(storagePath) error = %v", err)
	}
	if err := os.WriteFile(storagePath, []byte("not-a-directory"), 0o644); err != nil {
		t.Fatalf("WriteFile(storagePath) error = %v", err)
	}

	sourceIdentity := mustTestNewIdentity(t, true)
	port := listener.Addr().(*net.TCPAddr).Port
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "BackboneInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Broken Backbone",
		discoveryFieldReachableOn:   "127.0.0.1",
		discoveryFieldPort:          port,
	}, 2)

	discovery.handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	select {
	case conn := <-accepted:
		_ = conn.Close()
		t.Fatal("unexpected auto-connect when discovered interface persistence failed")
	case <-time.After(300 * time.Millisecond):
	}

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected no auto-connected interfaces when persistence fails, got %v", got)
	}
}

type monitorTestInterface struct {
	*interfaces.BaseInterface
	online        bool
	detached      bool
	bootstrapOnly bool
	panicStatus   bool
	panicDetach   bool
}

func (m *monitorTestInterface) Type() string { return "monitor-test" }
func (m *monitorTestInterface) Status() bool {
	if m.panicStatus {
		panic("boom")
	}
	return m.online
}
func (m *monitorTestInterface) IsOut() bool       { return true }
func (m *monitorTestInterface) Send([]byte) error { return nil }
func (m *monitorTestInterface) Detach() error {
	if m.panicDetach {
		panic("boom")
	}
	m.detached = true
	m.SetDetached(true)
	return nil
}
func (m *monitorTestInterface) BootstrapOnly() bool {
	return m.bootstrapOnly
}

type autoconnectCountTestInterface struct {
	*interfaces.BaseInterface
	autoconnectHash []byte
}

func (a *autoconnectCountTestInterface) Type() string            { return "autoconnect-count-test" }
func (a *autoconnectCountTestInterface) Status() bool            { return true }
func (a *autoconnectCountTestInterface) IsOut() bool             { return true }
func (a *autoconnectCountTestInterface) Send([]byte) error       { return nil }
func (a *autoconnectCountTestInterface) Detach() error           { return nil }
func (a *autoconnectCountTestInterface) AutoconnectHash() []byte { return a.autoconnectHash }

func TestInterfaceDiscoveryMonitorDetachesOfflineAutoconnect(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(NewLogger())
	iface := &monitorTestInterface{
		BaseInterface: interfaces.NewBaseInterface("monitored", interfaces.ModeFull, 1000),
		online:        true,
	}
	ts.RegisterInterface(iface)

	discovery := NewInterfaceDiscovery(&Reticulum{
		transport: ts,
		logger:    NewLogger(),
	})
	discovery.monitorInterval = 0
	discovery.detachThreshold = 12 * time.Second
	discovery.monitorInterface(iface)

	start := time.Unix(100, 0)
	iface.online = false
	discovery.monitorAutoconnectsOnce(start)
	if iface.detached {
		t.Fatal("expected interface to remain attached on first offline observation")
	}
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected interface to remain registered, got %v interfaces", got)
	}

	discovery.monitorAutoconnectsOnce(start.Add(13 * time.Second))
	if !iface.detached {
		t.Fatal("expected interface to be detached after staying offline past threshold")
	}
	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected interface to be removed from transport, got %v interfaces", got)
	}
}

func TestInterfaceDiscoveryMonitorReconnectResetsDownTimer(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(NewLogger())
	iface := &monitorTestInterface{
		BaseInterface: interfaces.NewBaseInterface("monitored-reset", interfaces.ModeFull, 1000),
		online:        true,
	}
	ts.RegisterInterface(iface)

	discovery := NewInterfaceDiscovery(&Reticulum{
		transport: ts,
		logger:    NewLogger(),
	})
	discovery.monitorInterval = 0
	discovery.detachThreshold = 12 * time.Second
	discovery.monitorInterface(iface)

	start := time.Unix(200, 0)
	iface.online = false
	discovery.monitorAutoconnectsOnce(start)

	iface.online = true
	discovery.monitorAutoconnectsOnce(start.Add(5 * time.Second))

	iface.online = false
	discovery.monitorAutoconnectsOnce(start.Add(10 * time.Second))
	discovery.monitorAutoconnectsOnce(start.Add(21 * time.Second))
	if iface.detached {
		t.Fatal("expected reconnect to reset down timer before threshold elapses again")
	}
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected interface to remain registered, got %v interfaces", got)
	}

	discovery.monitorAutoconnectsOnce(start.Add(23 * time.Second))
	if !iface.detached {
		t.Fatal("expected interface to detach after second offline period crosses threshold")
	}
}

func TestInterfaceDiscoveryMonitorRecoversStatusPanic(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	panicking := &monitorTestInterface{
		BaseInterface: interfaces.NewBaseInterface("panicking", interfaces.ModeFull, 1000),
		panicStatus:   true,
	}
	offline := &monitorTestInterface{
		BaseInterface: interfaces.NewBaseInterface("offline", interfaces.ModeFull, 1000),
		online:        false,
	}
	ts.RegisterInterface(panicking)
	ts.RegisterInterface(offline)

	discovery := NewInterfaceDiscovery(&Reticulum{
		transport: ts,
		logger:    logger,
	})
	discovery.monitorInterval = 0
	discovery.detachThreshold = 10 * time.Second
	discovery.monitorInterface(panicking)
	discovery.monitorInterface(offline)
	discovery.monitorMu.Lock()
	discovery.autoconnectDownSince[offline] = time.Unix(100, 0)
	discovery.monitorMu.Unlock()

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("monitorAutoconnectsOnce() propagated status panic: %v", recovered)
			}
		}()
		discovery.monitorAutoconnectsOnce(time.Unix(111, 0))
	}()

	if !offline.detached {
		t.Fatal("expected non-panicking offline interface to still detach")
	}
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected panicking interface to remain registered, got %v interfaces", got)
	}
}

func TestInterfaceDiscoveryMonitorRecoversTeardownPanic(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	panicking := &monitorTestInterface{
		BaseInterface: interfaces.NewBaseInterface("panic-detach", interfaces.ModeFull, 1000),
		online:        false,
		panicDetach:   true,
	}
	offline := &monitorTestInterface{
		BaseInterface: interfaces.NewBaseInterface("detach-ok", interfaces.ModeFull, 1000),
		online:        false,
	}
	ts.RegisterInterface(panicking)
	ts.RegisterInterface(offline)

	discovery := NewInterfaceDiscovery(&Reticulum{
		transport: ts,
		logger:    logger,
	})
	discovery.monitorInterval = 0
	discovery.detachThreshold = 10 * time.Second
	discovery.monitorInterface(panicking)
	discovery.monitorInterface(offline)
	discovery.monitorMu.Lock()
	discovery.autoconnectDownSince[panicking] = time.Unix(100, 0)
	discovery.autoconnectDownSince[offline] = time.Unix(100, 0)
	discovery.monitorMu.Unlock()

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("monitorAutoconnectsOnce() propagated teardown panic: %v", recovered)
			}
		}()
		discovery.monitorAutoconnectsOnce(time.Unix(111, 0))
	}()

	if !offline.detached {
		t.Fatal("expected non-panicking interface to still detach")
	}
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected only panicking interface to remain registered, got %v interfaces", got)
	}
	if ts.GetInterfaces()[0] != panicking {
		t.Fatal("expected panicking interface to remain registered after failed detach")
	}
}

func TestInterfaceDiscoveryMonitorAutoconnectsAvailableCandidate(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-monitor-autoconnect-")
	defer cleanup()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen error = %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("listener.Close() error = %v", err)
		}
	})

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
	}()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "monitor-candidate.data"), mustMsgpackPack(map[string]any{
		"name":         "Monitor Candidate",
		"type":         "BackboneInterface",
		"transport":    true,
		"last_heard":   now - 30,
		"discovered":   now - 60,
		"reachable_on": "127.0.0.1",
		"port":         port,
		"network_id":   "0a0b0c0d",
	}), 0o644); err != nil {
		t.Fatalf("failed to write cached discovery file: %v", err)
	}

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	discovery := NewInterfaceDiscovery(&Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 4,
	})
	discovery.monitorInterval = 0
	discovery.initialAutoconnectRan = true

	discovery.monitorAutoconnectsOnce(time.Unix(300, 0))

	var acceptedConn net.Conn
	select {
	case acceptedConn = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for monitor-triggered auto-connect")
	}
	t.Cleanup(func() {
		if acceptedConn != nil {
			if err := acceptedConn.Close(); err != nil {
				t.Errorf("acceptedConn.Close() error = %v", err)
			}
		}
	})

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", got)
	}
	iface := ts.GetInterfaces()[0]
	t.Cleanup(func() {
		if err := iface.Detach(); err != nil {
			t.Errorf("iface.Detach() error = %v", err)
		}
	})
	if got := iface.Type(); got != "BackboneClientInterface" {
		t.Fatalf("Type() = %q, want %q", got, "BackboneClientInterface")
	}
}

func TestInterfaceDiscoveryMonitorAutoconnectUsesShuffledCandidateOrder(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-monitor-shuffle-")
	defer cleanup()

	listenerA, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen(listenerA) error = %v", err)
	}
	t.Cleanup(func() {
		if err := listenerA.Close(); err != nil {
			t.Errorf("listenerA.Close() error = %v", err)
		}
	})
	listenerB, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen(listenerB) error = %v", err)
	}
	t.Cleanup(func() {
		if err := listenerB.Close(); err != nil {
			t.Errorf("listenerB.Close() error = %v", err)
		}
	})

	acceptA := make(chan net.Conn, 1)
	go func() {
		conn, err := listenerA.Accept()
		if err == nil {
			acceptA <- conn
		}
	}()
	acceptB := make(chan net.Conn, 1)
	go func() {
		conn, err := listenerB.Accept()
		if err == nil {
			acceptB <- conn
		}
	}()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	now := float64(time.Now().UnixNano()) / 1e9
	portA := listenerA.Addr().(*net.TCPAddr).Port
	portB := listenerB.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(filepath.Join(storagePath, "candidate-a.data"), mustMsgpackPack(map[string]any{
		"name":         "Candidate A",
		"type":         "BackboneInterface",
		"transport":    true,
		"last_heard":   now - 10,
		"discovered":   now - 60,
		"reachable_on": "127.0.0.1",
		"port":         portA,
		"network_id":   "aaaaaaaa",
	}), 0o644); err != nil {
		t.Fatalf("failed to write candidate A: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storagePath, "candidate-b.data"), mustMsgpackPack(map[string]any{
		"name":         "Candidate B",
		"type":         "BackboneInterface",
		"transport":    true,
		"last_heard":   now - 20,
		"discovered":   now - 60,
		"reachable_on": "127.0.0.1",
		"port":         portB,
		"network_id":   "bbbbbbbb",
	}), 0o644); err != nil {
		t.Fatalf("failed to write candidate B: %v", err)
	}

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	discovery := NewInterfaceDiscovery(&Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 4,
	})
	discovery.monitorInterval = 0
	discovery.initialAutoconnectRan = true
	discovery.shuffleCandidates = func(candidates []DiscoveredInterface) {
		if len(candidates) >= 2 {
			candidates[0], candidates[1] = candidates[1], candidates[0]
		}
	}

	discovery.monitorAutoconnectsOnce(time.Unix(301, 0))

	select {
	case conn := <-acceptB:
		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Errorf("acceptB conn.Close() error = %v", err)
			}
		})
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for shuffled candidate to be auto-connected")
	}

	select {
	case conn := <-acceptA:
		_ = conn.Close()
		t.Fatal("unexpected connection to unshuffled first candidate")
	case <-time.After(200 * time.Millisecond):
	}

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", got)
	}
	meta, ok := ts.GetInterfaces()[0].(interface{ TargetPort() int })
	if !ok {
		t.Fatalf("auto-connected interface %T does not expose TargetPort()", ts.GetInterfaces()[0])
	}
	if got := meta.TargetPort(); got != portB {
		t.Fatalf("TargetPort() = %v, want %v", got, portB)
	}
}

func TestInterfaceDiscoveryMonitorDoesNotFallbackPastSelectedExistingCandidate(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-monitor-selected-")
	defer cleanup()

	existingPort := reserveTCPPort(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("listener.Close() error = %v", err)
		}
	})

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "selected-existing.data"), mustMsgpackPack(map[string]any{
		"name":         "Selected Existing",
		"type":         "BackboneInterface",
		"transport":    true,
		"last_heard":   now - 10,
		"discovered":   now - 60,
		"reachable_on": "127.0.0.1",
		"port":         existingPort,
		"network_id":   "01010101",
	}), 0o644); err != nil {
		t.Fatalf("failed to write existing candidate: %v", err)
	}
	otherPort := listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(filepath.Join(storagePath, "other-candidate.data"), mustMsgpackPack(map[string]any{
		"name":         "Other Candidate",
		"type":         "BackboneInterface",
		"transport":    true,
		"last_heard":   now - 20,
		"discovered":   now - 60,
		"reachable_on": "127.0.0.1",
		"port":         otherPort,
		"network_id":   "02020202",
	}), 0o644); err != nil {
		t.Fatalf("failed to write other candidate: %v", err)
	}

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	existing, err := interfaces.NewBackboneClientInterface("Existing", "127.0.0.1", existingPort, func(data []byte, iface interfaces.Interface) {
		ts.Inbound(data, iface)
	})
	if err != nil {
		t.Fatalf("NewBackboneClientInterface(existing) error = %v", err)
	}
	ts.RegisterInterface(existing)
	t.Cleanup(func() {
		if err := existing.Detach(); err != nil {
			t.Errorf("existing.Detach() error = %v", err)
		}
	})

	discovery := NewInterfaceDiscovery(&Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 4,
	})
	discovery.monitorInterval = 0
	discovery.initialAutoconnectRan = true
	discovery.shuffleCandidates = func([]DiscoveredInterface) {}

	discovery.monitorAutoconnectsOnce(time.Unix(302, 0))

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected monitor to stop after selected existing candidate, got %v interfaces", got)
	}

	select {
	case conn := <-accepted:
		_ = conn.Close()
		t.Fatal("unexpected fallback autoconnect to second candidate")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestInterfaceDiscoveryMonitorDetachesBootstrapOnlyWhenTargetReached(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(NewLogger())
	autoA := &monitorTestInterface{
		BaseInterface: interfaces.NewBaseInterface("auto-a", interfaces.ModeFull, 1000),
		online:        true,
	}
	autoB := &monitorTestInterface{
		BaseInterface: interfaces.NewBaseInterface("auto-b", interfaces.ModeFull, 1000),
		online:        true,
	}
	bootstrap := &monitorTestInterface{
		BaseInterface: interfaces.NewBaseInterface("bootstrap", interfaces.ModeFull, 1000),
		online:        true,
		bootstrapOnly: true,
	}
	ts.RegisterInterface(autoA)
	ts.RegisterInterface(autoB)
	ts.RegisterInterface(bootstrap)

	discovery := NewInterfaceDiscovery(&Reticulum{
		transport:           ts,
		logger:              NewLogger(),
		autoconnectDiscover: 2,
	})
	discovery.monitorInterval = 0
	discovery.monitorInterface(autoA)
	discovery.monitorInterface(autoB)

	discovery.monitorAutoconnectsOnce(time.Unix(400, 0))

	if !bootstrap.detached {
		t.Fatal("expected bootstrap-only interface to detach once online autoconnect target is reached")
	}
	if got := len(ts.GetInterfaces()); got != 2 {
		t.Fatalf("expected bootstrap-only interface to be removed from transport, got %v interfaces", got)
	}
}

func TestInterfaceDiscoveryMonitorReenablesBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(NewLogger())
	owner := &Reticulum{
		transport:           ts,
		logger:              NewLogger(),
		autoconnectDiscover: 2,
		bootstrapRestarters: []func() error{
			func() error {
				iface := &monitorTestInterface{
					BaseInterface: interfaces.NewBaseInterface("bootstrap-restored", interfaces.ModeFull, 1000),
					online:        true,
					bootstrapOnly: true,
				}
				ts.RegisterInterface(iface)
				return nil
			},
		},
	}
	discovery := NewInterfaceDiscovery(owner)
	discovery.monitorInterval = 0

	discovery.monitorAutoconnectsOnce(time.Unix(500, 0))

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected bootstrap interface to be re-enabled, got %v interfaces", got)
	}
	getter, ok := ts.GetInterfaces()[0].(interface{ BootstrapOnly() bool })
	if !ok {
		t.Fatalf("re-enabled interface %T does not expose BootstrapOnly()", ts.GetInterfaces()[0])
	}
	if !getter.BootstrapOnly() {
		t.Fatal("expected re-enabled bootstrap interface to preserve bootstrap-only metadata")
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredTCPBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap TCP]]
type = TCPServerInterface
listen_ip = 127.0.0.1
listen_port = ` + strconv.Itoa(port) + `
bootstrap_only = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 configured interface, got %v", got)
	}

	discovery := NewInterfaceDiscovery(r)
	discovery.monitorInterval = 0

	iface := ts.GetInterfaces()[0]
	discovery.teardownInterface(iface)

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected bootstrap interface teardown to remove interface, got %v interfaces", got)
	}

	discovery.monitorAutoconnectsOnce(time.Unix(600, 0))

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected configured TCP bootstrap interface to be re-enabled, got %v interfaces", got)
	}

	getter, ok := ts.GetInterfaces()[0].(interface{ BootstrapOnly() bool })
	if !ok {
		t.Fatalf("re-enabled interface %T does not expose BootstrapOnly()", ts.GetInterfaces()[0])
	}
	if !getter.BootstrapOnly() {
		t.Fatal("expected re-enabled TCP bootstrap interface to preserve bootstrap-only metadata")
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredTCPClientBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap TCP Client]]
type = TCPClientInterface
target_host = 127.0.0.1
target_port = ` + strconv.Itoa(port) + `
bootstrap_only = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 configured interface, got %v", got)
	}

	discovery := NewInterfaceDiscovery(r)
	discovery.monitorInterval = 0

	iface := ts.GetInterfaces()[0]
	discovery.teardownInterface(iface)

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected bootstrap interface teardown to remove interface, got %v interfaces", got)
	}

	discovery.monitorAutoconnectsOnce(time.Unix(650, 0))

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected configured TCP client bootstrap interface to be re-enabled, got %v interfaces", got)
	}

	getter, ok := ts.GetInterfaces()[0].(interface{ BootstrapOnly() bool })
	if !ok {
		t.Fatalf("re-enabled interface %T does not expose BootstrapOnly()", ts.GetInterfaces()[0])
	}
	if !getter.BootstrapOnly() {
		t.Fatal("expected re-enabled TCP client bootstrap interface to preserve bootstrap-only metadata")
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredUDPBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	listenPort := reserveUDPPort(t)
	forwardPort := reserveUDPPort(t)
	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap UDP]]
type = UDPInterface
listen_ip = 127.0.0.1
listen_port = ` + strconv.Itoa(listenPort) + `
forward_ip = 127.0.0.1
forward_port = ` + strconv.Itoa(forwardPort) + `
bootstrap_only = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 configured interface, got %v", got)
	}

	discovery := NewInterfaceDiscovery(r)
	discovery.monitorInterval = 0

	iface := ts.GetInterfaces()[0]
	discovery.teardownInterface(iface)

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected bootstrap interface teardown to remove interface, got %v interfaces", got)
	}

	discovery.monitorAutoconnectsOnce(time.Unix(675, 0))

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected configured UDP bootstrap interface to be re-enabled, got %v interfaces", got)
	}

	getter, ok := ts.GetInterfaces()[0].(interface{ BootstrapOnly() bool })
	if !ok {
		t.Fatalf("re-enabled interface %T does not expose BootstrapOnly()", ts.GetInterfaces()[0])
	}
	if !getter.BootstrapOnly() {
		t.Fatal("expected re-enabled UDP bootstrap interface to preserve bootstrap-only metadata")
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredPipeBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap Pipe]]
type = PipeInterface
command = cat
respawn_delay = 1
bootstrap_only = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 configured interface, got %v", got)
	}

	discovery := NewInterfaceDiscovery(r)
	discovery.monitorInterval = 0

	iface := ts.GetInterfaces()[0]
	discovery.teardownInterface(iface)

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected bootstrap interface teardown to remove interface, got %v interfaces", got)
	}

	discovery.monitorAutoconnectsOnce(time.Unix(690, 0))

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected configured Pipe bootstrap interface to be re-enabled, got %v interfaces", got)
	}

	getter, ok := ts.GetInterfaces()[0].(interface{ BootstrapOnly() bool })
	if !ok {
		t.Fatalf("re-enabled interface %T does not expose BootstrapOnly()", ts.GetInterfaces()[0])
	}
	if !getter.BootstrapOnly() {
		t.Fatal("expected re-enabled Pipe bootstrap interface to preserve bootstrap-only metadata")
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredAutoBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap Auto]]
type = AutoInterface
devices = bootstrap-test-device-that-does-not-exist
bootstrap_only = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 configured interface, got %v", got)
	}

	discovery := NewInterfaceDiscovery(r)
	discovery.monitorInterval = 0

	iface := ts.GetInterfaces()[0]
	discovery.teardownInterface(iface)

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected bootstrap interface teardown to remove interface, got %v interfaces", got)
	}

	discovery.monitorAutoconnectsOnce(time.Unix(695, 0))

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected configured Auto bootstrap interface to be re-enabled, got %v interfaces", got)
	}

	getter, ok := ts.GetInterfaces()[0].(interface{ BootstrapOnly() bool })
	if !ok {
		t.Fatalf("re-enabled interface %T does not expose BootstrapOnly()", ts.GetInterfaces()[0])
	}
	if !getter.BootstrapOnly() {
		t.Fatal("expected re-enabled Auto bootstrap interface to preserve bootstrap-only metadata")
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredSerialBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	serialPort, _ := startSocatLinkedPTYPair(t)
	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap Serial]]
type = SerialInterface
port = ` + serialPort + `
speed = 115200
bootstrap_only = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 configured interface, got %v", got)
	}

	discovery := NewInterfaceDiscovery(r)
	discovery.monitorInterval = 0

	iface := ts.GetInterfaces()[0]
	discovery.teardownInterface(iface)

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected bootstrap interface teardown to remove interface, got %v interfaces", got)
	}

	discovery.monitorAutoconnectsOnce(time.Unix(697, 0))

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected configured Serial bootstrap interface to be re-enabled, got %v interfaces", got)
	}

	getter, ok := ts.GetInterfaces()[0].(interface{ BootstrapOnly() bool })
	if !ok {
		t.Fatalf("re-enabled interface %T does not expose BootstrapOnly()", ts.GetInterfaces()[0])
	}
	if !getter.BootstrapOnly() {
		t.Fatal("expected re-enabled Serial bootstrap interface to preserve bootstrap-only metadata")
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredI2PPeerBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap I2P Peer]]
type = I2PInterface
peers = 127.0.0.1:9
bootstrap_only = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 configured interface, got %v", got)
	}
	if got := ts.GetInterfaces()[0].Type(); got != "I2PInterfacePeer" {
		t.Fatalf("registered interface type = %q, want I2PInterfacePeer", got)
	}

	discovery := NewInterfaceDiscovery(r)
	discovery.monitorInterval = 0

	iface := ts.GetInterfaces()[0]
	discovery.teardownInterface(iface)

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected bootstrap interface teardown to remove interface, got %v interfaces", got)
	}

	discovery.monitorAutoconnectsOnce(time.Unix(698, 0))

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected configured I2P peer bootstrap interface to be re-enabled, got %v interfaces", got)
	}
	if got := ts.GetInterfaces()[0].Type(); got != "I2PInterfacePeer" {
		t.Fatalf("re-enabled interface type = %q, want I2PInterfacePeer", got)
	}

	getter, ok := ts.GetInterfaces()[0].(interface{ BootstrapOnly() bool })
	if !ok {
		t.Fatalf("re-enabled interface %T does not expose BootstrapOnly()", ts.GetInterfaces()[0])
	}
	if !getter.BootstrapOnly() {
		t.Fatal("expected re-enabled I2P peer bootstrap interface to preserve bootstrap-only metadata")
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredI2PBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap I2P]]
type = I2PInterface
connectable = Yes
bind_ip = 127.0.0.1
bind_port = ` + strconv.Itoa(port) + `
bootstrap_only = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 configured interface, got %v", got)
	}

	discovery := NewInterfaceDiscovery(r)
	discovery.monitorInterval = 0

	iface := ts.GetInterfaces()[0]
	discovery.teardownInterface(iface)

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected bootstrap interface teardown to remove interface, got %v interfaces", got)
	}

	discovery.monitorAutoconnectsOnce(time.Unix(700, 0))

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected configured I2P bootstrap interface to be re-enabled, got %v interfaces", got)
	}

	getter, ok := ts.GetInterfaces()[0].(interface{ BootstrapOnly() bool })
	if !ok {
		t.Fatalf("re-enabled interface %T does not expose BootstrapOnly()", ts.GetInterfaces()[0])
	}
	if !getter.BootstrapOnly() {
		t.Fatal("expected re-enabled I2P bootstrap interface to preserve bootstrap-only metadata")
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredBackboneClientBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap Backbone Client]]
type = BackboneClientInterface
target_host = 127.0.0.1
target_port = ` + strconv.Itoa(port) + `
bootstrap_only = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 configured interface, got %v", got)
	}

	discovery := NewInterfaceDiscovery(r)
	discovery.monitorInterval = 0

	iface := ts.GetInterfaces()[0]
	discovery.teardownInterface(iface)

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected bootstrap interface teardown to remove interface, got %v interfaces", got)
	}

	discovery.monitorAutoconnectsOnce(time.Unix(800, 0))

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected configured Backbone client bootstrap interface to be re-enabled, got %v interfaces", got)
	}

	getter, ok := ts.GetInterfaces()[0].(interface{ BootstrapOnly() bool })
	if !ok {
		t.Fatalf("re-enabled interface %T does not expose BootstrapOnly()", ts.GetInterfaces()[0])
	}
	if !getter.BootstrapOnly() {
		t.Fatal("expected re-enabled Backbone client bootstrap interface to preserve bootstrap-only metadata")
	}
}

type announceTestInterface struct {
	*interfaces.BaseInterface
	ifaceType string
	bindIP    string
	bindPort  int
	kiss      bool
}

func (a *announceTestInterface) Type() string      { return a.ifaceType }
func (a *announceTestInterface) Status() bool      { return true }
func (a *announceTestInterface) IsOut() bool       { return true }
func (a *announceTestInterface) Send([]byte) error { return nil }
func (a *announceTestInterface) Detach() error     { a.SetDetached(true); return nil }
func (a *announceTestInterface) BindIP() string    { return a.bindIP }
func (a *announceTestInterface) BindPort() int     { return a.bindPort }
func (a *announceTestInterface) KISSFraming() bool { return a.kiss }

type announceCaptureTransport struct {
	*TransportSystem

	mu         sync.Mutex
	lastPacket *Packet
	packets    chan *Packet
}

func newAnnounceCaptureTransport(logger *Logger) *announceCaptureTransport {
	return &announceCaptureTransport{
		TransportSystem: NewTransportSystem(logger),
		packets:         make(chan *Packet, 8),
	}
}

func (ts *announceCaptureTransport) Outbound(packet *Packet) error {
	ts.mu.Lock()
	ts.lastPacket = packet
	ts.mu.Unlock()
	select {
	case ts.packets <- packet:
	default:
	}
	return nil
}

func TestInterfaceAnnouncerPayload(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	transportIdentity := mustTestNewIdentity(t, true)
	ts.identity = transportIdentity
	ts.SetEnabled(true)

	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	announcer := NewInterfaceAnnouncer(r, logger)

	lat := 12.34
	lon := 56.78
	height := 90.12
	iface := &announceTestInterface{
		BaseInterface: interfaces.NewBaseInterface("announce-backbone", interfaces.ModeGateway, 1000),
		ifaceType:     "BackboneInterface",
		bindIP:        "127.0.0.1",
		bindPort:      4242,
	}
	iface.SetIFACConfig(interfaces.IFACConfig{
		Enabled: true,
		NetName: "mesh",
		NetKey:  "secret",
		Size:    16,
	})
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "Discovery Backbone\n",
		ReachableOn:       "discovery.example.net",
		PublishIFAC:       true,
		Latitude:          &lat,
		Longitude:         &lon,
		Height:            &height,
	})

	appData, err := announcer.getInterfaceAnnounceData(iface)
	if err != nil {
		t.Fatalf("getInterfaceAnnounceData() error = %v", err)
	}
	if len(appData) <= 1+discoveryStampSize {
		t.Fatalf("getInterfaceAnnounceData() returned %v bytes, want > %v", len(appData), 1+discoveryStampSize)
	}
	if got := appData[0]; got != 0 {
		t.Fatalf("flags = %08b, want 00000000", got)
	}

	payload := appData[1:]
	packed := payload[:len(payload)-discoveryStampSize]
	stamp := payload[len(payload)-discoveryStampSize:]
	workblock, err := discoveryStampWorkblock(FullHash(packed), discoveryWorkblockRounds)
	if err != nil {
		t.Fatalf("discoveryStampWorkblock() error = %v", err)
	}
	if !discoveryStampValid(stamp, 6, workblock) {
		t.Fatal("expected generated stamp to satisfy configured stamp cost")
	}

	unpacked, err := msgpack.Unpack(packed)
	if err != nil {
		t.Fatalf("msgpack.Unpack() error = %v", err)
	}
	info := asAnyMap(unpacked)
	if info == nil {
		t.Fatalf("unexpected announce payload type %T", unpacked)
	}

	if got := asString(lookupDiscoveryValue(info, discoveryFieldInterfaceType)); got != "BackboneInterface" {
		t.Fatalf("interface type = %q, want %q", got, "BackboneInterface")
	}
	if got := asBool(lookupDiscoveryValue(info, discoveryFieldTransport)); !got {
		t.Fatal("expected transport flag to be true")
	}
	if got := asBytes(lookupDiscoveryValue(info, discoveryFieldTransportID)); hex.EncodeToString(got) != transportIdentity.HexHash {
		t.Fatalf("transport ID = %x, want %v", got, transportIdentity.HexHash)
	}
	if got := asString(lookupDiscoveryValue(info, discoveryFieldName)); got != "Discovery Backbone" {
		t.Fatalf("name = %q, want %q", got, "Discovery Backbone")
	}
	if got := asString(lookupDiscoveryValue(info, discoveryFieldReachableOn)); got != "discovery.example.net" {
		t.Fatalf("reachable_on = %q, want %q", got, "discovery.example.net")
	}
	if got := asInt(lookupDiscoveryValue(info, discoveryFieldPort)); got != 4242 {
		t.Fatalf("port = %v, want 4242", got)
	}
	if got := asString(lookupDiscoveryValue(info, discoveryFieldIFACNetname)); got != "mesh" {
		t.Fatalf("ifac netname = %q, want %q", got, "mesh")
	}
	if got := asString(lookupDiscoveryValue(info, discoveryFieldIFACNetkey)); got != "secret" {
		t.Fatalf("ifac netkey = %q, want %q", got, "secret")
	}
	if got := asFloat64(lookupDiscoveryValue(info, discoveryFieldLatitude)); got != lat {
		t.Fatalf("latitude = %v, want %v", got, lat)
	}
	if got := asFloat64(lookupDiscoveryValue(info, discoveryFieldLongitude)); got != lon {
		t.Fatalf("longitude = %v, want %v", got, lon)
	}
	if got := asFloat64(lookupDiscoveryValue(info, discoveryFieldHeight)); got != height {
		t.Fatalf("height = %v, want %v", got, height)
	}
}

func TestInterfaceAnnouncerPayloadKeepsEmptyDiscoveryName(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	transportIdentity := mustTestNewIdentity(t, true)
	ts.identity = transportIdentity
	ts.SetEnabled(true)

	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	announcer := NewInterfaceAnnouncer(r, logger)

	iface := &announceTestInterface{
		BaseInterface: interfaces.NewBaseInterface("announce-backbone-fallback", interfaces.ModeGateway, 1000),
		ifaceType:     "BackboneInterface",
		bindIP:        "127.0.0.1",
		bindPort:      4242,
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "",
		ReachableOn:       "discovery.example.net",
	})

	appData, err := announcer.getInterfaceAnnounceData(iface)
	if err != nil {
		t.Fatalf("getInterfaceAnnounceData() error = %v", err)
	}

	payload := appData[1:]
	packed := payload[:len(payload)-discoveryStampSize]

	unpacked, err := msgpack.Unpack(packed)
	if err != nil {
		t.Fatalf("msgpack.Unpack() error = %v", err)
	}
	info := asAnyMap(unpacked)
	if info == nil {
		t.Fatalf("unexpected announce payload type %T", unpacked)
	}

	if got := asString(lookupDiscoveryValue(info, discoveryFieldName)); got != "" {
		t.Fatalf("name = %q, want empty string", got)
	}
}

func TestInterfaceAnnouncerPayloadKeepsNilCoordinates(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	transportIdentity := mustTestNewIdentity(t, true)
	ts.identity = transportIdentity
	ts.SetEnabled(true)

	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	announcer := NewInterfaceAnnouncer(r, logger)

	iface := &announceTestInterface{
		BaseInterface: interfaces.NewBaseInterface("announce-backbone-nil-coords", interfaces.ModeGateway, 1000),
		ifaceType:     "BackboneInterface",
		bindIP:        "127.0.0.1",
		bindPort:      4242,
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "No Coords",
		ReachableOn:       "discovery.example.net",
	})

	appData, err := announcer.getInterfaceAnnounceData(iface)
	if err != nil {
		t.Fatalf("getInterfaceAnnounceData() error = %v", err)
	}

	payload := appData[1:]
	packed := payload[:len(payload)-discoveryStampSize]
	unpacked, err := msgpack.Unpack(packed)
	if err != nil {
		t.Fatalf("msgpack.Unpack() error = %v", err)
	}
	info := asAnyMap(unpacked)
	if info == nil {
		t.Fatalf("unexpected announce payload type %T", unpacked)
	}

	for _, field := range []int{discoveryFieldLatitude, discoveryFieldLongitude, discoveryFieldHeight} {
		if got, ok := lookupDiscovery(info, field); !ok {
			t.Fatalf("field %v missing, want present nil field", field)
		} else if got != nil {
			t.Fatalf("field %v = %v, want nil", field, got)
		}
	}
}

func TestInterfaceAnnouncerPayloadKeepsEmptyModulationField(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	transportIdentity := mustTestNewIdentity(t, true)
	ts.identity = transportIdentity
	ts.SetEnabled(true)

	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	announcer := NewInterfaceAnnouncer(r, logger)

	tests := []struct {
		name      string
		ifaceType string
		cfg       interfaces.DiscoveryConfig
	}{
		{
			name:      "weave",
			ifaceType: "WeaveInterface",
			cfg: interfaces.DiscoveryConfig{
				SupportsDiscovery: true,
				Discoverable:      true,
				AnnounceInterval:  time.Hour,
				Name:              "Weave Empty Mod",
				ReachableOn:       "weave.example.net",
				Frequency:         intPtr(2450000000),
				Bandwidth:         intPtr(2000000),
				Channel:           intPtr(11),
				Modulation:        "",
			},
		},
		{
			name:      "kiss",
			ifaceType: "KISSInterface",
			cfg: interfaces.DiscoveryConfig{
				SupportsDiscovery: true,
				Discoverable:      true,
				AnnounceInterval:  time.Hour,
				Name:              "KISS Empty Mod",
				ReachableOn:       "kiss.example.net",
				Frequency:         intPtr(145500000),
				Bandwidth:         intPtr(25000),
				Modulation:        "",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			iface := &announceTestInterface{
				BaseInterface: interfaces.NewBaseInterface(tt.name, interfaces.ModeGateway, 1000),
				ifaceType:     tt.ifaceType,
			}
			iface.SetDiscoveryConfig(tt.cfg)

			appData, err := announcer.getInterfaceAnnounceData(iface)
			if err != nil {
				t.Fatalf("getInterfaceAnnounceData() error = %v", err)
			}

			payload := appData[1:]
			packed := payload[:len(payload)-discoveryStampSize]
			unpacked, err := msgpack.Unpack(packed)
			if err != nil {
				t.Fatalf("msgpack.Unpack() error = %v", err)
			}
			info := asAnyMap(unpacked)
			if info == nil {
				t.Fatalf("unexpected announce payload type %T", unpacked)
			}

			if got, ok := lookupDiscovery(info, discoveryFieldModulation); !ok {
				t.Fatal("modulation field missing, want present empty field")
			} else if got != "" {
				t.Fatalf("modulation = %v, want empty string", got)
			}
		})
	}
}

func TestInterfaceAnnouncerPayloadI2P(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	transportIdentity := mustTestNewIdentity(t, true)
	ts.identity = transportIdentity
	ts.SetEnabled(true)

	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	announcer := NewInterfaceAnnouncer(r, logger)

	iface := &announceTestInterface{
		BaseInterface: interfaces.NewBaseInterface("announce-i2p", interfaces.ModeGateway, 1000),
		ifaceType:     "I2PInterface",
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "Discovery I2P\n",
		ReachableOn:       "exampleabcdefghijklmnopqrstuvwxyz.b32.i2p",
	})

	appData, err := announcer.getInterfaceAnnounceData(iface)
	if err != nil {
		t.Fatalf("getInterfaceAnnounceData() error = %v", err)
	}
	if len(appData) <= 1+discoveryStampSize {
		t.Fatalf("getInterfaceAnnounceData() returned %v bytes, want > %v", len(appData), 1+discoveryStampSize)
	}
	if got := appData[0]; got != 0 {
		t.Fatalf("flags = %08b, want 00000000", got)
	}

	payload := appData[1:]
	packed := payload[:len(payload)-discoveryStampSize]
	stamp := payload[len(payload)-discoveryStampSize:]
	workblock, err := discoveryStampWorkblock(FullHash(packed), discoveryWorkblockRounds)
	if err != nil {
		t.Fatalf("discoveryStampWorkblock() error = %v", err)
	}
	if !discoveryStampValid(stamp, 6, workblock) {
		t.Fatal("expected generated stamp to satisfy configured stamp cost")
	}

	unpacked, err := msgpack.Unpack(packed)
	if err != nil {
		t.Fatalf("msgpack.Unpack() error = %v", err)
	}
	info := asAnyMap(unpacked)
	if info == nil {
		t.Fatalf("unexpected announce payload type %T", unpacked)
	}

	if got := asString(lookupDiscoveryValue(info, discoveryFieldInterfaceType)); got != "I2PInterface" {
		t.Fatalf("interface type = %q, want %q", got, "I2PInterface")
	}
	if got := asString(lookupDiscoveryValue(info, discoveryFieldName)); got != "Discovery I2P" {
		t.Fatalf("name = %q, want %q", got, "Discovery I2P")
	}
	if got := asString(lookupDiscoveryValue(info, discoveryFieldReachableOn)); got != "exampleabcdefghijklmnopqrstuvwxyz.b32.i2p" {
		t.Fatalf("reachable_on = %q, want %q", got, "exampleabcdefghijklmnopqrstuvwxyz.b32.i2p")
	}
	if got := lookupDiscoveryValue(info, discoveryFieldPort); got != nil {
		t.Fatalf("port = %v, want nil", got)
	}
}

func TestInterfaceAnnouncerStart(t *testing.T) {
	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	ts.identity = mustTestNewIdentity(t, true)

	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	announcer := NewInterfaceAnnouncer(r, logger)
	announcer.jobInterval = 10 * time.Millisecond

	iface := &announceTestInterface{
		BaseInterface: interfaces.NewBaseInterface("announce-start", interfaces.ModeGateway, 1000),
		ifaceType:     "BackboneInterface",
		bindIP:        "127.0.0.1",
		bindPort:      4243,
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  time.Hour,
		Name:              "Start Backbone",
		ReachableOn:       "start.example.net",
	})
	ts.RegisterInterface(iface)

	announcer.Start()
	defer announcer.Stop()

	select {
	case <-ts.packets:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for discovery announce packet")
	}

	ts.mu.Lock()
	ts.lastPacket = nil
	ts.mu.Unlock()

	select {
	case packet := <-ts.packets:
		t.Fatalf("received unexpected second announce packet before interval elapsed: %#v", packet)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestInterfaceAnnouncerParity(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("reachable_on executable parity path is not used on Windows")
	}

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-announce-")
	defer cleanup()

	reachableScript := filepath.Join(tmpDir, "reachable-on.sh")
	if err := os.WriteFile(reachableScript, []byte("#!/bin/sh\necho script.example.net\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(reachableScript) error = %v", err)
	}

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	transportIdentity := mustTestNewIdentity(t, true)
	networkIdentity := mustTestNewIdentity(t, true)
	ts.identity = transportIdentity

	r := &Reticulum{
		transport:          ts,
		logger:             logger,
		networkIdentity:    networkIdentity,
		requiredDiscoveryV: 7,
	}
	announcer := NewInterfaceAnnouncer(r, logger)

	iface := &announceTestInterface{
		BaseInterface: interfaces.NewBaseInterface("announce-tcp", interfaces.ModeGateway, 1000),
		ifaceType:     "TCPInterface",
		bindIP:        "127.0.0.1",
		bindPort:      4244,
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        7,
		Name:              "Encrypted TCP\n",
		Encrypt:           true,
		ReachableOn:       reachableScript,
	})

	appData, err := announcer.getInterfaceAnnounceData(iface)
	if err != nil {
		t.Fatalf("getInterfaceAnnounceData() error = %v", err)
	}
	if len(appData) <= 1 {
		t.Fatalf("getInterfaceAnnounceData() returned %v bytes, want > 1", len(appData))
	}
	if got := appData[0]; got != discoveryFlagEncrypted {
		t.Fatalf("flags = %08b, want %08b", got, discoveryFlagEncrypted)
	}

	decrypted, err := networkIdentity.Decrypt(appData[1:], nil, false)
	if err != nil {
		t.Fatalf("networkIdentity.Decrypt() error = %v", err)
	}
	packed := decrypted[:len(decrypted)-discoveryStampSize]
	stamp := decrypted[len(decrypted)-discoveryStampSize:]
	workblock, err := discoveryStampWorkblock(FullHash(packed), discoveryWorkblockRounds)
	if err != nil {
		t.Fatalf("discoveryStampWorkblock() error = %v", err)
	}
	if !discoveryStampValid(stamp, 7, workblock) {
		t.Fatal("expected encrypted announce stamp to satisfy configured stamp cost")
	}

	unpacked, err := msgpack.Unpack(packed)
	if err != nil {
		t.Fatalf("msgpack.Unpack() error = %v", err)
	}
	info := asAnyMap(unpacked)
	if info == nil {
		t.Fatalf("unexpected announce payload type %T", unpacked)
	}
	if got := asString(lookupDiscoveryValue(info, discoveryFieldInterfaceType)); got != "TCPServerInterface" {
		t.Fatalf("interface type = %q, want %q", got, "TCPServerInterface")
	}
	if got := asBytes(lookupDiscoveryValue(info, discoveryFieldTransportID)); hex.EncodeToString(got) != transportIdentity.HexHash {
		t.Fatalf("transport ID = %x, want %v", got, transportIdentity.HexHash)
	}
	if got := asString(lookupDiscoveryValue(info, discoveryFieldReachableOn)); got != "script.example.net" {
		t.Fatalf("reachable_on = %q, want %q", got, "script.example.net")
	}
	if got := asInt(lookupDiscoveryValue(info, discoveryFieldPort)); got != 4244 {
		t.Fatalf("port = %v, want 4244", got)
	}
}

func TestInterfaceAnnouncerPayloadRadioInterfaces(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	transportIdentity := mustTestNewIdentity(t, true)
	ts.identity = transportIdentity
	ts.SetEnabled(true)

	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	announcer := NewInterfaceAnnouncer(r, logger)

	tests := []struct {
		name                string
		ifaceType           string
		kiss                bool
		cfg                 interfaces.DiscoveryConfig
		wantInterfaceType   string
		wantFrequency       int
		wantBandwidth       int
		wantSpreadingFactor int
		wantCodingRate      int
		wantChannel         int
		wantModulation      string
	}{
		{
			name:      "rnode",
			ifaceType: "RNodeInterface",
			cfg: interfaces.DiscoveryConfig{
				SupportsDiscovery: true,
				Discoverable:      true,
				AnnounceInterval:  time.Hour,
				Name:              "RNode",
				ReachableOn:       "radio.example.net",
				Frequency:         intPtr(868100000),
				Bandwidth:         intPtr(125000),
				SpreadingFactor:   intPtr(7),
				CodingRate:        intPtr(5),
			},
			wantInterfaceType:   "RNodeInterface",
			wantFrequency:       868100000,
			wantBandwidth:       125000,
			wantSpreadingFactor: 7,
			wantCodingRate:      5,
		},
		{
			name:      "weave",
			ifaceType: "WeaveInterface",
			cfg: interfaces.DiscoveryConfig{
				SupportsDiscovery: true,
				Discoverable:      true,
				AnnounceInterval:  time.Hour,
				Name:              "Weave",
				ReachableOn:       "weave.example.net",
				Frequency:         intPtr(2450000000),
				Bandwidth:         intPtr(2000000),
				Channel:           intPtr(11),
				Modulation:        "gmsk",
			},
			wantInterfaceType: "WeaveInterface",
			wantFrequency:     2450000000,
			wantBandwidth:     2000000,
			wantChannel:       11,
			wantModulation:    "gmsk",
		},
		{
			name:      "kiss",
			ifaceType: "KISSInterface",
			cfg: interfaces.DiscoveryConfig{
				SupportsDiscovery: true,
				Discoverable:      true,
				AnnounceInterval:  time.Hour,
				Name:              "KISS",
				ReachableOn:       "kiss.example.net",
				Frequency:         intPtr(145500000),
				Bandwidth:         intPtr(25000),
				Modulation:        " afsk \n",
			},
			wantInterfaceType: "KISSInterface",
			wantFrequency:     145500000,
			wantBandwidth:     25000,
			wantModulation:    "afsk",
		},
		{
			name:      "tcp-client-kiss-normalizes-type",
			ifaceType: "TCPInterface",
			kiss:      true,
			cfg: interfaces.DiscoveryConfig{
				SupportsDiscovery: true,
				Discoverable:      true,
				AnnounceInterval:  time.Hour,
				Name:              "TCP KISS",
				ReachableOn:       "tcp-kiss.example.net",
				Frequency:         intPtr(433920000),
				Bandwidth:         intPtr(12500),
				Modulation:        "LoRa",
			},
			wantInterfaceType: "KISSInterface",
			wantFrequency:     433920000,
			wantBandwidth:     12500,
			wantModulation:    "LoRa",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			iface := &announceTestInterface{
				BaseInterface: interfaces.NewBaseInterface(tt.name, interfaces.ModeGateway, 1000),
				ifaceType:     tt.ifaceType,
				kiss:          tt.kiss,
			}
			iface.SetDiscoveryConfig(tt.cfg)

			appData, err := announcer.getInterfaceAnnounceData(iface)
			if err != nil {
				t.Fatalf("getInterfaceAnnounceData() error = %v", err)
			}
			payload := appData[1:]
			packed := payload[:len(payload)-discoveryStampSize]

			unpacked, err := msgpack.Unpack(packed)
			if err != nil {
				t.Fatalf("msgpack.Unpack() error = %v", err)
			}
			info := asAnyMap(unpacked)
			if info == nil {
				t.Fatalf("unexpected announce payload type %T", unpacked)
			}

			if got := asString(lookupDiscoveryValue(info, discoveryFieldInterfaceType)); got != tt.wantInterfaceType {
				t.Fatalf("interface type = %q, want %q", got, tt.wantInterfaceType)
			}
			if tt.wantFrequency != 0 {
				if got := asInt(lookupDiscoveryValue(info, discoveryFieldFrequency)); got != tt.wantFrequency {
					t.Fatalf("frequency = %v, want %v", got, tt.wantFrequency)
				}
			}
			if tt.wantBandwidth != 0 {
				if got := asInt(lookupDiscoveryValue(info, discoveryFieldBandwidth)); got != tt.wantBandwidth {
					t.Fatalf("bandwidth = %v, want %v", got, tt.wantBandwidth)
				}
			}
			if tt.wantSpreadingFactor != 0 {
				if got := asInt(lookupDiscoveryValue(info, discoveryFieldSpreadingFactor)); got != tt.wantSpreadingFactor {
					t.Fatalf("spreading factor = %v, want %v", got, tt.wantSpreadingFactor)
				}
			}
			if tt.wantCodingRate != 0 {
				if got := asInt(lookupDiscoveryValue(info, discoveryFieldCodingRate)); got != tt.wantCodingRate {
					t.Fatalf("coding rate = %v, want %v", got, tt.wantCodingRate)
				}
			}
			if tt.wantChannel != 0 {
				if got := asInt(lookupDiscoveryValue(info, discoveryFieldChannel)); got != tt.wantChannel {
					t.Fatalf("channel = %v, want %v", got, tt.wantChannel)
				}
			}
			if tt.wantModulation != "" {
				if got := asString(lookupDiscoveryValue(info, discoveryFieldModulation)); got != tt.wantModulation {
					t.Fatalf("modulation = %q, want %q", got, tt.wantModulation)
				}
			}
		})
	}
}
