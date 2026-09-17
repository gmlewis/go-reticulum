// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gmlewis/go-reticulum/bot"
	"github.com/gmlewis/go-reticulum/rrc/toml"
)

// Configuration defaults and file names. Every one of these appears in the
// generated config.toml, so an operator can read the defaults rather than
// having to remember them.
const (
	// DefaultCallsign is the name a fresh appliance answers to on the mesh.
	// It is deliberately obviously fictional: an operator must replace it
	// before transmitting, and a stranger who hears it knows it is a default.
	DefaultCallsign = "GRL-NOMAD"
	// DefaultPortalAddr binds the survival dashboard to the loopback
	// interface, so a fresh install serves the dashboard to a browser on the
	// same workstation without exposing it to the local network. Set it to
	// "0.0.0.0:9111" to let a smartphone on the same Wi-Fi reach it.
	DefaultPortalAddr = "127.0.0.1:9111"
	// DefaultGNSSBaud and DefaultCompassBaud are the conventional NMEA-0183
	// line speeds of a USB-UART GNSS receiver and electronic compass.
	DefaultGNSSBaud    = 9600
	DefaultCompassBaud = 9600

	// configFileName is the configuration file inside the appliance's home.
	configFileName = "config.toml"
	// homeEnvVar overrides the appliance's home directory, which is how a
	// test and a second appliance on one workstation stay isolated.
	homeEnvVar = "GRL_HOME"
	// homeDirName is the appliance's home directory under the user's home.
	homeDirName = ".grl"
	// storageDirName holds the appliance's own state under its home.
	storageDirName = "storage"
)

// Config is the appliance's configuration: one file that describes the device,
// the dashboard, the two sensors, the Reticulum stack, and the mesh rooms. The
// struct tags name the keys of config.toml, which is a TOML document parsed by
// the in-repo, stdlib-only rrc/toml package.
//
// The file is deliberately the same shape on a desktop workstation and on the
// ESP32-C5 target, so one configuration drives both: a section whose port is
// empty is simply a peripheral that is not attached, and the static fallback in
// the same section keeps the field tools answering while it is unplugged.
type Config struct {
	// Device names the appliance and says where it keeps its own state.
	Device struct {
		Callsign   string `toml:"callsign"`
		Nickname   string `toml:"nickname"`
		StorageDir string `toml:"storage_dir"`
	} `toml:"device"`
	// Portal is the captive survival dashboard's HTTP listener.
	Portal struct {
		PortalAddr string `toml:"portal_addr"`
	} `toml:"portal"`
	// GNSS is the NMEA-0183 position receiver, or a static fix.
	GNSS struct {
		Port      string `toml:"port"`
		Baud      int    `toml:"baud"`
		StaticFix string `toml:"static_fix"`
	} `toml:"gnss"`
	// Compass is the NMEA-0183 electronic compass, or a static heading.
	Compass struct {
		Port          string `toml:"port"`
		Baud          int    `toml:"baud"`
		StaticHeading string `toml:"static_heading"`
	} `toml:"compass"`
	// RNS is the Reticulum stack's own configuration directory.
	RNS struct {
		ConfigPath string `toml:"config_path"`
	} `toml:"rns"`
	// Mesh is the set of RRC rooms this appliance joins when a hub is
	// reachable, and the rooms its own local broker serves.
	Mesh struct {
		Rooms []string `toml:"rooms"`
	} `toml:"mesh"`
}

// DefaultRooms returns the rooms a fresh appliance joins. It returns a fresh
// slice, so a caller can edit the result without changing the default.
func DefaultRooms() []string {
	return []string{"general", "emergency"}
}

// DefaultHome returns the appliance's state directory: the value of GRL_HOME
// when it is set and not blank (used literally, with no expansion), and
// ~/.grl otherwise.
func DefaultHome() string {
	if home := strings.TrimSpace(os.Getenv(homeEnvVar)); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join("~", homeDirName)
	}
	return filepath.Join(home, homeDirName)
}

// DefaultConfigPath returns the configuration file a fresh appliance uses:
// <home>/config.toml, where home is DefaultHome.
func DefaultConfigPath() string {
	return filepath.Join(DefaultHome(), configFileName)
}

