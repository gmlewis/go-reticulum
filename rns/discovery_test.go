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
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
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

type bootstrapConstructorTestInterface struct {
	*interfaces.BaseInterface
	ifaceType string
}

func newBootstrapConstructorTestInterface(name, ifaceType string) *bootstrapConstructorTestInterface {
	return &bootstrapConstructorTestInterface{
		BaseInterface: interfaces.NewBaseInterface(name, interfaces.ModeFull, 0),
		ifaceType:     ifaceType,
	}
}

func (i *bootstrapConstructorTestInterface) Type() string      { return i.ifaceType }
func (i *bootstrapConstructorTestInterface) Status() bool      { return true }
func (i *bootstrapConstructorTestInterface) IsOut() bool       { return true }
func (i *bootstrapConstructorTestInterface) Send([]byte) error { return nil }
func (i *bootstrapConstructorTestInterface) Detach() error {
	i.SetDetached(true)
	return nil
}

func runBootstrapReenableConstructorTest(t *testing.T, config, wantType string) {
	t.Helper()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 configured interface, got %v", got)
	}
	if got := ts.GetInterfaces()[0].Type(); got != wantType {
		t.Fatalf("registered interface type = %q, want %q", got, wantType)
	}

	discovery := NewInterfaceDiscovery(r)
	discovery.monitorInterval = 0

	iface := ts.GetInterfaces()[0]
	discovery.teardownInterface(iface)

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected bootstrap interface teardown to remove interface, got %v interfaces", got)
	}

	discovery.monitorAutoconnectsOnce(time.Unix(810, 0))

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected configured %v bootstrap interface to be re-enabled, got %v interfaces", wantType, got)
	}
	if got := ts.GetInterfaces()[0].Type(); got != wantType {
		t.Fatalf("re-enabled interface type = %q, want %q", got, wantType)
	}

	getter, ok := ts.GetInterfaces()[0].(interface{ BootstrapOnly() bool })
	if !ok {
		t.Fatalf("re-enabled interface %T does not expose BootstrapOnly()", ts.GetInterfaces()[0])
	}
	if !getter.BootstrapOnly() {
		t.Fatalf("expected re-enabled %v bootstrap interface to preserve bootstrap-only metadata", wantType)
	}
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
		"value":      100,
	}
	// Unknown: heard 2 days ago
	unknownData := map[string]any{
		"name":       "Unknown",
		"last_heard": now - (ThresholdUnknown + 3600),
		"value":      200,
	}
	// Stale: heard 4 days ago
	staleData := map[string]any{
		"name":       "Stale",
		"last_heard": now - (ThresholdStale + 3600),
		"value":      300,
	}
	// Expired: heard 8 days ago (should be removed)
	expiredData := map[string]any{
		"name":       "Expired",
		"last_heard": now - (ThresholdRemove + 3600),
		"value":      400,
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
		"value":        100,
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
	writeData("invalid-network-id-hex", map[string]any{
		"name":       "InvalidNetworkIDHex",
		"last_heard": now - 3600,
		"network_id": "not-hex",
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
	if _, err := os.Stat(filepath.Join(storagePath, "invalid-network-id-hex.data")); err != nil {
		t.Fatalf("expected malformed network_id discovery file to remain for corrupt-file handling parity: %v", err)
	}
}

func TestListDiscoveredInterfaces_CorruptNonMapFileLogsAndRemains(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-corrupt-list-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "valid.data"), mustMsgpackPack(map[string]any{
		"name":       "Valid",
		"last_heard": now - 60,
		"value":      1,
	}), 0o644); err != nil {
		t.Fatalf("failed to write valid discovery file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storagePath, "corrupt.data"), mustMsgpackPack("not a map"), 0o644); err != nil {
		t.Fatalf("failed to write corrupt discovery file: %v", err)
	}

	var logs bytes.Buffer
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		logs.WriteString(msg)
		logs.WriteByte('\n')
	})

	r := &Reticulum{
		configDir: tmpDir,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 valid discovered interface, got %v", len(discovered))
	}
	if discovered[0].Name != "Valid" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}
	if _, err := os.Stat(filepath.Join(storagePath, "corrupt.data")); err != nil {
		t.Fatalf("expected corrupt discovery file to remain on disk: %v", err)
	}

	logOutput := logs.String()
	if !strings.Contains(logOutput, "error while loading discovered interface data") {
		t.Fatalf("expected corrupt-file error log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "corrupt.data") || !strings.Contains(logOutput, "may be corrupt") {
		t.Fatalf("expected corrupt-file path warning in logs, got %q", logOutput)
	}
}

func TestListDiscoveredInterfaces_CorruptDirectoryLogsAndRemains(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-corrupt-dir-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "valid.data"), mustMsgpackPack(map[string]any{
		"name":       "Valid",
		"last_heard": now - 60,
		"value":      1,
	}), 0o644); err != nil {
		t.Fatalf("failed to write valid discovery file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(storagePath, "corrupt-dir"), 0o755); err != nil {
		t.Fatalf("failed to create corrupt discovery directory: %v", err)
	}

	var logs bytes.Buffer
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		logs.WriteString(msg)
		logs.WriteByte('\n')
	})

	r := &Reticulum{
		configDir: tmpDir,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 valid discovered interface, got %v", len(discovered))
	}
	if discovered[0].Name != "Valid" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}
	if fi, err := os.Stat(filepath.Join(storagePath, "corrupt-dir")); err != nil || !fi.IsDir() {
		t.Fatalf("expected corrupt discovery directory to remain on disk, stat err=%v", err)
	}

	logOutput := logs.String()
	if !strings.Contains(logOutput, "error while loading discovered interface data") {
		t.Fatalf("expected corrupt-directory error log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "corrupt-dir") || !strings.Contains(logOutput, "may be corrupt") {
		t.Fatalf("expected corrupt-directory path warning in logs, got %q", logOutput)
	}
}

func TestListDiscoveredInterfaces_MissingLastHeardLogsAndRemains(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-missing-last-heard-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "valid.data"), mustMsgpackPack(map[string]any{
		"name":       "Valid",
		"last_heard": now - 60,
		"value":      1,
	}), 0o644); err != nil {
		t.Fatalf("failed to write valid discovery file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storagePath, "missing-last-heard.data"), mustMsgpackPack(map[string]any{
		"name": "MissingLastHeard",
	}), 0o644); err != nil {
		t.Fatalf("failed to write corrupt discovery file: %v", err)
	}

	var logs bytes.Buffer
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		logs.WriteString(msg)
		logs.WriteByte('\n')
	})

	r := &Reticulum{
		configDir: tmpDir,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 valid discovered interface, got %v", len(discovered))
	}
	if discovered[0].Name != "Valid" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}
	if _, err := os.Stat(filepath.Join(storagePath, "missing-last-heard.data")); err != nil {
		t.Fatalf("expected corrupt discovery file to remain on disk: %v", err)
	}

	logOutput := logs.String()
	if !strings.Contains(logOutput, "error while loading discovered interface data") {
		t.Fatalf("expected corrupt-file error log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "missing-last-heard.data") || !strings.Contains(logOutput, "may be corrupt") {
		t.Fatalf("expected corrupt-file path warning in logs, got %q", logOutput)
	}
}

func TestListDiscoveredInterfaces_BoolLastHeardExpiresAndRemoves(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-bool-last-heard-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "valid.data"), mustMsgpackPack(map[string]any{
		"name":       "Valid",
		"last_heard": now - 60,
		"value":      1,
	}), 0o644); err != nil {
		t.Fatalf("failed to write valid discovery file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storagePath, "bool-last-heard.data"), mustMsgpackPack(map[string]any{
		"name":       "BoolLastHeard",
		"last_heard": true,
		"value":      99,
	}), 0o644); err != nil {
		t.Fatalf("failed to write bool last_heard discovery file: %v", err)
	}

	var logs bytes.Buffer
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		logs.WriteString(msg)
		logs.WriteByte('\n')
	})

	r := &Reticulum{
		configDir: tmpDir,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 valid discovered interface, got %v", len(discovered))
	}
	if discovered[0].Name != "Valid" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}
	if _, err := os.Stat(filepath.Join(storagePath, "bool-last-heard.data")); !os.IsNotExist(err) {
		t.Fatalf("expected bool last_heard discovery file to be removed, stat err=%v", err)
	}
	if logOutput := logs.String(); strings.Contains(logOutput, "bool-last-heard.data") {
		t.Fatalf("expected no corrupt-file log for bool last_heard removal, got %q", logOutput)
	}
}

func TestListDiscoveredInterfaces_BoolValueSortsLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-bool-value-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	entries := []struct {
		filename string
		name     string
		value    any
	}{
		{filename: "true.data", name: "TrueValue", value: true},
		{filename: "false.data", name: "FalseValue", value: false},
		{filename: "five.data", name: "FiveValue", value: 5},
	}
	for _, tc := range entries {
		if err := os.WriteFile(filepath.Join(storagePath, tc.filename), mustMsgpackPack(map[string]any{
			"name":       tc.name,
			"last_heard": now - 60,
			"value":      tc.value,
		}), 0o644); err != nil {
			t.Fatalf("failed to write discovery file %q: %v", tc.filename, err)
		}
	}

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 3 {
		t.Fatalf("expected 3 discovered interfaces, got %v", len(discovered))
	}

	gotNames := []string{discovered[0].Name, discovered[1].Name, discovered[2].Name}
	wantNames := []string{"FiveValue", "TrueValue", "FalseValue"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("names = %v, want %v", gotNames, wantNames)
	}
	gotValues := []int{discovered[0].Value, discovered[1].Value, discovered[2].Value}
	wantValues := []int{5, 1, 0}
	if !reflect.DeepEqual(gotValues, wantValues) {
		t.Fatalf("values = %v, want %v", gotValues, wantValues)
	}
}

func TestListDiscoveredInterfaces_FloatValueSortsLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-float-value-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	entries := []struct {
		filename  string
		name      string
		lastHeard float64
		value     any
	}{
		{filename: "higher-float.data", name: "HigherFloatValue", lastHeard: now - 120, value: 7.9},
		{filename: "lower-float.data", name: "LowerFloatValue", lastHeard: now - 60, value: 7.1},
	}
	for _, tc := range entries {
		if err := os.WriteFile(filepath.Join(storagePath, tc.filename), mustMsgpackPack(map[string]any{
			"name":       tc.name,
			"last_heard": tc.lastHeard,
			"value":      tc.value,
		}), 0o644); err != nil {
			t.Fatalf("failed to write discovery file %q: %v", tc.filename, err)
		}
	}

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 2 {
		t.Fatalf("expected 2 discovered interfaces, got %v", len(discovered))
	}

	gotNames := []string{discovered[0].Name, discovered[1].Name}
	wantNames := []string{"HigherFloatValue", "LowerFloatValue"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("names = %v, want %v", gotNames, wantNames)
	}
}

func TestListDiscoveredInterfaces_StringValueSingleEntryDoesNotFail(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-string-value-single-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "string-value.data"), mustMsgpackPack(map[string]any{
		"name":       "StringValue",
		"last_heard": now - 60,
		"value":      "7",
	}), 0o644); err != nil {
		t.Fatalf("failed to write string value discovery file: %v", err)
	}

	var logs bytes.Buffer
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		logs.WriteString(msg)
		logs.WriteByte('\n')
	})

	r := &Reticulum{
		configDir: tmpDir,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 discovered interface, got %v", len(discovered))
	}
	if discovered[0].Name != "StringValue" {
		t.Fatalf("unexpected discovered interface %q", discovered[0].Name)
	}
	if got := logs.String(); strings.Contains(got, "error while loading discovered interface data") {
		t.Fatalf("unexpected corrupt-file log output: %q", got)
	}
}

func TestListDiscoveredInterfaces_StringValueSortsLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-string-value-sort-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	entries := []struct {
		filename  string
		name      string
		lastHeard float64
		value     any
	}{
		{filename: "string-seven.data", name: "StringSeven", lastHeard: now - 120, value: "7"},
		{filename: "string-three.data", name: "StringThree", lastHeard: now - 60, value: "3"},
	}
	for _, tc := range entries {
		if err := os.WriteFile(filepath.Join(storagePath, tc.filename), mustMsgpackPack(map[string]any{
			"name":       tc.name,
			"last_heard": tc.lastHeard,
			"value":      tc.value,
		}), 0o644); err != nil {
			t.Fatalf("failed to write discovery file %q: %v", tc.filename, err)
		}
	}

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 2 {
		t.Fatalf("expected 2 discovered interfaces, got %v", len(discovered))
	}

	gotNames := []string{discovered[0].Name, discovered[1].Name}
	wantNames := []string{"StringSeven", "StringThree"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("names = %v, want %v", gotNames, wantNames)
	}
}

func TestListDiscoveredInterfaces_MixedStringAndIntegerValueReturnsError(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-mixed-value-sort-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	entries := []struct {
		filename  string
		name      string
		lastHeard float64
		value     any
	}{
		{filename: "string-seven.data", name: "StringSeven", lastHeard: now - 120, value: "7"},
		{filename: "int-three.data", name: "IntThree", lastHeard: now - 60, value: 3},
	}
	for _, tc := range entries {
		if err := os.WriteFile(filepath.Join(storagePath, tc.filename), mustMsgpackPack(map[string]any{
			"name":       tc.name,
			"last_heard": tc.lastHeard,
			"value":      tc.value,
		}), 0o644); err != nil {
			t.Fatalf("failed to write discovery file %q: %v", tc.filename, err)
		}
	}

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)

	if _, err := discovery.ListDiscoveredInterfaces(false, false); err == nil {
		t.Fatal("ListDiscoveredInterfaces() error = nil, want error for mixed string and integer values")
	}
}

