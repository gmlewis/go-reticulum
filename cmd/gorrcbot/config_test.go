// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testHubOne = "a012129c10205c0b9441fcd2b755b2a7"
	testHubTwo = "28c7c1a68c735693aa8e6b8193ed44b2"
)

// TestDecodeBotConfigBotDefaults asserts every documented [bot] default.
func TestDecodeBotConfigBotDefaults(t *testing.T) {
	t.Parallel()

	cfg, warnings, err := DecodeBotConfig("config.toml", `
[[hubs]]
name = "One"
destination = "`+testHubOne+`"
rooms = ["general"]
`)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if cfg.Nick != DefaultNick {
		t.Errorf("Nick = %q, want %q", cfg.Nick, DefaultNick)
	}
	if cfg.Reply != ReplyAuto {
		t.Errorf("Reply = %q, want %q", cfg.Reply, ReplyAuto)
	}
	if cfg.CooldownSecs != DefaultCooldownSecs {
		t.Errorf("CooldownSecs = %v, want %v", cfg.CooldownSecs, DefaultCooldownSecs)
	}
	if cfg.AnnounceOnJoin {
		t.Error("AnnounceOnJoin = true, want false by default (the bot speaks only when addressed)")
	}
	if cfg.MaxReplyLines != DefaultMaxReplyLines {
		t.Errorf("MaxReplyLines = %v, want %v", cfg.MaxReplyLines, DefaultMaxReplyLines)
	}
	if cfg.WeatherURL != "" {
		t.Errorf("WeatherURL = %q, want empty by default", cfg.WeatherURL)
	}
	if cfg.KJVTxtFile != "" {
		t.Errorf("KJVTxtFile = %q, want empty by default (the kjv command is off)", cfg.KJVTxtFile)
	}
	if cfg.IdentityPath == "" || cfg.StorageDir == "" {
		t.Errorf("IdentityPath/StorageDir = %q/%q, want the default paths",
			cfg.IdentityPath, cfg.StorageDir)
	}
}

// TestDecodeBotConfigBotOverrides asserts every [bot] key is read and coerced.
func TestDecodeBotConfigBotOverrides(t *testing.T) {
	t.Parallel()

	cfg, warnings, err := DecodeBotConfig("config.toml", `
[bot]
identity_path = "/tmp/bot_id"
nick = "meshbot"
reply = "room"
cooldown_s = 2.5
announce_on_join = true
max_reply_lines = 3
storage_dir = "/tmp/storage"
weather_url = "https://example.invalid/{place}"
flight_url = "https://api.adsb.invalid/v2/callsign/{flight}"
flight_route_url = "https://api.adsbdb.invalid/v0/callsign/{flight}"

[[hubs]]
name = "One"
destination = "`+testHubOne+`"
rooms = ["general"]
`)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if cfg.IdentityPath != "/tmp/bot_id" {
		t.Errorf("IdentityPath = %q, want %q", cfg.IdentityPath, "/tmp/bot_id")
	}
	if cfg.Nick != "meshbot" {
		t.Errorf("Nick = %q, want %q", cfg.Nick, "meshbot")
	}
	if cfg.Reply != ReplyRoom {
		t.Errorf("Reply = %q, want %q", cfg.Reply, ReplyRoom)
	}
	if cfg.CooldownSecs != 2.5 {
		t.Errorf("CooldownSecs = %v, want 2.5", cfg.CooldownSecs)
	}
	if !cfg.AnnounceOnJoin {
		t.Error("AnnounceOnJoin = false, want true (announce_on_join turns the greeting on)")
	}
	if cfg.MaxReplyLines != 3 {
		t.Errorf("MaxReplyLines = %v, want 3", cfg.MaxReplyLines)
	}
	if cfg.StorageDir != "/tmp/storage" {
		t.Errorf("StorageDir = %q, want %q", cfg.StorageDir, "/tmp/storage")
	}
	if cfg.WeatherURL != "https://example.invalid/{place}" {
		t.Errorf("WeatherURL = %q, want the configured provider template", cfg.WeatherURL)
	}
	if cfg.FlightURL != "https://api.adsb.invalid/v2/callsign/{flight}" {
		t.Errorf("FlightURL = %q, want the configured provider template", cfg.FlightURL)
	}
	if cfg.FlightRouteURL != "https://api.adsbdb.invalid/v0/callsign/{flight}" {
		t.Errorf("FlightRouteURL = %q, want the configured provider template", cfg.FlightRouteURL)
	}
}

