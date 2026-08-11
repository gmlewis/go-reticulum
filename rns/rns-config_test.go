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
	tmp := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

// TestReticulumParsesStaticTransportIdentity verifies the
// static_transport_identity config option (RNS v1.3.7) flows through to the
// transport and, combined with enable_transport=No, keeps the persistent
// transport identity instead of generating an ephemeral one (Python
// Transport.py:235-237).
func TestReticulumParsesStaticTransportIdentity(t *testing.T) {
	t.Parallel()

	// With static_transport_identity = Yes and enable_transport = No, the
	// operative identity must equal the persistent identity (no ephemeral).
	dir1 := testutils.TempDir(t, tempDirPrefix)
	cfg1 := "[reticulum]\nenable_transport = No\nstatic_transport_identity = Yes\n\n[logging]\nloglevel = 2\n\n[interfaces]\n"
	if err := os.WriteFile(filepath.Join(dir1, "config"), []byte(cfg1), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ts1 := NewTransportSystem(nil)
	r1 := mustTestNewReticulum(t, ts1, dir1)
	defer closeReticulum(t, r1)
	tsys1 := r1.Transport().(*TransportSystem)
	if tsys1.PersistentIdentity() == nil {
		t.Fatal("static case: persistent identity not initialized")
	}
	if !bytes.Equal(tsys1.Identity().Hash, tsys1.PersistentIdentity().Hash) {
		t.Errorf("static_transport_identity should keep persistent identity; got %x want %x", tsys1.Identity().Hash, tsys1.PersistentIdentity().Hash)
	}

	// Without static_transport_identity and enable_transport = No, the
	// operative identity must be an ephemeral distinct from persistent.
	dir2 := testutils.TempDir(t, tempDirPrefix)
	cfg2 := "[reticulum]\nenable_transport = No\n\n[logging]\nloglevel = 2\n\n[interfaces]\n"
	if err := os.WriteFile(filepath.Join(dir2, "config"), []byte(cfg2), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ts2 := NewTransportSystem(nil)
	r2 := mustTestNewReticulum(t, ts2, dir2)
	defer closeReticulum(t, r2)
	tsys2 := r2.Transport().(*TransportSystem)
	if tsys2.PersistentIdentity() == nil {
		t.Fatal("ephemeral case: persistent identity not initialized")
	}
	if bytes.Equal(tsys2.Identity().Hash, tsys2.PersistentIdentity().Hash) {
		t.Errorf("non-transport without static_transport_identity should use ephemeral identity; both = %x", tsys2.Identity().Hash)
	}
}

func TestParseUseImplicitProof(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)

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
	configDir := testutils.TempDir(t, tempDirPrefix)
	port := reserveTCPPort(t)

	config := `[reticulum]
share_instance = No
panic_on_interface_error = Yes

[logging]
loglevel = 4

[interfaces]
  [[Policy Test Interface]]
    type = TCPServerInterface
    enabled = Yes
    listen_ip = 127.0.0.1
    listen_port = ` + strconv.Itoa(port) + `
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if !r.panicOnIfaceError {
		t.Fatal("expected panic_on_interface_error = true from config")
	}
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("expected 1 configured interface, got %v", got)
	}
	getter, ok := ts.GetInterfaces()[0].(interface{ PanicOnInterfaceErrorEnabled() bool })
	if !ok {
		t.Fatalf("configured interface %T does not expose PanicOnInterfaceErrorEnabled()", ts.GetInterfaces()[0])
	}
	if !getter.PanicOnInterfaceErrorEnabled() {
		t.Fatal("expected configured interface to receive panic_on_interface_error = true from config")
	}
}

func TestSerialInterfaceMissingPortDoesNotRegister(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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
	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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
	sharedPort := reserveTCPPort(t)
	controlPort := reserveTCPPort(t)
	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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
		{
			name:      "internal mode via mode key",
			ifaceType: "UDPInterface",
			props:     map[string]string{"mode": "internal"},
			want:      interfaces.ModeInternal,
		},
		{
			name:      "internal mode via interface_mode key",
			ifaceType: "BackboneInterface",
			props:     map[string]string{"interface_mode": "internal"},
			want:      interfaces.ModeInternal,
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

// TestDiscoverPathsForMatchesPython asserts the discover-paths mode set mirrors
// RNS 1.4.2's Interface.DISCOVER_PATHS_FOR, which gained MODE_INTERNAL at v1.3.7.
func TestDiscoverPathsForMatchesPython(t *testing.T) {
	t.Parallel()
	want := []int{
		interfaces.ModeAccessPoint,
		interfaces.ModeGateway,
		interfaces.ModeRoaming,
		interfaces.ModeInternal,
	}
	if got := interfaces.DiscoverPathsFor; len(got) != len(want) {
		t.Fatalf("DiscoverPathsFor length = %v, want %v", len(got), len(want))
	}
	for i, m := range want {
		if got := interfaces.DiscoverPathsFor[i]; got != m {
			t.Fatalf("DiscoverPathsFor[%v] = %v, want %v", i, got, m)
		}
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

// TestParseDiscoveryConfigPreservesInternalMode asserts Phase 6 Task 7: when
// discovery is enabled on an interface whose configured mode is MODE_INTERNAL,
// the mode is preserved rather than auto-reconfigured to gateway/access_point.
// This mirrors RNS/Reticulum.py (v1.3.9), whose discovery auto-mode allowed set
// is [MODE_GATEWAY, MODE_ACCESS_POINT, MODE_INTERNAL] — a mode already in that
// set is left untouched when discoverable=true.
func TestParseDiscoveryConfigPreservesInternalMode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		iface    string
		mode     int
		wantMode int
	}{
		{"internal preserved (TCP)", "TCPServerInterface", interfaces.ModeInternal, interfaces.ModeInternal},
		{"internal preserved (RNode)", "RNodeInterface", interfaces.ModeInternal, interfaces.ModeInternal},
		{"gateway preserved", "TCPServerInterface", interfaces.ModeGateway, interfaces.ModeGateway},
		{"access_point preserved", "TCPServerInterface", interfaces.ModeAccessPoint, interfaces.ModeAccessPoint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, mode := parseDiscoveryConfig(&ConfigSection{Properties: map[string]string{
				"discoverable": "yes",
			}}, tc.iface, tc.mode)
			if !cfg.Discoverable {
				t.Fatalf("expected discoverable config")
			}
			if mode != tc.wantMode {
				t.Fatalf("discovery mode = %v, want %v (should be preserved)", mode, tc.wantMode)
			}
		})
	}
}

func TestParseDiscoveryConfigSupportsKISSAndChannel(t *testing.T) {
	t.Parallel()

	cfg, mode := parseDiscoveryConfig(&ConfigSection{Properties: map[string]string{
		"discoverable":         "yes",
		"announce_interval":    "15",
		"discovery_channel":    "11",
		"discovery_frequency":  "433920000",
		"discovery_bandwidth":  "12500",
		"discovery_modulation": "afsk",
	}}, "KISSInterface", interfaces.ModeFull)

	if !cfg.SupportsDiscovery || !cfg.Discoverable {
		t.Fatalf("unexpected discovery flags: %+v", cfg)
	}
	if mode != interfaces.ModeGateway {
		t.Fatalf("discoverable KISS should promote mode to gateway, got %v", mode)
	}
	if cfg.Channel == nil || *cfg.Channel != 11 {
		t.Fatalf("Channel = %v, want 11", cfg.Channel)
	}
	if cfg.Frequency == nil || *cfg.Frequency != 433920000 {
		t.Fatalf("Frequency = %v, want 433920000", cfg.Frequency)
	}
	if cfg.Bandwidth == nil || *cfg.Bandwidth != 12500 {
		t.Fatalf("Bandwidth = %v, want 12500", cfg.Bandwidth)
	}
	if cfg.Modulation != "afsk" {
		t.Fatalf("Modulation = %q, want %q", cfg.Modulation, "afsk")
	}
}

func TestReticulumInterfaceDiscoveryConfig(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
[[Discovery TCP]]
type = TCPServerInterface
interface_enabled = Yes
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

	if r.interfaceAnnouncer == nil {
		t.Fatalf("expected interface announcer to be initialized for discoverable interface")
	}

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

// TestReticulumDefaultGravityAndInterfaceContractConfig verifies the Phase 1
// per-interface keys (gravity, recursive_prs, announces_from_internal,
// announces_to_internal) and the instance-wide default_gravity flow from a
// config file into the Reticulum and the registered interface, mirroring
// Reticulum.py:771-772,842-849,935-937 and _default_gravity().
func TestReticulumDefaultGravityAndInterfaceContractConfig(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No
default_gravity = 5

[logging]
loglevel = 4

[interfaces]
[[Contract TCP]]
type = TCPServerInterface
interface_enabled = Yes
listen_ip = 127.0.0.1
listen_port = ` + strconv.Itoa(port) + `
interface_mode = gateway
gravity = 9
recursive_prs = true
announces_from_internal = false
announces_to_internal = true
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := r.DefaultGravity(); got != 5 {
		t.Fatalf("DefaultGravity() = %v, want 5", got)
	}

	ifaces := ts.GetInterfaces()
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 interface, got %v", len(ifaces))
	}
	iface := ifaces[0]

	if got := iface.Gravity(); got != 9 {
		t.Fatalf("Gravity() = %v, want 9", got)
	}
	if got := iface.RecursivePrs(); !got {
		t.Fatalf("RecursivePrs() = %v, want true", got)
	}
	if got := iface.AnnouncesFromInternal(); got {
		t.Fatalf("AnnouncesFromInternal() = %v, want false", got)
	}
	if got := iface.AnnouncesToInternal(); got == nil || !*got {
		t.Fatalf("AnnouncesToInternal() = %v, want &true", got)
	}
}

// TestReticulumInterfaceContractDefaults asserts that when an interface omits
// the Phase 1 contract keys, gravity inherits the instance-wide default_gravity
// and the remaining fields take their Python defaults (recursive_prs=false,
// announces_from_internal=true, announces_to_internal=nil).
func TestReticulumInterfaceContractDefaults(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No
default_gravity = 7

[logging]
loglevel = 4

[interfaces]
[[Default Contract TCP]]
type = TCPServerInterface
interface_enabled = Yes
listen_ip = 127.0.0.1
listen_port = ` + strconv.Itoa(port) + `
interface_mode = gateway
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := r.DefaultGravity(); got != 7 {
		t.Fatalf("DefaultGravity() = %v, want 7", got)
	}

	ifaces := ts.GetInterfaces()
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 interface, got %v", len(ifaces))
	}
	iface := ifaces[0]

	if got := iface.Gravity(); got != 7 {
		t.Fatalf("Gravity() = %v, want 7 (inherited default_gravity)", got)
	}
	if got := iface.RecursivePrs(); got {
		t.Fatalf("RecursivePrs() = %v, want false", got)
	}
	if got := iface.AnnouncesFromInternal(); !got {
		t.Fatalf("AnnouncesFromInternal() = %v, want true", got)
	}
	if got := iface.AnnouncesToInternal(); got != nil {
		t.Fatalf("AnnouncesToInternal() = %v, want nil", got)
	}
}

// TestReticulumDefaultGravityUnsetZero asserts that without a default_gravity
// key the getter returns 0 (Interface.DEFAULT_GRAVITY), matching Python's
// `_default_gravity()` which resolves None to DEFAULT_GRAVITY.
func TestReticulumDefaultGravityUnsetZero(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
[[Unset Gravity TCP]]
type = TCPServerInterface
interface_enabled = Yes
listen_ip = 127.0.0.1
listen_port = ` + strconv.Itoa(port) + `
interface_mode = gateway
`

	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	if got := r.DefaultGravity(); got != 0 {
		t.Fatalf("DefaultGravity() = %v, want 0", got)
	}
	ifaces := ts.GetInterfaces()
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 interface, got %v", len(ifaces))
	}
	if got := ifaces[0].Gravity(); got != 0 {
		t.Fatalf("Gravity() = %v, want 0", got)
	}
}

