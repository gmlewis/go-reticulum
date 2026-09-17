// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// tempDir creates an isolated directory for one test.
//
// t.TempDir is deliberately not used: on macOS it creates a path under a
// directory that is too long for Unix domain sockets, which breaks every test
// that starts a Reticulum instance.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := newTempDir()
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// newTempDir keeps the path short on Darwin, where /tmp is the short base the
// rest of this repository's suites use, and lets the standard library pick the
// base everywhere else. The two branches spell the prefix out literally so the
// /tmp sweeper can recognize it (scripts/clean-test-tmp.sh -c).
func newTempDir() (string, error) {
	if runtime.GOOS == "darwin" {
		return os.MkdirTemp("/tmp", "grl-test-*")
	}
	return os.MkdirTemp("", "grl-test-*")
}

// writeConfig writes text to <dir>/config.toml and returns the path.
func writeConfig(t *testing.T, dir, text string) string {
	t.Helper()
	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// TestDefaultConfig asserts the documented defaults, which are what an
// appliance with no configuration file at all runs.
func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	if cfg.Device.Callsign != DefaultCallsign {
		t.Errorf("Callsign = %q, want %q", cfg.Device.Callsign, DefaultCallsign)
	}
	if cfg.Device.Nickname != "" {
		t.Errorf("Nickname = %q, want the empty default", cfg.Device.Nickname)
	}
	if cfg.Portal.PortalAddr != DefaultPortalAddr {
		t.Errorf("PortalAddr = %q, want %q", cfg.Portal.PortalAddr, DefaultPortalAddr)
	}
	if cfg.GNSS.Baud != DefaultGNSSBaud {
		t.Errorf("GNSS.Baud = %v, want %v", cfg.GNSS.Baud, DefaultGNSSBaud)
	}
	if cfg.Compass.Baud != DefaultCompassBaud {
		t.Errorf("Compass.Baud = %v, want %v", cfg.Compass.Baud, DefaultCompassBaud)
	}
	if got, want := cfg.Mesh.Rooms, DefaultRooms(); !reflect.DeepEqual(got, want) {
		t.Errorf("Mesh.Rooms = %v, want %v", got, want)
	}
	if got, want := cfg.Device.StorageDir, filepath.Join(DefaultHome(), storageDirName); got != want {
		t.Errorf("StorageDir = %q, want %q", got, want)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("the documented defaults do not validate: %v", err)
	}

	// The rooms a caller gets are its own, so editing them cannot change the
	// default every later appliance starts from.
	rooms := DefaultRooms()
	rooms[0] = "compromised"
	if again := DefaultRooms(); again[0] != "general" {
		t.Error("DefaultRooms returns a shared slice that a caller can mutate")
	}
}

// TestDefaultPathsHonorGRLHome asserts the appliance's home, and therefore its
// configuration and storage paths, follow GRL_HOME so that a test and a second
// appliance on one workstation never touch ~/.grl.
func TestDefaultPathsHonorGRLHome(t *testing.T) {
	dir := tempDir(t)
	t.Setenv(homeEnvVar, dir)

	if got := DefaultHome(); got != dir {
		t.Errorf("DefaultHome() = %q, want %q", got, dir)
	}
	if got, want := DefaultConfigPath(), filepath.Join(dir, configFileName); got != want {
		t.Errorf("DefaultConfigPath() = %q, want %q", got, want)
	}
	if got, want := DefaultConfig().Device.StorageDir, filepath.Join(dir, storageDirName); got != want {
		t.Errorf("StorageDir = %q, want %q", got, want)
	}
}

// TestEnsureConfigFileCreatesDocumentedDefaults asserts a first run leaves an
// operator a file they can read and edit, and that the file it writes parses
// back to exactly the in-memory defaults.
func TestEnsureConfigFileCreatesDocumentedDefaults(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, "nested", configFileName)

	cfg, err := EnsureConfigFile(path)
	if err != nil {
		t.Fatalf("EnsureConfigFile: %v", err)
	}
	if want := DefaultConfig(); !reflect.DeepEqual(cfg, want) {
		t.Errorf("EnsureConfigFile = %+v,\nwant the in-memory defaults %+v", cfg, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"# Go Reticulum Lifesaver (GRL) configuration.",
		"[device]", "[portal]", "[gnss]", "[compass]", "[rns]", "[mesh]",
		"callsign", "nickname", "storage_dir", "portal_addr",
		"static_fix", "static_heading", "config_path", "rooms",
		"0.0.0.0:9111", "hotspot-detect.html", "generate_204",
		"/dev/tty.usbserial-0001", "/dev/ttyUSB0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the generated config.toml does not mention %q:\n%v", want, text)
		}
	}

	// The file is created with a mode that does not expose it to other users,
	// because a mesh configuration is nobody else's business.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config.toml mode = %v, want 0600", perm)
	}
}

