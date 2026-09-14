// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/hex"
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
	if !cfg.AnnounceOnJoin {
		t.Error("AnnounceOnJoin = false, want true by default")
	}
	if cfg.MaxReplyLines != DefaultMaxReplyLines {
		t.Errorf("MaxReplyLines = %v, want %v", cfg.MaxReplyLines, DefaultMaxReplyLines)
	}
	if cfg.WeatherURL != "" {
		t.Errorf("WeatherURL = %q, want empty by default", cfg.WeatherURL)
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
announce_on_join = false
max_reply_lines = 3
storage_dir = "/tmp/storage"
weather_url = "https://example.invalid/{place}"

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
	if cfg.AnnounceOnJoin {
		t.Error("AnnounceOnJoin = true, want false")
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
name = "RNS Community"
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
# name = "RNS Community"
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
