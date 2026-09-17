// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file decodes ~/.gorrcbot/config.toml into typed configuration. The
// parser is the in-repo, stdlib-only rrc/toml package — the same one rrcd.toml
// and rooms.toml use — which preserves raw text and exposes values by kind
// rather than through struct tags, so the decoding below is explicit.

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/gmlewis/go-reticulum/rrc"
	"github.com/gmlewis/go-reticulum/rrc/toml"
)

// Reply modes for the [bot] reply key.
const (
	// ReplyAuto answers a room request in the room, and a request that arrived
	// as a direct NOTICE with a direct NOTICE. A hub never publishes another
	// client's capabilities, so the room is the one route a room asker is known
	// to be able to read.
	ReplyAuto = "auto"
	// ReplyDirect always sends a direct NOTICE, staying silent when the hub
	// cannot deliver one.
	ReplyDirect = "direct"
	// ReplyRoom always sends an in-room NOTICE.
	ReplyRoom = "room"
)

// Bot configuration defaults.
const (
	// DefaultNick is both the advertised nick and the default trigger nick.
	DefaultNick = "gorrcbot"
	// DefaultTriggerNick is the last-resort trigger nick, used when neither
	// the config nor a per-hub/per-room override names one.
	DefaultTriggerNick = "gorrcbot"
	// DefaultCooldownSecs is the minimum gap between replies to one identity.
	DefaultCooldownSecs = 8.0
	// DefaultMaxReplyLines bounds how many NOTICE lines one reply may produce.
	DefaultMaxReplyLines = 12
	// DefaultLXMFAnnounceMinutes is how often the bot re-announces its own
	// lxmf.delivery destination. Six hours is deliberately unhurried: the
	// announce exists so a peer can learn where to send a reply, not to keep a
	// path warm.
	DefaultLXMFAnnounceMinutes = 360
)

// HubDestinationHexLen is the length of a hub destination hash in hexadecimal
// characters (16 bytes).
const HubDestinationHexLen = rrc.IdentityHashLen * 2