// TestDecodeBotConfigWarnsOnUnusableFlightTemplates asserts both flight templates
// follow the rule the other provider templates follow: an unusable one is reported
// once at startup, with its own key named, and the value is kept so the command
// refuses it instead of claiming nothing is configured.
func TestDecodeBotConfigWarnsOnUnusableFlightTemplates(t *testing.T) {
	t.Parallel()

	cfg, warnings, err := DecodeBotConfig("config.toml", `
[bot]
flight_url = "https://api.adsb.invalid/v2/callsign"
flight_route_url = "file:///etc/{flight}"

[[hubs]]
name = "One"
destination = "`+testHubOne+`"
rooms = ["general"]
`)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if cfg.FlightURL == "" || cfg.FlightRouteURL == "" {
		t.Fatalf("templates were dropped: %q, %q", cfg.FlightURL, cfg.FlightRouteURL)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "flight_url") || !strings.Contains(joined, "placeholder") {
		t.Errorf("warnings = %v, want one naming flight_url and the missing placeholder", warnings)
	}
	if !strings.Contains(joined, "flight_route_url") || !strings.Contains(joined, "http or https") {
		t.Errorf("warnings = %v, want one naming flight_route_url and the scheme problem", warnings)
	}
	if len(warnings) != 2 {
		t.Errorf("warnings = %v, want exactly two", warnings)
	}

	// A usable pair of templates warns about nothing.
	_, warnings, err = DecodeBotConfig("config.toml", `
[bot]
flight_url = "https://api.adsb.lol/v2/callsign/{flight}"
flight_route_url = "https://api.adsbdb.com/v0/callsign/{flight}"

[[hubs]]
name = "One"
destination = "`+testHubOne+`"
rooms = ["general"]
`)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for two usable templates", warnings)
	}
}

// TestDecodeBotConfigHubs covers the hub and room shapes the schema allows.
func TestDecodeBotConfigHubs(t *testing.T) {
	t.Parallel()

	cfg, warnings, err := DecodeBotConfig("config.toml", `
[[hubs]]
name = "gonomadnet Public Hub"
destination = "`+testHubOne+`"
nick = "hubnick"
rooms = ["general", { name = "Ops", key = "s3cret" }]
respond_to = { general = "hubot", OPS = "opsbot" }

[[hubs]]
name = "Second Hub"
destination = "`+testHubTwo+`"
rooms = [{ name = "general" }]
`)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if len(cfg.Hubs) != 2 {
		t.Fatalf("hubs = %v, want 2", len(cfg.Hubs))
	}

	first := cfg.Hubs[0]
	if first.Name != "gonomadnet Public Hub" {
		t.Errorf("Name = %q, want %q", first.Name, "gonomadnet Public Hub")
	}
	if first.Destination != testHubOne {
		t.Errorf("Destination = %q, want %q", first.Destination, testHubOne)
	}
	if got := hex.EncodeToString(first.DestHash); got != testHubOne {
		t.Errorf("DestHash = %v, want %v", got, testHubOne)
	}
	if first.Nick != "hubnick" {
		t.Errorf("Nick = %q, want %q", first.Nick, "hubnick")
	}
	if len(first.Rooms) != 2 {
		t.Fatalf("Rooms = %v, want 2", first.Rooms)
	}
	if first.Rooms[0].Name != "general" || first.Rooms[0].Key != "" {
		t.Errorf("Rooms[0] = %+v, want {general }", first.Rooms[0])
	}
	if first.Rooms[1].Name != "ops" || first.Rooms[1].Key != "s3cret" {
		t.Errorf("Rooms[1] = %+v, want {ops s3cret} (lowercased name)", first.Rooms[1])
	}
	if got := first.RespondTo["general"]; got != "hubot" {
		t.Errorf("RespondTo[general] = %q, want %q", got, "hubot")
	}
	if got := first.RespondTo["ops"]; got != "opsbot" {
		t.Errorf("RespondTo[ops] = %q, want %q (lowercased room key)", got, "opsbot")
	}

	second := cfg.Hubs[1]
	if second.Nick != "" {
		t.Errorf("second hub Nick = %q, want empty (no per-hub override)", second.Nick)
	}
	if len(second.Rooms) != 1 || second.Rooms[0].Name != "general" {
		t.Errorf("second hub Rooms = %+v, want one general room", second.Rooms)
	}
}