func TestListDiscoveredInterfaces_NilValueSortsByLastHeard(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-nil-value-sort-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	entries := []struct {
		filename  string
		name      string
		lastHeard float64
		value     any
	}{
		{filename: "none-older.data", name: "NoneOlder", lastHeard: now - 120, value: nil},
		{filename: "none-newer.data", name: "NoneNewer", lastHeard: now - 60, value: nil},
	}
	for _, tc := range entries {
		if err := os.WriteFile(filepath.Join(storagePath, tc.filename), mustMsgpackPack(map[string]any{
			"name":       tc.name,
			"last_heard": tc.lastHeard,
			"value":      tc.value,
		}), 0o644); err != nil {
			t.Fatalf("failed to write discovery file %q: %v", tc.filename, err)
		}
	}

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 2 {
		t.Fatalf("expected 2 discovered interfaces, got %v", len(discovered))
	}

	gotNames := []string{discovered[0].Name, discovered[1].Name}
	wantNames := []string{"NoneNewer", "NoneOlder"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("names = %v, want %v", gotNames, wantNames)
	}
}

func TestListDiscoveredInterfaces_BoolDiscoveredUsesPythonNumericSemantics(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-bool-discovered-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "bool-discovered.data"), mustMsgpackPack(map[string]any{
		"name":       "BoolDiscovered",
		"last_heard": now - 60,
		"discovered": true,
		"value":      5,
	}), 0o644); err != nil {
		t.Fatalf("failed to write bool discovered discovery file: %v", err)
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
	if got := discovered[0].Discovered; got != 1 {
		t.Fatalf("Discovered = %v, want 1", got)
	}
}

func TestListDiscoveredInterfaces_BoolPortUsesPythonNumericSemantics(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-bool-port-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "bool-port.data"), mustMsgpackPack(map[string]any{
		"name":         "BoolPort",
		"last_heard":   now - 60,
		"discovered":   now - 60,
		"reachable_on": "127.0.0.1",
		"value":        5,
		"port":         true,
	}), 0o644); err != nil {
		t.Fatalf("failed to write bool port discovery file: %v", err)
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
	if discovered[0].Port == nil || *discovered[0].Port != 1 {
		t.Fatalf("Port = %v, want 1", discovered[0].Port)
	}
	if got, want := hex.EncodeToString(discovery.endpointHash(discovered[0])), hex.EncodeToString(FullHash([]byte("127.0.0.1:True"))); got != want {
		t.Fatalf("endpointHash(bool port) = %q, want %q", got, want)
	}
}

func TestListDiscoveredInterfaces_NonIntegralFloatPortDoesNotDeduplicateAgainstIntegerHost(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-float-port-dedupe-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "float-port.data"), mustMsgpackPack(map[string]any{
		"name":         "FloatPort",
		"last_heard":   now - 60,
		"discovered":   now - 60,
		"reachable_on": "127.0.0.1",
		"value":        5,
		"port":         1.5,
	}), 0o644); err != nil {
		t.Fatalf("failed to write float port discovery file: %v", err)
	}

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	ts.RegisterInterface(newTargetHostTestInterface("Existing", "BackboneClientInterface", "127.0.0.1", 1))

	r := &Reticulum{configDir: tmpDir, transport: ts, logger: logger}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 discovered interface, got %v", len(discovered))
	}
	if discovery.interfaceExists(discovered[0]) {
		t.Fatal("expected non-integral float port not to match existing integer target port")
	}
}

func TestListDiscoveredInterfaces_EmptyReachableOnLogsAndRemains(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-empty-reachable-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "valid.data"), mustMsgpackPack(map[string]any{
		"name":       "Valid",
		"last_heard": now - 60,
		"value":      1,
	}), 0o644); err != nil {
		t.Fatalf("failed to write valid discovery file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storagePath, "empty-reachable.data"), mustMsgpackPack(map[string]any{
		"name":         "EmptyReachable",
		"last_heard":   now - 60,
		"value":        2,
		"reachable_on": "",
	}), 0o644); err != nil {
		t.Fatalf("failed to write empty reachable discovery file: %v", err)
	}

	var logs bytes.Buffer
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		logs.WriteString(msg)
		logs.WriteByte('\n')
	})

	r := &Reticulum{
		configDir: tmpDir,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 valid discovered interface, got %v", len(discovered))
	}
	if discovered[0].Name != "Valid" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}
	if _, err := os.Stat(filepath.Join(storagePath, "empty-reachable.data")); err != nil {
		t.Fatalf("expected empty reachable discovery file to remain on disk: %v", err)
	}

	logOutput := logs.String()
	if !strings.Contains(logOutput, "error while loading discovered interface data") {
		t.Fatalf("expected corrupt-file error log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "empty-reachable.data") || !strings.Contains(logOutput, "may be corrupt") {
		t.Fatalf("expected corrupt-file path warning in logs, got %q", logOutput)
	}
}

func TestListDiscoveredInterfaces_BytesReachableOnLogsAndRemains(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-bytes-reachable-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "valid.data"), mustMsgpackPack(map[string]any{
		"name":       "Valid",
		"last_heard": now - 60,
		"value":      1,
	}), 0o644); err != nil {
		t.Fatalf("failed to write valid discovery file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storagePath, "bytes-reachable.data"), mustMsgpackPack(map[string]any{
		"name":         "BytesReachable",
		"last_heard":   now - 60,
		"value":        2,
		"reachable_on": []byte("127.0.0.1"),
	}), 0o644); err != nil {
		t.Fatalf("failed to write bytes reachable discovery file: %v", err)
	}

	var logs bytes.Buffer
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		logs.WriteString(msg)
		logs.WriteByte('\n')
	})

	r := &Reticulum{
		configDir: tmpDir,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 valid discovered interface, got %v", len(discovered))
	}
	if discovered[0].Name != "Valid" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}
	if _, err := os.Stat(filepath.Join(storagePath, "bytes-reachable.data")); err != nil {
		t.Fatalf("expected bytes reachable discovery file to remain on disk: %v", err)
	}

	logOutput := logs.String()
	if !strings.Contains(logOutput, "error while loading discovered interface data") {
		t.Fatalf("expected corrupt-file error log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "bytes-reachable.data") || !strings.Contains(logOutput, "may be corrupt") {
		t.Fatalf("expected corrupt-file path warning in logs, got %q", logOutput)
	}
}

func TestListDiscoveredInterfaces_IntegerReachableOnRemainsAvailable(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-int-reachable-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "int-reachable.data"), mustMsgpackPack(map[string]any{
		"name":         "IntegerReachable",
		"last_heard":   now - 60,
		"value":        1,
		"transport":    true,
		"reachable_on": 3232235777,
	}), 0o644); err != nil {
		t.Fatalf("failed to write integer reachable discovery file: %v", err)
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
	if discovered[0].Name != "IntegerReachable" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}
	if got := discovered[0].ReachableOn; got != "3232235777" {
		t.Fatalf("ReachableOn = %q, want %q", got, "3232235777")
	}
	if _, err := os.Stat(filepath.Join(storagePath, "int-reachable.data")); err != nil {
		t.Fatalf("expected integer reachable discovery file to remain on disk: %v", err)
	}
}

func TestListDiscoveredInterfaces_BoolReachableOnRemainsAvailable(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-bool-reachable-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "bool-reachable.data"), mustMsgpackPack(map[string]any{
		"name":         "BoolReachable",
		"last_heard":   now - 60,
		"value":        1,
		"transport":    true,
		"reachable_on": true,
	}), 0o644); err != nil {
		t.Fatalf("failed to write bool reachable discovery file: %v", err)
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
	if discovered[0].Name != "BoolReachable" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}
	if got := discovered[0].ReachableOn; got != "true" {
		t.Fatalf("ReachableOn = %q, want %q", got, "true")
	}
	if _, err := os.Stat(filepath.Join(storagePath, "bool-reachable.data")); err != nil {
		t.Fatalf("expected bool reachable discovery file to remain on disk: %v", err)
	}
}

func TestListDiscoveredInterfaces_InvalidNetworkIDTypeLogsAndRemains(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-network-id-type-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "valid.data"), mustMsgpackPack(map[string]any{
		"name":       "Valid",
		"last_heard": now - 60,
		"value":      1,
		"transport":  true,
		"network_id": "aabb",
	}), 0o644); err != nil {
		t.Fatalf("failed to write valid discovery file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storagePath, "invalid-network-id-type.data"), mustMsgpackPack(map[string]any{
		"name":       "InvalidNetworkIDType",
		"last_heard": now - 60,
		"value":      2,
		"transport":  true,
		"network_id": 123,
	}), 0o644); err != nil {
		t.Fatalf("failed to write invalid network_id type discovery file: %v", err)
	}

	var logs bytes.Buffer
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		logs.WriteString(msg)
		logs.WriteByte('\n')
	})

	r := &Reticulum{
		configDir:        tmpDir,
		logger:           logger,
		interfaceSources: [][]byte{{0xaa, 0xbb}},
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 valid discovered interface, got %v", len(discovered))
	}
	if discovered[0].Name != "Valid" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}
	if _, err := os.Stat(filepath.Join(storagePath, "invalid-network-id-type.data")); err != nil {
		t.Fatalf("expected invalid network_id type discovery file to remain on disk: %v", err)
	}

	logOutput := logs.String()
	if !strings.Contains(logOutput, "error while loading discovered interface data") {
		t.Fatalf("expected corrupt-file error log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "invalid-network-id-type.data") || !strings.Contains(logOutput, "may be corrupt") {
		t.Fatalf("expected corrupt-file path warning in logs, got %q", logOutput)
	}
}

func TestListDiscoveredInterfaces_OnlyTransportMissingTransportLogsAndRemains(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-missing-transport-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "valid.data"), mustMsgpackPack(map[string]any{
		"name":       "Valid",
		"last_heard": now - 60,
		"transport":  true,
		"value":      1,
	}), 0o644); err != nil {
		t.Fatalf("failed to write valid discovery file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storagePath, "missing-transport.data"), mustMsgpackPack(map[string]any{
		"name":       "MissingTransport",
		"last_heard": now - 60,
	}), 0o644); err != nil {
		t.Fatalf("failed to write corrupt discovery file: %v", err)
	}

	var logs bytes.Buffer
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		logs.WriteString(msg)
		logs.WriteByte('\n')
	})

	r := &Reticulum{
		configDir: tmpDir,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, true)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 valid transport discovered interface, got %v", len(discovered))
	}
	if discovered[0].Name != "Valid" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}
	if _, err := os.Stat(filepath.Join(storagePath, "missing-transport.data")); err != nil {
		t.Fatalf("expected corrupt discovery file to remain on disk: %v", err)
	}

	logOutput := logs.String()
	if !strings.Contains(logOutput, "error while loading discovered interface data") {
		t.Fatalf("expected corrupt-file error log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "missing-transport.data") || !strings.Contains(logOutput, "may be corrupt") {
		t.Fatalf("expected corrupt-file path warning in logs, got %q", logOutput)
	}
}

func TestListDiscoveredInterfaces_OnlyAvailableMissingTransportLogsAndRemains(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-only-available-transport-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "missing-transport.data"), mustMsgpackPack(map[string]any{
		"name":       "MissingTransport",
		"last_heard": now - 60,
		"value":      42,
	}), 0o644); err != nil {
		t.Fatalf("failed to write discovery file: %v", err)
	}

	var logs bytes.Buffer
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		logs.WriteString(msg)
		logs.WriteByte('\n')
	})

	r := &Reticulum{
		configDir: tmpDir,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(true, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("expected 0 available discovered interfaces, got %v", len(discovered))
	}
	logOutput := logs.String()
	if !strings.Contains(logOutput, "error while loading discovered interface data") {
		t.Fatalf("expected corrupt-file error log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "missing-transport.data") || !strings.Contains(logOutput, "may be corrupt") {
		t.Fatalf("expected corrupt-file path warning in logs, got %q", logOutput)
	}
	if _, err := os.Stat(filepath.Join(storagePath, "missing-transport.data")); err != nil {
		t.Fatalf("expected discovery file to remain on disk: %v", err)
	}
}

func TestListDiscoveredInterfaces_OnlyAvailableAllowsNilTransport(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-only-available-nil-transport-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "nil-transport.data"), mustMsgpackPack(map[string]any{
		"name":       "NilTransport",
		"last_heard": now - 60,
		"transport":  nil,
		"value":      42,
	}), 0o644); err != nil {
		t.Fatalf("failed to write discovery file: %v", err)
	}

	var logs bytes.Buffer
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		logs.WriteString(msg)
		logs.WriteByte('\n')
	})

	r := &Reticulum{
		configDir: tmpDir,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(true, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 available discovered interface, got %v", len(discovered))
	}
	if discovered[0].Name != "NilTransport" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}
	if discovered[0].Status != "available" {
		t.Fatalf("status = %q, want available", discovered[0].Status)
	}
	if discovered[0].Transport {
		t.Fatal("expected nil transport to map to Transport=false")
	}
	if got := logs.String(); strings.Contains(got, "error while loading discovered interface data") {
		t.Fatalf("unexpected corrupt-file log output: %q", got)
	}
}

func TestListDiscoveredInterfaces_OnlyTransportNilTransportSkipsWithoutLog(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-only-transport-nil-transport-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "nil-transport.data"), mustMsgpackPack(map[string]any{
		"name":       "NilTransport",
		"last_heard": now - 60,
		"transport":  nil,
		"value":      42,
	}), 0o644); err != nil {
		t.Fatalf("failed to write discovery file: %v", err)
	}

	var logs bytes.Buffer
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		logs.WriteString(msg)
		logs.WriteByte('\n')
	})

	r := &Reticulum{
		configDir: tmpDir,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, true)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("expected 0 transport discovered interfaces, got %v", len(discovered))
	}
	if got := logs.String(); strings.Contains(got, "error while loading discovered interface data") {
		t.Fatalf("unexpected corrupt-file log output: %q", got)
	}
	if _, err := os.Stat(filepath.Join(storagePath, "nil-transport.data")); err != nil {
		t.Fatalf("expected discovery file to remain on disk: %v", err)
	}
}

