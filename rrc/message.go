// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"fmt"

	"github.com/gmlewis/go-reticulum/rrc/cbor"
)

// RRCMessage represents a single chat message.
type RRCMessage struct {
	Kind    string // "msg", "action", "notice", "system", "error"
	Room    string // room name (lowercased), empty for global notices
	Src     []byte // sender identity hash, nil for system/notices
	Nick    string // sender display nick
	Text    string // message content
	Ts      int64  // timestamp in milliseconds since epoch
	Mention bool   // true if message mentions the local user
	// Pinned marks the hub's greeting MOTD notice: exempt from the
	// ephemeral-notice purge. The greeting is standing hub info that rrcd
	// re-sends on every WELCOME; the purge previously erased it minutes
	// after a connect, which made some fleet nodes appear MOTD-less
	// (2026-09-03 captures).
	Pinned bool
}

// HistoryEntry returns a map suitable for CBOR encoding to the
// history file format.
func (m *RRCMessage) HistoryEntry() map[string]any {
	entry := map[string]any{
		HKind: m.Kind,
		HTS:   m.Ts,
		HText: m.Text,
	}
	if len(m.Src) > 0 {
		entry[HSrc] = m.Src
	}
	if m.Nick != "" {
		entry[HNick] = m.Nick
	}
	// if m.Room != "" {
	//   Room is stored implicitly in the file path, but included for clarity
	// }
	if m.Mention {
		entry[HMention] = true
	}
	return entry
}

// DecodeHistoryEntry creates an RRCMessage from a CBOR-decoded
// history entry map.
func DecodeHistoryEntry(entry map[string]any) *RRCMessage {
	msg := &RRCMessage{}

	if v, ok := entry[HKind].(string); ok {
		msg.Kind = v
	}
	if v, ok := entry[HTS].(int64); ok {
		msg.Ts = v
	} else if v, ok := entry[HTS].(uint64); ok {
		msg.Ts = int64(v)
	} else if v, ok := entry[HTS].(int); ok {
		msg.Ts = int64(v)
	}
	if v, ok := entry[HText].(string); ok {
		msg.Text = v
	}
	if v, ok := entry[HSrc].([]byte); ok {
		msg.Src = v
	}
	if v, ok := entry[HNick].(string); ok {
		msg.Nick = v
	}
	if v, ok := entry[HMention].(bool); ok {
		msg.Mention = v
	}

	return msg
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case []byte:
		return x == nil
	case []any:
		return x == nil
	case []string:
		return x == nil
	case map[any]any:
		return x == nil
	case map[string]any:
		return x == nil
	}
	return false
}

// MakeClientEnvelope constructs a CBOR-encodable envelope map for the RRC protocol.
// TEXT fields (room, nick) are converted to Go strings so cbor encodes them
// as CBOR TEXT strings — Python's RRC client and hubs send room names, nicks,
// and hello body fields as text, and a byte-string encoding makes the hub
// silently drop the message (the differential explorer's Channels finding:
// the link connected but the hub never responded). Binary fields (source hash,
// message ID) stay []byte (CBOR byte strings), matching Python's bytes source
// hash and os.urandom message id.
func MakeClientEnvelope(msgType int, src, room, nick []byte, body any, mid []byte, ts int64) map[any]any {
	env := map[any]any{
		KeyVersion:   RRCVersion,
		KeyType:      msgType,
		KeyTimestamp: ts,
	}
	if len(mid) > 0 {
		env[KeyMessageID] = mid
	}
	if len(src) > 0 {
		env[KeySource] = src
	}
	if len(room) > 0 {
		env[KeyRoom] = string(room)
	}
	if !isNil(body) {
		env[KeyBody] = body
	}
	if len(nick) > 0 {
		env[KeyNick] = string(nick)
	}
	return env
}

// EncodeEnvelope serializes an envelope map to CBOR bytes.
func EncodeEnvelope(env map[any]any) ([]byte, error) {
	return cbor.Encode(env), nil
}

func toMapRecursive(v any) any {
	switch val := v.(type) {
	case *cbor.Map:
		out := make(map[any]any, val.Len())
		for _, p := range val.Pairs() {
			out[p.Key] = toMapRecursive(p.Val)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = toMapRecursive(item)
		}
		return out
	default:
		return v
	}
}

// DecodeEnvelope deserializes CBOR bytes to an envelope map.
func DecodeEnvelope(data []byte) (map[any]any, error) {
	val, err := cbor.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("decoding RRC envelope: %w", err)
	}
	if m, ok := val.(*cbor.Map); ok {
		if res, ok := toMapRecursive(m).(map[any]any); ok {
			return res, nil
		}
	}
	if m, ok := val.(map[any]any); ok {
		return m, nil
	}
	return nil, fmt.Errorf("decoding RRC envelope: expected map, got %T", val)
}

// MentionRegex returns a simple pattern for detecting @mentions.
// The actual implementation uses word-boundary-aware matching.
func MentionRegex(nick string) string {
	return fmt.Sprintf(`(?i)\b@%v\b`, nick)
}
