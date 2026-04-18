package rns

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestChooseConfigDir(t *testing.T) {
	t.Parallel()
	home := "/home/testuser"

	tests := []struct {
		name     string
		explicit string
		has      map[string]bool
		want     string
	}{
		{
			name:     "explicit config dir wins",
			explicit: "/custom/rns",
			has:      map[string]bool{systemConfigDir: true, filepath.Join(home, ".config", "reticulum"): true},
			want:     "/custom/rns",
		},
		{
			name:     "system config preferred when present",
			explicit: "",
			has:      map[string]bool{systemConfigDir: true, filepath.Join(home, ".config", "reticulum"): true},
			want:     systemConfigDir,
		},
		{
			name:     "user config used when system missing",
			explicit: "",
			has:      map[string]bool{filepath.Join(home, ".config", "reticulum"): true},
			want:     filepath.Join(home, ".config", "reticulum"),
		},
		{
			name:     "fallback to .reticulum",
			explicit: "",
			has:      map[string]bool{},
			want:     filepath.Join(home, ".reticulum"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chooseConfigDir(tt.explicit, home, func(path string) bool {
				return tt.has[path]
			})
			if got != tt.want {
				t.Fatalf("chooseConfigDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCreateDefaultConfigMatchesPythonShape(t *testing.T) {
	t.Parallel()
	tmp, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	configPath := filepath.Join(tmp, "config")

	r := &Reticulum{}
	if err := r.createDefaultConfig(configPath); err != nil {
		t.Fatalf("createDefaultConfig() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	content := string(data)
	mustContain := []string{
		"[reticulum]",
		"[logging]",
		"[interfaces]",
		"[[Default Interface]]",
		"type = AutoInterface",
		"enabled = Yes",
	}
	for _, token := range mustContain {
		if !strings.Contains(content, token) {
			t.Fatalf("default config missing %q", token)
		}
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	interfacesSection, ok := cfg.GetSection("interfaces")
	if !ok {
		t.Fatalf("expected [interfaces] section in default config")
	}

	sub, ok := interfacesSection.Subsections["Default Interface"]
	if !ok {
		t.Fatalf("expected [[Default Interface]] subsection in default config")
	}

	if ifaceType, _ := sub.GetProperty("type"); ifaceType != "AutoInterface" {
		t.Fatalf("Default Interface type = %q, want %q", ifaceType, "AutoInterface")
	}
}

func TestParseListProperty(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty", in: "", want: nil},
		{name: "single", in: "eth0", want: []string{"eth0"}},
		{name: "csv", in: "eth0, wlan0", want: []string{"eth0", "wlan0"}},
		{name: "bracketed csv", in: "[eth0, wlan0]", want: []string{"eth0", "wlan0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseListProperty(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parseListProperty(%q) length = %v, want %v", tt.in, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("parseListProperty(%q)[%v] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestNewReticulumCreatesPythonStartupLayout(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	requiredDirs := []string{
		filepath.Join(configDir, "storage"),
		filepath.Join(configDir, "storage", "cache"),
		filepath.Join(configDir, "storage", "cache", "announces"),
		filepath.Join(configDir, "storage", "resources"),
		filepath.Join(configDir, "storage", "identities"),
		filepath.Join(configDir, "storage", "blackhole"),
		filepath.Join(configDir, "interfaces"),
	}

	for _, dir := range requiredDirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%q is not a directory", dir)
		}
	}
}

func TestReticulumOptionParitySliceNetworkIdentityAndBooleans(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	networkIdentityPath := filepath.Join(configDir, "storage", "identities", "network-id")
	config := `[reticulum]
share_instance = No
network_identity = ` + networkIdentityPath + `
link_mtu_discovery = No
use_implicit_proof = No

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if r.networkIdentity == nil {
		t.Fatalf("expected network identity to be initialized")
	}
	if _, err := os.Stat(networkIdentityPath); err != nil {
		t.Fatalf("expected network identity file at %q: %v", networkIdentityPath, err)
	}
	if got := r.transport.NetworkIdentityHash(); len(got) == 0 {
		t.Fatalf("expected transport network identity hash to be set")
	}

	if r.linkMTUDiscovery {
		t.Fatalf("expected link_mtu_discovery = false from config")
	}
	// Note: linkMTUDiscoveryEnabled() is global, but the config setting should be correctly parsed into the Reticulum instance.

	if r.useImplicitProof {
		t.Fatalf("expected use_implicit_proof = false from config")
	}

	l := mustTestNewLink(t, r.Transport(), nil)
	if got := l.signallingBytes(); len(got) != 0 {
		t.Fatalf("expected signalling bytes omitted when link_mtu_discovery disabled, got len=%v", len(got))
	}
}

func TestParseUseImplicitProof(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	config := `[reticulum]
share_instance = No
use_implicit_proof = No

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if r.useImplicitProof {
		t.Fatal("expected use_implicit_proof = false from config")
	}
	if ts.UseImplicitProof() {
		t.Fatal("expected transport to receive use_implicit_proof = false from config")
	}
}

func TestParsePanicOnInterfaceError(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	config := `[reticulum]
share_instance = No
panic_on_interface_error = Yes

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	interfaces.SetPanicOnInterfaceErrorEnabled(false)
	t.Cleanup(func() {
		interfaces.SetPanicOnInterfaceErrorEnabled(false)
	})

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if !r.panicOnIfaceError {
		t.Fatal("expected panic_on_interface_error = true from config")
	}
	if !interfaces.PanicOnInterfaceErrorEnabled() {
		t.Fatal("expected interfaces package to receive panic_on_interface_error = true from config")
	}
}

func TestSerialInterfaceMissingPortDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[Serial Missing Port]]
    type = SerialInterface
    enabled = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestKISSInterfaceMissingPortDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[KISS Missing Port]]
    type = KISSInterface
    enabled = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestKISSInterfaceUnsupportedPlatformNotRegistered(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "linux" {
		t.Skip("unsupported-platform behavior test")
	}

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[KISS Unsupported]]
    type = KISSInterface
    enabled = Yes
    port = /dev/ttyUSB0
    speed = 9600
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestAX25KISSInterfaceMissingPortDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[AX25 Missing Port]]
    type = AX25KISSInterface
    enabled = Yes
    callsign = N0CALL
    ssid = 0
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestAX25KISSInterfaceMissingCallsignDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[AX25 Missing Callsign]]
    type = AX25KISSInterface
    enabled = Yes
    port = /dev/ttyUSB0
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestAX25KISSInterfaceUnsupportedPlatformNotRegistered(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "linux" {
		t.Skip("unsupported-platform behavior test")
	}

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[AX25 Unsupported]]
    type = AX25KISSInterface
    enabled = Yes
    port = /dev/ttyUSB0
    callsign = N0CALL
    ssid = 0
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestPipeInterfaceMissingCommandDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[Pipe Missing Command]]
    type = PipeInterface
    enabled = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestPipeInterfaceBadCommandDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[Pipe Bad Command]]
    type = PipeInterface
    enabled = Yes
    command = /this/command/does/not/exist
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestBackboneInterfaceMissingPortDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[Backbone Missing Port]]
    type = BackboneInterface
    enabled = Yes
    listen_ip = 127.0.0.1
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestBackboneClientInterfaceMissingTargetDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[Backbone Client Missing Target]]
    type = BackboneClientInterface
    enabled = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestI2PInterfaceMissingConfigDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[I2P Missing Config]]
    type = I2PInterface
    enabled = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestI2PInterfaceConnectableMissingPortDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[I2P Missing Connectable Port]]
    type = I2PInterface
    enabled = Yes
    connectable = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestI2PInterfacePeerConfigRegisters(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[I2P Peer]]
    type = I2PInterface
    enabled = Yes
    peers = 127.0.0.1:9
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 1 {
		t.Fatalf("registered interfaces = %v, want 1", got)
	}

	if got := r.Transport().GetInterfaces()[0].Type(); got != "I2PInterfacePeer" {
		t.Fatalf("registered interface type = %q, want I2PInterfacePeer", got)
	}
}

func TestI2PInterfaceConnectableRegisters(t *testing.T) {
	t.Parallel()

	port := reserveTCPPort(t)
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := fmt.Sprintf(`[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[I2P Connectable]]
    type = I2PInterface
    enabled = Yes
    connectable = Yes
    bind_ip = 127.0.0.1
    bind_port = %v
`, port)

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 1 {
		t.Fatalf("registered interfaces = %v, want 1", got)
	}

	if got := r.Transport().GetInterfaces()[0].Type(); got != "I2PInterface" {
		t.Fatalf("registered interface type = %q, want I2PInterface", got)
	}
}

func TestRNodeInterfaceMissingPortDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[RNode Missing Port]]
    type = RNodeInterface
    enabled = Yes
    frequency = 433050000
    bandwidth = 125000
    txpower = 10
    spreadingfactor = 7
    codingrate = 5
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestRNodeInterfaceMissingRequiredFieldsDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[RNode Missing Required]]
    type = RNodeInterface
    enabled = Yes
    port = /dev/ttyUSB0
    bandwidth = 125000
    txpower = 10
    spreadingfactor = 7
    codingrate = 5
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestRNodeInterfaceUnsupportedPlatformNotRegistered(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "linux" {
		t.Skip("unsupported-platform behavior test")
	}

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[RNode Unsupported]]
    type = RNodeInterface
    enabled = Yes
    port = /dev/ttyUSB0
    frequency = 433050000
    bandwidth = 125000
    txpower = 10
    spreadingfactor = 7
    codingrate = 5
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestWeaveInterfaceMissingPortDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[Weave Missing Port]]
    type = WeaveInterface
    enabled = Yes
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestWeaveInterfaceUnsupportedPlatformNotRegistered(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "linux" {
		t.Skip("unsupported-platform behavior test")
	}

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[Weave Unsupported]]
    type = WeaveInterface
    enabled = Yes
    port = /dev/ttyUSB0
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestRNodeMultiInterfaceNoSubinterfacesDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[RNode Multi Missing Subs]]
    type = RNodeMultiInterface
    enabled = Yes
    port = /dev/ttyUSB0
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestRNodeMultiInterfaceMultipleEnabledSubsDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[RNode Multi Two Subs]]
    type = RNodeMultiInterface
    enabled = Yes
    port = /dev/ttyUSB0

    [[[sub0]]]
      interface_enabled = Yes
      frequency = 433050000
      bandwidth = 125000
      txpower = 10
      spreadingfactor = 7
      codingrate = 5

    [[[sub1]]]
      interface_enabled = Yes
      frequency = 433150000
      bandwidth = 125000
      txpower = 10
      spreadingfactor = 7
      codingrate = 5
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestRNodeMultiInterfaceUnsupportedPlatformNotRegistered(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "linux" {
		t.Skip("unsupported-platform behavior test")
	}

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[RNode Multi Unsupported]]
    type = RNodeMultiInterface
    enabled = Yes
    port = /dev/ttyUSB0

    [[[sub0]]]
      interface_enabled = Yes
      frequency = 433050000
      bandwidth = 125000
      txpower = 10
      spreadingfactor = 7
      codingrate = 5
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestSerialInterfaceUnsupportedPlatformNotRegistered(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "linux" {
		t.Skip("unsupported-platform behavior test")
	}

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
  [[Serial Unsupported]]
    type = SerialInterface
    enabled = Yes
    port = /dev/ttyUSB0
    speed = 9600
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := len(r.Transport().GetInterfaces()); got != 0 {
		t.Fatalf("registered interfaces = %v, want 0", got)
	}
}

func TestReticulumOptionParityRemoteManagementAndProbes(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	hash1 := "00112233445566778899aabbccddeeff"
	hash2 := "ffeeddccbbaa99887766554433221100"
	config := `[reticulum]
share_instance = No
enable_remote_management = Yes
respond_to_probes = Yes
remote_management_allowed = [` + hash1 + `, ` + strings.ToUpper(hash1) + `, ` + hash2 + `]

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if !r.remoteMgmtEnabled {
		t.Fatalf("expected enable_remote_management = true")
	}
	if !r.allowProbes {
		t.Fatalf("expected respond_to_probes = true")
	}
	if len(r.remoteMgmtAllowed) != 2 {
		t.Fatalf("expected 2 unique remote_management_allowed hashes, got %v", len(r.remoteMgmtAllowed))
	}

	want1 := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	want2 := []byte{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00}

	seen1 := false
	seen2 := false
	for _, got := range r.remoteMgmtAllowed {
		if bytes.Equal(got, want1) {
			seen1 = true
		}
		if bytes.Equal(got, want2) {
			seen2 = true
		}
	}
	if !seen1 || !seen2 {
		t.Fatalf("unexpected remote management ACL contents: %x", r.remoteMgmtAllowed)
	}
}

func TestReticulumOptionParityRemoteManagementAllowedInvalidLength(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No
remote_management_allowed = [abcd]

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	if _, err := NewReticulum(ts, configDir); err == nil {
		t.Fatalf("expected NewReticulum() to fail for invalid remote_management_allowed hash length")
	}
}

func TestReticulumOptionParityRemoteManagementAllowedInvalidHex(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No
remote_management_allowed = [00112233445566778899aabbccddeezz]

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	if _, err := NewReticulum(ts, configDir); err == nil {
		t.Fatalf("expected NewReticulum() to fail for invalid remote_management_allowed hex")
	}
}

func TestReticulumOptionParityForceBitratePanicAndDiscover(t *testing.T) {
	// origPanic := panicOnInterfaceErrorEnabled()
	// defer setPanicOnInterfaceErrorEnabled(origPanic)

	sharedPort := reserveTCPPort(t)
	controlPort := reserveTCPPort(t)
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	forcedBitrate := 24680

	config := `[reticulum]
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = ` + strconv.Itoa(sharedPort) + `
instance_control_port = ` + strconv.Itoa(controlPort) + `
force_shared_instance_bitrate = ` + strconv.Itoa(forcedBitrate) + `
panic_on_interface_error = Yes
discover_interfaces = Yes

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if !r.panicOnIfaceError {
		t.Fatalf("expected panic_on_interface_error=true")
	}

	if !r.discoverInterfaces {
		t.Fatalf("expected discover_interfaces=true")
	}
	if r.Transport().DiscoverInterfacesCallCount() == 0 {
		t.Fatalf("expected discover interfaces hook to be invoked")
	}
	if r.interfaceDiscovery == nil {
		t.Fatalf("expected interface discovery to be initialized")
	}
	handlers := r.Transport().AnnounceHandlers()
	if len(handlers) != 1 {
		t.Fatalf("expected 1 discovery announce handler, got %v", len(handlers))
	}
	if handlers[0].AspectFilter != discoveryAppName+".discovery.interface" {
		t.Fatalf("AspectFilter = %q, want %q", handlers[0].AspectFilter, discoveryAppName+".discovery.interface")
	}
	if _, err := os.Stat(filepath.Join(configDir, "discovery", "interfaces")); err != nil {
		t.Fatalf("expected discovery storage path to exist: %v", err)
	}

	if r.forceSharedBitrate != forcedBitrate {
		t.Fatalf("expected force_shared_instance_bitrate=%v, got %v", forcedBitrate, r.forceSharedBitrate)
	}
	if r.sharedInstanceInterface == nil {
		t.Fatalf("expected local shared interface to be initialized")
	}
	if got := r.sharedInstanceInterface.Bitrate(); got != forcedBitrate {
		t.Fatalf("expected shared interface bitrate=%v, got %v", forcedBitrate, got)
	}
}

func TestReticulumOptionParityDiscoveryAndBlackholeSettings(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	bh1 := "11223344556677889900aabbccddeeff"
	bh2 := "ffeeddccbbaa00998877665544332211"
	is1 := "0102030405060708090a0b0c0d0e0f10"
	is2 := "a1a2a3a4a5a6a7a8a9aaabacadaeaf01"

	config := `[reticulum]
share_instance = No
required_discovery_value = 7
publish_blackhole = Yes
blackhole_sources = [` + bh1 + `, ` + strings.ToUpper(bh1) + `, ` + bh2 + `]
interface_discovery_sources = [` + is1 + `, ` + strings.ToUpper(is1) + `, ` + is2 + `]
autoconnect_discovered_interfaces = 3

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if r.requiredDiscoveryV != 7 {
		t.Fatalf("expected required_discovery_value=7, got %v", r.requiredDiscoveryV)
	}
	if !r.publishBlackhole {
		t.Fatalf("expected publish_blackhole=true")
	}
	if r.autoconnectDiscover != 3 {
		t.Fatalf("expected autoconnect_discovered_interfaces=3, got %v", r.autoconnectDiscover)
	}

	if len(r.blackholeSources) != 2 {
		t.Fatalf("expected 2 unique blackhole_sources, got %v", len(r.blackholeSources))
	}
	if len(r.interfaceSources) != 2 {
		t.Fatalf("expected 2 unique interface_discovery_sources, got %v", len(r.interfaceSources))
	}
}

func TestParseInterfaceMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		ifaceType string
		props     map[string]string
		want      int
	}{
		{
			name:      "interface_mode alias wins",
			ifaceType: "UDPInterface",
			props:     map[string]string{"interface_mode": "ap"},
			want:      interfaces.ModeAccessPoint,
		},
		{
			name:      "mode alias applies on non tcp-interface selector",
			ifaceType: "SerialInterface",
			props:     map[string]string{"mode": "gw"},
			want:      interfaces.ModeGateway,
		},
		{
			name:      "tcp interface client mode is not treated as interface mode",
			ifaceType: "TCPInterface",
			props:     map[string]string{"mode": "client"},
			want:      interfaces.ModeFull,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sub := &ConfigSection{Properties: tt.props}
			if got := parseInterfaceMode(sub, tt.ifaceType); got != tt.want {
				t.Fatalf("parseInterfaceMode(%v) = %v, want %v", tt.ifaceType, got, tt.want)
			}
		})
	}
}

func TestParseDiscoveryConfig(t *testing.T) {
	t.Parallel()

	sub := &ConfigSection{Properties: map[string]string{
		"discoverable":          "yes",
		"announce_interval":     "1",
		"discovery_stamp_value": "11",
		"discovery_name":        "Discovery Node",
		"discovery_encrypt":     "yes",
		"reachable_on":          "discovery.example.net",
		"publish_ifac":          "yes",
		"latitude":              "12.34",
		"longitude":             "56.78",
		"height":                "90.12",
		"discovery_frequency":   "123456789",
		"discovery_bandwidth":   "250000",
		"discovery_modulation":  "lora",
	}}

	cfg, mode := parseDiscoveryConfig(sub, "TCPServerInterface", interfaces.ModePointToPoint)
	if !cfg.SupportsDiscovery || !cfg.Discoverable {
		t.Fatalf("unexpected discovery flags: %+v", cfg)
	}
	if mode != interfaces.ModeGateway {
		t.Fatalf("discoverable TCP server should promote mode to gateway, got %v", mode)
	}
	if cfg.AnnounceInterval != 5*time.Minute {
		t.Fatalf("announce interval = %v, want %v", cfg.AnnounceInterval, 5*time.Minute)
	}
	if cfg.StampValue != 11 || cfg.Name != "Discovery Node" || !cfg.Encrypt || cfg.ReachableOn != "discovery.example.net" || !cfg.PublishIFAC {
		t.Fatalf("unexpected discovery config values: %+v", cfg)
	}
	if cfg.Latitude == nil || *cfg.Latitude != 12.34 || cfg.Longitude == nil || *cfg.Longitude != 56.78 || cfg.Height == nil || *cfg.Height != 90.12 {
		t.Fatalf("unexpected discovery coordinates: %+v", cfg)
	}
	if cfg.Frequency == nil || *cfg.Frequency != 123456789 || cfg.Bandwidth == nil || *cfg.Bandwidth != 250000 || cfg.Modulation != "lora" {
		t.Fatalf("unexpected discovery radio config: %+v", cfg)
	}
}

func TestParseDiscoveryConfigPromotesRNodeToAccessPoint(t *testing.T) {
	t.Parallel()

	cfg, mode := parseDiscoveryConfig(&ConfigSection{Properties: map[string]string{
		"discoverable": "yes",
	}}, "RNodeInterface", interfaces.ModeFull)
	if !cfg.Discoverable {
		t.Fatalf("expected discoverable RNode config")
	}
	if mode != interfaces.ModeAccessPoint {
		t.Fatalf("discoverable RNode should promote mode to access point, got %v", mode)
	}
	if cfg.AnnounceInterval != 6*time.Hour {
		t.Fatalf("default announce interval = %v, want %v", cfg.AnnounceInterval, 6*time.Hour)
	}
}

func TestReticulumInterfaceDiscoveryConfig(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
[[Discovery TCP]]
type = TCPServerInterface
listen_ip = 127.0.0.1
listen_port = ` + strconv.Itoa(port) + `
interface_mode = ptp
discoverable = Yes
announce_interval = 1
discovery_stamp_value = 9
discovery_name = Test Discovery Interface
discovery_encrypt = Yes
reachable_on = discovery.example.net
publish_ifac = Yes
latitude = 12.34
longitude = 56.78
height = 90.12
discovery_frequency = 123456789
discovery_bandwidth = 250000
discovery_modulation = lora
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	ifaces := ts.GetInterfaces()
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 interface, got %v", len(ifaces))
	}

	if got := ifaces[0].Mode(); got != interfaces.ModeGateway {
		t.Fatalf("Mode() = %v, want %v", got, interfaces.ModeGateway)
	}

	getter, ok := ifaces[0].(interface {
		DiscoveryConfig() interfaces.DiscoveryConfig
	})
	if !ok {
		t.Fatalf("interface %T does not expose DiscoveryConfig()", ifaces[0])
	}
	cfg := getter.DiscoveryConfig()
	if !cfg.SupportsDiscovery || !cfg.Discoverable {
		t.Fatalf("unexpected discovery flags: %+v", cfg)
	}
	if cfg.AnnounceInterval != 5*time.Minute || cfg.StampValue != 9 || cfg.Name != "Test Discovery Interface" {
		t.Fatalf("unexpected discovery timing/name config: %+v", cfg)
	}
	if !cfg.Encrypt || cfg.ReachableOn != "discovery.example.net" || !cfg.PublishIFAC || cfg.Modulation != "lora" {
		t.Fatalf("unexpected discovery metadata: %+v", cfg)
	}
	if cfg.Latitude == nil || *cfg.Latitude != 12.34 || cfg.Longitude == nil || *cfg.Longitude != 56.78 || cfg.Height == nil || *cfg.Height != 90.12 {
		t.Fatalf("unexpected discovery coordinates: %+v", cfg)
	}
	if cfg.Frequency == nil || *cfg.Frequency != 123456789 || cfg.Bandwidth == nil || *cfg.Bandwidth != 250000 {
		t.Fatalf("unexpected discovery radio config: %+v", cfg)
	}
}

func TestReticulumOptionParityDiscoveryValueNonPositiveClears(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No
required_discovery_value = 0

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)
	if r.requiredDiscoveryV != 0 {
		t.Fatalf("expected required_discovery_value to clear to 0, got %v", r.requiredDiscoveryV)
	}
}

func TestReticulumOptionParityBlackholeSourcesInvalidLength(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No
blackhole_sources = [abcd]

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	if _, err := NewReticulum(ts, configDir); err == nil {
		t.Fatalf("expected NewReticulum() to fail for invalid blackhole_sources hash length")
	}
}

func TestReticulumOptionParityBlackholeSourcesInvalidHex(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No
blackhole_sources = [00112233445566778899aabbccddeezz]

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	if _, err := NewReticulum(ts, configDir); err == nil {
		t.Fatalf("expected NewReticulum() to fail for invalid blackhole_sources hex")
	}
}

func TestReticulumOptionParityInterfaceDiscoverySourcesInvalidLength(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No
interface_discovery_sources = [abcd]

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	if _, err := NewReticulum(ts, configDir); err == nil {
		t.Fatalf("expected NewReticulum() to fail for invalid interface_discovery_sources hash length")
	}
}

func TestReticulumOptionParityInterfaceDiscoverySourcesInvalidHex(t *testing.T) {
	t.Parallel()

	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	config := `[reticulum]
share_instance = No
interface_discovery_sources = [00112233445566778899aabbccddeezz]

[logging]
loglevel = 4

[interfaces]
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	if _, err := NewReticulum(ts, configDir); err == nil {
		t.Fatalf("expected NewReticulum() to fail for invalid interface_discovery_sources hex")
	}
}

func TestParseIFACConfig(t *testing.T) {
	sub := &ConfigSection{Properties: map[string]string{
		"ifac_netname": "mesh-alpha",
		"ifac_netkey":  "key-material",
		"ifac_size":    "32",
	}}

	cfg := parseIFACConfig(sub)
	if !cfg.Enabled {
		t.Fatalf("expected IFAC config to be enabled")
	}
	if cfg.NetName != "mesh-alpha" {
		t.Fatalf("netname mismatch: got %q", cfg.NetName)
	}
	if cfg.NetKey != "key-material" {
		t.Fatalf("netkey mismatch: got %q", cfg.NetKey)
	}
	if cfg.Size != 4 {
		t.Fatalf("size mismatch: got %v", cfg.Size)
	}
}

func TestParseIFACConfigAliases(t *testing.T) {
	sub := &ConfigSection{Properties: map[string]string{
		"network_name": "mesh-beta",
		"pass_phrase":  "secret-pass",
		"ifac_size":    "16",
	}}

	cfg := parseIFACConfig(sub)
	if !cfg.Enabled {
		t.Fatalf("expected alias IFAC config to be enabled")
	}
	if cfg.NetName != "mesh-beta" {
		t.Fatalf("alias netname mismatch: got %q", cfg.NetName)
	}
	if cfg.NetKey != "secret-pass" {
		t.Fatalf("alias netkey mismatch: got %q", cfg.NetKey)
	}
	if cfg.Size != 2 {
		t.Fatalf("alias size mismatch: got %v", cfg.Size)
	}
}

func TestParseIFACConfigSizeOnlyDoesNotEnable(t *testing.T) {
	sub := &ConfigSection{Properties: map[string]string{
		"ifac_size": "64",
	}}

	cfg := parseIFACConfig(sub)
	if cfg.Enabled {
		t.Fatalf("expected size-only IFAC config to remain disabled")
	}
}

func TestParseIFACConfigDisabledByDefault(t *testing.T) {
	sub := &ConfigSection{Properties: map[string]string{}}
	cfg := parseIFACConfig(sub)
	if cfg.Enabled {
		t.Fatalf("expected empty IFAC config to be disabled")
	}
}
