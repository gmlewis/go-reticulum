// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the gorrcbot first-run state files: the self-documenting
// config.toml template and the 64-byte Reticulum identity. Both are created on
// the first run and left untouched afterwards, mirroring the gorrcd bootstrap.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// BotPaths holds the resolved state-file paths for one bot installation.
type BotPaths struct {
	// Home is the state directory: GORRCBOT_HOME when set, else ~/.gorrcbot.
	Home string
	// ConfigPath is the TOML configuration file.
	ConfigPath string
	// IdentityPath is the Reticulum identity file holding the 64-byte private
	// key material, shared by every hub the bot dials.
	IdentityPath string
	// StorageDir is the RRC client's own storage directory, where it keeps the
	// per-hub message history it saves. It is a DIRECTORY: the client creates
	// it and writes one history file per room inside it.
	StorageDir string
	// TowersPath is the optional local tower dataset the tower command merges
	// over its embedded catalog. It is a FILE, and it need not exist: an
	// absent dataset means the embedded catalog is the whole answer.
	TowersPath string
}

// The file and directory names, relative to BotPaths.Home.
const (
	defaultConfigFileName   = "config.toml"
	defaultIdentityFileName = "bot_identity"
	defaultStorageDirName   = "storage"
	defaultTowersFileName   = "towers.csv"
)

// DefaultBotPaths resolves the state directory and the three state files. The
// GORRCBOT_HOME environment variable overrides the state directory, which is
// how tests and alternate installs stay isolated from ~/.gorrcbot.
func DefaultBotPaths() BotPaths {
	home := os.Getenv("GORRCBOT_HOME")
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(userHome, ".gorrcbot")
		} else {
			home = ".gorrcbot"
		}
	}
	return BotPaths{
		Home:         home,
		ConfigPath:   filepath.Join(home, defaultConfigFileName),
		IdentityPath: filepath.Join(home, defaultIdentityFileName),
		StorageDir:   filepath.Join(home, defaultStorageDirName),
		TowersPath:   filepath.Join(home, defaultTowersFileName),
	}
}

