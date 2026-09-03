// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

// Wire constants for the RRC protocol, matching rrcd/constants.py exactly.
// Renaming any of these breaks interop with Python clients.

const (
	// RRCVersion is the protocol version; envelopes carry it in key 0.
	RRCVersion = 1
	// HubDestName is the hub destination name (app "rrc", aspect "hub").
	HubDestName = "rrc.hub"
)

// Envelope keys (flat CBOR map, unsigned-int keys).
const (
	KV    int64 = 0
	KT    int64 = 1
	KID   int64 = 2
	KTS   int64 = 3
	KSrc  int64 = 4
	KRoom int64 = 5
	KBody int64 = 6
	KNick int64 = 7
	KDst  int64 = 8
)

// Message types.
const (
	THello    int64 = 1
	TWelcome  int64 = 2
	TJoin     int64 = 10
	TJoined   int64 = 11
	TPart     int64 = 12
	TParted   int64 = 13
	TMsg      int64 = 20
	TNotice   int64 = 21
	TAction   int64 = 22
	TPing     int64 = 30
	TPong     int64 = 31
	TError    int64 = 40
	TResource int64 = 50
)

// HELLO body keys.
const (
	BHelloName       int64 = 0
	BHelloVer        int64 = 1
	BHelloCaps       int64 = 2
	BHelloNickLegacy int64 = 64
)

// WELCOME body keys.
const (
	BWelcomeHub    int64 = 0
	BWelcomeVer    int64 = 1
	BWelcomeCaps   int64 = 2
	BWelcomeLimits int64 = 3
)

// Limits-map keys within the WELCOME body's limits map.
const (
	BLimitMaxNickBytes       int64 = 0
	BLimitMaxRoomNameBytes   int64 = 1
	BLimitMaxMsgBodyBytes    int64 = 2
	BLimitMaxRoomsPerSession int64 = 3
	BLimitRateMsgsPerMinute  int64 = 4
)

// Capability-map keys (advisory; no client gates on them).
const (
	CAPResourceEnvelope int64 = 0
	CAPAction           int64 = 1
	CAPDirectNotice     int64 = 2
)

// RESOURCE_ENVELOPE body keys.
const (
	BResID       int64 = 0
	BResKind     int64 = 1
	BResSize     int64 = 2
	BResSHA256   int64 = 3
	BResEncoding int64 = 4
)

// Resource kinds (string values on the wire).
const (
	ResKindNotice = "notice"
	ResKindMOTD   = "motd"
	ResKindBlob   = "blob"
)

// MaxNoticeChunkChars is the maximum characters per NOTICE chunk for
// MTU-safe delivery.
const MaxNoticeChunkChars = 512

// defaultNickMaxBytes is the hardcoded default nick limit used by
// make_envelope's nick normalization (a parity quirk: make_envelope does not
// consult the configured max_nick_bytes).
const defaultNickMaxBytes = 32