func TestListDiscoveredInterfaces_OnlyTransportIncludesTruthyStringTransport(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-string-transport-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "string-transport.data"), mustMsgpackPack(map[string]any{
		"name":       "TruthyStringTransport",
		"last_heard": now - 60,
		"transport":  "yes",
		"value":      1,
	}), 0o644); err != nil {
		t.Fatalf("failed to write string transport discovery file: %v", err)
	}

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, true)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 truthy string transport discovered interface, got %v", len(discovered))
	}
	if discovered[0].Name != "TruthyStringTransport" {
		t.Fatalf("unexpected surviving interface %q", discovered[0].Name)
	}
	if !discovered[0].Transport {
		t.Fatal("expected truthy string transport to map to Transport=true")
	}
}

func TestListDiscoveredInterfaces_OnlyTransportUsesContainerTruthiness(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-container-transport-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	write := func(name string, transport any) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(storagePath, name+".data"), mustMsgpackPack(map[string]any{
			"name":       name,
			"last_heard": now - 60,
			"transport":  transport,
			"value":      1,
		}), 0o644); err != nil {
			t.Fatalf("failed to write %s discovery file: %v", name, err)
		}
	}
	write("empty-list", []any{})
	write("nonempty-list", []any{1})
	write("empty-map", map[any]any{})
	write("nonempty-map", map[any]any{"x": 1})

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, true)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 2 {
		t.Fatalf("expected 2 truthy container transports, got %v", len(discovered))
	}
	gotNames := []string{discovered[0].Name, discovered[1].Name}
	sort.Strings(gotNames)
	if !reflect.DeepEqual(gotNames, []string{"nonempty-list", "nonempty-map"}) {
		t.Fatalf("transport truthiness names = %v, want %v", gotNames, []string{"nonempty-list", "nonempty-map"})
	}
	for _, entry := range discovered {
		if !entry.Transport {
			t.Fatalf("expected Transport=true for %q", entry.Name)
		}
	}
}

func TestListDiscoveredInterfaces_MissingValueReturnsErrorAndRemains(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-missing-value-")
	defer cleanup()
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "valid.data"), mustMsgpackPack(map[string]any{
		"name":       "Valid",
		"last_heard": now - 60,
		"transport":  true,
		"value":      1,
	}), 0o644); err != nil {
		t.Fatalf("failed to write valid discovery file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storagePath, "missing-value.data"), mustMsgpackPack(map[string]any{
		"name":       "MissingValue",
		"last_heard": now - 60,
		"transport":  true,
	}), 0o644); err != nil {
		t.Fatalf("failed to write corrupt discovery file: %v", err)
	}

	discovery := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir})

	if _, err := discovery.ListDiscoveredInterfaces(false, false); err == nil {
		t.Fatal("ListDiscoveredInterfaces() error = nil, want error for missing value")
	}
	if _, err := os.Stat(filepath.Join(storagePath, "missing-value.data")); err != nil {
		t.Fatalf("expected corrupt discovery file to remain on disk: %v", err)
	}
}

func TestListDiscoveredInterfaces_PresentNilNameDisplaysAsPythonNone(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-none-name-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "none-name.data"), mustMsgpackPack(map[string]any{
		"name":       nil,
		"last_heard": now - 60,
		"transport":  true,
		"value":      1,
	}), 0o644); err != nil {
		t.Fatalf("failed to write discovery file: %v", err)
	}

	discovery := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir})
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if got, want := len(discovered), 1; got != want {
		t.Fatalf("len(discovered) = %v, want %v", got, want)
	}
	if got := discovered[0].Name; got != "None" {
		t.Fatalf("Name = %q, want %q", got, "None")
	}
}

func TestListDiscoveredInterfaces_BytesTransportIDDisplaysAsPythonBytes(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-bytes-transport-id-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "bytes-transport-id.data"), mustMsgpackPack(map[string]any{
		"name":         "Bytes Transport ID",
		"type":         "TCPServerInterface",
		"last_heard":   now - 60,
		"transport":    true,
		"value":        1,
		"transport_id": []byte("deadbeef"),
	}), 0o644); err != nil {
		t.Fatalf("failed to write discovery file: %v", err)
	}

	discovery := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir})
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if got, want := len(discovered), 1; got != want {
		t.Fatalf("len(discovered) = %v, want %v", got, want)
	}
	if got := discovered[0].TransportID; got != "b'deadbeef'" {
		t.Fatalf("TransportID = %q, want %q", got, "b'deadbeef'")
	}
}

func TestListDiscoveredInterfaces_BytesConfigEntryDisplaysAsPythonBytes(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-bytes-config-entry-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "bytes-config-entry.data"), mustMsgpackPack(map[string]any{
		"name":         "Bytes Config Entry",
		"type":         "TCPServerInterface",
		"last_heard":   now - 60,
		"transport":    true,
		"value":        1,
		"config_entry": []byte("[[Bytes]]"),
	}), 0o644); err != nil {
		t.Fatalf("failed to write discovery file: %v", err)
	}

	discovery := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir})
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if got, want := len(discovered), 1; got != want {
		t.Fatalf("len(discovered) = %v, want %v", got, want)
	}
	if got := discovered[0].ConfigEntry; got != "b'[[Bytes]]'" {
		t.Fatalf("ConfigEntry = %q, want %q", got, "b'[[Bytes]]'")
	}
}

func TestListDiscoveredInterfaces_BytesNetworkIDDisplaysAsPythonBytes(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-bytes-network-id-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "bytes-network-id.data"), mustMsgpackPack(map[string]any{
		"name":       "Bytes Network ID",
		"type":       "TCPServerInterface",
		"last_heard": now - 60,
		"transport":  true,
		"value":      1,
		"network_id": []byte("01020304"),
	}), 0o644); err != nil {
		t.Fatalf("failed to write discovery file: %v", err)
	}

	discovery := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir})
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if got, want := len(discovered), 1; got != want {
		t.Fatalf("len(discovered) = %v, want %v", got, want)
	}
	if got := discovered[0].NetworkID; got != "b'01020304'" {
		t.Fatalf("NetworkID = %q, want %q", got, "b'01020304'")
	}
}

func TestListDiscoveredInterfaces_BytesIFACFieldsDisplayAsPythonBytes(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-bytes-ifac-fields-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "bytes-ifac-fields.data"), mustMsgpackPack(map[string]any{
		"name":         "Bytes IFAC",
		"type":         "TCPServerInterface",
		"last_heard":   now - 60,
		"transport":    true,
		"value":        1,
		"ifac_netname": []byte("mesh"),
		"ifac_netkey":  []byte("secret"),
	}), 0o644); err != nil {
		t.Fatalf("failed to write discovery file: %v", err)
	}

	discovery := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir})
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if got, want := len(discovered), 1; got != want {
		t.Fatalf("len(discovered) = %v, want %v", got, want)
	}
	if got := discovered[0].IFACNetname; got != "b'mesh'" {
		t.Fatalf("IFACNetname = %q, want %q", got, "b'mesh'")
	}
	if got := discovered[0].IFACNetkey; got != "b'secret'" {
		t.Fatalf("IFACNetkey = %q, want %q", got, "b'secret'")
	}
}

func TestListDiscoveredInterfaces_BytesModulationDisplaysAsPythonBytes(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-bytes-modulation-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "bytes-modulation.data"), mustMsgpackPack(map[string]any{
		"name":       "Bytes Modulation",
		"type":       "KISSInterface",
		"last_heard": now - 60,
		"transport":  true,
		"value":      1,
		"modulation": []byte("lora"),
	}), 0o644); err != nil {
		t.Fatalf("failed to write discovery file: %v", err)
	}

	discovery := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir})
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if got, want := len(discovered), 1; got != want {
		t.Fatalf("len(discovered) = %v, want %v", got, want)
	}
	if got := discovered[0].Modulation; got != "b'lora'" {
		t.Fatalf("Modulation = %q, want %q", got, "b'lora'")
	}
}

func TestIsHostnameMatchesPython(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "trailing dot", in: "example.com.", want: true},
		{name: "numeric tld", in: "example.123", want: false},
		{name: "localhost", in: "localhost", want: true},
		{name: "trailing hyphen", in: "a-", want: false},
		{name: "leading hyphen", in: "-a", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isHostname(tt.in); got != tt.want {
				t.Fatalf("isHostname(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
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

func TestPersistDiscoveredInterface_NewEntryAllowsZeroReceived(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-zero-received-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       0.0,
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

	if got := asFloat64(lookupAnyValue(m, "discovered")); got != 0 {
		t.Fatalf("discovered = %v, want 0", got)
	}
	if got := asFloat64(lookupAnyValue(m, "last_heard")); got != 0 {
		t.Fatalf("last_heard = %v, want 0", got)
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

func TestPersistDiscoveredInterface_ExistingEntryAllowsZeroReceived(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-existing-zero-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       5.0,
		"discovery_hash": "aabbcc",
		"value":          1234,
	}

	if err := discovery.persistDiscoveredInterface(info); err != nil {
		t.Fatalf("first persistDiscoveredInterface failed: %v", err)
	}

	if err := discovery.persistDiscoveredInterface(map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       0.0,
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

	if got := asFloat64(lookupAnyValue(m, "discovered")); got != 5.0 {
		t.Fatalf("discovered = %v, want 5.0", got)
	}
	if got := asFloat64(lookupAnyValue(m, "last_heard")); got != 0 {
		t.Fatalf("last_heard = %v, want 0", got)
	}
	if got := asInt(lookupAnyValue(m, "heard_count")); got != 1 {
		t.Fatalf("heard_count = %v, want 1", got)
	}
	if got := asInt(lookupAnyValue(m, "value")); got != 5678 {
		t.Fatalf("value = %v, want 5678", got)
	}
}

func TestPersistDiscoveredInterface_NewEntryMissingReceivedLeavesEmptyFile(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-missing-received-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"discovery_hash": []byte{0xaa, 0xbb, 0xcc},
		"value":          1234,
	}

	if err := discovery.persistDiscoveredInterface(info); err == nil {
		t.Fatal("persistDiscoveredInterface() error = nil, want error for missing received timestamp")
	}

	path := filepath.Join(tmpDir, "discovery", "interfaces", "aabbcc.data")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected empty persisted discovery file: %v", err)
	}
	if fi.Size() != 0 {
		t.Fatalf("empty persisted discovery file size = %v, want 0", fi.Size())
	}
}

func TestPersistDiscoveredInterface_NewEntryAllowsNilReceived(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-nil-received-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       nil,
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

	if got, ok := lookupAny(m, "discovered"); !ok || got != nil {
		t.Fatalf("discovered = %#v, present=%v, want nil present", got, ok)
	}
	if got, ok := lookupAny(m, "last_heard"); !ok || got != nil {
		t.Fatalf("last_heard = %#v, present=%v, want nil present", got, ok)
	}
	if got := asInt(lookupAnyValue(m, "heard_count")); got != 0 {
		t.Fatalf("heard_count = %v, want 0", got)
	}
}

func TestPersistDiscoveredInterface_NewEntryAllowsStringReceived(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-string-received-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       "oops",
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

	if got := asString(lookupAnyValue(m, "discovered")); got != "oops" {
		t.Fatalf("discovered = %q, want %q", got, "oops")
	}
	if got := asString(lookupAnyValue(m, "last_heard")); got != "oops" {
		t.Fatalf("last_heard = %q, want %q", got, "oops")
	}
	if got := asInt(lookupAnyValue(m, "heard_count")); got != 0 {
		t.Fatalf("heard_count = %v, want 0", got)
	}
}

func TestPersistDiscoveredInterface_NewEntryAllowsIntegerDiscoveryHash(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-int-hash-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       1.0,
		"discovery_hash": 123,
		"value":          1234,
	}

	if err := discovery.persistDiscoveredInterface(info); err != nil {
		t.Fatalf("persistDiscoveredInterface failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", "7b.data"))
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

	if got := asFloat64(lookupAnyValue(m, "discovered")); got != 1.0 {
		t.Fatalf("discovered = %v, want 1.0", got)
	}
	if got := asFloat64(lookupAnyValue(m, "last_heard")); got != 1.0 {
		t.Fatalf("last_heard = %v, want 1.0", got)
	}
	if got := asInt(lookupAnyValue(m, "heard_count")); got != 0 {
		t.Fatalf("heard_count = %v, want 0", got)
	}
}

func TestPersistDiscoveredInterface_NewEntryAllowsBoolDiscoveryHash(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-bool-hash-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       1.0,
		"discovery_hash": true,
		"value":          1234,
	}

	if err := discovery.persistDiscoveredInterface(info); err != nil {
		t.Fatalf("persistDiscoveredInterface failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", "01.data"))
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

	if got := asFloat64(lookupAnyValue(m, "discovered")); got != 1.0 {
		t.Fatalf("discovered = %v, want 1.0", got)
	}
	if got := asFloat64(lookupAnyValue(m, "last_heard")); got != 1.0 {
		t.Fatalf("last_heard = %v, want 1.0", got)
	}
	if got := asInt(lookupAnyValue(m, "heard_count")); got != 0 {
		t.Fatalf("heard_count = %v, want 0", got)
	}
}

func TestPersistDiscoveredInterface_ExistingEntryMissingReceivedTruncatesFile(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-truncate-received-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       5.0,
		"discovery_hash": "aabbcc",
		"value":          1234,
	}

	if err := discovery.persistDiscoveredInterface(info); err != nil {
		t.Fatalf("first persistDiscoveredInterface failed: %v", err)
	}

	if err := discovery.persistDiscoveredInterface(map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"discovery_hash": "aabbcc",
		"value":          5678,
	}); err == nil {
		t.Fatal("persistDiscoveredInterface() error = nil, want error for missing received timestamp")
	}

	path := filepath.Join(tmpDir, "discovery", "interfaces", "aabbcc.data")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected truncated persisted discovery file: %v", err)
	}
	if fi.Size() != 0 {
		t.Fatalf("truncated persisted discovery file size = %v, want 0", fi.Size())
	}
}