// TestDecodeBotConfigSkipsImplicitParentTable asserts the extra implicit parent
// table a stray [hubs.x] sub-table creates is not mistaken for a third hub.
func TestDecodeBotConfigSkipsImplicitParentTable(t *testing.T) {
	t.Parallel()

	cfg, _, err := DecodeBotConfig("config.toml", `
[[hubs]]
name = "One"
destination = "`+testHubOne+`"
rooms = ["general"]

[hubs.extra]
whatever = 1
`)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(cfg.Hubs) != 1 {
		t.Fatalf("hubs = %v, want 1 (the implicit parent table must be skipped)", len(cfg.Hubs))
	}
	if cfg.Hubs[0].Name != "One" {
		t.Errorf("hub name = %q, want %q", cfg.Hubs[0].Name, "One")
	}
}

// TestDecodeBotConfigHubWithoutRooms asserts a hub that joins nothing is valid.
func TestDecodeBotConfigHubWithoutRooms(t *testing.T) {
	t.Parallel()

	cfg, _, err := DecodeBotConfig("config.toml", `
[[hubs]]
name = "Quiet"
destination = "`+testHubOne+`"
`)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(cfg.Hubs) != 1 {
		t.Fatalf("hubs = %v, want 1", len(cfg.Hubs))
	}
	if len(cfg.Hubs[0].Rooms) != 0 {
		t.Errorf("Rooms = %+v, want none", cfg.Hubs[0].Rooms)
	}
}

// TestDecodeBotConfigErrors asserts every malformed configuration fails with a
// message that names the offending key.
func TestDecodeBotConfigErrors(t *testing.T) {
	t.Parallel()

	const good = `
[[hubs]]
name = "One"
destination = "` + testHubOne + `"
rooms = ["general"]
`

	tests := []struct {
		name     string
		src      string
		wantKey  string
		wantAlso string
	}{
		{
			name:    "no hubs configured",
			src:     "[bot]\nnick = \"gorrcbot\"\n",
			wantKey: "hubs",
		},
		{
			name: "empty hub entry misses destination",
			src: `
[[hubs]]
`,
			wantKey:  "destination",
			wantAlso: "1",
		},
		{
			name: "destination missing",
			src: `
[[hubs]]
name = "One"
rooms = ["general"]
`,
			wantKey:  "destination",
			wantAlso: "One",
		},
		{
			name: "destination too short",
			src: `
[[hubs]]
name = "One"
destination = "a012129c"
`,
			wantKey:  "destination",
			wantAlso: "32",
		},
		{
			name: "destination not hex",
			src: `
[[hubs]]
name = "One"
destination = "zzzz129c10205c0b9441fcd2b755b2a7"
`,
			wantKey:  "destination",
			wantAlso: "hexadecimal",
		},
		{
			name: "duplicate hub names",
			src: good + `
[[hubs]]
name = "One"
destination = "` + testHubTwo + `"
`,
			wantKey:  "name",
			wantAlso: "One",
		},
		{
			name: "hub name missing",
			src: `
[[hubs]]
destination = "` + testHubOne + `"
`,
			wantKey:  "name",
			wantAlso: "1",
		},
		{
			name:     "bad reply mode",
			src:      "[bot]\nreply = \"loud\"\n" + good,
			wantKey:  "reply",
			wantAlso: "auto",
		},
		{
			name:    "negative cooldown",
			src:     "[bot]\ncooldown_s = -1\n" + good,
			wantKey: "cooldown_s",
		},
		{
			name:    "zero max_reply_lines",
			src:     "[bot]\nmax_reply_lines = 0\n" + good,
			wantKey: "max_reply_lines",
		},
		{
			name:    "empty nick",
			src:     "[bot]\nnick = \"  \"\n" + good,
			wantKey: "nick",
		},
		{
			name: "rooms entry is neither string nor table",
			src: `
[[hubs]]
name = "One"
destination = "` + testHubOne + `"
rooms = [42]
`,
			wantKey: "rooms",
		},
		{
			name: "room table without a name",
			src: `
[[hubs]]
name = "One"
destination = "` + testHubOne + `"
rooms = [{ key = "s3cret" }]
`,
			wantKey:  "rooms",
			wantAlso: "name",
		},
		{
			name: "room name empty",
			src: `
[[hubs]]
name = "One"
destination = "` + testHubOne + `"
rooms = ["   "]
`,
			wantKey: "rooms",
		},
		{
			name: "malformed toml",
			src:  "[[hubs]\nname = ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := DecodeBotConfig("config.toml", tt.src)
			if err == nil {
				t.Fatal("DecodeBotConfig = nil error, want a failure")
			}
			msg := err.Error()
			if !strings.Contains(msg, "config.toml") {
				t.Errorf("error = %q, want it to name the config file", msg)
			}
			if tt.wantKey != "" && !strings.Contains(msg, tt.wantKey) {
				t.Errorf("error = %q, want it to name the key %q", msg, tt.wantKey)
			}
			if tt.wantAlso != "" && !strings.Contains(msg, tt.wantAlso) {
				t.Errorf("error = %q, want it to mention %q", msg, tt.wantAlso)
			}
		})
	}
}