// DefaultConfig returns the configuration an appliance uses when its file says
// nothing: the documented callsign, the dashboard on the loopback interface,
// the conventional NMEA line speeds, state under the appliance's home, and the
// two rooms a Lone Worker is expected to be reachable in.
func DefaultConfig() Config {
	cfg := Config{}
	cfg.Device.Callsign = DefaultCallsign
	cfg.Device.StorageDir = filepath.Join(DefaultHome(), storageDirName)
	cfg.Portal.PortalAddr = DefaultPortalAddr
	cfg.GNSS.Baud = DefaultGNSSBaud
	cfg.Compass.Baud = DefaultCompassBaud
	cfg.Mesh.Rooms = DefaultRooms()
	return cfg
}

// AdvertisedName returns the name the appliance announces: the nickname when
// the operator set one, and the callsign otherwise. A nickname is a convenience
// for a household sharing one callsign, never a replacement for it.
func (c Config) AdvertisedName() string {
	if name := strings.TrimSpace(c.Device.Nickname); name != "" {
		return name
	}
	return strings.TrimSpace(c.Device.Callsign)
}

// LoadConfig reads and validates the configuration at path. It starts from
// DefaultConfig, so a file that names only one option keeps the documented
// default for every other one; a key that is absent is left alone, while a key
// that is present but empty is honoured (an empty portal_addr really does turn
// the dashboard off).
//
// A leading ~ in path, storage_dir, config_path, or either device port expands
// to the user's home directory.
func LoadConfig(path string) (Config, error) {
	resolved := expandHome(path)
	if strings.TrimSpace(resolved) == "" {
		return Config{}, errors.New("no configuration file path")
	}
	src, err := os.ReadFile(resolved)
	if err != nil {
		return Config{}, fmt.Errorf("reading %v: %w", resolved, err)
	}

	cfg := DefaultConfig()
	doc, err := toml.Parse(string(src))
	if err != nil {
		return Config{}, fmt.Errorf("parsing %v: %w", resolved, err)
	}
	if err := decodeConfig(doc.Root(), &cfg); err != nil {
		return Config{}, fmt.Errorf("%v: %w", resolved, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%v: %w", resolved, err)
	}
	return cfg, nil
}

// EnsureConfigFile returns the configuration at path, creating it with
// documented defaults when it does not exist yet. A file that already exists is
// never rewritten: an operator's edits are the only source of truth. The
// generated file is parsed back before it is returned, so a template that
// stopped matching the decoder fails here rather than at the next start-up.
func EnsureConfigFile(path string) (Config, error) {
	resolved := expandHome(path)
	if strings.TrimSpace(resolved) == "" {
		return Config{}, errors.New("no configuration file path")
	}
	if _, err := os.Stat(resolved); errors.Is(err, os.ErrNotExist) {
		if dir := filepath.Dir(resolved); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return Config{}, fmt.Errorf("creating %v: %w", dir, err)
			}
		}
		template := defaultConfigContent(DefaultConfig())
		if err := os.WriteFile(resolved, []byte(template), 0o600); err != nil {
			return Config{}, fmt.Errorf("creating %v: %w", resolved, err)
		}
	} else if err != nil {
		return Config{}, fmt.Errorf("reading %v: %w", resolved, err)
	}
	return LoadConfig(resolved)
}

// Validate reports whether a configuration could bring up an appliance. It
// checks the values a typo would otherwise turn into a silent failure at
// start-up: an unusable listen address, a line speed that is not a speed, and a
// static fallback that is not a position or a bearing.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Device.Callsign) == "" {
		return errors.New("device.callsign must not be empty")
	}
	if err := validatePortalAddr(c.Portal.PortalAddr); err != nil {
		return fmt.Errorf("portal.portal_addr: %w", err)
	}
	if c.GNSS.Baud <= 0 {
		return fmt.Errorf("gnss.baud: want a positive line speed, got %v", c.GNSS.Baud)
	}
	if c.Compass.Baud <= 0 {
		return fmt.Errorf("compass.baud: want a positive line speed, got %v", c.Compass.Baud)
	}
	if fix := strings.TrimSpace(c.GNSS.StaticFix); fix != "" {
		if _, err := bot.ParseLocation(fix); err != nil {
			return fmt.Errorf("gnss.static_fix: %w", err)
		}
	}
	if heading := strings.TrimSpace(c.Compass.StaticHeading); heading != "" {
		if _, err := bot.ParseBearing(heading); err != nil {
			return fmt.Errorf("compass.static_heading: %w", err)
		}
	}
	return nil
}