func TestPersistDiscoveredInterface_ExistingEntryAllowsNilReceived(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-existing-nil-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       5.0,
		"discovery_hash": "aabbcc",
		"value":          1234,
	}

	if err := discovery.persistDiscoveredInterface(info); err != nil {
		t.Fatalf("first persistDiscoveredInterface failed: %v", err)
	}

	if err := discovery.persistDiscoveredInterface(map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       nil,
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

	if got := asFloat64(lookupAnyValue(m, "discovered")); got != 5.0 {
		t.Fatalf("discovered = %v, want 5.0", got)
	}
	if got, ok := lookupAny(m, "last_heard"); !ok || got != nil {
		t.Fatalf("last_heard = %#v, present=%v, want nil present", got, ok)
	}
	if got := asInt(lookupAnyValue(m, "heard_count")); got != 1 {
		t.Fatalf("heard_count = %v, want 1", got)
	}
	if got := asInt(lookupAnyValue(m, "value")); got != 5678 {
		t.Fatalf("value = %v, want 5678", got)
	}
}

func TestPersistDiscoveredInterface_ExistingEntryAllowsStringReceived(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-existing-string-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       5.0,
		"discovery_hash": "aabbcc",
		"value":          1234,
	}

	if err := discovery.persistDiscoveredInterface(info); err != nil {
		t.Fatalf("first persistDiscoveredInterface failed: %v", err)
	}

	if err := discovery.persistDiscoveredInterface(map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       "oops",
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

	if got := asFloat64(lookupAnyValue(m, "discovered")); got != 5.0 {
		t.Fatalf("discovered = %v, want 5.0", got)
	}
	if got := asString(lookupAnyValue(m, "last_heard")); got != "oops" {
		t.Fatalf("last_heard = %q, want %q", got, "oops")
	}
	if got := asInt(lookupAnyValue(m, "heard_count")); got != 1 {
		t.Fatalf("heard_count = %v, want 1", got)
	}
	if got := asInt(lookupAnyValue(m, "value")); got != 5678 {
		t.Fatalf("value = %v, want 5678", got)
	}
}

func TestPersistDiscoveredInterface_ExistingEntryAllowsIntegerDiscoveryHash(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-existing-int-hash-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       5.0,
		"discovery_hash": 123,
		"value":          1234,
	}

	if err := discovery.persistDiscoveredInterface(info); err != nil {
		t.Fatalf("first persistDiscoveredInterface failed: %v", err)
	}

	if err := discovery.persistDiscoveredInterface(map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       1.0,
		"discovery_hash": 123,
		"value":          5678,
	}); err != nil {
		t.Fatalf("second persistDiscoveredInterface failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", "7b.data"))
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

	if got := asFloat64(lookupAnyValue(m, "discovered")); got != 5.0 {
		t.Fatalf("discovered = %v, want 5.0", got)
	}
	if got := asFloat64(lookupAnyValue(m, "last_heard")); got != 1.0 {
		t.Fatalf("last_heard = %v, want 1.0", got)
	}
	if got := asInt(lookupAnyValue(m, "heard_count")); got != 1 {
		t.Fatalf("heard_count = %v, want 1", got)
	}
	if got := asInt(lookupAnyValue(m, "value")); got != 5678 {
		t.Fatalf("value = %v, want 5678", got)
	}
}

func TestPersistDiscoveredInterface_ExistingEntryBoolTrueHeardCountBecomesTwo(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-bool-heard-count-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	filePath := filepath.Join(storagePath, "aabbcc.data")
	if err := os.WriteFile(filePath, mustMsgpackPack(map[string]any{
		"discovered":  5.0,
		"heard_count": true,
	}), 0o644); err != nil {
		t.Fatalf("failed to seed discovery file: %v", err)
	}

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	if err := discovery.persistDiscoveredInterface(map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       1.0,
		"discovery_hash": "aabbcc",
		"value":          5678,
	}); err != nil {
		t.Fatalf("persistDiscoveredInterface failed: %v", err)
	}

	data, err := os.ReadFile(filePath)
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
	if got := asInt(lookupAnyValue(m, "heard_count")); got != 2 {
		t.Fatalf("heard_count = %v, want 2", got)
	}
}

func TestPersistDiscoveredInterface_ExistingEntryFloatHeardCountPreservesFraction(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-float-heard-count-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	filePath := filepath.Join(storagePath, "aabbcc.data")
	if err := os.WriteFile(filePath, mustMsgpackPack(map[string]any{
		"discovered":  5.0,
		"heard_count": 7.5,
	}), 0o644); err != nil {
		t.Fatalf("failed to seed discovery file: %v", err)
	}

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	if err := discovery.persistDiscoveredInterface(map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       1.0,
		"discovery_hash": "aabbcc",
		"value":          5678,
	}); err != nil {
		t.Fatalf("persistDiscoveredInterface failed: %v", err)
	}

	data, err := os.ReadFile(filePath)
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
	got := lookupAnyValue(m, "heard_count")
	if gotFloat, ok := got.(float64); !ok || gotFloat != 8.5 {
		t.Fatalf("heard_count = %#v, want float64(8.5)", got)
	}
}

func TestPersistDiscoveredInterface_ExistingEntryNilDiscoveredUsesIncomingDiscovered(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-nil-discovered-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	filePath := filepath.Join(storagePath, "aabbcc.data")
	if err := os.WriteFile(filePath, mustMsgpackPack(map[string]any{
		"discovered":  nil,
		"heard_count": 2,
	}), 0o644); err != nil {
		t.Fatalf("failed to seed discovery file: %v", err)
	}

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       5.0,
		"discovered":     7.0,
		"discovery_hash": "aabbcc",
		"value":          5678,
	}
	if err := discovery.persistDiscoveredInterface(info); err != nil {
		t.Fatalf("persistDiscoveredInterface failed: %v", err)
	}

	data, err := os.ReadFile(filePath)
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
	if got := asFloat64(lookupAnyValue(m, "discovered")); got != 7.0 {
		t.Fatalf("persisted discovered = %v, want 7.0", got)
	}
	if got := asFloat64(lookupAnyValue(m, "last_heard")); got != 5.0 {
		t.Fatalf("persisted last_heard = %v, want 5.0", got)
	}
	if got := asInt(lookupAnyValue(m, "heard_count")); got != 3 {
		t.Fatalf("persisted heard_count = %v, want 3", got)
	}
	if got := asFloat64(info["discovered"]); got != 7.0 {
		t.Fatalf("info[\"discovered\"] = %v, want 7.0", got)
	}
	if got := asFloat64(info["last_heard"]); got != 5.0 {
		t.Fatalf("info[\"last_heard\"] = %v, want 5.0", got)
	}
	if got := asInt(info["heard_count"]); got != 3 {
		t.Fatalf("info[\"heard_count\"] = %v, want 3", got)
	}
}

func TestPersistDiscoveredInterface_ExistingEntryStringHeardCountTruncatesFile(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-string-heard-count-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	filePath := filepath.Join(storagePath, "aabbcc.data")
	if err := os.WriteFile(filePath, mustMsgpackPack(map[string]any{
		"discovered":  5.0,
		"heard_count": "7",
	}), 0o644); err != nil {
		t.Fatalf("failed to seed discovery file: %v", err)
	}

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)
	if err := discovery.persistDiscoveredInterface(map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       1.0,
		"discovery_hash": "aabbcc",
		"value":          5678,
	}); err == nil {
		t.Fatal("persistDiscoveredInterface() error = nil, want error for string heard_count")
	}

	fi, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("expected truncated persisted discovery file: %v", err)
	}
	if fi.Size() != 0 {
		t.Fatalf("truncated persisted discovery file size = %v, want 0", fi.Size())
	}
}

func TestPersistDiscoveredInterface_CorruptExistingEntryMissingHeardCountFailsClosed(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-persist-corrupt-")
	defer cleanup()

	r := &Reticulum{configDir: tmpDir}
	discovery := NewInterfaceDiscovery(r)

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	filePath := filepath.Join(storagePath, "aabbcc.data")
	original := map[string]any{
		"name":         "Persisted",
		"type":         "TCPServerInterface",
		"discovered":   1234.0,
		"last_heard":   1234.0,
		"received":     1234.0,
		"value":        1,
		"transport":    true,
		"network_id":   "01020304",
		"reachable_on": "127.0.0.1",
		"port":         4242,
	}
	if err := os.WriteFile(filePath, mustMsgpackPack(original), 0o644); err != nil {
		t.Fatalf("failed to seed corrupt discovery file: %v", err)
	}

	info := map[string]any{
		"name":           "Persisted",
		"type":           "TCPServerInterface",
		"received":       1300.0,
		"discovery_hash": "aabbcc",
		"value":          5678,
	}

	if err := discovery.persistDiscoveredInterface(info); err == nil {
		t.Fatal("persistDiscoveredInterface() error = nil, want error for corrupt cached record")
	}

	if _, ok := info["discovered"]; ok {
		t.Fatalf("info[\"discovered\"] unexpectedly set on corrupt-cache error: %v", info["discovered"])
	}
	if _, ok := info["heard_count"]; ok {
		t.Fatalf("info[\"heard_count\"] unexpectedly set on corrupt-cache error: %v", info["heard_count"])
	}
	if _, ok := info["last_heard"]; ok {
		t.Fatalf("info[\"last_heard\"] unexpectedly set on corrupt-cache error: %v", info["last_heard"])
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read discovery file after failed persist: %v", err)
	}
	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("failed to unpack discovery file after failed persist: %v", err)
	}
	m := asAnyMap(unpacked)
	if m == nil {
		t.Fatalf("unexpected persisted discovery type %T", unpacked)
	}
	if got := asFloat64(lookupAnyValue(m, "discovered")); got != 1234.0 {
		t.Fatalf("discovered = %v, want %v", got, 1234.0)
	}
	if _, ok := m["heard_count"]; ok {
		t.Fatalf("heard_count unexpectedly added to corrupt discovery file: %v", m["heard_count"])
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

func TestInterfaceDiscoveryReceiveAndPersistRejectsMissingGeolocationFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload map[any]any
	}{
		{
			name: "missing-latitude",
			payload: map[any]any{
				discoveryFieldInterfaceType: "TCPServerInterface",
				discoveryFieldTransport:     true,
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:          "Broken TCP",
				discoveryFieldReachableOn:   "discovery.example.net",
				discoveryFieldPort:          4242,
				discoveryFieldLongitude:     nil,
				discoveryFieldHeight:        nil,
			},
		},
		{
			name: "missing-longitude",
			payload: map[any]any{
				discoveryFieldInterfaceType: "TCPServerInterface",
				discoveryFieldTransport:     true,
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:          "Broken TCP",
				discoveryFieldReachableOn:   "discovery.example.net",
				discoveryFieldPort:          4242,
				discoveryFieldLatitude:      nil,
				discoveryFieldHeight:        nil,
			},
		},
		{
			name: "missing-height",
			payload: map[any]any{
				discoveryFieldInterfaceType: "TCPServerInterface",
				discoveryFieldTransport:     true,
				discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
				discoveryFieldName:          "Broken TCP",
				discoveryFieldReachableOn:   "discovery.example.net",
				discoveryFieldPort:          4242,
				discoveryFieldLatitude:      nil,
				discoveryFieldLongitude:     nil,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-missing-geo-")
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
			appData := mustDiscoveryAnnounceAppDataRaw(t, tt.payload, 2)

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

func TestInterfaceDiscoveryReceiveAndPersistPlainTCPClientOmitsConfigEntry(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-tcp-client-")
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

	var callbackInfo map[string]any
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackInfo = cloneStringAnyMap(info)
		if err := discovery.persistDiscoveredInterface(info); err != nil {
			t.Fatalf("persist callback failed: %v", err)
		}
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPClientInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Discovered TCP Client",
		discoveryFieldReachableOn:   "tcp-client.example.net",
	}, 2)

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	if got, ok := callbackInfo["config_entry"]; ok {
		t.Fatalf("callback unexpectedly included config_entry %#v", got)
	}
	if got, ok := callbackInfo["reachable_on"]; ok {
		t.Fatalf("callback unexpectedly included reachable_on %#v", got)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", hex.EncodeToString(FullHash([]byte("deadbeefDiscovered TCP Client")))+".data"))
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
	if got, ok := lookupAny(m, "config_entry"); ok {
		t.Fatalf("persisted config_entry unexpectedly present %#v", got)
	}
	if got, ok := lookupAny(m, "reachable_on"); ok {
		t.Fatalf("persisted reachable_on unexpectedly present %#v", got)
	}
}

func TestDiscoveryConfigEntryKeepsEmptyTransportIdentityForBackbone(t *testing.T) {
	t.Parallel()

	connectionType := "BackboneInterface"
	remoteKey := "remote"
	if runtime.GOOS == "windows" {
		connectionType = "TCPClientInterface"
		remoteKey = "target_host"
	}

	got := discoveryConfigEntry(map[string]any{
		"type":         "BackboneInterface",
		"name":         "Discovered TCP",
		"transport_id": "",
		"reachable_on": "discovery.example.net",
		"port":         4242,
	})

	want := "[[Discovered TCP]]\n  type = " + connectionType +
		"\n  enabled = yes\n  " + remoteKey + " = discovery.example.net\n  target_port = 4242" +
		"\n  transport_identity = "
	if got != want {
		t.Fatalf("discoveryConfigEntry() = %q, want %q", got, want)
	}
}

func TestInterfaceDiscoveryReceiveAndPersistPreservesRawTransportValue(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-transport-")
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

	var callbackInfo map[string]any
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackInfo = cloneStringAnyMap(info)
		if err := discovery.persistDiscoveredInterface(info); err != nil {
			t.Fatalf("persist callback failed: %v", err)
		}
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     "yes",
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "String Transport",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
	}, 2)

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	if got := callbackInfo["transport"]; got != "yes" {
		t.Fatalf("callback transport = %#v, want %q", got, "yes")
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", hex.EncodeToString(FullHash([]byte("deadbeefString Transport")))+".data"))
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
	if got := lookupAnyValue(m, "transport"); got != "yes" {
		t.Fatalf("persisted transport = %#v, want %q", got, "yes")
	}
}

func TestInterfaceDiscoveryReceiveAndPersistPreservesEmptyIFACFields(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-ifac-empty-")
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

	var callbackInfo map[string]any
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackInfo = cloneStringAnyMap(info)
		if err := discovery.persistDiscoveredInterface(info); err != nil {
			t.Fatalf("persist callback failed: %v", err)
		}
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Empty IFAC",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
		discoveryFieldIFACNetname:   "",
		discoveryFieldIFACNetkey:    "",
	}, 2)

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	if got, ok := callbackInfo["ifac_netname"]; !ok || got != "" {
		t.Fatalf("callback ifac_netname = %#v, present=%v, want empty string present", got, ok)
	}
	if got, ok := callbackInfo["ifac_netkey"]; !ok || got != "" {
		t.Fatalf("callback ifac_netkey = %#v, present=%v, want empty string present", got, ok)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", hex.EncodeToString(FullHash([]byte("deadbeefEmpty IFAC")))+".data"))
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
	if got, ok := lookupAny(m, "ifac_netname"); !ok || got != "" {
		t.Fatalf("persisted ifac_netname = %#v, present=%v, want empty string present", got, ok)
	}
	if got, ok := lookupAny(m, "ifac_netkey"); !ok || got != "" {
		t.Fatalf("persisted ifac_netkey = %#v, present=%v, want empty string present", got, ok)
	}
}