// TestDecodeBotConfigWarnsOnAnUnusableWeatherURL asserts a provider template the
// bot would refuse is reported once at startup, with the reason, and is still
// kept: the operator sees the problem before a user asks, and the command then
// reports its own line rather than claiming nothing is configured.
func TestDecodeBotConfigWarnsOnAnUnusableWeatherURL(t *testing.T) {
	t.Parallel()

	cfg, warnings, err := DecodeBotConfig("config.toml", `
[bot]
weather_url = "file:///etc/{place}"

[[hubs]]
name = "One"
destination = "`+testHubOne+`"
rooms = ["general"]
`)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if cfg.WeatherURL != "file:///etc/{place}" {
		t.Errorf("WeatherURL = %q, want the configured template kept", cfg.WeatherURL)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "weather_url") || !strings.Contains(joined, "http or https") {
		t.Errorf("warnings = %v, want one naming weather_url and the scheme problem", warnings)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want exactly one", warnings)
	}

	// A usable template warns about nothing.
	_, warnings, err = DecodeBotConfig("config.toml", `
[bot]
weather_url = "https://wttr.in/{place}?format=%l:+%C+%t+%w+%h"

[[hubs]]
name = "One"
destination = "`+testHubOne+`"
rooms = ["general"]
`)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v with a usable provider, want none", warnings)
	}
}

// TestDecodeBotConfigWarnsOnUnknownKeys asserts unknown keys are reported
// without failing the load, so a template from a newer version still runs.
func TestDecodeBotConfigWarnsOnUnknownKeys(t *testing.T) {
	t.Parallel()

	cfg, warnings, err := DecodeBotConfig("config.toml", `
[bot]
nick = "gorrcbot"
nck = "typo"
future_option = true

[[hubs]]
name = "One"
destination = "`+testHubOne+`"
rooms = [{ name = "general", mode = "+k" }]
respond_to = { general = "gorrcbot" }
extra_hub_key = "ignored"
`)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if cfg.Nick != "gorrcbot" {
		t.Errorf("Nick = %q, want %q", cfg.Nick, "gorrcbot")
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"nck", "future_option", "extra_hub_key", "mode"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings = %v, want one naming %q", warnings, want)
		}
	}
	if len(warnings) != 4 {
		t.Errorf("warnings = %v, want 4", warnings)
	}
}

// TestDecodeBotConfigWarnsOnUnjoinedRespondToRoom asserts a respond_to entry
// for a room the hub does not join is reported but not fatal.
func TestDecodeBotConfigWarnsOnUnjoinedRespondToRoom(t *testing.T) {
	t.Parallel()

	_, warnings, err := DecodeBotConfig("config.toml", `
[[hubs]]
name = "One"
destination = "`+testHubOne+`"
rooms = ["general"]
respond_to = { general = "gorrcbot", ops = "opsbot" }
`)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	if !strings.Contains(warnings[0], "ops") {
		t.Errorf("warning = %q, want it to name the room %q", warnings[0], "ops")
	}
}