// defaultConfigTemplate is the first-run config.toml. It is written verbatim
// and is a working configuration: the one live hub below is the gonomadnet
// Public Hub, which is the hub this bot is developed and tested against. No
// other hub appears in it, not even commented out, so a first run can only
// ever talk to that one hub; reaching any other hub takes a deliberate
// [[hubs]] block. The template must always parse through this program's own
// reader, so any change here has to keep the schema valid.
const defaultConfigTemplate = `# gorrcbot configuration (TOML)
#
# This file was created on first run. Edit it, then start gorrcbot again.
#
# gorrcbot is an RRC (Reticulum Relay Chat) CLIENT bot. It connects to each hub
# below over a normal RRC link, joins the listed rooms, and answers only when it
# is addressed by name. No hub-side support is required: any RRC hub works.

[bot]

# Reticulum identity file: 64 bytes of private key material (X25519 ‖ Ed25519),
# created on first run with mode 0600. The same identity hash is used on every
# hub, which is what makes the @<hash-prefix> alias below stable.
identity_path = {{identity_path}}

# Advertised nick (sent in HELLO) AND the default trigger nick (what the bot
# answers to). A hub or a room may override either one; see respond_to below.
nick = "gobot"

# How replies are delivered:
#   auto   - an in-room NOTICE, and a direct NOTICE only when the request
#            itself arrived as one (default). A hub advertises its own
#            capabilities in WELCOME and never publishes what another client
#            announced in HELLO, so the room is the one route a room asker is
#            known to be able to read.
#   direct - always a direct NOTICE, and stay silent if that is impossible
#   room   - always an in-room NOTICE
reply = "auto"

# Minimum seconds between replies to the same identity. Suppresses reply storms
# from a client that repeats a request.
cooldown_s = 8.0

# Post one short self-introduction NOTICE in each room, once per session. OFF by
# default: the bot joins, leaves and answers exactly like any other member, and
# speaks only when it is addressed. Turn it on to tell a room that a bot has
# arrived.
announce_on_join = false

# Maximum number of NOTICE lines a single reply may produce. Longer replies are
# truncated with a visible marker, because each line is one MTU-sized envelope.
# The discovery commands size a catalog page from this budget, so raising it
# widens every page.
max_reply_lines = 12

# Render the discovery (search, near, and list) rows as clickable Micron links,
# the notation a NomadNet client displays as a button. Every other client shows
# the link text literally, so leave this false unless the room reads Micron.
micron_links = false

# The RRC client's storage DIRECTORY: it keeps the saved per-room message
# history here. The client creates it on first connect.
storage_dir = {{storage_dir}}

# kjv: The full text of the King James Version Bible for the 'kjv' command
# that can look up verses and search through the Bible.
# Full kjv.txt file available for download here:
# https://github.com/gmlewis/kjv-ref/blob/master/kjv.txt
kjv_txt_file = ""

# tower: An optional local dataset of cell and repeater sites, merged over the
# catalog embedded in the binary. It is CSV with this exact column order:
#   id,name,type,lat,lng,freq,offset,tone,operator,city,region,country,elev
# type is RPT, CELL, EMERG, or MAR (the words "repeater", "cellular",
# "emergency", and "maritime" are accepted too), coordinates are WGS-84, and a
# row whose id matches an embedded one replaces it while a new id is added.
# A file that is absent is normal: the embedded catalog is then the whole
# answer, and the command stays fully offline either way.
towers_path = {{towers_path}}

# Optional weather provider template for the weather/wx commands. The literal
# {place} is replaced with the requested place, which is validated first: only
# letters, digits, spaces, commas, periods, hyphens and apostrophes are
# accepted, so a requested place can never change this URL's host, path or
# query. The template itself must be an absolute http:// or https:// URL that
# carries no credentials. Leave empty to disable the commands, which then say so
# instead of guessing. http:// avoids the expired SSL certificate on wttr.in:
#
weather_url = "http://wttr.in/{place}?format=%l:+%C+%t+%w+%h"

# Optional provider template for the tide command: the high and low water
# predictions for a station. It must carry BOTH {place} (the station id) and
# {date} (YYYYMMDD), and must keep its own scheme and host. The answer is the
# station's high/low predictions; the state of the tide between them is
# estimated with the rule of twelfths, so one pair of predictions gives the
# depth all day. A {place} may be a 7-digit station id (9414290 is San
# Francisco), a port name, or a position, which resolves to the nearest station
# in the bot's own reference table. Leave empty to disable the command.
#
tide_url = "https://api.tidesandcurrents.noaa.gov/api/prod/datagetter?product=predictions&datum=MLLW&time_zone=gmt&units=english&interval=hilo&format=json&station={place}&begin_date={date}&range=48"

# Optional provider template for the buoy command: the real-time sea state from
# a weather buoy. {place} becomes the lowercased buoy id, and the template must
# keep its own scheme and host. The answer is the fixed-column text feed the
# National Data Buoy Center publishes; the newest observation in it is decoded.
# Leave empty to disable the command.
#
buoy_url = "https://www.ndbc.noaa.gov/data/realtime2/{place}.txt"

# Optional provider template for the river command: the instantaneous stage and
# discharge at a stream gauge. {place} becomes the USGS site number, and the
# template must keep its own scheme and host. Appending "&period=P1D" makes the
# provider return a day of readings, which is what the three-hour trend is
# measured over; without it the command reports the stage and flow with no
# trend. Leave empty to disable the command.
#
river_url = "https://waterservices.usgs.gov/nwis/iv/?sites={place}&format=json&parameterCd=00065,00060&period=P1D"

# Optional second template for the river command: the flood categories a river
# forecast center publishes for the same gauge. {place} is the same USGS site
# number. With it, the answer names the flood status and the action stage;
# without it, the command reports the stage and says it has nothing to compare
# it against.
#
river_flood_url = "https://api.water.noaa.gov/nwps/v1/gauges/{place}"

# Optional provider URL for the spacewx command (alias: solar): the solar flux
# index, the sunspot number, and the planetary K-index, read as JSON. It takes
# no substitution tokens and must be an absolute http:// or https:// URL
# carrying no credentials. The reading is cached for an hour, and with no
# provider reachable the command reports the last reading it has; an operator
# can also enter one by hand with "spacewx set sfi=158 ssn=112 kp=4". Leave
# empty to disable the fetch.
#
space_weather_url = "https://services.swpc.noaa.gov/products/noaa-planetary-k-index.json"

# The Go Reticulum Lifesaver's captive web portal: a self-contained survival
# dashboard a traveler's phone opens by itself the moment it joins this device's
# Wi-Fi. Nothing has to be installed, because the page is served from this
# binary: the position panel, the SOS button, and the offline field assistant
# are all in it. It answers the captive-network probes Apple, Android, and
# Windows use, so the phone pops the dashboard instead of reporting "connected,
# no internet".
# Set an address to turn it on, for example "127.0.0.1:8080" while testing or
# ":80" in the field. Leave empty to bind no HTTP listener at all.
portal_addr = ""

# GNSS receiver: the device that streams NMEA-0183 sentences, for example
# /dev/ttyUSB0 or /dev/ttyACM0. A receiver supplies the position that
# /whereami, "tower near", "tide near", "sun", and /sos use automatically, so
# nothing has to be typed with cold hands. The port must already be configured
# for the receiver's line speed. Leave empty for no streaming receiver.
gps_port = ""

# A static position for a node with no receiver: a fixed relay, a headless
# installation, or an operator rehearsing the field tools. It accepts every
# notation the location commands accept, like "37.7553,-122.4527", a Plus Code,
# a Maidenhead grid, or DMS. Leave empty when gps_port supplies the position.
gps_fix = ""

# Electronic compass: the device that streams NMEA-0183 heading sentences
# ($HCHDG, $HCHDM, or $HCHDT), for example a QMC5883L or LSM303 magnetometer
# behind a serial bridge on /dev/ttyUSB1 or /dev/ttyACM1. A compass supplies the
# heading a GNSS receiver cannot: it knows which way the device points while the
# operator is standing still, so /whereami prints a heading, "tower near" adds
# relative steering to aim a directional antenna, and the captive portal draws a
# live compass rose. Leave empty for no streaming compass.
compass_port = ""

# A static magnetic heading for a node with no compass sensor: a fixed
# installation, or an operator rehearsing the direction-finding tools. It
# accepts degrees ("042") or a compass point ("NE"). The World Magnetic Model
# converts it to true north from the node's position. Leave empty when
# compass_port supplies the heading.
compass_heading = ""

# Optional provider template for the metar command: the aviation weather report
# for an ICAO station code. {place} becomes the lowercased station code, and the
# template must keep its own scheme and host. The answer is decoded into wind,
# visibility, temperature, dewpoint, and altimeter setting; a report that does
# not decode is reported raw. Leave empty to disable the command.
#
metar_url = "https://aviationweather.gov/api/data/metar?ids={place}&format=raw"

# Optional provider template for the wxalert command: the severe weather
# warnings in force for a place or area. {place} is substituted after the same
# sanitizing the weather command applies, and the template must keep its own
# scheme and host. A JSON answer built on CAP becomes one line per warning; a
# plain-text answer becomes one headline. Answers are cached for 15 minutes.
# Leave empty to disable the command.
#
weather_alert_url = "https://api.weather.gov/alerts/active?area={place}"

# Optional provider template for the launches command: what is going up soon, and
# what just went up. {mode} is the provider's window name and {limit} is how many
# launches the bot asks the provider for; both are validated before substitution,
# and the built URL is required to keep this template's scheme and host. The
# upcoming window is asked for a margin over the number it lists, because the
# provider keeps launches it has not yet removed after they flew at the head of
# that window. Leave empty to disable the command, which then says so. This
# provider needs no API key and allows 15 anonymous calls per hour per IP, which
# is why the bot caches every answer:
#
launch_url = "https://ll.thespacedevs.com/2.3.0/launches/{mode}/?limit={limit}"

# flight reports where one flight is right now, by the number a passenger knows
# ("BA123"). flight_route_url is asked first: it turns that number into the radio
# callsign the live feed uses and names the airline and the two airports.
# flight_url is the live state. Both providers are keyless and both are optional;
# with no flight_url the command says it is not configured. Any keyed provider
# works too: put its key in the template, and note that the bot never repeats a
# configured URL into a room.
flight_url = "https://api.adsb.lol/v2/callsign/{flight}"
flight_route_url = "https://api.adsbdb.com/v0/callsign/{flight}"

# LXMF messaging, which is what the msg command (alias: lxmf) uses. RRC is
# connection-oriented, so a mention of a peer who is offline is lost; LXMF is
# store-and-forward, so the same message can wait for them.
#
#   lxmf_enabled          - OFF by default, and off means absent: no LXMF router
#                           is created, no state is written under the storage
#                           directory, and the command answers with the line
#                           that says how to turn it on
#   lxmf_propagation_node - the lxmf.propagation destination hash (32 hex
#                           characters) of a store-and-forward node. The bot
#                           never discovers one on its own, so without this a
#                           message to a peer with no known path fails visibly
#                           instead of waiting for them
#   lxmf_announce_minutes - how often the bot announces its own lxmf.delivery
#                           destination, so a peer can route a reply back to it
#
# This command writes into somebody else's inbox, so it carries its own budget
# on top of the reply cooldown: 5 messages per asker per minute, 20 for the whole
# bot. The outcome of a message arrives later as a direct NOTICE to the asker;
# a propagated message is reported as accepted by the node, never as delivered.
lxmf_enabled = false
lxmf_propagation_node = ""
lxmf_announce_minutes = 360

# The lxmf.delivery destination hash (32 hex characters) of an emergency
# dispatch destination. With lxmf_enabled = true, a new distress beacon raised
# with the sos command is also queued to it: LXMF is store-and-forward, so that
# copy keeps trying after the local link has failed. Empty means beacons are
# only alerted in the joined rooms.
emergency_lxmf_destination = ""

# One [[hubs]] entry per RRC hub. Every entry is dialed on startup, kept
# connected with auto-reconnect, and joined to its rooms.
#
#   name         - display name (must be unique)
#   destination  - the hub's rrc.hub destination hash, 32 hex characters
#   nick         - optional advertised-nick override for THIS hub
#   rooms        - room names, or { name = "...", key = "..." } for a keyed (+k) room
#   respond_to   - inline table mapping a room to the nick the bot answers to
#                  in that room; defaults to this hub's nick

[[hubs]]
name = "gonomadnet Public Hub"
destination = "a012129c10205c0b9441fcd2b755b2a7"
rooms = ["general"]
respond_to = { general = "gobot" }

# A second hub would be one more [[hubs]] block, with its own name,
# destination, rooms, and optional respond_to. None is configured here: this
# bot talks only to the hub above until an operator deliberately adds another.
`