// BotConfig is the decoded gorrcbot configuration.
type BotConfig struct {
	// IdentityPath is the Reticulum identity file shared by every hub.
	IdentityPath string
	// Nick is the global advertised and trigger nick.
	Nick string
	// Reply is one of ReplyAuto, ReplyDirect, or ReplyRoom.
	Reply string
	// CooldownSecs is the per-identity reply cooldown.
	CooldownSecs float64
	// AnnounceOnJoin posts one self-introduction NOTICE per room per session.
	// It is OFF by default: an always-on bot reconnects often, and each
	// reconnect re-introduced it into every room, so a room filled up with
	// "I am gorrbot, an RRC bot …" lines nobody asked for. Left off, the bot
	// behaves like any other member — it joins, leaves, and speaks only when
	// addressed. Set announce_on_join = true to publish the greeting.
	AnnounceOnJoin bool
	// MaxReplyLines bounds the NOTICE lines a single reply may produce.
	MaxReplyLines int
	// MicronLinks renders the discovery commands' rows as clickable Micron
	// links, the notation a NomadNet client turns into a button and every other
	// client shows literally. It is OFF by default: an RRC NOTICE is plain text
	// to every reader except a NomadNet one.
	MicronLinks bool
	// StorageDir is the RRC client's storage directory for saved history.
	StorageDir string
	// LaunchURL is an optional provider template for launches; {mode} and
	// {limit} are substituted after validation, and the window is "upcoming" or
	// "previous". Empty disables the commands, which then say so.
	LaunchURL string
	// FlightURL is an optional provider template for flight; {flight} is replaced
	// with the requested flight number. Empty disables the command, which then
	// says so.
	FlightURL string
	// FlightRouteURL is an optional second provider template for flight, asked
	// first to turn the number a passenger knows into the radio callsign the live
	// feed uses, and to name the airline and the two airports. Empty means the
	// command reports the live state alone.
	FlightRouteURL string
	// WeatherURL is an optional provider template for weather/wx; {place} is
	// replaced with the requested place. It must be an absolute http:// or
	// https:// URL carrying no credentials, and the place is validated against
	// an allowlist before it is substituted, so a request can never retarget
	// the template's own host. Empty disables the commands.
	WeatherURL string
	// SpaceWeatherURL is an optional provider URL for the spacewx command. It
	// is an absolute http:// or https:// URL carrying no credentials and no
	// substitution tokens, and its answer is read as JSON for the solar flux
	// index, the sunspot number, and the K-index. Empty disables the fetch, and
	// the command then reports the last reading it has, or says how to turn it
	// on.
	SpaceWeatherURL string
	// KJVTxtFile is the path to a King James Version Bible text file, one verse
	// per line in the reference data's shape ("John3:16 For God so loved..."),
	// which is what the kjv command looks verses up in and searches. The file is
	// opened read-only and parsed lazily, on the first kjv command. Empty
	// disables the command, which then says how to turn it on.
	KJVTxtFile string
	// TowersPath is the path to an optional local tower dataset in the shape
	// tower.go documents ("id,name,type,lat,lng,freq,...,country,elev"), which
	// the tower command merges over its embedded cell and repeater catalog: a
	// row whose id matches an embedded one replaces it, and a new id is added.
	// The file is opened read-only and parsed once, on the first tower command.
	// An absent file is normal, and empty means the embedded catalog alone.
	TowersPath string
	// TideURL is an optional provider template for the tide command. It must
	// carry both {place} (the station id) and {date} (YYYYMMDD), and be an
	// absolute http:// or https:// URL with no credentials. Empty disables the
	// command, which then says how to turn it on.
	TideURL string
	// BuoyURL is an optional provider template for the buoy command; {place} is
	// replaced with the requested buoy id. Empty disables the command.
	BuoyURL string
	// RiverURL is an optional provider template for the river command; {place}
	// is replaced with the requested gauge id. Empty disables the command.
	RiverURL string
	// RiverFloodURL is an optional provider template for the river command's
	// flood thresholds; it is asked with the same gauge id. Empty means the
	// command reports the stage and flow without a flood comparison.
	RiverFloodURL string
	// MetarURL is an optional provider template for the metar command; {place}
	// is replaced with the requested ICAO station code. It must be an absolute
	// http:// or https:// URL carrying no credentials. Empty disables the
	// command, which then says how to turn it on.
	MetarURL string
	// WeatherAlertURL is an optional provider template for the wxalert command;
	// {place} is replaced with the requested place or area. It must be an
	// absolute http:// or https:// URL carrying no credentials. Empty disables
	// the command, which then says how to turn it on.
	WeatherAlertURL string
	// LXMFEnabled switches the msg command on. It is OFF by default, and off
	// means absent: no LXMF router is created, no state is written under the
	// storage directory, and the command answers with the line that says how to
	// turn it on.
	LXMFEnabled bool
	// LXMFPropagationNode is the lxmf.propagation destination hash of a
	// store-and-forward node, lowercase hexadecimal, empty when none is
	// configured. The router never discovers a node by itself, so without one
	// there is no store-and-forward at all and a message to a peer with no path
	// fails visibly instead of waiting.
	LXMFPropagationNode string
	// LXMFPropagationNodeHash is LXMFPropagationNode decoded to bytes.
	LXMFPropagationNodeHash []byte
	// LXMFAnnounceMinutes is how often the bot announces its own lxmf.delivery
	// destination, so a peer can route a reply back to it.
	LXMFAnnounceMinutes int
	// EmergencyLXMFDestination is the lxmf.delivery destination hash of an
	// emergency dispatch destination, lowercase hexadecimal, empty when none is
	// configured. With LXMF enabled, a new distress beacon is also queued to
	// it: LXMF is store-and-forward, so that copy keeps trying after the local
	// link has failed.
	EmergencyLXMFDestination string
	// EmergencyLXMFDestinationHash is EmergencyLXMFDestination decoded to bytes.
	EmergencyLXMFDestinationHash []byte
	// PortalAddr is the captive web portal's listen address, like
	// "127.0.0.1:8080" or ":80". Empty disables the portal entirely: a bot
	// that never asked for an HTTP listener never binds one.
	PortalAddr string
	// GPSPort is the device or file the GNSS receiver's NMEA sentences are
	// read from, like /dev/ttyUSB0 or /dev/ttyACM0. Empty means no streaming
	// receiver.
	GPSPort string
	// GPSFix is a static position for a node with no receiver — a headless
	// installation, a fixed relay, or an operator rehearsing the field tools.
	// It accepts every notation the location commands accept. Empty means the
	// node has no position unless a receiver supplies one.
	GPSFix string
	// Hubs are the RRC hubs to dial, in configuration order.
	Hubs []HubConfig
}

// HubConfig is one [[hubs]] entry.
type HubConfig struct {
	// Name is the display name, unique within the configuration.
	Name string
	// Destination is the hub's rrc.hub destination hash, lowercase hex.
	Destination string
	// DestHash is Destination decoded to bytes.
	DestHash []byte
	// Nick is this hub's advertised-nick override; empty means use the
	// global [bot] nick.
	Nick string
	// Rooms are the rooms to join, in configuration order.
	Rooms []RoomConfig
	// RespondTo maps a lowercased room name to the nick the bot answers to in
	// that room, overriding the hub and global nicks.
	RespondTo map[string]string
}

// RoomConfig is one room within a hub's rooms list.
type RoomConfig struct {
	// Name is the lowercased room name.
	Name string
	// Key is the room key for a keyed (+k) room, empty otherwise.
	Key string
}

// AdvertisedNick returns the nick this hub sends in HELLO: the hub override,
// else the global nick, else DefaultNick.
func (c *BotConfig) AdvertisedNick(hub *HubConfig) string {
	if hub != nil && hub.Nick != "" {
		return hub.Nick
	}
	if c.Nick != "" {
		return c.Nick
	}
	return DefaultNick
}