func TestInterfaceDiscoveryReceiveAndPersistPreservesRawPortValue(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-port-")
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

	var callbackInfo map[string]any
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackInfo = cloneStringAnyMap(info)
		if err := discovery.persistDiscoveredInterface(info); err != nil {
			t.Fatalf("persist callback failed: %v", err)
		}
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Bool Port",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          true,
	}, 2)

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	if got := callbackInfo["port"]; got != true {
		t.Fatalf("callback port = %#v, want true", got)
	}
	if got := callbackInfo["config_entry"]; !strings.Contains(asString(got), "target_port = True") {
		t.Fatalf("config_entry = %q, want Python-shaped bool port", asString(got))
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", hex.EncodeToString(FullHash([]byte("deadbeefBool Port")))+".data"))
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
	if got := lookupAnyValue(m, "port"); got != true {
		t.Fatalf("persisted port = %#v, want true", got)
	}
}

func TestInterfaceDiscoveryReceiveAndPersistFormatsIterablePortLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-iterable-port-")
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

	var callbackInfo map[string]any
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackInfo = cloneStringAnyMap(info)
		if err := discovery.persistDiscoveredInterface(info); err != nil {
			t.Fatalf("persist callback failed: %v", err)
		}
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Iterable Port",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          []any{1, 2},
	}, 2)

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	if callbackInfo == nil {
		t.Fatal("expected iterable port discovery announce to invoke callback")
	}
	if got := callbackInfo["config_entry"]; !strings.Contains(asString(got), "target_port = [1, 2]") {
		t.Fatalf("config_entry = %q, want Python-shaped iterable port", asString(got))
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", hex.EncodeToString(FullHash([]byte("deadbeefIterable Port")))+".data"))
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
	if got := lookupAnyValue(m, "port"); !reflect.DeepEqual(got, []any{int64(1), int64(2)}) {
		t.Fatalf("persisted port = %#v, want []any{1, 2}", got)
	}
}

func TestInterfaceDiscoveryReceiveAndPersistFormatsMapPortLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-map-port-")
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

	var callbackInfo map[string]any
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackInfo = cloneStringAnyMap(info)
		if err := discovery.persistDiscoveredInterface(info); err != nil {
			t.Fatalf("persist callback failed: %v", err)
		}
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Map Port",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          map[any]any{"a": 1},
	}, 2)

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	if callbackInfo == nil {
		t.Fatal("expected map port discovery announce to invoke callback")
	}
	if got := callbackInfo["config_entry"]; !strings.Contains(asString(got), "target_port = {'a': 1}") {
		t.Fatalf("config_entry = %q, want Python-shaped map port", asString(got))
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", hex.EncodeToString(FullHash([]byte("deadbeefMap Port")))+".data"))
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
	if got := lookupAnyValue(m, "port"); !reflect.DeepEqual(got, map[any]any{"a": int64(1)}) {
		t.Fatalf("persisted port = %#v, want map[any]any{\"a\": 1}", got)
	}
}

func TestInterfaceDiscoveryReceiveAndPersistFormatsWholeFloatPortLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-float-port-")
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

	var callbackInfo map[string]any
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackInfo = cloneStringAnyMap(info)
		if err := discovery.persistDiscoveredInterface(info); err != nil {
			t.Fatalf("persist callback failed: %v", err)
		}
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Float Port",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          1.0,
	}, 2)

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	if callbackInfo == nil {
		t.Fatal("expected float port discovery announce to invoke callback")
	}
	if got := callbackInfo["config_entry"]; !strings.Contains(asString(got), "target_port = 1.0") {
		t.Fatalf("config_entry = %q, want Python-shaped whole float port", asString(got))
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", hex.EncodeToString(FullHash([]byte("deadbeefFloat Port")))+".data"))
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
	if got := lookupAnyValue(m, "port"); got != 1.0 {
		t.Fatalf("persisted port = %#v, want 1.0", got)
	}
}

func TestInterfaceDiscoveryReceiveAndPersistAcceptsIntegerReachableOn(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-reachable-int-")
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

	var callbackInfo map[string]any
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackInfo = cloneStringAnyMap(info)
		if err := discovery.persistDiscoveredInterface(info); err != nil {
			t.Fatalf("persist callback failed: %v", err)
		}
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppDataRaw(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Integer Reachable",
		discoveryFieldReachableOn:   1,
		discoveryFieldPort:          4242,
		discoveryFieldLatitude:      nil,
		discoveryFieldLongitude:     nil,
		discoveryFieldHeight:        nil,
	}, 2)

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	if got, ok := numericIntValue(callbackInfo["reachable_on"]); !ok || got != 1 {
		t.Fatalf("callback reachable_on = %#v, want numeric 1", callbackInfo["reachable_on"])
	}
	if got := callbackInfo["config_entry"]; !strings.Contains(asString(got), "remote = 1") {
		t.Fatalf("config_entry = %q, want integer reachable_on", asString(got))
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", hex.EncodeToString(FullHash([]byte("deadbeefInteger Reachable")))+".data"))
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
	if got, ok := numericIntValue(lookupAnyValue(m, "reachable_on")); !ok || got != 1 {
		t.Fatalf("persisted reachable_on = %#v, want numeric 1", lookupAnyValue(m, "reachable_on"))
	}
}

func TestInterfaceDiscoveryReceiveAndPersistAcceptsIntegerTransportID(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-transport-id-int-")
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

	var callbackInfo map[string]any
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackInfo = cloneStringAnyMap(info)
		if err := discovery.persistDiscoveredInterface(info); err != nil {
			t.Fatalf("persist callback failed: %v", err)
		}
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   123,
		discoveryFieldName:          "Integer Transport ID",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
	}, 2)

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	if got := callbackInfo["transport_id"]; got != "7b" {
		t.Fatalf("callback transport_id = %#v, want %q", got, "7b")
	}
	if got := callbackInfo["config_entry"]; !strings.Contains(asString(got), "transport_identity = 7b") {
		t.Fatalf("config_entry = %q, want integer transport_id hex", asString(got))
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", hex.EncodeToString(FullHash([]byte("7bInteger Transport ID")))+".data"))
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
	if got := lookupAnyValue(m, "transport_id"); got != "7b" {
		t.Fatalf("persisted transport_id = %#v, want %q", got, "7b")
	}
}

func TestInterfaceDiscoveryReceiveAndPersistAcceptsIterableTransportID(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-iterable-transport-id-")
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

	var callbackInfo map[string]any
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackInfo = cloneStringAnyMap(info)
		if err := discovery.persistDiscoveredInterface(info); err != nil {
			t.Fatalf("persist callback failed: %v", err)
		}
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []any{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Iterable Transport ID",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
	}, 2)

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	if callbackInfo == nil {
		t.Fatal("expected iterable transport_id discovery announce to invoke callback")
	}
	if got := callbackInfo["transport_id"]; got != "deadbeef" {
		t.Fatalf("callback transport_id = %#v, want %q", got, "deadbeef")
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", hex.EncodeToString(FullHash([]byte("deadbeefIterable Transport ID")))+".data"))
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
	if got := lookupAnyValue(m, "transport_id"); got != "deadbeef" {
		t.Fatalf("persisted transport_id = %#v, want %q", got, "deadbeef")
	}
}

func TestInterfaceDiscoveryReceiveAndPersistRejectsBytesName(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-bytes-name-")
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
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          []byte("bytes-name"),
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
	}, 2)

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	if callbackCalled {
		t.Fatal("expected bytes name discovery announce to be ignored")
	}

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("expected no persisted discovered interfaces, got %v", len(discovered))
	}
}

func TestInterfaceDiscoveryReceiveAndPersistRejectsNilAnnouncedIdentity(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-nil-identity-")
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

	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Nil Identity",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
	}, 2)

	handler.receivedAnnounce(destinationHash, nil, appData)

	if callbackCalled {
		t.Fatal("expected nil announced identity discovery announce to be ignored")
	}

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces failed: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("expected no persisted discovered interfaces, got %v", len(discovered))
	}
}

func TestInterfaceDiscoveryReceiveAndPersistIgnoresFieldsFromOtherInterfaceTypes(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-receive-extra-fields-")
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

	var callbackInfo map[string]any
	handler := NewInterfaceAnnounceHandler(r, 2, func(info map[string]any) {
		callbackInfo = cloneStringAnyMap(info)
		if err := discovery.persistDiscoveredInterface(info); err != nil {
			t.Fatalf("persist callback failed: %v", err)
		}
	})

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Extra Fields TCP",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
		discoveryFieldFrequency:     433920000,
		discoveryFieldBandwidth:     12500,
		discoveryFieldModulation:    "gmsk",
	}, 2)

	handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	for _, key := range []string{"frequency", "bandwidth", "modulation", "sf", "cr", "channel"} {
		if got, ok := callbackInfo[key]; ok {
			t.Fatalf("callback %q unexpectedly preserved extra field %#v", key, got)
		}
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", hex.EncodeToString(FullHash([]byte("deadbeefExtra Fields TCP")))+".data"))
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
	for _, key := range []string{"frequency", "bandwidth", "modulation", "sf", "cr", "channel"} {
		if got, ok := lookupAny(m, key); ok {
			t.Fatalf("persisted %q unexpectedly preserved extra field %#v", key, got)
		}
	}
}

func mustDiscoveryAnnounceAppData(t *testing.T, payload map[any]any, targetCost int) []byte {
	t.Helper()

	payloadWithDefaults := make(map[any]any, len(payload)+3)
	for k, v := range payload {
		payloadWithDefaults[k] = v
	}
	for _, field := range []int{discoveryFieldLatitude, discoveryFieldLongitude, discoveryFieldHeight} {
		if _, ok := payloadWithDefaults[field]; !ok {
			payloadWithDefaults[field] = nil
		}
	}

	return mustDiscoveryAnnounceAppDataRaw(t, payloadWithDefaults, targetCost)
}

func mustDiscoveryAnnounceAppDataRaw(t *testing.T, payload map[any]any, targetCost int) []byte {
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
		"value":        1,
		"config_entry": "[[Cached Backbone]]",
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

func TestInterfaceDiscoveryConnectDiscoveredMissingConfigEntrySkipsAutoconnect(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-connect-missing-config-entry-")
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
		if err == nil {
			accepted <- conn
		}
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
		"value":        1,
		"reachable_on": "127.0.0.1",
		"port":         port,
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
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)

	discovery.connectDiscovered()

	select {
	case conn := <-accepted:
		_ = conn.Close()
		t.Fatal("unexpected auto-connect without config_entry")
	case <-time.After(300 * time.Millisecond):
	}

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected no auto-connected interfaces, got %v", got)
	}
	if _, err := os.Stat(filepath.Join(storagePath, "cached-backbone.data")); err != nil {
		t.Fatalf("expected cached discovery file to remain on disk: %v", err)
	}
}

func TestInterfaceDiscoveryConnectDiscoveredBytesTypeSkipsAutoconnectLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-connect-bytes-type-")
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
		if err == nil {
			accepted <- conn
		}
	}()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "cached-backbone-bytes-type.data"), mustMsgpackPack(map[string]any{
		"name":         "Cached Bytes Type",
		"type":         []byte("BackboneInterface"),
		"transport":    true,
		"last_heard":   now - 60,
		"discovered":   now - 120,
		"value":        1,
		"config_entry": "[[Cached Bytes Type]]",
		"reachable_on": "127.0.0.1",
		"port":         port,
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
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if got := len(discovered); got != 1 {
		t.Fatalf("expected 1 cached discovered interface, got %v", got)
	}
	if got := discovered[0].Type; got != "b'BackboneInterface'" {
		t.Fatalf("discovered[0].Type = %q, want %q", got, "b'BackboneInterface'")
	}

	discovery.connectDiscovered()

	select {
	case conn := <-accepted:
		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Errorf("accepted conn.Close() error = %v", err)
			}
		})
		t.Fatal("unexpected cached auto-connect for bytes-typed discovery type")
	case <-time.After(200 * time.Millisecond):
	}

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected bytes-typed cached reconnect to keep 0 interfaces, got %v", got)
	}
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
		"value":        1,
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