// TestReticulumAnnounceRateDefaults verifies the announce-rate-control config
// flow (RNS/Reticulum.py:642-653,819-857,938-940,1145-1152; Interface.py:90-92):
// the [reticulum] default_ar_target/penalty/grace keys are parsed, and when
// transport is enabled an interface that omits the per-interface keys inherits
// those defaults (resolving a 0/ unset default_ar_target to the Interface
// class constant DEFAULT_AR_TARGET=3600).
func TestReticulumAnnounceRateDefaults(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No
enable_transport = Yes
default_ar_target = 7200
default_ar_penalty = 30
default_ar_grace = 8

[logging]
loglevel = 4

[interfaces]
[[AR Defaults TCP]]
type = TCPServerInterface
interface_enabled = Yes
listen_ip = 127.0.0.1
listen_port = ` + strconv.Itoa(port) + `
interface_mode = gateway
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
	iface := ifaces[0]
	if got := iface.AnnounceRateTarget(); got == nil || *got != 7200 {
		t.Fatalf("AnnounceRateTarget() = %v, want &7200 (inherit default_ar_target)", ptrVal(got))
	}
	if got := iface.AnnounceRatePenalty(); got == nil || *got != 30 {
		t.Fatalf("AnnounceRatePenalty() = %v, want &30 (inherit default_ar_penalty)", ptrVal(got))
	}
	if got := iface.AnnounceRateGrace(); got == nil || *got != 8 {
		t.Fatalf("AnnounceRateGrace() = %v, want &8 (inherit default_ar_grace)", ptrVal(got))
	}
}