// TriggerNick returns the nick the bot answers to in the given room. The
// resolution order is respond_to[room], then this hub's nick, then the global
// nick, then DefaultTriggerNick. The @<identity-hash-prefix> alias works
// regardless of the trigger nick, which is what makes the bot reachable on a
// hub where its nickname is already taken.
func (c *BotConfig) TriggerNick(hub *HubConfig, room string) string {
	if hub != nil {
		if nick, ok := hub.RespondTo[strings.ToLower(strings.TrimSpace(room))]; ok && nick != "" {
			return nick
		}
		if hub.Nick != "" {
			return hub.Nick
		}
	}
	if c.Nick != "" {
		return c.Nick
	}
	return DefaultTriggerNick
}

// LoadBotConfig reads and decodes the configuration at path. The returned
// warnings describe keys the bot does not understand and non-fatal
// inconsistencies; they are reported to the operator and never fail the load,
// so a configuration written for a newer version still runs.
func LoadBotConfig(path string) (*BotConfig, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %v: %w", path, err)
	}
	return DecodeBotConfig(path, string(data))
}

// DecodeBotConfig decodes configuration text. name is the display name used in
// every error and warning message, so a decode failure points the operator at
// the offending file and key.
func DecodeBotConfig(name, src string) (*BotConfig, []string, error) {
	doc, err := toml.Parse(src)
	if err != nil {
		return nil, nil, fmt.Errorf("%v: invalid TOML: %w", name, err)
	}

	d := &configDecoder{name: name, defaults: DefaultBotPaths()}
	if err := d.decodeBot(); err != nil {
		return nil, nil, err
	}
	root := doc.Root()
	if err := d.decodeRoot(root); err != nil {
		return nil, nil, err
	}
	if botTable := findTable(root, []string{"bot"}); botTable != nil {
		if err := d.decodeBotTable(botTable); err != nil {
			return nil, nil, err
		}
	}
	if err := d.decodeHubs(root); err != nil {
		return nil, nil, err
	}
	if err := d.finish(); err != nil {
		return nil, nil, err
	}
	return &d.cfg, d.warnings, nil
}

// configDecoder carries the decoding state: the document's display name, the
// configuration being built, and the accumulated warnings.
type configDecoder struct {
	name     string
	defaults BotPaths
	cfg      BotConfig
	warnings []string
}

// decodeBot applies the [bot] defaults before the table is read, so an absent
// table still yields a usable configuration. AnnounceOnJoin is deliberately
// absent from the literal: its zero value (false) is the default, and only an
// explicit announce_on_join = true turns the self-introduction back on.
func (d *configDecoder) decodeBot() error {
	d.cfg = BotConfig{
		IdentityPath:        d.defaults.IdentityPath,
		Nick:                DefaultNick,
		Reply:               ReplyAuto,
		CooldownSecs:        DefaultCooldownSecs,
		MaxReplyLines:       DefaultMaxReplyLines,
		StorageDir:          d.defaults.StorageDir,
		TowersPath:          d.defaults.TowersPath,
		LXMFAnnounceMinutes: DefaultLXMFAnnounceMinutes,
	}
	return nil
}

// botKeys are the recognized [bot] keys.
var botKeys = map[string]bool{
	"identity_path":              true,
	"nick":                       true,
	"reply":                      true,
	"cooldown_s":                 true,
	"announce_on_join":           true,
	"max_reply_lines":            true,
	"micron_links":               true,
	"storage_dir":                true,
	"weather_url":                true,
	"space_weather_url":          true,
	"metar_url":                  true,
	"tide_url":                   true,
	"buoy_url":                   true,
	"river_url":                  true,
	"river_flood_url":            true,
	"weather_alert_url":          true,
	"kjv_txt_file":               true,
	"towers_path":                true,
	"launch_url":                 true,
	"flight_url":                 true,
	"flight_route_url":           true,
	"lxmf_enabled":               true,
	"lxmf_propagation_node":      true,
	"lxmf_announce_minutes":      true,
	"emergency_lxmf_destination": true,
	"portal_addr":                true,
	"gps_port":                   true,
	"gps_fix":                    true,
}

// warn records a non-fatal configuration problem.
func (d *configDecoder) warn(format string, args ...any) {
	d.warnings = append(d.warnings, d.name+": "+fmt.Sprintf(format, args...))
}

// decodeRoot reads the keys written above any table header. Only the two path
// keys belong there; any other known key is misplaced, and saying so turns a
// silent drop into a visible one. Both path keys used to be dropped here without
// a word, so a bot told to keep its identity elsewhere quietly used the default
// path — and the template wrote exactly that shape.
func (d *configDecoder) decodeRoot(root *toml.Table) error {
	for i := range root.Keys {
		kv := &root.Keys[i]
		if kv.IsRaw {
			continue
		}
		key := strings.TrimSpace(kv.Key)
		switch {
		case key == "identity_path", key == "storage_dir", key == "towers_path":
			if err := d.decodeRootPathKey(key, kv); err != nil {
				return err
			}
		case key == "kjv_txt_file":
			// The Bible text path is a path, like the two above, so it is
			// accepted above [bot] as well as inside it. The [bot] table is
			// read afterwards and wins when both are present.
			if err := d.setKJVTxtFile("the top level", key, kv); err != nil {
				return err
			}
		case botKeys[key]:
			d.warn("%q must be inside [bot]; the top-level copy is ignored", key)
		default:
			d.warn("unknown top-level key %q ignored", key)
		}
	}
	return nil
}