func TestInterfaceDiscoveryAutoconnectPassesRawEndpointValuesToConstructorSeam(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	discovery := NewInterfaceDiscovery(&Reticulum{
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	})

	var (
		calls int
		got   discoveryBackboneClientConfig
	)
	discovery.backboneFactory = func(config discoveryBackboneClientConfig, handler interfaces.InboundHandler) (interfaces.Interface, error) {
		calls++
		got = config
		if handler == nil {
			t.Fatal("backboneFactory handler = nil, want non-nil")
		}
		return newBootstrapConstructorTestInterface(config.Name, "BackboneClientInterface"), nil
	}

	info, ok := mapToDiscoveredInterface(map[string]any{
		"name":         "Raw Port Backbone",
		"type":         "BackboneInterface",
		"config_entry": "[[raw-port]]",
		"reachable_on": []byte("127.0.0.1"),
		"port":         "4242",
	})
	if !ok {
		t.Fatal("mapToDiscoveredInterface() = false, want true")
	}

	if err := discovery.autoconnect(info); err != nil {
		t.Fatalf("autoconnect() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("backboneFactory calls = %v, want 1", calls)
	}
	if got.Name != "Raw Port Backbone" {
		t.Fatalf("backboneFactory Name = %q, want %q", got.Name, "Raw Port Backbone")
	}
	targetHost, ok := got.TargetHost.([]byte)
	if !ok {
		t.Fatalf("backboneFactory TargetHost type = %T, want []byte", got.TargetHost)
	}
	if string(targetHost) != "127.0.0.1" {
		t.Fatalf("backboneFactory TargetHost = %q, want %q", string(targetHost), "127.0.0.1")
	}
	targetPort, ok := got.TargetPort.(string)
	if !ok {
		t.Fatalf("backboneFactory TargetPort type = %T, want string", got.TargetPort)
	}
	if targetPort != "4242" {
		t.Fatalf("backboneFactory TargetPort = %q, want %q", targetPort, "4242")
	}
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", got)
	}
}

func TestInterfaceDiscoveryAutoconnectPythonPortCoercion(t *testing.T) {
	cases := []struct {
		name     string
		port     any
		wantPort int
	}{
		{name: "bool true", port: true, wantPort: 1},
		{name: "bool false", port: false, wantPort: 0},
		{name: "float", port: 1.5, wantPort: 1},
		{name: "string", port: "1", wantPort: 1},
		{name: "bytes", port: []byte("1"), wantPort: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logger := NewLogger()
			ts := NewTransportSystem(logger)
			discovery := NewInterfaceDiscovery(&Reticulum{
				transport:           ts,
				logger:              logger,
				autoconnectDiscover: 1,
			})

			info, ok := mapToDiscoveredInterface(map[string]any{
				"name":         "Python Port " + tc.name,
				"type":         "BackboneInterface",
				"config_entry": "[[python-port]]",
				"reachable_on": "127.0.0.1",
				"port":         tc.port,
			})
			if !ok {
				t.Fatal("mapToDiscoveredInterface() = false, want true")
			}

			if err := discovery.autoconnect(info); err != nil {
				t.Fatalf("autoconnect() error = %v", err)
			}
			if got := len(ts.GetInterfaces()); got != 1 {
				t.Fatalf("expected 1 auto-connected interface, got %v", got)
			}

			iface := ts.GetInterfaces()[0]
			t.Cleanup(func() {
				if err := iface.Detach(); err != nil {
					t.Errorf("Detach() error = %v", err)
				}
			})

			meta, ok := iface.(interface{ TargetPort() int })
			if !ok {
				t.Fatalf("auto-connected interface %T does not expose TargetPort()", iface)
			}
			if got := meta.TargetPort(); got != tc.wantPort {
				t.Fatalf("TargetPort() = %v, want %v", got, tc.wantPort)
			}
		})
	}
}

func TestInterfaceDiscoveryAutoconnectNilPortLogsPythonTypeError(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	logger.SetLogDest(LogCallback)

	var logs []string
	logger.SetLogCallback(func(msg string) {
		logs = append(logs, msg)
	})

	ts := NewTransportSystem(logger)
	discovery := NewInterfaceDiscovery(&Reticulum{
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	})

	info, ok := mapToDiscoveredInterface(map[string]any{
		"name":         "Nil Port Backbone",
		"type":         "BackboneInterface",
		"config_entry": "[[nil-port]]",
		"reachable_on": "127.0.0.1",
		"port":         nil,
	})
	if !ok {
		t.Fatal("mapToDiscoveredInterface() = false, want true")
	}

	if err := discovery.autoconnect(info); err != nil {
		t.Fatalf("autoconnect() error = %v, want nil after Python-style logging", err)
	}
	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected no auto-connected interfaces, got %v", got)
	}

	want := "error while auto-connecting discovered interface: int() argument must be a string, a bytes-like object or a real number, not 'NoneType'"
	for _, msg := range logs {
		if strings.Contains(msg, want) {
			return
		}
	}
	t.Fatalf("expected log containing %q, got %v", want, logs)
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

func TestInterfaceDiscoveryInterfaceExistsMatchesPythonBoolPortAutoconnectHash(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	ts.RegisterInterface(&autoconnectCountTestInterface{
		BaseInterface:   interfaces.NewBaseInterface("Existing Bool Port", interfaces.ModeFull, 1000),
		autoconnectHash: FullHash([]byte("127.0.0.1:True")),
	})

	info, ok := mapToDiscoveredInterface(map[string]any{
		"name":         "Bool Port Backbone",
		"type":         "BackboneInterface",
		"config_entry": "[[bool-port]]",
		"reachable_on": "127.0.0.1",
		"port":         true,
	})
	if !ok {
		t.Fatal("mapToDiscoveredInterface() = false, want true")
	}

	if !discovery.interfaceExists(info) {
		t.Fatal("expected live bool-port discovery info to match Python-shaped autoconnect hash")
	}
}

func TestInterfaceDiscoveryInterfaceExistsPresentNilPortDoesNotMatchExistingTargetPort(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	ts.RegisterInterface(newTargetHostTestInterface("Existing Backbone", "BackboneClientInterface", "127.0.0.1", 1))

	info, ok := mapToDiscoveredInterface(map[string]any{
		"name":         "Nil Port Backbone",
		"type":         "BackboneInterface",
		"config_entry": "[[nil-port]]",
		"reachable_on": "127.0.0.1",
		"port":         nil,
	})
	if !ok {
		t.Fatal("mapToDiscoveredInterface() = false, want true")
	}

	if discovery.interfaceExists(info) {
		t.Fatal("expected present nil port not to match an existing concrete target port")
	}
}

func TestInterfaceDiscoveryInterfaceExistsStringPortDoesNotMatchExistingTargetPort(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	discovery := NewInterfaceDiscovery(&Reticulum{
		transport: ts,
		logger:    logger,
	})

	ts.RegisterInterface(newTargetHostTestInterface("Existing Backbone", "BackboneClientInterface", "127.0.0.1", 1))

	info, ok := mapToDiscoveredInterface(map[string]any{
		"name":         "String Port Backbone",
		"type":         "BackboneInterface",
		"config_entry": "[[string-port]]",
		"reachable_on": "127.0.0.1",
		"port":         "1",
	})
	if !ok {
		t.Fatal("mapToDiscoveredInterface() = false, want true")
	}

	if discovery.interfaceExists(info) {
		t.Fatal("expected present string port not to match an existing concrete target port")
	}
}

func TestInterfaceDiscoveryConnectDiscoveredPassesRawEndpointValuesToConstructorSeam(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-connect-raw-constructor-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "cached-backbone.data"), mustMsgpackPack(map[string]any{
		"name":         "Cached Raw Backbone",
		"type":         "BackboneInterface",
		"transport":    true,
		"last_heard":   now - 60,
		"discovered":   now - 120,
		"value":        1,
		"config_entry": "[[cached-raw-port]]",
		"reachable_on": "127.0.0.1",
		"port":         nil,
	}), 0o644); err != nil {
		t.Fatalf("failed to write cached discovery file: %v", err)
	}

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	discovery := NewInterfaceDiscovery(&Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	})

	var (
		calls int
		got   discoveryBackboneClientConfig
	)
	discovery.backboneFactory = func(config discoveryBackboneClientConfig, handler interfaces.InboundHandler) (interfaces.Interface, error) {
		calls++
		got = config
		if handler == nil {
			t.Fatal("backboneFactory handler = nil, want non-nil")
		}
		return newBootstrapConstructorTestInterface(config.Name, "BackboneClientInterface"), nil
	}

	discovery.connectDiscovered()

	if calls != 1 {
		t.Fatalf("backboneFactory calls = %v, want 1", calls)
	}
	if got.Name != "Cached Raw Backbone" {
		t.Fatalf("backboneFactory Name = %q, want %q", got.Name, "Cached Raw Backbone")
	}
	targetHost, ok := got.TargetHost.(string)
	if !ok {
		t.Fatalf("backboneFactory TargetHost type = %T, want string", got.TargetHost)
	}
	if targetHost != "127.0.0.1" {
		t.Fatalf("backboneFactory TargetHost = %q, want %q", targetHost, "127.0.0.1")
	}
	if got.TargetPort != nil {
		t.Fatalf("backboneFactory TargetPort = %T(%v), want nil", got.TargetPort, got.TargetPort)
	}
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 cached auto-connected interface, got %v", got)
	}
	if !discovery.initialAutoconnectRan {
		t.Fatal("expected initialAutoconnectRan after cached autoconnect")
	}
}

func TestInterfaceDiscoveryConnectDiscoveredNilPortLogsPythonTypeError(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-connect-nil-port-")
	defer cleanup()

	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "cached-backbone.data"), mustMsgpackPack(map[string]any{
		"name":         "Cached Nil Port Backbone",
		"type":         "BackboneInterface",
		"transport":    true,
		"last_heard":   now - 60,
		"discovered":   now - 120,
		"value":        1,
		"config_entry": "[[cached-nil-port]]",
		"reachable_on": "127.0.0.1",
		"port":         nil,
	}), 0o644); err != nil {
		t.Fatalf("failed to write cached discovery file: %v", err)
	}

	logger := NewLogger()
	logger.SetLogDest(LogCallback)

	var logs []string
	logger.SetLogCallback(func(msg string) {
		logs = append(logs, msg)
	})

	ts := NewTransportSystem(logger)
	discovery := NewInterfaceDiscovery(&Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	})

	discovery.connectDiscovered()

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected no cached auto-connected interfaces, got %v", got)
	}
	if !discovery.initialAutoconnectRan {
		t.Fatal("expected initialAutoconnectRan after cached discovery pass")
	}

	want := "error while auto-connecting discovered interface: int() argument must be a string, a bytes-like object or a real number, not 'NoneType'"
	for _, msg := range logs {
		if strings.Contains(msg, want) {
			return
		}
	}
	t.Fatalf("expected log containing %q, got %v", want, logs)
}

func TestInterfaceDiscoveryStartCallbackBytesReachableOnDoesNotDeduplicateAgainstStringHost(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-callback-bytes-reachable-on-")
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
		if err == nil {
			accepted <- conn
		}
	}()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	port := listener.Addr().(*net.TCPAddr).Port
	ts.RegisterInterface(newTargetHostTestInterface("Existing Backbone", "BackboneClientInterface", "127.0.0.1", port))

	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 2,
	}
	discovery := NewInterfaceDiscovery(r)
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	discovery.handler.callback(map[string]any{
		"name":           "Bytes ReachableOn",
		"value":          7,
		"type":           "BackboneInterface",
		"discovery_hash": []byte{0xaa, 0xd2},
		"config_entry":   "[[bytes-reachable-on]]",
		"hops":           1,
		"received":       1234.0,
		"reachable_on":   []byte("127.0.0.1"),
		"port":           port,
		"network_id":     "01020304",
	})

	var acceptedConn net.Conn
	select {
	case acceptedConn = <-accepted:
		t.Cleanup(func() {
			if err := acceptedConn.Close(); err != nil {
				t.Errorf("acceptedConn.Close() error = %v", err)
			}
		})
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback autoconnect with bytes reachable_on")
	}

	if got := len(ts.GetInterfaces()); got != 2 {
		t.Fatalf("expected existing interface plus new auto-connected interface, got %v", got)
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

func TestInterfaceDiscoveryStartCallbackReceivesPersistedMetadata(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-live-callback-meta-")
	defer cleanup()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	destinationHash := []byte("callback-meta-destination")
	ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 3, Expires: time.Now().Add(time.Hour)}

	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)

	type callbackResult struct {
		received   float64
		discovered float64
		lastHeard  float64
		heardCount int
	}
	callbackCh := make(chan callbackResult, 2)
	discovery.SetDiscoveryCallback(func(info map[string]any) {
		callbackCh <- callbackResult{
			received:   asFloat64(info["received"]),
			discovered: asFloat64(info["discovered"]),
			lastHeard:  asFloat64(info["last_heard"]),
			heardCount: asInt(info["heard_count"]),
		}
	})
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	sourceIdentity := mustTestNewIdentity(t, true)
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   []byte{0xde, 0xad, 0xbe, 0xef},
		discoveryFieldName:          "Callback TCP Server",
		discoveryFieldReachableOn:   "127.0.0.1",
		discoveryFieldPort:          4242,
	}, 2)

	discovery.handler.receivedAnnounce(destinationHash, sourceIdentity, appData)
	discovery.handler.receivedAnnounce(destinationHash, sourceIdentity, appData)

	var first, second callbackResult
	select {
	case first = <-callbackCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first discovery callback")
	}
	select {
	case second = <-callbackCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for second discovery callback")
	}

	if first.discovered != first.received {
		t.Fatalf("first callback discovered = %v, want received %v", first.discovered, first.received)
	}
	if first.lastHeard != first.received {
		t.Fatalf("first callback last_heard = %v, want received %v", first.lastHeard, first.received)
	}
	if first.heardCount != 0 {
		t.Fatalf("first callback heard_count = %v, want 0", first.heardCount)
	}

	if second.discovered != first.received {
		t.Fatalf("second callback discovered = %v, want first received %v", second.discovered, first.received)
	}
	if second.lastHeard != second.received {
		t.Fatalf("second callback last_heard = %v, want received %v", second.lastHeard, second.received)
	}
	if second.heardCount != 1 {
		t.Fatalf("second callback heard_count = %v, want 1", second.heardCount)
	}
}

