// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"crypto/rand"
	"fmt"
	"time"

	"github.com/gmlewis/go-reticulum/rrcd/cbor"
)

// EnvelopeOpt configures optional envelope fields. Field presence mirrors
// Python's None-vs-value distinction: WithBody(nil) omits K_BODY while
// WithBody("") includes it.
type EnvelopeOpt func(*envelopeFields)

type envelopeFields struct {
	dst     []byte
	hasDst  bool
	room    *string
	hasRoom bool
	body    any
	hasBody bool
	nick    string
	hasNick bool
	mid     []byte
	hasMid  bool
	ts      int64
	hasTS   bool
}

// WithDst sets the destination identity (K_DST, key 8).
func WithDst(dst []byte) EnvelopeOpt {
	return func(f *envelopeFields) { f.dst, f.hasDst = dst, true }
}

// WithRoom sets the room name (K_ROOM, key 5). An empty room is still sent.
func WithRoom(room string) EnvelopeOpt {
	r := room
	return func(f *envelopeFields) { f.room, f.hasRoom = &r, true }
}

// WithRoomPtr sets the optional room name (K_ROOM, key 5), mirroring
// Python's room: str | None. nil omits the key.
func WithRoomPtr(room *string) EnvelopeOpt {
	return func(f *envelopeFields) { f.room, f.hasRoom = room, true }
}

// WithBody sets the body (K_BODY, key 6). A nil body is omitted; an empty
// string body is sent.
func WithBody(body any) EnvelopeOpt {
	return func(f *envelopeFields) { f.body, f.hasBody = body, true }
}

// WithNick sets the sender nickname (K_NICK, key 7) after normalization with
// the hardcoded 32-byte default limit; an invalid nick is omitted.
func WithNick(nick string) EnvelopeOpt {
	return func(f *envelopeFields) { f.nick, f.hasNick = nick, true }
}

// WithID sets the message id; an empty id falls back to a fresh random one.
func WithID(mid []byte) EnvelopeOpt {
	return func(f *envelopeFields) { f.mid, f.hasMid = mid, true }
}

// WithTS sets the millisecond timestamp; zero falls back to the current
// time, mirroring Python's falsy ts check.
func WithTS(ts int64) EnvelopeOpt {
	return func(f *envelopeFields) { f.ts, f.hasTS = ts, true }
}

// NowMS returns the current Unix time in milliseconds.
func NowMS() int64 { return time.Now().UnixMilli() }

// NewMsgID returns a fresh 8-byte random message id.
func NewMsgID() []byte {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is effectively unreachable; an all-zero id
		// is still a valid CBOR byte string.
		return b
	}
	return b
}

// MakeEnvelope builds an envelope in make_envelope's exact key order: the
// base map 0,1,2,3,4 then optional 8,5,6,7.
func MakeEnvelope(msgType int, src []byte, opts ...EnvelopeOpt) *cbor.Map {
	f := &envelopeFields{}
	for _, opt := range opts {
		opt(f)
	}
	m := cbor.NewMap()
	m.Set(KV, int64(RRCVersion))
	m.Set(KT, int64(msgType))
	if f.hasMid && len(f.mid) > 0 {
		m.Set(KID, f.mid)
	} else {
		m.Set(KID, NewMsgID())
	}
	if f.hasTS && f.ts != 0 {
		m.Set(KTS, f.ts)
	} else {
		m.Set(KTS, NowMS())
	}
	m.Set(KSrc, src)
	if f.hasDst {
		m.Set(KDst, f.dst)
	}
	// Python's make_envelope includes K_ROOM whenever room is not None;
	// a nil Go pointer stands in for None, an empty name is still sent.
	if f.hasRoom && f.room != nil {
		m.Set(KRoom, *f.room)
	}
	// Python's make_envelope includes the body whenever it is not None;
	// a nil Go interface value stands in for None.
	if f.body != nil {
		m.Set(KBody, f.body)
	}
	if f.hasNick {
		if n := NormalizeNick(f.nick, defaultNickMaxBytes); n != "" {
			m.Set(KNick, n)
		}
	}
	return m
}