// decodeRootPathKey records a path key found above [bot]. The [bot] table is read
// afterwards, so a value written there overrides this one.
func (d *configDecoder) decodeRootPathKey(key string, kv *toml.KeyVal) error {
	switch key {
	case "identity_path":
		return d.setIdentityPath("the top level", key, kv)
	case "towers_path":
		return d.setTowersPath("the top level", key, kv)
	default:
		return d.setStorageDir("the top level", key, kv)
	}
}

// setIdentityPath records the identity file path. table labels the location for
// error messages, so a failure names where the operator actually wrote the key.
func (d *configDecoder) setIdentityPath(table, key string, kv *toml.KeyVal) error {
	s, err := d.stringValue(table, key, kv)
	if err != nil {
		return err
	}
	if s == "" {
		return d.keyError(table, key, "must not be empty")
	}
	d.cfg.IdentityPath = s
	return nil
}

// setStorageDir records the storage directory path.
func (d *configDecoder) setStorageDir(table, key string, kv *toml.KeyVal) error {
	s, err := d.stringValue(table, key, kv)
	if err != nil {
		return err
	}
	d.cfg.StorageDir = s
	return nil
}

// setKJVTxtFile records the King James text-file path. A path that does not name
// a readable file is an operator error, so it is reported once at startup rather
// than only when somebody asks for a verse. The value is kept either way: the
// command reports it, and clearing it would make a configured bot claim it is
// not configured.
func (d *configDecoder) setKJVTxtFile(table, key string, kv *toml.KeyVal) error {
	s, err := d.stringValue(table, key, kv)
	if err != nil {
		return err
	}
	d.cfg.KJVTxtFile = strings.TrimSpace(s)
	if d.cfg.KJVTxtFile == "" {
		return nil
	}
	if info, statErr := os.Stat(d.cfg.KJVTxtFile); statErr != nil {
		d.warn("%v %q cannot be read: %v; the kjv command will report this until it is fixed",
			table, key, statErr)
	} else if info.IsDir() {
		d.warn("%v %q is a directory, not a Bible text file; the kjv command will report this until it is fixed",
			table, key)
	}
	return nil
}

// setTowersPath records the optional local tower dataset path. A path that does
// not name a readable file is NOT an error: the documented way to use the feature
// is to drop the file in only when a dataset is wanted, so an absent file is
// reported to the operator as normal and the embedded catalog answers.
func (d *configDecoder) setTowersPath(table, key string, kv *toml.KeyVal) error {
	s, err := d.stringValue(table, key, kv)
	if err != nil {
		return err
	}
	d.cfg.TowersPath = strings.TrimSpace(s)
	if d.cfg.TowersPath == "" {
		return nil
	}
	info, statErr := os.Stat(d.cfg.TowersPath)
	switch {
	case statErr != nil:
		// Absent is expected; anything else is worth one startup line.
		if !os.IsNotExist(statErr) {
			d.warn("%v %q cannot be read: %v; the tower command will use its embedded catalog",
				table, key, statErr)
		}
	case info.IsDir():
		d.warn("%v %q is a directory, not a CSV file; the tower command will use its embedded catalog",
			table, key)
	}
	return nil
}