func TestInterfaceDiscoveryStartLogsDiscoveredInterfaceLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-log-python-")
	defer cleanup()

	logger := NewLogger()
	logger.SetLogLevel(LogDebug)
	logger.SetLogDest(LogCallback)

	var logs []string
	logger.SetLogCallback(func(msg string) {
		logs = append(logs, msg)
	})

	ts := NewTransportSystem(logger)
	destinationHash := []byte("logged-discovery-destination")
	ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 1, Expires: time.Now().Add(time.Hour)}

	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 0,
	}
	discovery := NewInterfaceDiscovery(r)
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	discovery.handler.callback(map[string]any{
		"name":           "Logged Backbone",
		"value":          7,
		"type":           "BackboneInterface",
		"discovery_hash": []byte{0xde, 0xac},
		"hops":           1,
		"received":       1234.0,
		"reachable_on":   "127.0.0.1",
		"port":           4242,
	})

	want := "Discovered BackboneInterface 1 hop away with stamp value 7: Logged Backbone"
	for _, msg := range logs {
		if strings.Contains(msg, want) {
			return
		}
	}
	t.Fatalf("expected log containing %q, got %v", want, logs)
}

func TestInterfaceDiscoveryStartLogsPluralHopsLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-log-python-plural-")
	defer cleanup()

	logger := NewLogger()
	logger.SetLogLevel(LogDebug)
	logger.SetLogDest(LogCallback)

	var logs []string
	logger.SetLogCallback(func(msg string) {
		logs = append(logs, msg)
	})

	ts := NewTransportSystem(logger)
	destinationHash := []byte("logged-discovery-plural-destination")
	ts.pathTable[string(destinationHash)] = &PathEntry{Hops: 3, Expires: time.Now().Add(time.Hour)}

	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 0,
	}
	discovery := NewInterfaceDiscovery(r)
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	discovery.handler.callback(map[string]any{
		"name":           []byte("Logged Bytes Backbone"),
		"value":          9,
		"type":           []byte("BackboneInterface"),
		"discovery_hash": []byte{0xde, 0xad},
		"hops":           3,
		"received":       1234.0,
		"reachable_on":   "127.0.0.1",
		"port":           4242,
	})

	want := "Discovered b'BackboneInterface' 3 hops away with stamp value 9: b'Logged Bytes Backbone'"
	for _, msg := range logs {
		if strings.Contains(msg, want) {
			return
		}
	}
	t.Fatalf("expected log containing %q, got %v", want, logs)
}

func TestInterfaceDiscoveryStartCallbackMissingValueFailsBeforePersist(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-start-missing-value-")
	defer cleanup()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)

	callbackCalled := false
	discovery.SetDiscoveryCallback(func(map[string]any) {
		callbackCalled = true
	})
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	info := map[string]any{
		"name":           "Missing Value TCP",
		"type":           "TCPServerInterface",
		"discovery_hash": "aabbccdd",
		"received":       1234.0,
		"hops":           1,
	}
	discovery.handler.callback(info)

	if callbackCalled {
		t.Fatal("expected external discovery callback not to run for malformed discovered info")
	}
	if _, ok := info["discovered"]; ok {
		t.Fatalf("info[\"discovered\"] unexpectedly set: %v", info["discovered"])
	}
	if _, ok := info["last_heard"]; ok {
		t.Fatalf("info[\"last_heard\"] unexpectedly set: %v", info["last_heard"])
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "discovery", "interfaces", "aabbccdd.data")); !os.IsNotExist(err) {
		t.Fatalf("expected malformed discovered info not to be persisted, stat err=%v", err)
	}
}

func TestInterfaceDiscoveryStartCallbackStringDiscoveryHashFailsBeforePersist(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-start-string-hash-")
	defer cleanup()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)

	callbackCalled := false
	discovery.SetDiscoveryCallback(func(map[string]any) {
		callbackCalled = true
	})
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	info := map[string]any{
		"name":           "String Hash TCP",
		"type":           "TCPServerInterface",
		"discovery_hash": "aabbccdd",
		"received":       1234.0,
		"hops":           1,
		"value":          12,
	}
	discovery.handler.callback(info)

	if callbackCalled {
		t.Fatal("expected external discovery callback not to run for string discovery hash")
	}
	if _, ok := info["discovered"]; ok {
		t.Fatalf("info[\"discovered\"] unexpectedly set: %v", info["discovered"])
	}
	if _, ok := info["last_heard"]; ok {
		t.Fatalf("info[\"last_heard\"] unexpectedly set: %v", info["last_heard"])
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "discovery", "interfaces", "aabbccdd.data")); !os.IsNotExist(err) {
		t.Fatalf("expected string discovery hash not to be persisted, stat err=%v", err)
	}
}

func TestInterfaceDiscoveryStartCallbackBoolDiscoveryHashPersists(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-start-bool-hash-")
	defer cleanup()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	r := &Reticulum{
		configDir: tmpDir,
		transport: ts,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	callbackCalled := false
	discovery.SetDiscoveryCallback(func(info map[string]any) {
		callbackCalled = true
	})
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	info := map[string]any{
		"name":           "Bool Hash TCP",
		"type":           "TCPServerInterface",
		"discovery_hash": true,
		"received":       1234.0,
		"hops":           1,
		"value":          12,
	}
	discovery.handler.callback(info)

	if !callbackCalled {
		t.Fatal("expected external discovery callback to run for bool discovery hash")
	}
	if got := asFloat64(info["discovered"]); got != 1234.0 {
		t.Fatalf("info[\"discovered\"] = %v, want 1234.0", got)
	}
	if got := asFloat64(info["last_heard"]); got != 1234.0 {
		t.Fatalf("info[\"last_heard\"] = %v, want 1234.0", got)
	}
	if got := asInt(info["heard_count"]); got != 0 {
		t.Fatalf("info[\"heard_count\"] = %v, want 0", got)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "discovery", "interfaces", "01.data")); err != nil {
		t.Fatalf("expected bool discovery hash to be persisted: %v", err)
	}
}

func TestInterfaceDiscoveryStartCallbackIterableDiscoveryHashPersists(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-start-iterable-hash-")
	defer cleanup()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	r := &Reticulum{
		configDir: tmpDir,
		transport: ts,
		logger:    logger,
	}
	discovery := NewInterfaceDiscovery(r)

	callbackCalled := false
	discovery.SetDiscoveryCallback(func(map[string]any) {
		callbackCalled = true
	})
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	info := map[string]any{
		"name":           "Iterable Hash TCP",
		"type":           "TCPServerInterface",
		"discovery_hash": []any{0xde, 0xad, 0xbe, 0xef},
		"received":       1234.0,
		"hops":           1,
		"value":          12,
	}
	discovery.handler.callback(info)

	if !callbackCalled {
		t.Fatal("expected external discovery callback to run for iterable discovery hash")
	}
	if got := asFloat64(info["discovered"]); got != 1234.0 {
		t.Fatalf("info[\"discovered\"] = %v, want 1234.0", got)
	}
	if got := asFloat64(info["last_heard"]); got != 1234.0 {
		t.Fatalf("info[\"last_heard\"] = %v, want 1234.0", got)
	}
	if got := asInt(info["heard_count"]); got != 0 {
		t.Fatalf("info[\"heard_count\"] = %v, want 0", got)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "discovery", "interfaces", "deadbeef.data")); err != nil {
		t.Fatalf("expected iterable discovery hash to be persisted: %v", err)
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

func TestInterfaceDiscoveryStartCallbackAutoconnectFormatsBytesNameLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-callback-bytes-name-")
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
		if err == nil {
			accepted <- conn
		}
	}()

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

	port := listener.Addr().(*net.TCPAddr).Port
	discovery.handler.callback(map[string]any{
		"name":           []byte("BytesName"),
		"value":          7,
		"type":           "BackboneInterface",
		"discovery_hash": []byte{0xaa, 0xbb},
		"config_entry":   "[[BytesName]]",
		"hops":           1,
		"received":       1234.0,
		"reachable_on":   "127.0.0.1",
		"port":           port,
		"network_id":     "01020304",
	})

	select {
	case conn := <-accepted:
		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Errorf("accepted conn.Close() error = %v", err)
			}
		})
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback autoconnect with bytes name")
	}

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", got)
	}
	if got := ts.GetInterfaces()[0].Name(); got != "b'BytesName'" {
		t.Fatalf("Name() = %q, want %q", got, "b'BytesName'")
	}
}

func TestInterfaceDiscoveryStartCallbackAutoconnectFormatsListNameLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-callback-list-name-")
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
		if err == nil {
			accepted <- conn
		}
	}()

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

	port := listener.Addr().(*net.TCPAddr).Port
	discovery.handler.callback(map[string]any{
		"name":           []any{"list", "name"},
		"value":          7,
		"type":           "BackboneInterface",
		"discovery_hash": []byte{0xaa, 0xcc},
		"config_entry":   "[[list-name]]",
		"hops":           1,
		"received":       1234.0,
		"reachable_on":   "127.0.0.1",
		"port":           port,
		"network_id":     "01020304",
	})

	select {
	case conn := <-accepted:
		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Errorf("accepted conn.Close() error = %v", err)
			}
		})
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback autoconnect with list name")
	}

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", got)
	}
	if got := ts.GetInterfaces()[0].Name(); got != "['list', 'name']" {
		t.Fatalf("Name() = %q, want %q", got, "['list', 'name']")
	}
}

func TestInterfaceDiscoveryStartCallbackAutoconnectFormatsNilNameLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-callback-nil-name-")
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
		if err == nil {
			accepted <- conn
		}
	}()

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

	port := listener.Addr().(*net.TCPAddr).Port
	discovery.handler.callback(map[string]any{
		"name":           nil,
		"value":          7,
		"type":           "BackboneInterface",
		"discovery_hash": []byte{0xaa, 0xcd},
		"config_entry":   "[[nil-name]]",
		"hops":           1,
		"received":       1234.0,
		"reachable_on":   "127.0.0.1",
		"port":           port,
		"network_id":     "01020304",
	})

	select {
	case conn := <-accepted:
		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Errorf("accepted conn.Close() error = %v", err)
			}
		})
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback autoconnect with nil name")
	}

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", got)
	}
	if got := ts.GetInterfaces()[0].Name(); got != "None" {
		t.Fatalf("Name() = %q, want %q", got, "None")
	}
}

func TestInterfaceDiscoveryStartCallbackBytesTypeSkipsAutoconnectLikePython(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-callback-bytes-type-")
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
		if err == nil {
			accepted <- conn
		}
	}()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)

	callbackCh := make(chan map[string]any, 1)
	discovery.SetDiscoveryCallback(func(info map[string]any) {
		callbackCh <- cloneStringAnyMap(info)
	})
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	discovery.handler.callback(map[string]any{
		"name":           "Bytes Type Backbone",
		"value":          7,
		"type":           []byte("BackboneInterface"),
		"discovery_hash": []byte{0xaa, 0xcf},
		"config_entry":   "[[bytes-type]]",
		"hops":           1,
		"received":       1234.0,
		"reachable_on":   "127.0.0.1",
		"port":           port,
		"network_id":     "01020304",
	})

	select {
	case callbackInfo := <-callbackCh:
		if got, ok := callbackInfo["type"].([]byte); !ok || string(got) != "BackboneInterface" {
			t.Fatalf("callback type = %#v, want []byte(\"BackboneInterface\")", callbackInfo["type"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for external discovery callback")
	}

	select {
	case conn := <-accepted:
		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Errorf("accepted conn.Close() error = %v", err)
			}
		})
		t.Fatal("unexpected auto-connect for bytes-typed discovery type")
	case <-time.After(200 * time.Millisecond):
	}

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected 0 auto-connected interfaces, got %v", got)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "discovery", "interfaces", "aacf.data"))
	if err != nil {
		t.Fatalf("failed to read persisted discovery file: %v", err)
	}
	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("msgpack.Unpack() error = %v", err)
	}
	m := asAnyMap(unpacked)
	if m == nil {
		t.Fatalf("unexpected persisted discovery type %T", unpacked)
	}
	if got, ok := lookupAny(m, "type"); !ok {
		t.Fatal("persisted type missing")
	} else if b, ok := got.([]byte); !ok || string(b) != "BackboneInterface" {
		t.Fatalf("persisted type = %#v, want []byte(\"BackboneInterface\")", got)
	}
}

func TestInterfaceDiscoveryStartCallbackBytesNetworkIDUsesPythonBytesAutoconnectSource(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-callback-bytes-network-id-")
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
		if err == nil {
			accepted <- conn
		}
	}()

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

	port := listener.Addr().(*net.TCPAddr).Port
	discovery.handler.callback(map[string]any{
		"name":           "Bytes Network ID",
		"value":          7,
		"type":           "BackboneInterface",
		"discovery_hash": []byte{0xaa, 0xd0},
		"config_entry":   "[[bytes-network-id]]",
		"hops":           1,
		"received":       1234.0,
		"reachable_on":   "127.0.0.1",
		"port":           port,
		"network_id":     []byte("01020304"),
	})

	var acceptedConn net.Conn
	select {
	case acceptedConn = <-accepted:
		t.Cleanup(func() {
			if err := acceptedConn.Close(); err != nil {
				t.Errorf("acceptedConn.Close() error = %v", err)
			}
		})
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback autoconnect with bytes network_id")
	}

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", got)
	}
	meta, ok := ts.GetInterfaces()[0].(interface {
		AutoconnectSource() string
	})
	if !ok {
		t.Fatal("auto-connected interface does not expose AutoconnectSource")
	}
	if got := meta.AutoconnectSource(); got != "b'01020304'" {
		t.Fatalf("AutoconnectSource() = %q, want %q", got, "b'01020304'")
	}
}