// TestEnsureConfigFileNeverRewritesAnOperatorsFile asserts a second run leaves
// an edited file exactly as it found it: the operator's choices are the only
// source of truth.
func TestEnsureConfigFileNeverRewritesAnOperatorsFile(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, configFileName)
	edited := `[device]
callsign = "GRL-EDITED"

[portal]
portal_addr = "0.0.0.0:9111"
`
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := EnsureConfigFile(path)
	if err != nil {
		t.Fatalf("EnsureConfigFile: %v", err)
	}
	if cfg.Device.Callsign != "GRL-EDITED" {
		t.Errorf("Callsign = %q, want the operator's value", cfg.Device.Callsign)
	}
	if cfg.Portal.PortalAddr != "0.0.0.0:9111" {
		t.Errorf("PortalAddr = %q, want the operator's value", cfg.Portal.PortalAddr)
	}
	// The rest still comes from the defaults.
	if got, want := cfg.Mesh.Rooms, DefaultRooms(); !reflect.DeepEqual(got, want) {
		t.Errorf("Mesh.Rooms = %v, want the defaults %v", got, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != edited {
		t.Errorf("EnsureConfigFile rewrote an existing file:\n%v", string(data))
	}
}

// TestLoadConfigReadsEveryKey asserts a fully specified file reaches every
// field, so a documented option that is silently ignored is a visible failure.
func TestLoadConfigReadsEveryKey(t *testing.T) {
	dir := tempDir(t)
	path := writeConfig(t, dir, `[device]
callsign = "GRL-7"
nickname = "lifesaver"
storage_dir = "`+dir+`/state"

[portal]
portal_addr = "0.0.0.0:9111"

[gnss]
port = "`+dir+`/gps"
baud = 38400
static_fix = "37.7553,-122.4527"

[compass]
port = "`+dir+`/compass"
baud = 4800
static_heading = "042"

[rns]
config_path = "`+dir+`/rns"

[mesh]
rooms = ["general", "emergency", "ops"]
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Device.Callsign != "GRL-7" {
		t.Errorf("Callsign = %q", cfg.Device.Callsign)
	}
	if cfg.Device.Nickname != "lifesaver" {
		t.Errorf("Nickname = %q", cfg.Device.Nickname)
	}
	if want := filepath.Join(dir, "state"); cfg.Device.StorageDir != want {
		t.Errorf("StorageDir = %q, want %q", cfg.Device.StorageDir, want)
	}
	if cfg.Portal.PortalAddr != "0.0.0.0:9111" {
		t.Errorf("PortalAddr = %q", cfg.Portal.PortalAddr)
	}
	if want := filepath.Join(dir, "gps"); cfg.GNSS.Port != want {
		t.Errorf("GNSS.Port = %q, want %q", cfg.GNSS.Port, want)
	}
	if cfg.GNSS.Baud != 38400 {
		t.Errorf("GNSS.Baud = %v", cfg.GNSS.Baud)
	}
	if cfg.GNSS.StaticFix != "37.7553,-122.4527" {
		t.Errorf("GNSS.StaticFix = %q", cfg.GNSS.StaticFix)
	}
	if want := filepath.Join(dir, "compass"); cfg.Compass.Port != want {
		t.Errorf("Compass.Port = %q, want %q", cfg.Compass.Port, want)
	}
	if cfg.Compass.Baud != 4800 {
		t.Errorf("Compass.Baud = %v", cfg.Compass.Baud)
	}
	if cfg.Compass.StaticHeading != "042" {
		t.Errorf("Compass.StaticHeading = %q", cfg.Compass.StaticHeading)
	}
	if want := filepath.Join(dir, "rns"); cfg.RNS.ConfigPath != want {
		t.Errorf("RNS.ConfigPath = %q, want %q", cfg.RNS.ConfigPath, want)
	}
	if got, want := cfg.Mesh.Rooms, []string{"general", "emergency", "ops"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Mesh.Rooms = %v, want %v", got, want)
	}
}

// TestLoadConfigKeepsDefaultsForAbsentKeys asserts a file that names one option
// keeps the documented default for every other one, which is what makes a
// one-line configuration usable.
func TestLoadConfigKeepsDefaultsForAbsentKeys(t *testing.T) {
	dir := tempDir(t)
	path := writeConfig(t, dir, "[portal]\nportal_addr = \"127.0.0.1:9199\"\n")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	want := DefaultConfig()
	want.Portal.PortalAddr = "127.0.0.1:9199"
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("LoadConfig = %+v,\nwant %+v", cfg, want)
	}
}

// TestLoadConfigHonoursAnExplicitEmptyValue asserts an empty value really does
// turn a subsystem off, rather than being mistaken for an absent key.
func TestLoadConfigHonoursAnExplicitEmptyValue(t *testing.T) {
	dir := tempDir(t)
	path := writeConfig(t, dir, `[portal]
portal_addr = ""

[gnss]
port = ""
static_fix = ""
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Portal.PortalAddr != "" {
		t.Errorf("PortalAddr = %q, want the dashboard off", cfg.Portal.PortalAddr)
	}
	if cfg.GNSS.Port != "" || cfg.GNSS.StaticFix != "" {
		t.Errorf("GNSS = %+v, want no receiver and no static fix", cfg.GNSS)
	}
}

// TestLoadConfigExpandsHome asserts a leading ~ in a path or a device name
// expands to the user's home directory, so a configuration file can be shared
// between machines with different user names.
func TestLoadConfigExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no user home directory on this machine")
	}
	dir := tempDir(t)
	path := writeConfig(t, dir, `[device]
storage_dir = "~/.grl/state"

[rns]
config_path = "~/reticulum"

[gnss]
port = "~/dev/gps"
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if want := filepath.Join(home, ".grl", "state"); cfg.Device.StorageDir != want {
		t.Errorf("StorageDir = %q, want %q", cfg.Device.StorageDir, want)
	}
	if want := filepath.Join(home, "reticulum"); cfg.RNS.ConfigPath != want {
		t.Errorf("ConfigPath = %q, want %q", cfg.RNS.ConfigPath, want)
	}
	if want := filepath.Join(home, "dev", "gps"); cfg.GNSS.Port != want {
		t.Errorf("GNSS.Port = %q, want %q", cfg.GNSS.Port, want)
	}
}

// TestLoadConfigRejectsUnusableValues asserts a typo is refused at load time
// rather than becoming a silent failure at start-up.
func TestLoadConfigRejectsUnusableValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "portal address without a port",
			text: "[portal]\nportal_addr = \"127.0.0.1\"\n",
			want: "portal.portal_addr",
		},
		{
			name: "portal port that is not a number",
			text: "[portal]\nportal_addr = \"127.0.0.1:http\"\n",
			want: "portal.portal_addr",
		},
		{
			name: "portal port out of range",
			text: "[portal]\nportal_addr = \"127.0.0.1:99999\"\n",
			want: "portal.portal_addr",
		},
		{
			name: "line speed of zero",
			text: "[gnss]\nbaud = 0\n",
			want: "gnss.baud",
		},
		{
			name: "negative compass speed",
			text: "[compass]\nbaud = -9600\n",
			want: "compass.baud",
		},
		{
			name: "line speed that is not a number",
			text: "[gnss]\nbaud = \"fast\"\n",
			want: "gnss.baud",
		},
		{
			name: "static fix that is not a place",
			text: "[gnss]\nstatic_fix = \"somewhere north\"\n",
			want: "gnss.static_fix",
		},
		{
			name: "static heading that is not a bearing",
			text: "[compass]\nstatic_heading = \"sideways\"\n",
			want: "compass.static_heading",
		},
		{
			name: "empty callsign",
			text: "[device]\ncallsign = \"\"\n",
			want: "device.callsign",
		},
		{
			name: "rooms that are not strings",
			text: "[mesh]\nrooms = [1, 2]\n",
			want: "mesh.rooms",
		},
		{
			name: "rooms that are not an array",
			text: "[mesh]\nrooms = \"general\"\n",
			want: "mesh.rooms",
		},
		{
			name: "callsign that is not a string",
			text: "[device]\ncallsign = 7\n",
			want: "device.callsign",
		},
		{
			name: "unparseable document",
			text: "[device\ncallsign = \"x\"\n",
			want: "parsing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := tempDir(t)
			path := writeConfig(t, dir, tc.text)
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatalf("LoadConfig accepted %v", tc.text)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// TestLoadConfigRejectsAMissingFile asserts a missing file is an error rather
// than a silent fall back to the defaults: a mistyped --config path would
// otherwise bring up an appliance that ignores every choice the operator made.
func TestLoadConfigRejectsAMissingFile(t *testing.T) {
	t.Parallel()

	dir := tempDir(t)
	if _, err := LoadConfig(filepath.Join(dir, "absent.toml")); err == nil {
		t.Fatal("LoadConfig accepted a missing file")
	}
	if _, err := LoadConfig(""); err == nil {
		t.Fatal("LoadConfig accepted an empty path")
	}
}

// TestConfigAdvertisedName asserts the name the appliance announces: the
// nickname when one was set, and the callsign otherwise.
func TestConfigAdvertisedName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		callsign string
		nickname string
		want     string
	}{
		{"callsign alone", "GRL-7", "", "GRL-7"},
		{"nickname wins", "GRL-7", "lifesaver", "lifesaver"},
		{"blank nickname falls back", "GRL-7", "   ", "GRL-7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cfg Config
			cfg.Device.Callsign = tc.callsign
			cfg.Device.Nickname = tc.nickname
			if got := cfg.AdvertisedName(); got != tc.want {
				t.Errorf("AdvertisedName() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDefaultConfigContentRoundTrips asserts the generated template loads back
// to the values it was generated from, so the file an operator is handed always
// describes the appliance they will get.
func TestDefaultConfigContentRoundTrips(t *testing.T) {
	t.Parallel()

	dir := tempDir(t)
	cfg := DefaultConfig()
	path := writeConfig(t, dir, defaultConfigContent(cfg))
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v\n%v", err, defaultConfigContent(cfg))
	}
	if !reflect.DeepEqual(loaded, cfg) {
		t.Errorf("the template round trips to %+v,\nwant %+v", loaded, cfg)
	}
}