// decodeBotTable reads the [bot] table.
func (d *configDecoder) decodeBotTable(t *toml.Table) error {
	for i := range t.Keys {
		kv := &t.Keys[i]
		// The parser keeps comments and blank lines as raw entries so a
		// format-preserving rewrite can put them back; they are not keys.
		if kv.IsRaw {
			continue
		}
		key := strings.TrimSpace(kv.Key)
		if !botKeys[key] {
			d.warn("[bot] unknown key %q ignored", key)
			continue
		}
		switch key {
		case "identity_path":
			if err := d.setIdentityPath("[bot]", key, kv); err != nil {
				return err
			}
		case "storage_dir":
			if err := d.setStorageDir("[bot]", key, kv); err != nil {
				return err
			}
		case "kjv_txt_file":
			if err := d.setKJVTxtFile("[bot]", key, kv); err != nil {
				return err
			}
		case "towers_path":
			if err := d.setTowersPath("[bot]", key, kv); err != nil {
				return err
			}
		case "weather_url":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			d.cfg.WeatherURL = s
			// An unusable template is an operator error, so it is reported
			// once at startup rather than only when a user asks. The value is
			// kept: the command refuses it with its own line, and clearing it
			// here would report "not configured" for a setting that is set.
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				if err := validateProviderTemplate(trimmed); err != nil {
					d.warn("[bot] weather_url is unusable (%v); weather stays off until it is fixed", err)
				}
			}
		case "tide_url":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			d.cfg.TideURL = s
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				if err := validateProviderTemplate(trimmed, tideStationToken, tideDateToken); err != nil {
					d.warn("[bot] tide_url is unusable (%v); tide stays off until it is fixed", err)
				}
			}
		case "buoy_url", "river_url", "river_flood_url":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			switch key {
			case "buoy_url":
				d.cfg.BuoyURL = s
			case "river_url":
				d.cfg.RiverURL = s
			default:
				d.cfg.RiverFloodURL = s
			}
			// Same rule as the other provider templates: an unusable template is
			// an operator error, reported once at startup, and the value is kept
			// so the command refuses it instead of reporting "not configured".
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				if err := validateProviderTemplate(trimmed); err != nil {
					d.warn("[bot] %v is unusable (%v); the command stays off until it is fixed", key, err)
				}
			}
		case "metar_url", "weather_alert_url":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			if key == "metar_url" {
				d.cfg.MetarURL = s
			} else {
				d.cfg.WeatherAlertURL = s
			}
			// Same rule as the other provider templates: an unusable template is
			// an operator error, reported once at startup, and the value is kept
			// so the command refuses it instead of reporting "not configured".
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				if err := validateProviderTemplate(trimmed); err != nil {
					d.warn("[bot] %v is unusable (%v); the command stays off until it is fixed", key, err)
				}
			}
		case "space_weather_url":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			d.cfg.SpaceWeatherURL = s
			// Same rule as the other provider templates: an unusable URL is an
			// operator error, reported once at startup, and the value is kept so
			// the command refuses it instead of reporting "not configured".
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				if err := validateProviderURL(trimmed); err != nil {
					d.warn("[bot] space_weather_url is unusable (%v); spacewx stays off until it is fixed", err)
				}
			}
		case "launch_url":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			d.cfg.LaunchURL = s
			// Same rule as weather_url: an unusable template is an operator
			// error, reported once at startup, and the value is kept so the
			// command can refuse it instead of reporting "not configured".
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				if err := validateProviderTemplate(trimmed, launchTokens...); err != nil {
					d.warn("[bot] launch_url is unusable (%v); launches stays off until it is fixed", err)
				}
			}
		case "flight_url", "flight_route_url":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			if key == "flight_url" {
				d.cfg.FlightURL = s
			} else {
				d.cfg.FlightRouteURL = s
			}
			// Same rule as the other provider templates: an unusable template
			// is an operator error, reported once at startup, and the value is
			// kept so the command refuses it instead of reporting "not
			// configured" for a setting that is set.
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				if err := validateProviderTemplate(trimmed, flightToken); err != nil {
					d.warn("[bot] %v is unusable (%v); flight stays off until it is fixed", key, err)
				}
			}
		case "nick":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			if strings.TrimSpace(s) == "" {
				return d.keyError("[bot]", key, "must not be empty")
			}
			d.cfg.Nick = strings.TrimSpace(s)
		case "reply":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			mode := strings.ToLower(strings.TrimSpace(s))
			if mode != ReplyAuto && mode != ReplyDirect && mode != ReplyRoom {
				return d.keyError("[bot]", key,
					fmt.Sprintf("must be %v, %v, or %v; got %q", ReplyAuto, ReplyDirect, ReplyRoom, s))
			}
			d.cfg.Reply = mode
		case "cooldown_s":
			secs, err := d.floatValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			if secs < 0 {
				return d.keyError("[bot]", key, fmt.Sprintf("must not be negative; got %v", secs))
			}
			d.cfg.CooldownSecs = secs
		case "announce_on_join":
			b, err := d.boolValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			d.cfg.AnnounceOnJoin = b
		case "max_reply_lines":
			n, err := d.intValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			if n < 1 {
				return d.keyError("[bot]", key, fmt.Sprintf("must be at least 1; got %v", n))
			}
			d.cfg.MaxReplyLines = int(n)
		case "micron_links":
			b, err := d.boolValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			d.cfg.MicronLinks = b
		case "lxmf_enabled":
			b, err := d.boolValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			d.cfg.LXMFEnabled = b
		case "lxmf_announce_minutes":
			n, err := d.intValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			if n < 1 {
				return d.keyError("[bot]", key, fmt.Sprintf("must be at least 1; got %v", n))
			}
			d.cfg.LXMFAnnounceMinutes = int(n)
		case "lxmf_propagation_node":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			node := strings.ToLower(strings.TrimSpace(s))
			if node == "" {
				d.cfg.LXMFPropagationNode = ""
				d.cfg.LXMFPropagationNodeHash = nil
				break
			}
			if len(node) != lxmfDestinationHexLen {
				return d.keyError("[bot]", key, fmt.Sprintf(
					"must be %v hexadecimal characters (%v bytes), the length of an lxmf.propagation destination hash; got %v characters",
					lxmfDestinationHexLen, lxmfDestinationHashLen, len(node)))
			}
			raw, err := hex.DecodeString(node)
			if err != nil {
				return d.keyError("[bot]", key, fmt.Sprintf(
					"must be a hexadecimal destination hash; %q is not valid hexadecimal: %v", s, err))
			}
			d.cfg.LXMFPropagationNode = node
			d.cfg.LXMFPropagationNodeHash = raw
		case "emergency_lxmf_destination":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			destination := strings.ToLower(strings.TrimSpace(s))
			if destination == "" {
				d.cfg.EmergencyLXMFDestination = ""
				d.cfg.EmergencyLXMFDestinationHash = nil
				break
			}
			if len(destination) != lxmfDestinationHexLen {
				return d.keyError("[bot]", key, fmt.Sprintf(
					"must be %v hexadecimal characters (%v bytes), the length of an lxmf.delivery destination hash; got %v characters",
					lxmfDestinationHexLen, lxmfDestinationHashLen, len(destination)))
			}
			raw, err := hex.DecodeString(destination)
			if err != nil {
				return d.keyError("[bot]", key, fmt.Sprintf(
					"must be a hexadecimal destination hash; %q is not valid hexadecimal: %v", s, err))
			}
			d.cfg.EmergencyLXMFDestination = destination
			d.cfg.EmergencyLXMFDestinationHash = raw
		case "portal_addr":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			d.cfg.PortalAddr = strings.TrimSpace(s)
			// An address the listener cannot use is an operator error,
			// reported once at startup rather than as a failure to start. The
			// value is kept so the startup line names what was configured.
			if err := validatePortalAddr(d.cfg.PortalAddr); err != nil {
				d.warn("[bot] portal_addr is unusable (%v); the captive portal stays off until it is fixed", err)
			}
		case "gps_port":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			d.cfg.GPSPort = strings.TrimSpace(s)
		case "gps_fix":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			d.cfg.GPSFix = strings.TrimSpace(s)
			// A static fix that cannot be placed is worse than none: it would
			// answer every location query with a position from nowhere. The
			// value is kept so the warning can name it and help can show it.
			if d.cfg.GPSFix != "" {
				if _, err := ParseLocation(d.cfg.GPSFix); err != nil {
					d.warn("[bot] gps_fix %q is not a location the bot can place; the static fix is unusable", d.cfg.GPSFix)
				}
			}
		}
	}
	return nil
}

