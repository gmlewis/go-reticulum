// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

// Envelope keys for CBOR-encoded messages (client aliases).
const (
	KeyVersion   = int(KV)
	KeyType      = int(KT)
	KeyMessageID = int(KID)
	KeyTimestamp = int(KTS)
	KeySource    = int(KSrc)
	KeyRoom      = int(KRoom)
	KeyBody      = int(KBody)
	KeyNick      = int(KNick)
	KeyDst       = int(KDst)
)

// Message types (client aliases).
const (
	TypeHello            = int(THello)
	TypeWelcome          = int(TWelcome)
	TypeJoin             = int(TJoin)
	TypeJoined           = int(TJoined)
	TypePart             = int(TPart)
	TypeParted           = int(TParted)
	TypeMsg              = int(TMsg)
	TypeNotice           = int(TNotice)
	TypeAction           = int(TAction)
	TypePing             = int(TPing)
	TypePong             = int(TPong)
	TypeError            = int(TError)
	TypeResourceEnvelope = int(TResource)
)

// Limit keys in WELCOME body.
const (
	LMaxNickBytes           = int(BLimitMaxNickBytes)
	LMaxRoomNameBytes       = int(BLimitMaxRoomNameBytes)
	LMaxMsgBodyBytes        = int(BLimitMaxMsgBodyBytes)
	LMaxRoomsPerSession     = int(BLimitMaxRoomsPerSession)
	LRateLimitMsgsPerMinute = int(BLimitRateMsgsPerMinute)
)

// Capability flags (client aliases).
const (
	CapResourceEnvelope = int(CAPResourceEnvelope)
	CapAction           = int(CAPAction)
	CapDirectNotice     = int(CAPDirectNotice)
)

// Resource envelope body keys (client aliases).
const (
	ResKeyID       = int(BResID)
	ResKeyKind     = int(BResKind)
	ResKeySize     = int(BResSize)
	ResKeySHA256   = int(BResSHA256)
	ResKeyEncoding = int(BResEncoding)
)

// Default values.
const (
	DefaultDestName      = HubDestName
	DefaultMaxNickBytes  = 32
	DefaultMaxRoomBytes  = 64
	DefaultMaxMsgBytes   = 350
	DefaultMaxRooms      = 32
	DefaultRatePerMinute = 240
)

// History entry keys for persistence.
const (
	HKind    = "k"
	HSrc     = "s"
	HNick    = "n"
	HText    = "t"
	HTS      = "ts"
	HMention = "m"
)

// Hub connection status.
const (
	StatusDisconnected = 0
	StatusConnecting   = 1
	StatusConnected    = 2
	StatusFailed       = 3
)

// Timing constants.
const (
	CleanHistoryInterval = 5   // seconds between history cleanups
	NoticeTimeout        = 600 // seconds before ephemeral notices expire
)

// NowMs returns the current Unix time in milliseconds.
func NowMs() int64 {
	return NowMS()
}

// MsgID returns an 8-byte random message ID.
func MsgID() []byte {
	return NewMsgID()
}