// TestTriggerNickResolution asserts the documented resolution order for a
// room's trigger nick.
func TestTriggerNickResolution(t *testing.T) {
	t.Parallel()

	cfg := &BotConfig{
		Nick: "botnick",
		Hubs: []HubConfig{
			{
				Name:      "Overridden",
				Nick:      "hubnick",
				Rooms:     []RoomConfig{{Name: "general"}, {Name: "ops"}},
				RespondTo: map[string]string{"general": "roomnick"},
			},
			{
				Name:  "HubNickOnly",
				Nick:  "hubnick",
				Rooms: []RoomConfig{{Name: "general"}},
			},
			{
				Name:  "BotNickOnly",
				Rooms: []RoomConfig{{Name: "general"}},
			},
		},
	}

	tests := []struct {
		name string
		hub  int
		room string
		want string
	}{
		{name: "respond_to wins", hub: 0, room: "general", want: "roomnick"},
		{name: "hub nick when no respond_to", hub: 0, room: "ops", want: "hubnick"},
		{name: "hub nick beats bot nick", hub: 1, room: "general", want: "hubnick"},
		{name: "bot nick is the fallback", hub: 2, room: "general", want: "botnick"},
		{name: "unknown room falls back to the hub nick", hub: 0, room: "nowhere", want: "hubnick"},
		{name: "room lookup is case-insensitive", hub: 0, room: "GENERAL", want: "roomnick"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := cfg.TriggerNick(&cfg.Hubs[tt.hub], tt.room); got != tt.want {
				t.Errorf("TriggerNick = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAdvertisedNickResolution asserts the advertised nick is the per-hub
// override or the global nick — never a room's trigger nick.
func TestAdvertisedNickResolution(t *testing.T) {
	t.Parallel()

	cfg := &BotConfig{Nick: "botnick"}
	withOverride := &HubConfig{Nick: "hubnick"}
	withoutOverride := &HubConfig{}
	if got := cfg.AdvertisedNick(withOverride); got != "hubnick" {
		t.Errorf("AdvertisedNick(override) = %q, want %q", got, "hubnick")
	}
	if got := cfg.AdvertisedNick(withoutOverride); got != "botnick" {
		t.Errorf("AdvertisedNick(no override) = %q, want %q", got, "botnick")
	}
}

// TestDefaultTriggerNick asserts the last-resort trigger nick.
func TestDefaultTriggerNick(t *testing.T) {
	t.Parallel()

	cfg := &BotConfig{}
	hub := &HubConfig{}
	if got := cfg.TriggerNick(hub, "general"); got != DefaultTriggerNick {
		t.Errorf("TriggerNick with everything unset = %q, want %q", got, DefaultTriggerNick)
	}
	if got := cfg.AdvertisedNick(hub); got != DefaultNick {
		t.Errorf("AdvertisedNick with everything unset = %q, want %q", got, DefaultNick)
	}
}

// TestDecodeBotConfigIgnoresACommentedOutHub asserts a hub an operator has
// commented out is never dialled. The generated template no longer ships a
// commented public hub, but the parser must keep honouring comments in a
// hand-edited config.
func TestDecodeBotConfigIgnoresACommentedOutHub(t *testing.T) {
	t.Parallel()

	cfg, warnings, err := DecodeBotConfig("config.toml", `
[[hubs]]
name = "gonomadnet Public Hub"
destination = "`+testHubOne+`"
rooms = ["general"]

# [[hubs]]
# name = "Second Hub"
# destination = "`+testHubTwo+`"
# rooms = [{ name = "general" }]
`)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if len(cfg.Hubs) != 1 {
		t.Fatalf("hubs = %v, want only the live hub", len(cfg.Hubs))
	}
	if cfg.Hubs[0].Destination != testHubOne {
		t.Errorf("Destination = %q, want %q", cfg.Hubs[0].Destination, testHubOne)
	}
	for _, hub := range cfg.Hubs {
		if hub.Destination == testHubTwo {
			t.Error("the commented-out hub was decoded as live")
		}
	}
}

// lxmfConfig is one LXMF configuration, with the one [[hubs]] entry every
// decoding test needs.
func lxmfConfig(body string) string {
	return body + `

[[hubs]]
name = "One"
destination = "` + testHubOne + `"
rooms = ["general"]
`
}

// TestDecodeBotConfigLXMFDefaults asserts LXMF is off unless an operator turns
// it on, and that the announce interval has its documented default.
func TestDecodeBotConfigLXMFDefaults(t *testing.T) {
	t.Parallel()

	cfg, warnings, err := DecodeBotConfig("config.toml", lxmfConfig(""))
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if cfg.LXMFEnabled {
		t.Error("LXMFEnabled = true, want false by default (no router until an operator opts in)")
	}
	if cfg.LXMFPropagationNode != "" || cfg.LXMFPropagationNodeHash != nil {
		t.Errorf("propagation node = %q/%v, want none by default",
			cfg.LXMFPropagationNode, cfg.LXMFPropagationNodeHash)
	}
	if cfg.LXMFAnnounceMinutes != DefaultLXMFAnnounceMinutes {
		t.Errorf("LXMFAnnounceMinutes = %v, want %v", cfg.LXMFAnnounceMinutes, DefaultLXMFAnnounceMinutes)
	}
}

// TestDecodeBotConfigLXMFOverrides asserts every LXMF key is read, that the node
// hash is normalized to lowercase and decoded, and that an empty node means no
// node rather than an empty hash.
func TestDecodeBotConfigLXMFOverrides(t *testing.T) {
	t.Parallel()

	cfg, warnings, err := DecodeBotConfig("config.toml", lxmfConfig(`[bot]
lxmf_enabled = true
lxmf_propagation_node = "`+strings.ToUpper(testHubTwo)+`"
lxmf_announce_minutes = 60
`))
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if !cfg.LXMFEnabled {
		t.Error("LXMFEnabled = false, want true")
	}
	if cfg.LXMFPropagationNode != testHubTwo {
		t.Errorf("LXMFPropagationNode = %q, want the lowercase %q", cfg.LXMFPropagationNode, testHubTwo)
	}
	if got := len(cfg.LXMFPropagationNodeHash); got != lxmfDestinationHashLen {
		t.Errorf("the decoded node hash = %v bytes, want %v", got, lxmfDestinationHashLen)
	}
	if cfg.LXMFAnnounceMinutes != 60 {
		t.Errorf("LXMFAnnounceMinutes = %v, want 60", cfg.LXMFAnnounceMinutes)
	}

	// An explicitly empty node is no node.
	cfg, _, err = DecodeBotConfig("config.toml", lxmfConfig(`[bot]
lxmf_enabled = true
lxmf_propagation_node = ""
`))
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if cfg.LXMFPropagationNode != "" || cfg.LXMFPropagationNodeHash != nil {
		t.Errorf("propagation node = %q/%v, want none", cfg.LXMFPropagationNode, cfg.LXMFPropagationNodeHash)
	}
}

// TestDecodeBotConfigLXMFErrors asserts a badly shaped LXMF key fails the load
// with a message naming the key and what is wrong with it, because silently
// ignoring either would leave an operator believing a message will be queued
// when it cannot be.
func TestDecodeBotConfigLXMFErrors(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		body    string
		wantKey string
		wantSub string
	}{
		{
			name:    "a node hash that is too short",
			body:    "[bot]\nlxmf_propagation_node = \"abcd12\"\n",
			wantKey: "lxmf_propagation_node",
			wantSub: "hexadecimal characters",
		},
		{
			name:    "a node hash that is not hexadecimal",
			body:    "[bot]\nlxmf_propagation_node = \"" + strings.Repeat("z", lxmfDestinationHexLen) + "\"\n",
			wantKey: "lxmf_propagation_node",
			wantSub: "not valid hexadecimal",
		},
		{
			name:    "an announce interval of zero",
			body:    "[bot]\nlxmf_announce_minutes = 0\n",
			wantKey: "lxmf_announce_minutes",
			wantSub: "at least 1",
		},
		{
			name:    "an announce interval that is not a number",
			body:    "[bot]\nlxmf_announce_minutes = \"often\"\n",
			wantKey: "lxmf_announce_minutes",
			wantSub: "whole number",
		},
		{
			name:    "lxmf_enabled that is not a boolean",
			body:    "[bot]\nlxmf_enabled = \"yes\"\n",
			wantKey: "lxmf_enabled",
			wantSub: "true or false",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := DecodeBotConfig("config.toml", lxmfConfig(tt.body))
			if err == nil {
				t.Fatalf("DecodeBotConfig accepted %q", tt.body)
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error = %q, want it to name %q", err, tt.wantKey)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want it to explain %q", err, tt.wantSub)
			}
		})
	}
}

// TestDecodeBotConfigWarnsOnANodeWithoutLXMF asserts a propagation node
// configured while LXMF is off is reported once at startup: the key is dead
// configuration, and the operator has to hear that rather than wonder later why
// nothing is store-and-forwarded.
func TestDecodeBotConfigWarnsOnANodeWithoutLXMF(t *testing.T) {
	t.Parallel()

	cfg, warnings, err := DecodeBotConfig("config.toml", lxmfConfig(`[bot]
lxmf_propagation_node = "`+testHubTwo+`"
`))
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if cfg.LXMFEnabled {
		t.Error("LXMFEnabled = true, want false: a node alone must not switch LXMF on")
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "lxmf_propagation_node") || !strings.Contains(joined, "lxmf_enabled") {
		t.Errorf("warnings = %v, want one naming both keys", warnings)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want exactly one", warnings)
	}

	// With LXMF on there is nothing to warn about.
	_, warnings, err = DecodeBotConfig("config.toml", lxmfConfig(`[bot]
lxmf_enabled = true
lxmf_propagation_node = "`+testHubTwo+`"
`))
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v with LXMF enabled, want none", warnings)
	}
}

// TestDecodeBotConfigReadsTheTopLevelPaths asserts the two keys the first-run
// template writes above [bot] take effect there. Both were dropped silently, so a
// bot told to keep its identity elsewhere quietly used the default path instead.
func TestDecodeBotConfigReadsTheTopLevelPaths(t *testing.T) {
	t.Parallel()

	cfg, warnings, err := DecodeBotConfig("config.toml", `
identity_path = "/etc/gorrcbot/bot_identity"
storage_dir = "/var/lib/gorrcbot"

[bot]
nick = "gorrcbot"
`+misplacedHub)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none: the template writes exactly this shape", warnings)
	}
	if cfg.IdentityPath != "/etc/gorrcbot/bot_identity" {
		t.Errorf("IdentityPath = %q, want the top-level value", cfg.IdentityPath)
	}
	if cfg.StorageDir != "/var/lib/gorrcbot" {
		t.Errorf("StorageDir = %q, want the top-level value", cfg.StorageDir)
	}
}