// validatePortalAddr reports whether a configured portal address is one the TCP
// listener can use. It checks the shape only — an empty host is the legal
// "every interface" form — because resolving a name belongs to Start, not to a
// configuration read that must work on a node with no network.
func validatePortalAddr(addr string) error {
	if addr == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("want host:port, like 127.0.0.1:8080 or :80: %w", err)
	}
	_ = host
	if port == "" {
		return errors.New("want a port, like 127.0.0.1:8080 or :80")
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("want a numeric port, got %q", port)
	}
	return nil
}

// hubKeys are the recognized [[hubs]] keys. kjv_txt_file is not a hub setting,
// but it is recognized here so an operator who appended it after a [[hubs]]
// block is told where it belongs instead of watching the bot ignore it.
var hubKeys = map[string]bool{
	"name":         true,
	"destination":  true,
	"nick":         true,
	"rooms":        true,
	"respond_to":   true,
	"kjv_txt_file": true,
}

// arrayTableSegment is the path segment the TOML parser records for an
// array-of-tables header. parseHeader strips only the outermost pair of
// brackets, so `[[hubs]]` becomes the literal segment "[hubs]" while a plain
// `[hubs]` becomes "hubs". Matching the bracketed form is what distinguishes
// the array entries from the implicit parent table a stray `[hubs.x]` creates.
func arrayTableSegment(name string) string {
	return "[" + name + "]"
}

// decodeHubs reads every [[hubs]] array-of-tables entry, in document order. A
// stray [hubs.x] sub-table makes the parser create an extra implicit parent
// table whose path is the unbracketed "hubs"; selecting on the bracketed
// segment is what keeps that parent from being read as another hub.
func (d *configDecoder) decodeHubs(root *toml.Table) error {
	hubsSegment := arrayTableSegment("hubs")
	var entries []*toml.Table
	for _, t := range root.Tables {
		if len(t.Path) != 1 || t.Path[0] != hubsSegment {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(t.HeaderRaw), "[[") {
			continue
		}
		entries = append(entries, t)
	}
	if len(entries) == 0 {
		return fmt.Errorf("%v: no [[hubs]] entries configured; add at least one hub to dial", d.name)
	}

	for i, entry := range entries {
		hub, err := d.decodeHub(i+1, entry)
		if err != nil {
			return err
		}
		d.cfg.Hubs = append(d.cfg.Hubs, hub)
	}
	return nil
}