// TestReticulumAnnounceRateZeroTargetResolvesClassDefault asserts that
// default_ar_target = 0 maps to None (Reticulum.py:644) and so resolves to the
// Interface.DEFAULT_AR_TARGET class constant (3600) when transport is enabled.
func TestReticulumAnnounceRateZeroTargetResolvesClassDefault(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No
enable_transport = Yes
default_ar_target = 0

[logging]
loglevel = 4

[interfaces]
[[AR Zero TCP]]
type = TCPServerInterface
interface_enabled = Yes
listen_ip = 127.0.0.1
listen_port = ` + strconv.Itoa(port) + `
interface_mode = gateway
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
	iface := ifaces[0]
	if got := iface.AnnounceRateTarget(); got == nil || *got != 3600 {
		t.Fatalf("AnnounceRateTarget() = %v, want &3600 (DEFAULT_AR_TARGET class constant)", ptrVal(got))
	}
}

// TestReticulumAnnounceRatePerInterfaceOverride asserts the per-interface
// announce_rate_target/penalty/grace keys override the instance-wide defaults
// (Reticulum.py:819-829,938-940), and that setting only announce_rate_target
// fills grace and penalty with 0 (Reticulum.py:831-832).
func TestReticulumAnnounceRatePerInterfaceOverride(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No
enable_transport = Yes
default_ar_target = 7200
default_ar_penalty = 30
default_ar_grace = 8

[logging]
loglevel = 4

[interfaces]
[[AR Override TCP]]
type = TCPServerInterface
interface_enabled = Yes
listen_ip = 127.0.0.1
listen_port = ` + strconv.Itoa(port) + `
interface_mode = gateway
announce_rate_target = 1800
announce_rate_penalty = 12
announce_rate_grace = 3
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
	iface := ifaces[0]
	if got := iface.AnnounceRateTarget(); got == nil || *got != 1800 {
		t.Fatalf("AnnounceRateTarget() = %v, want &1800 (per-interface override)", ptrVal(got))
	}
	if got := iface.AnnounceRatePenalty(); got == nil || *got != 12 {
		t.Fatalf("AnnounceRatePenalty() = %v, want &12 (per-interface override)", ptrVal(got))
	}
	if got := iface.AnnounceRateGrace(); got == nil || *got != 3 {
		t.Fatalf("AnnounceRateGrace() = %v, want &3 (per-interface override)", ptrVal(got))
	}
}