// TestDecodeBotConfigPrefersTheBotTablePaths asserts [bot] wins over the top level
// when a key appears twice, so the more specific location is the one that counts.
func TestDecodeBotConfigPrefersTheBotTablePaths(t *testing.T) {
	t.Parallel()

	cfg, _, err := DecodeBotConfig("config.toml", `
identity_path = "/etc/gorrcbot/bot_identity"
storage_dir = "/var/lib/gorrcbot"

[bot]
identity_path = "/srv/gorrcbot/id"
storage_dir = "/srv/gorrcbot/storage"
`+misplacedHub)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if cfg.IdentityPath != "/srv/gorrcbot/id" {
		t.Errorf("IdentityPath = %q, want the [bot] value", cfg.IdentityPath)
	}
	if cfg.StorageDir != "/srv/gorrcbot/storage" {
		t.Errorf("StorageDir = %q, want the [bot] value", cfg.StorageDir)
	}
}

// misplacedHub is one minimal hub entry, so a test about a top-level key is not
// answered with the separate complaint that no hub is configured.
const misplacedHub = "\n[[hubs]]\nname = \"h\"\ndestination = \"a012129c10205c0b9441fcd2b755b2a7\"\nrooms = [\"general\"]\n"

// TestDecodeBotConfigWarnsAboutMisplacedKeys asserts a key outside [bot] is
// reported rather than dropped in silence: a setting the operator wrote and the
// bot ignored is the worst of both worlds.
func TestDecodeBotConfigWarnsAboutMisplacedKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		src      string
		wantWarn string
	}{
		{
			name:     "a [bot] key at the top level",
			src:      "nick = \"sneaky\"\n\n[bot]\nnick = \"gorrcbot\"\n" + misplacedHub,
			wantWarn: "must be inside [bot]",
		},
		{
			name:     "an unknown key at the top level",
			src:      "state_path = \"/tmp/state\"\n\n[bot]\nnick = \"gorrcbot\"\n" + misplacedHub,
			wantWarn: "unknown top-level key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, warnings, err := DecodeBotConfig("config.toml", tt.src)
			if err != nil {
				t.Fatalf("DecodeBotConfig: %v", err)
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], tt.wantWarn) {
				t.Errorf("warnings = %v, want one containing %q", warnings, tt.wantWarn)
			}
			if cfg.Nick != "gorrcbot" {
				t.Errorf("Nick = %q, want the [bot] value to win", cfg.Nick)
			}
		})
	}
}