// defaultConfigContent renders the first-run config.toml for the given paths.
func defaultConfigContent(paths BotPaths) string {
	return strings.NewReplacer(
		"{{identity_path}}", quoteTOMLString(paths.IdentityPath),
		"{{storage_dir}}", quoteTOMLString(paths.StorageDir),
		"{{towers_path}}", quoteTOMLString(paths.TowersPath),
	).Replace(defaultConfigTemplate)
}

// quoteTOMLString renders a TOML basic string, escaping the characters a
// Windows-style path could contain.
func quoteTOMLString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := range len(s) {
		switch c := s[i]; c {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// EnsureFirstRun creates any missing state file and reports whether it created
// anything. The identity is created with mode 0600 through the same
// rns.Identity.ToFile path every other tool uses.
func EnsureFirstRun(paths BotPaths) (bool, error) {
	created := false

	identityExists := fileExists(paths.IdentityPath)
	configExists := fileExists(paths.ConfigPath)
	if identityExists && configExists {
		return false, nil
	}

	if err := rrc.EnsurePrivateDir(paths.Home); err != nil {
		return false, fmt.Errorf("creating %v: %w", paths.Home, err)
	}

	if !configExists {
		if err := os.WriteFile(paths.ConfigPath, []byte(defaultConfigContent(paths)), 0o600); err != nil {
			return false, fmt.Errorf("writing %v: %w", paths.ConfigPath, err)
		}
		created = true
	}

	if !identityExists {
		identity, err := rns.NewIdentity(true, rns.NewLogger())
		if err != nil {
			return created, fmt.Errorf("creating a Reticulum identity: %w", err)
		}
		if err := identity.ToFile(paths.IdentityPath); err != nil {
			return created, fmt.Errorf("writing %v: %w", paths.IdentityPath, err)
		}
		_ = os.Chmod(paths.IdentityPath, 0o600)
		if len(identity.Hash) != rrc.IdentityHashLen {
			return created, fmt.Errorf("created identity %v has a %v-byte hash, want %v",
				paths.IdentityPath, len(identity.Hash), rrc.IdentityHashLen)
		}
		created = true
	}

	return created, nil
}

// fileExists reports whether path names an existing regular file (or any other
// non-directory entry).
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// LoadBotIdentity loads the bot identity from path, creating it on first use.
// The bool reports whether a new identity was created. A file that exists but
// does not hold valid key material produces an error naming the path: silently
// replacing a corrupt identity would change the bot's identity hash, which is
// the one thing other clients key on.
func LoadBotIdentity(path string) (*rns.Identity, bool, error) {
	if fileExists(path) {
		identity, err := rns.FromFile(path, rns.NewLogger())
		if err != nil {
			return nil, false, fmt.Errorf(
				"could not load the gorrcbot identity from %v: the file may be corrupt or truncated: %w",
				path, err)
		}
		if len(identity.Hash) != rrc.IdentityHashLen {
			return nil, false, fmt.Errorf(
				"could not load the gorrcbot identity from %v: the file may be corrupt or truncated", path)
		}
		return identity, false, nil
	}

	if err := rrc.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, false, fmt.Errorf("creating %v: %w", filepath.Dir(path), err)
	}
	identity, err := rns.NewIdentity(true, rns.NewLogger())
	if err != nil {
		return nil, false, fmt.Errorf("creating a Reticulum identity: %w", err)
	}
	if err := identity.ToFile(path); err != nil {
		return nil, false, fmt.Errorf("writing %v: %w", path, err)
	}
	_ = os.Chmod(path, 0o600)
	return identity, true, nil
}

// firstRunMessage renders the first-run notice naming every file that was just
// created and what to do next.
func firstRunMessage(paths BotPaths) string {
	return "Created default gorrcbot files. Edit the configuration before starting:\n" +
		"- Config:   " + paths.ConfigPath + "\n" +
		"- Identity: " + paths.IdentityPath + "\n" +
		"- Storage:  " + paths.StorageDir + "\n" +
		"\nThen re-run gorrcbot.\n"
}
