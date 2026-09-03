// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

// Every constant value is captured from the Python rrcd/constants.py 0.3.2
// source; the Go Version diverges by documented convention.
func TestConstants(t *testing.T) {
	t.Parallel()
	if RRCVersion != 1 {
		t.Errorf("RRCVersion = %v, want 1", RRCVersion)
	}
	if HubDestName != "rrc.hub" {
		t.Errorf("HubDestName = %v, want rrc.hub", HubDestName)
	}
	if rns.VERSION == "" {
		t.Errorf("rns.VERSION must not be empty")
	}
	for _, tc := range []struct {
		name string
		got  int64
		want int64
	}{
		{"KV", KV, 0},
		{"KT", KT, 1},
		{"KID", KID, 2},
		{"KTS", KTS, 3},
		{"KSrc", KSrc, 4},
		{"KRoom", KRoom, 5},
		{"KBody", KBody, 6},
		{"KNick", KNick, 7},
		{"KDst", KDst, 8},
		{"THello", THello, 1},
		{"TWelcome", TWelcome, 2},
		{"TJoin", TJoin, 10},
		{"TJoined", TJoined, 11},
		{"TPart", TPart, 12},
		{"TParted", TParted, 13},
		{"TMsg", TMsg, 20},
		{"TNotice", TNotice, 21},
		{"TAction", TAction, 22},
		{"TPing", TPing, 30},
		{"TPong", TPong, 31},
		{"TError", TError, 40},
		{"TResource", TResource, 50},
		{"BHelloName", BHelloName, 0},
		{"BHelloVer", BHelloVer, 1},
		{"BHelloCaps", BHelloCaps, 2},
		{"BHelloNickLegacy", BHelloNickLegacy, 64},
		{"BWelcomeHub", BWelcomeHub, 0},
		{"BWelcomeVer", BWelcomeVer, 1},
		{"BWelcomeCaps", BWelcomeCaps, 2},
		{"BWelcomeLimits", BWelcomeLimits, 3},
		{"BLimitMaxNickBytes", BLimitMaxNickBytes, 0},
		{"BLimitMaxRoomNameBytes", BLimitMaxRoomNameBytes, 1},
		{"BLimitMaxMsgBodyBytes", BLimitMaxMsgBodyBytes, 2},
		{"BLimitMaxRoomsPerSession", BLimitMaxRoomsPerSession, 3},
		{"BLimitRateMsgsPerMinute", BLimitRateMsgsPerMinute, 4},
		{"CAPResourceEnvelope", CAPResourceEnvelope, 0},
		{"CAPAction", CAPAction, 1},
		{"CAPDirectNotice", CAPDirectNotice, 2},
		{"BResID", BResID, 0},
		{"BResKind", BResKind, 1},
		{"BResSize", BResSize, 2},
		{"BResSHA256", BResSHA256, 3},
		{"BResEncoding", BResEncoding, 4},
	} {
		if tc.got != tc.want {
			t.Errorf("%v = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	if ResKindNotice != "notice" || ResKindMOTD != "motd" || ResKindBlob != "blob" {
		t.Errorf("resource kinds wrong: %v %v %v", ResKindNotice, ResKindMOTD, ResKindBlob)
	}
	if MaxNoticeChunkChars != 512 {
		t.Errorf("MaxNoticeChunkChars = %v, want 512", MaxNoticeChunkChars)
	}
	if defaultNickMaxBytes != 32 {
		t.Errorf("defaultNickMaxBytes = %v, want 32", defaultNickMaxBytes)
	}
}
