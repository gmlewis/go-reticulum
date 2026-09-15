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
}

// The file and directory names, relative to BotPaths.Home.
const (
	defaultConfigFileName   = "config.toml"
	defaultIdentityFileName = "bot_identity"
	defaultStorageDirName   = "storage"
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
nick = "gorrcbot"

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
max_reply_lines = 12

# The RRC client's storage DIRECTORY: it keeps the saved per-room message
# history here. The client creates it on first connect.
storage_dir = {{storage_dir}}

# Optional weather provider template for the weather/wx commands. The literal
# {place} is replaced with the requested place, which is validated first: only
# letters, digits, spaces, commas, periods, hyphens and apostrophes are
# accepted, so a requested place can never change this URL's host, path or
# query. The template itself must be an absolute http:// or https:// URL that
# carries no credentials. Leave empty to disable the commands, which then say so
# instead of guessing. This provider needs no API key and answers in a single
# line of plain text:
#
#   weather_url = "https://wttr.in/{place}?format=%l:+%C+%t+%w+%h"
weather_url = ""

# Optional provider template for the launches command: what is going up soon, and
# what just went up. {mode} is the provider's window name and {limit} is how many
# launches to list; both are validated before substitution, and the built URL is
# required to keep this template's scheme and host. Leave empty to disable the
# command, which then says so. This provider needs no API key and allows 15
# anonymous calls per hour per IP, which is why the bot caches every answer:
#
#   launch_url = "https://ll.thespacedevs.com/2.3.0/launches/{mode}/?limit={limit}"
#   flight_url = "https://api.adsb.lol/v2/callsign/{flight}"
#   flight_route_url = "https://api.adsbdb.com/v0/callsign/{flight}"
#
# The provider's default answer carries the operator and pad names; appending
# "&mode=list" makes its answer roughly ten times smaller and drops both.
launch_url = ""

# flight reports where one flight is right now, by the number a passenger knows
# ("BA123"). flight_route_url is asked first: it turns that number into the radio
# callsign the live feed uses and names the airline and the two airports.
# flight_url is the live state. Both providers are keyless and both are optional;
# with no flight_url the command says it is not configured. Any keyed provider
# works too: put its key in the template, and note that the bot never repeats a
# configured URL into a room.
flight_url = ""
flight_route_url = ""

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
respond_to = { general = "gorrcbot" }

# A second hub would be one more [[hubs]] block, with its own name,
# destination, rooms, and optional respond_to. None is configured here: this
# bot talks only to the hub above until an operator deliberately adds another.
`

// defaultConfigContent renders the first-run config.toml for the given paths.
func defaultConfigContent(paths BotPaths) string {
	return strings.NewReplacer(
		"{{identity_path}}", quoteTOMLString(paths.IdentityPath),
		"{{storage_dir}}", quoteTOMLString(paths.StorageDir),
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