// TestDecodeBotConfigReadsKJVTxtFile asserts the Bible text path is read from
// [bot], is also accepted above it like the other path keys, and that the [bot]
// value wins when both are written.
func TestDecodeBotConfigReadsKJVTxtFile(t *testing.T) {
	t.Parallel()

	root := filepath.Join(tempDir(t), "root-kjv.txt")
	inner := filepath.Join(tempDir(t), "inner-kjv.txt")
	for _, path := range []string{root, inner} {
		if err := os.WriteFile(path, []byte("Ge1:1 In the beginning\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "inside [bot]",
			src:  "[bot]\nkjv_txt_file = \"" + inner + "\"\n" + misplacedHub,
			want: inner,
		},
		{
			name: "above [bot]",
			src:  "kjv_txt_file = \"" + root + "\"\n" + misplacedHub,
			want: root,
		},
		{
			name: "both, [bot] wins",
			src:  "kjv_txt_file = \"" + root + "\"\n\n[bot]\nkjv_txt_file = \"" + inner + "\"\n" + misplacedHub,
			want: inner,
		},
		{
			name: "empty means the command is off",
			src:  "[bot]\nkjv_txt_file = \"\"\n" + misplacedHub,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, warnings, err := DecodeBotConfig("config.toml", tt.src)
			if err != nil {
				t.Fatalf("DecodeBotConfig: %v", err)
			}
			if len(warnings) != 0 {
				t.Errorf("warnings = %v, want none", warnings)
			}
			if cfg.KJVTxtFile != tt.want {
				t.Errorf("KJVTxtFile = %q, want %q", cfg.KJVTxtFile, tt.want)
			}
		})
	}
}