// validatePortalAddr rejects an address the dashboard could not bind. An empty
// address is allowed: it means "run without the dashboard", which is a
// deliberate choice rather than a mistake.
func validatePortalAddr(addr string) error {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return nil
	}
	_, port, err := net.SplitHostPort(trimmed)
	if err != nil {
		return fmt.Errorf("want host:port, like 127.0.0.1:9111 or :9111: %w", err)
	}
	if port == "" {
		return errors.New("want a port, like 127.0.0.1:9111 or :9111")
	}
	numeric, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("want a numeric port, got %q", port)
	}
	if numeric < 0 || numeric > 65535 {
		return fmt.Errorf("port %v is outside the range 0 to 65535", numeric)
	}
	return nil
}

// decodeConfig overlays a parsed document on cfg. Only the keys the document
// actually carries are applied, so an absent key keeps the default and an
// explicit empty value clears it.
func decodeConfig(root *toml.Table, cfg *Config) error {
	device := tableNamed(root, "device")
	if err := decodeString(device, "callsign", &cfg.Device.Callsign); err != nil {
		return err
	}
	if err := decodeString(device, "nickname", &cfg.Device.Nickname); err != nil {
		return err
	}
	if err := decodeString(device, "storage_dir", &cfg.Device.StorageDir); err != nil {
		return err
	}

	portal := tableNamed(root, "portal")
	if err := decodeString(portal, "portal_addr", &cfg.Portal.PortalAddr); err != nil {
		return err
	}

	gnss := tableNamed(root, "gnss")
	if err := decodeString(gnss, "port", &cfg.GNSS.Port); err != nil {
		return err
	}
	if err := decodeInt(gnss, "baud", &cfg.GNSS.Baud); err != nil {
		return err
	}
	if err := decodeString(gnss, "static_fix", &cfg.GNSS.StaticFix); err != nil {
		return err
	}

	compass := tableNamed(root, "compass")
	if err := decodeString(compass, "port", &cfg.Compass.Port); err != nil {
		return err
	}
	if err := decodeInt(compass, "baud", &cfg.Compass.Baud); err != nil {
		return err
	}
	if err := decodeString(compass, "static_heading", &cfg.Compass.StaticHeading); err != nil {
		return err
	}

	rnsTable := tableNamed(root, "rns")
	if err := decodeString(rnsTable, "config_path", &cfg.RNS.ConfigPath); err != nil {
		return err
	}

	mesh := tableNamed(root, "mesh")
	if err := decodeStrings(mesh, "rooms", &cfg.Mesh.Rooms); err != nil {
		return err
	}

	cfg.Device.StorageDir = expandHome(cfg.Device.StorageDir)
	cfg.RNS.ConfigPath = expandHome(cfg.RNS.ConfigPath)
	cfg.GNSS.Port = expandHome(cfg.GNSS.Port)
	cfg.Compass.Port = expandHome(cfg.Compass.Port)
	return nil
}

// tableNamed returns the top-level table with the given name, or nil when the
// document has no such table.
func tableNamed(root *toml.Table, name string) *toml.Table {
	if root == nil {
		return nil
	}
	for _, t := range root.Tables {
		if len(t.Path) == 1 && t.Path[0] == name {
			return t
		}
	}
	return nil
}