// TestReticulumAnnounceRateTargetOnlyFillsGracePenaltyZero asserts that when
// only announce_rate_target is set on an interface, grace and penalty default
// to 0 rather than the instance-wide defaults (Reticulum.py:831-832).
func TestReticulumAnnounceRateTargetOnlyFillsGracePenaltyZero(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No
enable_transport = Yes
default_ar_target = 7200
default_ar_penalty = 30
default_ar_grace = 8

[logging]
loglevel = 4

[interfaces]
[[AR Target Only TCP]]
type = TCPServerInterface
interface_enabled = Yes
listen_ip = 127.0.0.1
listen_port = ` + strconv.Itoa(port) + `
interface_mode = gateway
announce_rate_target = 1800
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
	iface := ifaces[0]
	if got := iface.AnnounceRateTarget(); got == nil || *got != 1800 {
		t.Fatalf("AnnounceRateTarget() = %v, want &1800", ptrVal(got))
	}
	if got := iface.AnnounceRateGrace(); got == nil || *got != 0 {
		t.Fatalf("AnnounceRateGrace() = %v, want &0 (filled because target set)", ptrVal(got))
	}
	if got := iface.AnnounceRatePenalty(); got == nil || *got != 0 {
		t.Fatalf("AnnounceRatePenalty() = %v, want &0 (filled because target set)", ptrVal(got))
	}
}

