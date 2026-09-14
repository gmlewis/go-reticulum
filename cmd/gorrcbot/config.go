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
	"fmt"
	"os"
	"sort"
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
	AnnounceOnJoin bool
	// MaxReplyLines bounds the NOTICE lines a single reply may produce.
	MaxReplyLines int
	// StorageDir is the RRC client's storage directory for saved history.
	StorageDir string
	// WeatherURL is an optional provider template for weather/wx; {place} is
	// replaced with the requested place. Empty disables the commands.
	WeatherURL string
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
// table still yields a usable configuration.
func (d *configDecoder) decodeBot() error {
	d.cfg = BotConfig{
		IdentityPath:   d.defaults.IdentityPath,
		Nick:           DefaultNick,
		Reply:          ReplyAuto,
		CooldownSecs:   DefaultCooldownSecs,
		AnnounceOnJoin: true,
		MaxReplyLines:  DefaultMaxReplyLines,
		StorageDir:     d.defaults.StorageDir,
	}
	return nil
}

// botKeys are the recognized [bot] keys.
var botKeys = map[string]bool{
	"identity_path":    true,
	"nick":             true,
	"reply":            true,
	"cooldown_s":       true,
	"announce_on_join": true,
	"max_reply_lines":  true,
	"storage_dir":      true,
	"weather_url":      true,
}

// warn records a non-fatal configuration problem.
func (d *configDecoder) warn(format string, args ...any) {
	d.warnings = append(d.warnings, d.name+": "+fmt.Sprintf(format, args...))
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
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			if s == "" {
				return d.keyError("[bot]", key, "must not be empty")
			}
			d.cfg.IdentityPath = s
		case "storage_dir":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			d.cfg.StorageDir = s
		case "weather_url":
			s, err := d.stringValue("[bot]", key, kv)
			if err != nil {
				return err
			}
			d.cfg.WeatherURL = s
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
		}
	}
	return nil
}

// hubKeys are the recognized [[hubs]] keys.
var hubKeys = map[string]bool{
	"name":        true,
	"destination": true,
	"nick":        true,
	"rooms":       true,
	"respond_to":  true,
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