// valueOf returns the value of key in table, and whether the table carried it.
func valueOf(t *toml.Table, key string) (toml.Value, bool) {
	if t == nil {
		return toml.Value{}, false
	}
	for _, kv := range t.Keys {
		if kv.IsRaw {
			continue
		}
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return toml.Value{}, false
}

// decodeString applies a string key from table to dst.
func decodeString(t *toml.Table, key string, dst *string) error {
	value, ok := valueOf(t, key)
	if !ok {
		return nil
	}
	if value.Kind != toml.KindString {
		return fmt.Errorf("%v: want a quoted string", keyPath(t, key))
	}
	*dst = value.Str
	return nil
}

// decodeInt applies an integer key from table to dst.
func decodeInt(t *toml.Table, key string, dst *int) error {
	value, ok := valueOf(t, key)
	if !ok {
		return nil
	}
	if value.Kind != toml.KindInt {
		return fmt.Errorf("%v: want an integer", keyPath(t, key))
	}
	*dst = int(value.Int)
	return nil
}

// decodeStrings applies an array-of-strings key from table to dst.
func decodeStrings(t *toml.Table, key string, dst *[]string) error {
	value, ok := valueOf(t, key)
	if !ok {
		return nil
	}
	if value.Kind != toml.KindArray {
		return fmt.Errorf("%v: want an array of strings", keyPath(t, key))
	}
	items := make([]string, 0, len(value.Arr))
	for i, item := range value.Arr {
		if item.Kind != toml.KindString {
			return fmt.Errorf("%v: item %v want a quoted string", keyPath(t, key), i+1)
		}
		items = append(items, item.Str)
	}
	*dst = items
	return nil
}

// keyPath names a key for an error message, qualified by its table when the
// table is known.
func keyPath(t *toml.Table, key string) string {
	if t == nil || len(t.Path) == 0 {
		return key
	}
	return strings.Join(t.Path, ".") + "." + key
}

// expandHome replaces a leading ~ with the user's home directory. A path with
// no leading ~ is returned with its surrounding space trimmed.
func expandHome(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if trimmed != "~" && !strings.HasPrefix(trimmed, "~/") && !strings.HasPrefix(trimmed, `~\`) {
		return trimmed
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return trimmed
	}
	if trimmed == "~" {
		return home
	}
	return filepath.Join(home, trimmed[2:])
}

// defaultConfigContent renders the documentation-bearing config.toml a fresh
// install starts from. Every value is the default DefaultConfig returns, so the
// generated file and the in-memory defaults can never disagree; the surrounding
// comments are what make the file self-explanatory to an operator at 3 a.m.
func defaultConfigContent(cfg Config) string {
	return fmt.Sprintf(`# Go Reticulum Lifesaver (GRL) configuration.
#
# Every value below is the built-in default, so this file can be edited,
# trimmed, or deleted: an appliance with no configuration file at all still
# runs with exactly these values. A leading ~ expands to your home directory.
#
# A peripheral with an empty port is simply not attached. Its static fallback
# keeps the field tools answering while the hardware is unplugged, which is how
# the same configuration runs on a workstation with nothing attached and on the
# ESP32-C5 with everything attached.

[device]
# callsign is the name this appliance answers to on the mesh. It is announced
# to every hub, so replace the fictional default before you transmit.
callsign = %[1]q

# nickname is an optional shorter name used on the air in place of the
# callsign. Empty means "use the callsign".
nickname = ""

# storage_dir holds the appliance's own state: distress beacons, situation
# reports, check-in timers, and saved history.
storage_dir = %[2]q

[portal]
# portal_addr is where the captive survival dashboard listens.
#
#   127.0.0.1:9111  this workstation only (the default)
#   0.0.0.0:9111    this workstation and every phone on the same LAN
#   (empty)         no dashboard at all
#
# Navigating to http://localhost:9111/ serves the mobile dashboard, and the
# captive-probe routes every operating system already probes (/generate_204,
# /hotspot-detect.html, /ncsi.txt, /connecttest.txt, /gen_204) answer with a
# redirect to it, so the same URL works on a SoftAP and on a desk.
portal_addr = %[3]q

[gnss]
# port is the NMEA-0183 position receiver, for example
# /dev/tty.usbserial-0001 on macOS or /dev/ttyUSB0 on Linux Mint.
port = %[4]q

# baud is that device's line speed.
baud = %[5]v

# static_fix is the position to use when no receiver is attached, in any
# notation the whereami command accepts, for example "37.7553,-122.4527".
static_fix = %[6]q

[compass]
# port is the NMEA-0183 electronic compass, for example
# /dev/tty.usbserial-0002 on macOS or /dev/ttyUSB1 on Linux Mint.
port = %[7]q

# baud is that device's line speed.
baud = %[8]v

# static_heading is the magnetic heading to use when no compass is attached,
# in degrees, for example "042".
static_heading = %[9]q

[rns]
# config_path is the Reticulum configuration directory this appliance uses.
# Empty means the Reticulum default, ~/.reticulum.
config_path = %[10]q

[mesh]
# rooms are the RRC rooms this appliance joins when a hub is reachable and
# serves from its own local broker when one is not.
rooms = %[11]v
`,
		cfg.Device.Callsign,
		cfg.Device.StorageDir,
		cfg.Portal.PortalAddr,
		cfg.GNSS.Port,
		cfg.GNSS.Baud,
		cfg.GNSS.StaticFix,
		cfg.Compass.Port,
		cfg.Compass.Baud,
		cfg.Compass.StaticHeading,
		cfg.RNS.ConfigPath,
		formatRooms(cfg.Mesh.Rooms),
	)
}

// formatRooms renders a room list the way TOML writes an array of strings, so
// the generated file is valid TOML without a separate encoder.
func formatRooms(rooms []string) string {
	if len(rooms) == 0 {
		return "[]"
	}
	quoted := make([]string, 0, len(rooms))
	for _, room := range rooms {
		quoted = append(quoted, strconv.Quote(room))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