// TestReticulumAnnounceRateNoDefaultWhenTransportDisabled asserts that the
// announce-rate defaults are NOT applied when transport is disabled
// (Reticulum.py:855-857 only fills None values when transport_enabled()). The
// interface's announce-rate pointers remain nil.
func TestReticulumAnnounceRateNoDefaultWhenTransportDisabled(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No
enable_transport = No
default_ar_target = 7200

[logging]
loglevel = 4

[interfaces]
[[AR No Transport TCP]]
type = TCPServerInterface
interface_enabled = Yes
listen_ip = 127.0.0.1
listen_port = ` + strconv.Itoa(port) + `
interface_mode = gateway
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
	iface := ifaces[0]
	if got := iface.AnnounceRateTarget(); got != nil {
		t.Fatalf("AnnounceRateTarget() = %v, want nil (no default fill with transport disabled)", ptrVal(got))
	}
	if got := iface.AnnounceRateGrace(); got != nil {
		t.Fatalf("AnnounceRateGrace() = %v, want nil", ptrVal(got))
	}
	if got := iface.AnnounceRatePenalty(); got != nil {
		t.Fatalf("AnnounceRatePenalty() = %v, want nil", ptrVal(got))
	}
}

func ptrVal(p *int) string {
	if p == nil {
		return "nil"
	}
	return strconv.Itoa(*p)
}

func TestReticulumBootstrapOnlyInterfaceConfig(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
	port := reserveTCPPort(t)
	config := `[reticulum]
share_instance = No
autoconnect_discovered_interfaces = 2

[logging]
loglevel = 4

[interfaces]
[[Bootstrap Backbone]]
type = BackboneInterface
interface_enabled = Yes
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

	ifaces := ts.GetInterfaces()
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 interface, got %v", len(ifaces))
	}

	getter, ok := ifaces[0].(interface{ BootstrapOnly() bool })
	if !ok {
		t.Fatalf("interface %T does not expose BootstrapOnly()", ifaces[0])
	}
	if !getter.BootstrapOnly() {
		t.Fatal("expected bootstrap_only interface metadata to be preserved from config")
	}
}

func TestReticulumOptionParityDiscoveryValueNonPositiveClears(t *testing.T) {
	t.Parallel()

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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

	configDir := testutils.TempDir(t, tempDirPrefix)
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