// decodeHub reads one [[hubs]] entry. index is the 1-based position in the
// document, used in messages so an entry without a name is still identifiable.
func (d *configDecoder) decodeHub(index int, t *toml.Table) (HubConfig, error) {
	hub := HubConfig{RespondTo: make(map[string]string)}
	label := fmt.Sprintf("%v: [[hubs]] entry %v", d.name, index)

	// Name first, so every later message can name the hub.
	if v, ok := t.Get("name"); ok {
		name, err := d.stringValue(label, "name", &toml.KeyVal{Key: "name", Value: v})
		if err != nil {
			return hub, err
		}
		hub.Name = strings.TrimSpace(name)
		label = fmt.Sprintf("%v: [[hubs]] entry %v (name %q)", d.name, index, hub.Name)
	}

	var destination string
	var roomsValue *toml.Value
	for i := range t.Keys {
		kv := &t.Keys[i]
		// Comments and blank lines are stored as raw entries, not keys.
		if kv.IsRaw {
			continue
		}
		key := strings.TrimSpace(kv.Key)
		if !hubKeys[key] {
			d.warn("%v: unknown key %q ignored", label, key)
			continue
		}
		switch key {
		case "name":
			// Already read above.
		case "nick":
			s, err := d.stringValue(label, key, kv)
			if err != nil {
				return hub, err
			}
			hub.Nick = strings.TrimSpace(s)
		case "destination":
			s, err := d.stringValue(label, key, kv)
			if err != nil {
				return hub, err
			}
			destination = strings.TrimSpace(s)
		case "rooms":
			value := kv.Value
			roomsValue = &value
		case "kjv_txt_file":
			// The Bible text path belongs to the bot, not to one hub, so a
			// hub-level copy is used only when neither [bot] nor the top level
			// set one, and either way the operator is told where it belongs.
			s, err := d.stringValue(label, key, kv)
			if err != nil {
				return hub, err
			}
			if strings.TrimSpace(s) == "" {
				break
			}
			if strings.TrimSpace(d.cfg.KJVTxtFile) != "" {
				d.warn("%v: %q belongs inside [bot]; the hub-level copy is ignored", label, key)
				break
			}
			d.warn("%v: %q belongs inside [bot]; it is accepted here until it is moved", label, key)
			if err := d.setKJVTxtFile(label, key, kv); err != nil {
				return hub, err
			}
		case "respond_to":
			if err := d.decodeRespondTo(label, kv, &hub); err != nil {
				return hub, err
			}
		}
	}

	// An entry with nothing in it is missing both required keys at once; report
	// them together so one run tells the operator everything to add.
	var missing []string
	if hub.Name == "" {
		missing = append(missing, `"name"`)
	}
	if destination == "" {
		missing = append(missing, `"destination"`)
	}
	if len(missing) > 0 {
		return hub, fmt.Errorf("%v: missing required key %v; every hub needs a unique display name "+
			"and its rrc.hub destination hash (%v hexadecimal characters)",
			label, strings.Join(missing, " and "), HubDestinationHexLen)
	}

	for _, existing := range d.cfg.Hubs {
		if strings.EqualFold(existing.Name, hub.Name) {
			return hub, fmt.Errorf("%v: duplicate hub name %q; hub names must be unique",
				label, hub.Name)
		}
	}

	if len(destination) != HubDestinationHexLen {
		return hub, fmt.Errorf("%v: %q must be %v hexadecimal characters (%v bytes); got %v characters: %q",
			label, "destination", HubDestinationHexLen, rrc.IdentityHashLen, len(destination), destination)
	}
	raw, err := hex.DecodeString(strings.ToLower(destination))
	if err != nil {
		return hub, fmt.Errorf("%v: %q must be a hexadecimal destination hash; %q is not valid hexadecimal: %w",
			label, "destination", destination, err)
	}
	hub.Destination = strings.ToLower(destination)
	hub.DestHash = raw

	if roomsValue != nil {
		rooms, err := d.decodeRooms(label, roomsValue)
		if err != nil {
			return hub, err
		}
		hub.Rooms = rooms
	}

	for room := range hub.RespondTo {
		joined := false
		for _, r := range hub.Rooms {
			if r.Name == room {
				joined = true
				break
			}
		}
		if !joined {
			d.warn("%v: respond_to names room %q, which is not in that hub's rooms; the entry has no effect",
				label, room)
		}
	}

	return hub, nil
}

// decodeRooms reads a rooms array: string entries and { name = ..., key = ... }
// inline tables are both accepted.
func (d *configDecoder) decodeRooms(label string, value *toml.Value) ([]RoomConfig, error) {
	if value.Kind != toml.KindArray {
		return nil, fmt.Errorf("%v: %q must be an array of room names or { name = ..., key = ... } tables; got %v",
			label, "rooms", value.Raw)
	}
	var rooms []RoomConfig
	seen := make(map[string]bool)
	for i, item := range value.Arr {
		room, err := d.decodeRoom(label, i+1, &item)
		if err != nil {
			return nil, err
		}
		if seen[room.Name] {
			d.warn("%v: %q lists room %q more than once; the duplicate is ignored", label, "rooms", room.Name)
			continue
		}
		seen[room.Name] = true
		rooms = append(rooms, room)
	}
	return rooms, nil
}

// roomKeys are the recognized keys of a room inline table.
var roomKeys = map[string]bool{"name": true, "key": true}

// decodeRoom reads one rooms array item.
func (d *configDecoder) decodeRoom(label string, index int, item *toml.Value) (RoomConfig, error) {
	var room RoomConfig
	switch item.Kind {
	case toml.KindString:
		room.Name = normalizeRoomName(item.Str)
	case toml.KindInlineTable:
		for _, kv := range item.Tbl {
			if kv.IsRaw {
				continue
			}
			key := strings.TrimSpace(kv.Key)
			if !roomKeys[key] {
				d.warn("%v: rooms entry %v: unknown key %q ignored", label, index, key)
				continue
			}
			if kv.Value.Kind != toml.KindString {
				return room, fmt.Errorf("%v: rooms entry %v: %q must be a string; got %v",
					label, index, key, kv.Value.Raw)
			}
			switch key {
			case "name":
				room.Name = normalizeRoomName(kv.Value.Str)
			case "key":
				room.Key = kv.Value.Str
			}
		}
		if strings.TrimSpace(room.Name) == "" {
			return room, fmt.Errorf("%v: rooms entry %v is missing the required key %q",
				label, index, "name")
		}
	default:
		return room, fmt.Errorf("%v: %q entry %v must be a room name or a { name = ..., key = ... } table; got %v",
			label, "rooms", index, item.Raw)
	}
	if room.Name == "" {
		return room, fmt.Errorf("%v: %q entry %v has an empty room name", label, "rooms", index)
	}
	return room, nil
}