// ValidateEnvelope checks the exact Python validation order and produces the
// verbatim error strings. Bool values count as ints (True == 1) exactly as
// Python's isinstance checks allow.
func ValidateEnvelope(env *cbor.Map) error {
	if env == nil {
		return fmt.Errorf("envelope must be a CBOR map (dict)")
	}
	for _, pair := range env.Pairs() {
		switch pair.Key.(type) {
		case int64, uint64, bool:
		default:
			return fmt.Errorf("envelope keys must be integers")
		}
		if k, ok := pair.Key.(int64); ok && k < 0 {
			return fmt.Errorf("envelope keys must be unsigned integers")
		}
	}
	for _, k := range []int64{KV, KT, KID, KTS, KSrc} {
		if !env.Has(k) {
			return fmt.Errorf("missing envelope key %v", k)
		}
	}
	v, _ := env.Get(KV)
	if !isIntLike(v) {
		return fmt.Errorf("protocol version must be an integer")
	}
	if n, _ := intValue(v); n != RRCVersion {
		return fmt.Errorf("unsupported version %v", pythonRepr(v))
	}

	tv, _ := env.Get(KT)
	if !isIntLike(tv) {
		return fmt.Errorf("message type must be an integer")
	}

	mid, _ := env.Get(KID)
	if !isBytes(mid) {
		return fmt.Errorf("message id must be bytes")
	}

	tsv, _ := env.Get(KTS)
	if !isIntLike(tsv) {
		return fmt.Errorf("timestamp must be an integer")
	}
	if n, _ := intValue(tsv); n < 0 {
		return fmt.Errorf("timestamp must be unsigned")
	}

	src, _ := env.Get(KSrc)
	if !isBytes(src) {
		return fmt.Errorf("sender identity must be bytes")
	}

	if room, ok := env.Get(KRoom); ok {
		if _, isStr := room.(string); !isStr {
			return fmt.Errorf("room name must be a string")
		}
	}
	if nick, ok := env.Get(KNick); ok {
		if _, isStr := nick.(string); !isStr {
			return fmt.Errorf("nickname must be a string")
		}
	}
	if dst, ok := env.Get(KDst); ok {
		if !isBytes(dst) {
			return fmt.Errorf("destination identity must be bytes")
		}
	}
	return nil
}

// isIntLike reports whether the decoded value is an int in Python's sense
// (bools are ints).
func isIntLike(v any) bool {
	switch v.(type) {
	case int64, uint64, bool:
		return true
	}
	return false
}

// isBytes reports whether the decoded value is a byte string.
func isBytes(v any) bool {
	_, ok := v.([]byte)
	return ok
}

// intValue converts an int-like decoded value to int64. Bools map to 1/0.
// A uint64 beyond int64 range reports 0 (such values never occur in valid
// RRC traffic).
func intValue(v any) (int64, bool) {
	switch n := v.(type) {
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case int64:
		return n, true
	case uint64:
		if n <= 1<<63-1 {
			return int64(n), true
		}
	}
	return 0, false
}

// pythonRepr renders a decoded scalar the way Python's error messages embed
// it (True/False for bools, decimal for ints).
func pythonRepr(v any) string {
	switch n := v.(type) {
	case bool:
		if n {
			return "True"
		}
		return "False"
	case int64:
		return fmt.Sprintf("%v", n)
	case uint64:
		return fmt.Sprintf("%v", n)
	case string:
		return n
	case float64:
		return fmt.Sprintf("%v", n)
	case []byte:
		return fmt.Sprintf("%v", n)
	}
	return fmt.Sprintf("%v", v)
}

// EnvGetInt returns the envelope entry as an integer.
func EnvGetInt(env *cbor.Map, key int64) (int64, bool) {
	v, ok := env.Get(key)
	if !ok {
		return 0, false
	}
	return intValue(v)
}

// EnvGetString returns the envelope entry as a text string.
func EnvGetString(env *cbor.Map, key int64) (string, bool) {
	v, ok := env.Get(key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// EnvGetBytes returns the envelope entry as a byte string.
func EnvGetBytes(env *cbor.Map, key int64) ([]byte, bool) {
	v, ok := env.Get(key)
	if !ok {
		return nil, false
	}
	b, ok := v.([]byte)
	return b, ok
}

// EnvGetMap returns the envelope entry as a nested ordered map.
func EnvGetMap(env *cbor.Map, key int64) (*cbor.Map, bool) {
	v, ok := env.Get(key)
	if !ok {
		return nil, false
	}
	m, ok := v.(*cbor.Map)
	return m, ok
}

// EnvSetInt stores an integer under an integer key, preserving the key's
// position when present.
func EnvSetInt(env *cbor.Map, key, val int64) { env.Set(key, val) }

// EnvSetString stores a text value under an integer key, preserving the
// key's position when present.
func EnvSetString(env *cbor.Map, key int64, val string) { env.Set(key, val) }

// EnvSetBytes stores a byte string under an integer key, preserving the
// key's position when present.
func EnvSetBytes(env *cbor.Map, key int64, val []byte) { env.Set(key, val) }

// EnvPop removes a key and returns its value, mirroring dict.pop.
func EnvPop(env *cbor.Map, key int64) (any, bool) { return env.Pop(key) }

// roomDeref returns the string value of a room pointer, or empty for nil.
func roomDeref(r *string) string {
	if r == nil {
		return ""
	}
	return *r
}