// TestDecodeBotConfigWarnsOnUnreadableKJVTxtFile asserts an unusable path is
// reported once at startup, and that the value is kept so the command reports it
// rather than claiming the feature is unconfigured.
func TestDecodeBotConfigWarnsOnUnreadableKJVTxtFile(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(tempDir(t), "absent-kjv.txt")
	cfg, warnings, err := DecodeBotConfig("config.toml",
		"[bot]\nkjv_txt_file = \""+missing+"\"\n"+misplacedHub)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "kjv_txt_file") ||
		!strings.Contains(warnings[0], "cannot be read") {
		t.Errorf("warnings = %v, want one naming the unreadable kjv_txt_file", warnings)
	}
	if cfg.KJVTxtFile != missing {
		t.Errorf("KJVTxtFile = %q, want the configured value kept", cfg.KJVTxtFile)
	}

	dir := tempDir(t)
	_, warnings, err = DecodeBotConfig("config.toml",
		"kjv_txt_file = \""+dir+"\"\n"+misplacedHub)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "is a directory") {
		t.Errorf("warnings = %v, want one naming the directory", warnings)
	}

	// A template with the key empty is not a warning: it is the default.
	_, warnings, err = DecodeBotConfig("config.toml", "[bot]\nkjv_txt_file = \"\"\n"+misplacedHub)
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("an empty kjv_txt_file warned: %v", warnings)
	}
}

// TestDecodeBotConfigToleratesHubLevelKJVTxtFile asserts the Bible text path an
// operator appended after a [[hubs]] block still works, and that the misplacement
// is reported rather than silently ignored.
func TestDecodeBotConfigToleratesHubLevelKJVTxtFile(t *testing.T) {
	t.Parallel()

	hubPath := filepath.Join(tempDir(t), "hub-kjv.txt")
	botPath := filepath.Join(tempDir(t), "bot-kjv.txt")
	for _, path := range []string{hubPath, botPath} {
		if err := os.WriteFile(path, []byte("Ge1:1 In the beginning\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg, warnings, err := DecodeBotConfig("config.toml",
		"[[hubs]]\nname = \"One\"\ndestination = \""+testHubOne+"\"\nrooms = [\"general\"]\nkjv_txt_file = \""+hubPath+"\"\n")
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "belongs inside [bot]") {
		t.Errorf("warnings = %v, want one explaining where the key belongs", warnings)
	}
	if cfg.KJVTxtFile != hubPath {
		t.Errorf("KJVTxtFile = %q, want the hub-level path accepted", cfg.KJVTxtFile)
	}

	cfg, warnings, err = DecodeBotConfig("config.toml",
		"[bot]\nkjv_txt_file = \""+botPath+"\"\n[[hubs]]\nname = \"One\"\ndestination = \""+testHubOne+"\"\nrooms = [\"general\"]\nkjv_txt_file = \""+hubPath+"\"\n")
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	if cfg.KJVTxtFile != botPath {
		t.Errorf("KJVTxtFile = %q, want the [bot] value to win", cfg.KJVTxtFile)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "ignored") {
		t.Errorf("warnings = %v, want one saying the hub-level copy is ignored", warnings)
	}
}