// decodeRespondTo reads the respond_to inline table: room name to trigger nick.
func (d *configDecoder) decodeRespondTo(label string, kv *toml.KeyVal, hub *HubConfig) error {
	if kv.Value.Kind != toml.KindInlineTable {
		return fmt.Errorf("%v: %q must be an inline table mapping room names to trigger nicks, "+
			"for example { general = \"gorrcbot\" }; got %v", label, "respond_to", kv.Value.Raw)
	}
	for _, entry := range kv.Value.Tbl {
		if entry.IsRaw {
			continue
		}
		room := normalizeRoomName(entry.Key)
		if room == "" {
			return fmt.Errorf("%v: %q has an empty room name", label, "respond_to")
		}
		if entry.Value.Kind != toml.KindString {
			return fmt.Errorf("%v: %q[%v] must be a string nick; got %v",
				label, "respond_to", entry.Key, entry.Value.Raw)
		}
		nick := strings.TrimSpace(entry.Value.Str)
		if nick == "" {
			return fmt.Errorf("%v: %q[%v] must not be empty", label, "respond_to", entry.Key)
		}
		hub.RespondTo[room] = nick
	}
	return nil
}

// normalizeRoomName lowercases and trims a room name, mirroring the client's
// own room normalization so configuration keys and runtime room names match.
func normalizeRoomName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// finish runs the cross-key validation that needs the whole configuration.
func (d *configDecoder) finish() error {
	names := make([]string, 0, len(d.cfg.Hubs))
	for _, hub := range d.cfg.Hubs {
		if len(hub.Rooms) == 0 {
			d.warn("hub %q joins no rooms; the bot will connect but never answer there", hub.Name)
		}
		names = append(names, hub.Name)
	}
	sort.Strings(names)
	// A propagation node without LXMF enabled is dead configuration: saying so
	// once at startup is the difference between an operator noticing a typo and
	// wondering for a week why nothing is store-and-forwarded.
	if d.cfg.LXMFPropagationNode != "" && !d.cfg.LXMFEnabled {
		d.warn("[bot] lxmf_propagation_node is set but lxmf_enabled is false, so LXMF stays off and no message is queued")
	}
	return nil
}

// stringValue reads a string key.
func (d *configDecoder) stringValue(table, key string, kv *toml.KeyVal) (string, error) {
	if kv.Value.Kind != toml.KindString {
		return "", d.keyError(table, key, fmt.Sprintf("must be a string; got %v", kv.Value.Raw))
	}
	return kv.Value.Str, nil
}

// intValue reads an integer key, accepting a float that has no fraction so a
// hand-written `max_reply_lines = 12.0` still works.
func (d *configDecoder) intValue(table, key string, kv *toml.KeyVal) (int64, error) {
	switch kv.Value.Kind {
	case toml.KindInt:
		return kv.Value.Int, nil
	case toml.KindFloat:
		if kv.Value.Flt == float64(int64(kv.Value.Flt)) {
			return int64(kv.Value.Flt), nil
		}
		return 0, d.keyError(table, key, fmt.Sprintf("must be a whole number; got %v", kv.Value.Raw))
	default:
		return 0, d.keyError(table, key, fmt.Sprintf("must be a whole number; got %v", kv.Value.Raw))
	}
}

// floatValue reads a number key.
func (d *configDecoder) floatValue(table, key string, kv *toml.KeyVal) (float64, error) {
	switch kv.Value.Kind {
	case toml.KindInt:
		return float64(kv.Value.Int), nil
	case toml.KindFloat:
		return kv.Value.Flt, nil
	default:
		return 0, d.keyError(table, key, fmt.Sprintf("must be a number; got %v", kv.Value.Raw))
	}
}

// boolValue reads a boolean key.
func (d *configDecoder) boolValue(table, key string, kv *toml.KeyVal) (bool, error) {
	if kv.Value.Kind != toml.KindBool {
		return false, d.keyError(table, key, fmt.Sprintf("must be true or false; got %v", kv.Value.Raw))
	}
	return kv.Value.Bool, nil
}

// keyError builds the actionable configuration error for one key.
func (d *configDecoder) keyError(table, key, problem string) error {
	return fmt.Errorf("%v: %v %q %v", d.name, table, key, problem)
}

// findTable returns the table at path directly under root, or nil.
func findTable(root *toml.Table, path []string) *toml.Table {
	for _, t := range root.Tables {
		if len(t.Path) != len(path) {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(t.HeaderRaw), "[[") {
			continue
		}
		match := true
		for i, seg := range path {
			if t.Path[i] != seg {
				match = false
				break
			}
		}
		if match {
			return t
		}
	}
	return nil
}