func TestInterfaceDiscoveryStartCallbackBytesIFACUsesPythonBytesStrings(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-callback-bytes-ifac-")
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
		if err == nil {
			accepted <- conn
		}
	}()

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

	port := listener.Addr().(*net.TCPAddr).Port
	discovery.handler.callback(map[string]any{
		"name":           "Bytes IFAC",
		"value":          7,
		"type":           "BackboneInterface",
		"discovery_hash": []byte{0xaa, 0xd1},
		"config_entry":   "[[bytes-ifac]]",
		"hops":           1,
		"received":       1234.0,
		"reachable_on":   "127.0.0.1",
		"port":           port,
		"network_id":     "01020304",
		"ifac_netname":   []byte("mesh"),
		"ifac_netkey":    []byte("secret"),
	})

	var acceptedConn net.Conn
	select {
	case acceptedConn = <-accepted:
		t.Cleanup(func() {
			if err := acceptedConn.Close(); err != nil {
				t.Errorf("acceptedConn.Close() error = %v", err)
			}
		})
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback autoconnect with bytes IFAC")
	}

	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", got)
	}
	meta, ok := ts.GetInterfaces()[0].(interface {
		IFACConfig() interfaces.IFACConfig
	})
	if !ok {
		t.Fatal("auto-connected interface does not expose IFACConfig")
	}
	if got := meta.IFACConfig(); got.NetName != "b'mesh'" || got.NetKey != "b'secret'" {
		t.Fatalf("IFACConfig() = %+v, want NetName/NetKey Python bytes repr", got)
	}
}

func TestInterfaceDiscoveryStartCallbackMissingConfigEntrySkipsAutoconnectButStillCallsCallback(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := testutils.TempDir(t, "rns-discovery-callback-missing-config-entry-")
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
		if err == nil {
			accepted <- conn
		}
	}()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	r := &Reticulum{
		configDir:           tmpDir,
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	}
	discovery := NewInterfaceDiscovery(r)

	callbackCh := make(chan map[string]any, 1)
	discovery.SetDiscoveryCallback(func(info map[string]any) {
		callbackCh <- cloneStringAnyMap(info)
	})
	if err := discovery.Start(2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	receivedAt := float64(time.Now().UnixNano()) / 1e9
	discovery.handler.callback(map[string]any{
		"name":           "No Config Entry",
		"value":          7,
		"type":           "BackboneInterface",
		"discovery_hash": []byte{0xaa, 0xce},
		"hops":           1,
		"received":       receivedAt,
		"reachable_on":   "127.0.0.1",
		"port":           port,
		"network_id":     "01020304",
	})

	select {
	case info := <-callbackCh:
		if got := asString(info["name"]); got != "No Config Entry" {
			t.Fatalf("callback name = %q, want %q", got, "No Config Entry")
		}
		if got := asFloat64(info["discovered"]); got != receivedAt {
			t.Fatalf("callback discovered = %v, want %v", got, receivedAt)
		}
		if got := asFloat64(info["last_heard"]); got != receivedAt {
			t.Fatalf("callback last_heard = %v, want %v", got, receivedAt)
		}
		if got := asInt(info["heard_count"]); got != 0 {
			t.Fatalf("callback heard_count = %v, want 0", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for external discovery callback")
	}

	select {
	case conn := <-accepted:
		_ = conn.Close()
		t.Fatal("unexpected auto-connect without config_entry")
	case <-time.After(300 * time.Millisecond):
	}

	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected no auto-connected interfaces, got %v", got)
	}

	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if got := len(discovered); got != 1 {
		t.Fatalf("expected 1 persisted discovered interface, got %v", got)
	}
	if got := discovered[0].Name; got != "No Config Entry" {
		t.Fatalf("discovered[0].Name = %q, want %q", got, "No Config Entry")
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
		"value":        1,
		"config_entry": "[[Monitor Candidate]]",
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
		"value":        20,
		"config_entry": "[[Candidate A]]",
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
		"value":        10,
		"config_entry": "[[Candidate B]]",
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
		"value":        20,
		"config_entry": "[[Selected Existing]]",
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
		"value":        10,
		"config_entry": "[[Other Candidate]]",
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

func TestInterfaceDiscoveryMonitorDetachesBootstrapOnlyWhenTargetIsZero(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(NewLogger())
	auto := &monitorTestInterface{
		BaseInterface: interfaces.NewBaseInterface("auto", interfaces.ModeFull, 1000),
		online:        true,
	}
	bootstrap := &monitorTestInterface{
		BaseInterface: interfaces.NewBaseInterface("bootstrap", interfaces.ModeFull, 1000),
		online:        true,
		bootstrapOnly: true,
	}
	ts.RegisterInterface(auto)
	ts.RegisterInterface(bootstrap)

	discovery := NewInterfaceDiscovery(&Reticulum{
		transport:           ts,
		logger:              NewLogger(),
		autoconnectDiscover: 0,
	})
	discovery.monitorInterval = 0
	discovery.monitorInterface(auto)

	discovery.monitorAutoconnectsOnce(time.Unix(401, 0))

	if !bootstrap.detached {
		t.Fatal("expected bootstrap-only interface to detach when auto-discovered target is zero")
	}
	if got := len(ts.GetInterfaces()); got != 1 {
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

func TestInterfaceDiscoveryMonitorReenablesConfiguredKISSBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	original := newKISSInterface
	defer func() { newKISSInterface = original }()

	var calls int
	newKISSInterface = func(name, port string, speed, databits, stopbits int, parity string, handler interfaces.InboundHandler) (interfaces.Interface, error) {
		calls++
		return newBootstrapConstructorTestInterface(name, "KISSInterface"), nil
	}

	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap KISS]]
type = KISSInterface
port = /dev/ttyKISS0
speed = 9600
bootstrap_only = Yes
`

	runBootstrapReenableConstructorTest(t, config, "KISSInterface")

	if calls != 2 {
		t.Fatalf("KISS constructor call count = %v, want 2", calls)
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredRNodeBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	original := newRNodeInterface
	defer func() { newRNodeInterface = original }()

	var calls int
	newRNodeInterface = func(name, port string, speed, databits, stopbits int, parity string, frequency, bandwidth, txpower, spreadingFactor, codingRate int, flowControl bool, idInterval int, idCallsign string, handler interfaces.InboundHandler) (interfaces.Interface, error) {
		calls++
		return newBootstrapConstructorTestInterface(name, "RNodeInterface"), nil
	}

	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap RNode]]
type = RNodeInterface
port = /dev/ttyRNode0
frequency = 433050000
bandwidth = 125000
txpower = 10
spreadingfactor = 7
codingrate = 5
bootstrap_only = Yes
`

	runBootstrapReenableConstructorTest(t, config, "RNodeInterface")

	if calls != 2 {
		t.Fatalf("RNode constructor call count = %v, want 2", calls)
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredRNodeMultiBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	original := newRNodeMultiInterface
	defer func() { newRNodeMultiInterface = original }()

	var calls int
	newRNodeMultiInterface = func(name, port string, speed, databits, stopbits int, parity string, idInterval int, idCallsign string, subinterfaces []interfaces.RNodeMultiSubinterfaceConfig, handler interfaces.InboundHandler) (interfaces.Interface, error) {
		calls++
		return newBootstrapConstructorTestInterface(name, "RNodeMultiInterface"), nil
	}

	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap RNode Multi]]
type = RNodeMultiInterface
port = /dev/ttyRNodeMulti0
bootstrap_only = Yes

  [[[sub0]]]
  interface_enabled = Yes
  frequency = 433050000
  bandwidth = 125000
  txpower = 10
  spreadingfactor = 7
  codingrate = 5
`

	runBootstrapReenableConstructorTest(t, config, "RNodeMultiInterface")

	if calls != 2 {
		t.Fatalf("RNodeMulti constructor call count = %v, want 2", calls)
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredAX25KISSBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	original := newAX25KISSInterface
	defer func() { newAX25KISSInterface = original }()

	var calls int
	newAX25KISSInterface = func(name, port string, speed, databits, stopbits int, parity, callsign string, ssid, preambleMS, txTailMS, persistence, slotTimeMS int, flowControl bool, handler interfaces.InboundHandler) (interfaces.Interface, error) {
		calls++
		return newBootstrapConstructorTestInterface(name, "AX25KISSInterface"), nil
	}

	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap AX25]]
type = AX25KISSInterface
port = /dev/ttyAX250
callsign = N0CALL
ssid = 0
bootstrap_only = Yes
`

	runBootstrapReenableConstructorTest(t, config, "AX25KISSInterface")

	if calls != 2 {
		t.Fatalf("AX25KISS constructor call count = %v, want 2", calls)
	}
}

func TestInterfaceDiscoveryMonitorReenablesConfiguredWeaveBootstrapInterfacesWhenAutoconnectsGone(t *testing.T) {
	original := newWeaveInterface
	defer func() { newWeaveInterface = original }()

	var calls int
	newWeaveInterface = func(name, port string, configuredBitrate int, handler interfaces.InboundHandler) (interfaces.Interface, error) {
		calls++
		return newBootstrapConstructorTestInterface(name, "WeaveInterface"), nil
	}

	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 1

[logging]
loglevel = 4

[interfaces]
[[Bootstrap Weave]]
type = WeaveInterface
port = /dev/ttyWeave0
bootstrap_only = Yes
`

	runBootstrapReenableConstructorTest(t, config, "WeaveInterface")

	if calls != 2 {
		t.Fatalf("Weave constructor call count = %v, want 2", calls)
	}
}

type announceTestInterface struct {
	*interfaces.BaseInterface
	ifaceType   string
	bindIP      string
	bindPort    int
	kiss        bool
	connectable bool
	b32         string
}

func (a *announceTestInterface) Type() string      { return a.ifaceType }
func (a *announceTestInterface) Status() bool      { return true }
func (a *announceTestInterface) IsOut() bool       { return true }
func (a *announceTestInterface) Send([]byte) error { return nil }
func (a *announceTestInterface) Detach() error     { a.SetDetached(true); return nil }
func (a *announceTestInterface) BindIP() string    { return a.bindIP }
func (a *announceTestInterface) BindPort() int     { return a.bindPort }
func (a *announceTestInterface) KISSFraming() bool { return a.kiss }
func (a *announceTestInterface) Connectable() bool { return a.connectable }
func (a *announceTestInterface) B32() string       { return a.b32 }

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

func TestInterfaceAnnouncerPayloadKeepsEmptyIFACFields(t *testing.T) {
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
		BaseInterface: interfaces.NewBaseInterface("announce-backbone-empty-ifac", interfaces.ModeGateway, 1000),
		ifaceType:     "BackboneInterface",
		bindIP:        "127.0.0.1",
		bindPort:      4242,
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "Discovery Backbone",
		ReachableOn:       "discovery.example.net",
		PublishIFAC:       true,
	})

	appData, err := announcer.getInterfaceAnnounceData(iface)
	if err != nil {
		t.Fatalf("getInterfaceAnnounceData() error = %v", err)
	}

	unpacked, err := msgpack.Unpack(appData[1 : len(appData)-discoveryStampSize])
	if err != nil {
		t.Fatalf("msgpack.Unpack() error = %v", err)
	}
	info := asAnyMap(unpacked)
	if info == nil {
		t.Fatalf("unexpected announce payload type %T", unpacked)
	}

	if got := asString(lookupDiscoveryValue(info, discoveryFieldIFACNetname)); got != "" {
		t.Fatalf("ifac netname = %q, want empty string", got)
	}
	if got := asString(lookupDiscoveryValue(info, discoveryFieldIFACNetkey)); got != "" {
		t.Fatalf("ifac netkey = %q, want empty string", got)
	}
	if _, ok := lookupDiscovery(info, discoveryFieldIFACNetname); !ok {
		t.Fatal("expected ifac netname key to be present")
	}
	if _, ok := lookupDiscovery(info, discoveryFieldIFACNetkey); !ok {
		t.Fatal("expected ifac netkey key to be present")
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

func TestInterfaceAnnouncerPayloadI2PConnectableUsesB32(t *testing.T) {
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
		BaseInterface: interfaces.NewBaseInterface("announce-i2p-connectable", interfaces.ModeGateway, 1000),
		ifaceType:     "I2PInterface",
		connectable:   true,
		b32:           "liveannouncedestinationabcdefghijklmnopqrstuvwxyz.b32.i2p",
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "Discovery I2P Connectable",
		ReachableOn:       "configured.example.net",
	})

	appData, err := announcer.getInterfaceAnnounceData(iface)
	if err != nil {
		t.Fatalf("getInterfaceAnnounceData() error = %v", err)
	}

	unpacked, err := msgpack.Unpack(appData[1 : len(appData)-discoveryStampSize])
	if err != nil {
		t.Fatalf("msgpack.Unpack() error = %v", err)
	}
	info := asAnyMap(unpacked)
	if info == nil {
		t.Fatalf("unexpected announce payload type %T", unpacked)
	}

	if got := asString(lookupDiscoveryValue(info, discoveryFieldReachableOn)); got != "liveannouncedestinationabcdefghijklmnopqrstuvwxyz.b32.i2p" {
		t.Fatalf("reachable_on = %q, want %q", got, "liveannouncedestinationabcdefghijklmnopqrstuvwxyz.b32.i2p")
	}
}

func TestInterfaceAnnouncerPayloadPlainTCPClient(t *testing.T) {
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
		BaseInterface: interfaces.NewBaseInterface("announce-tcp-client", interfaces.ModeGateway, 1000),
		ifaceType:     "TCPClientInterface",
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "Discovery TCP Client\n",
		ReachableOn:       "client.example.net",
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

	if got := asString(lookupDiscoveryValue(info, discoveryFieldInterfaceType)); got != "TCPClientInterface" {
		t.Fatalf("interface type = %q, want %q", got, "TCPClientInterface")
	}
	if got := asString(lookupDiscoveryValue(info, discoveryFieldName)); got != "Discovery TCP Client" {
		t.Fatalf("name = %q, want %q", got, "Discovery TCP Client")
	}
	if got := lookupDiscoveryValue(info, discoveryFieldReachableOn); got != nil {
		t.Fatalf("reachable_on = %v, want nil", got)
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
		wantReachableOn     bool
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
			wantReachableOn:     false,
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
			wantReachableOn:   false,
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
			wantReachableOn:   false,
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
			wantReachableOn:   false,
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
			if _, ok := lookupDiscovery(info, discoveryFieldReachableOn); ok != tt.wantReachableOn {
				t.Fatalf("reachable_on presence = %v, want %v", ok, tt.wantReachableOn)
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
