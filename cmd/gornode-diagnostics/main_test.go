// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
	"time"
)

// newTestRadio returns a radio with no open port, safe for parser/codec tests.
func newTestRadio() *radio {
	r := newRadio("/dev/test")
	return r
}

func TestKissParserDetect(t *testing.T) {
	r := newTestRadio()
	p := r.newParser()

	// Detect response + fw version + platform + mcu + board, as an RNode
	// answers the detect burst (Fend, cmd, data..., Fend).
	stream := []byte{
		kissFend, kissCmdDetect, kissCmdDetectResp, kissFend,
		kissFend, kissCmdFwVersion, 0x01, 0x78, kissFend,
		kissFend, kissCmdPlatform, 0x80, kissFend,
		kissFend, kissCmdMcu, 0xEF, kissFend,
		kissFend, kissCmdBoard, 0xB8, kissFend,
	}
	p.feedBytes(stream)

	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	if !r.state.detected {
		t.Fatal("parser did not set detected on a detect-resp frame")
	}
	if r.state.fwMaj != 1 || r.state.fwMin != 120 {
		t.Fatalf("fw version = %v.%v, want 1.120", r.state.fwMaj, r.state.fwMin)
	}
	if r.state.platform == nil || *r.state.platform != 0x80 {
		t.Fatalf("platform = %v, want 0x80", r.state.platform)
	}
	if r.state.board == nil || *r.state.board != 0xB8 {
		t.Fatalf("board = %v, want 0xB8", r.state.board)
	}
	if got := r.state.describe(); got != "RNode, platform ESP32 (0x80), mcu 0xef, board 0xb8, firmware 1.120" {
		t.Fatalf("describe() = %q", got)
	}
}

func TestKissParserDataPayload(t *testing.T) {
	r := newTestRadio()
	r.nodeID = [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	peer := [8]byte{9, 9, 9, 9, 9, 9, 9, 9}

	packet := buildPacket(diagTypeTest, peer, 42, 12345, [8]byte{}, 0)
	frame := append([]byte{kissFend, kissCmdData}, kissEscape(packet)...)
	frame = append(frame, kissFend)

	p := r.newParser()
	p.feedBytes(frame)

	r.logMu.Lock()
	heard := r.heardPackets[peer]
	acked := len(r.ackedPackets)
	r.logMu.Unlock()
	if heard != 1 {
		t.Fatalf("heardPackets[%x] = %v, want 1", peer, heard)
	}
	if acked != 1 {
		t.Fatalf("acknowledged %v packets, want 1", acked)
	}

	// A duplicate of the same TEST packet must be heard again but not
	// re-acknowledged (ACK dedupe prevents receipt storms).
	p.feedBytes(frame)
	r.logMu.Lock()
	heard, acked = r.heardPackets[peer], len(r.ackedPackets)
	r.logMu.Unlock()
	if heard != 2 || acked != 1 {
		t.Fatalf("after duplicate: heard=%v acked=%v, want heard=2 acked=1", heard, acked)
	}
}

func TestKissParserStatCounters(t *testing.T) {
	r := newTestRadio()
	p := r.newParser()

	// CMD_STAT_RX = 5, CMD_STAT_TX = 7, RSSI byte (157+98=255 → -59 dBm-ish).
	stream := []byte{
		kissFend, statCmdRX, 0x00, 0x00, 0x00, 0x05, kissFend,
		kissFend, statCmdTX, 0x00, 0x00, 0x00, 0x07, kissFend,
		kissFend, statCmdRSSI, 0x63, kissFend, // 99 - 157 = -58 dBm
	}
	p.feedBytes(stream)

	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	if r.state.rStatRX == nil || *r.state.rStatRX != 5 {
		t.Fatalf("rStatRX = %v, want 5", r.state.rStatRX)
	}
	if r.state.rStatTX == nil || *r.state.rStatTX != 7 {
		t.Fatalf("rStatTX = %v, want 7", r.state.rStatTX)
	}
	if r.state.rRSSI == nil || *r.state.rRSSI != -58 {
		t.Fatalf("rRSSI = %v, want -58", r.state.rRSSI)
	}
	if r.state.statRXTotal() != 5 || r.state.statTXTotal() != 7 {
		t.Fatal("statRXTotal/statTXTotal mismatch")
	}
}

func TestParsePacketRejectsGarbage(t *testing.T) {
	if _, _, _, _, _, ok := parsePacket([]byte("hello world")); ok {
		t.Fatal("parsePacket accepted a non-diag payload")
	}
	badVersion := buildPacket(diagTypeTest, [8]byte{1}, 1, 0, [8]byte{}, 0)
	badVersion[4] = 0x99
	if _, _, _, _, _, ok := parsePacket(badVersion); ok {
		t.Fatal("parsePacket accepted a bad version")
	}
}

func TestBuildParseAckRoundTrip(t *testing.T) {
	node := [8]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0x11, 0x22, 0x33}
	origin := [8]byte{0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xAA, 0xBB}
	p := buildPacket(diagTypeAck, node, 0, 0, origin, 77)
	if len(p) != 38 {
		t.Fatalf("ACK packet length = %v, want 38", len(p))
	}
	typ, gotNode, _, gotOrigin, gotSeq, ok := parsePacket(p)
	if !ok || typ != diagTypeAck || gotNode != node || gotOrigin != origin || gotSeq != 77 {
		t.Fatalf("round trip = %v %x %x %v, want ack %x %x 77", ok, gotNode, gotOrigin, gotSeq, node, origin)
	}
}

func TestValidateConfigMismatches(t *testing.T) {
	r := newTestRadio()
	r.stateMu.Lock()
	f := 915000000
	bw := 125000
	sfv := 9
	crv := 5
	on := byte(radioStateOn)
	r.state.rFreq, r.state.rBW, r.state.rTXP = &f, &bw, &sfv // txpower slot reused as int
	r.state.rSF, r.state.rCR, r.state.rState = &sfv, &crv, &on
	r.stateMu.Unlock()

	// Note: rTXP points at sfv (9) — with -txpower 17 this must mismatch.
	mismatches := r.validateConfig()
	if len(mismatches) == 0 {
		t.Fatal("validateConfig found no mismatch with a wrong txpower")
	}

	r.stateMu.Lock()
	tx := 17
	r.state.rTXP = &tx
	r.stateMu.Unlock()
	if mismatches := r.validateConfig(); len(mismatches) != 0 {
		t.Fatalf("validateConfig reported %v, want none", mismatches)
	}
}

func TestVerdictTransmitOnly(t *testing.T) {
	r := newTestRadio()
	r.nodeID = [8]byte{1}
	r.sentPackets = 30
	peer := [8]byte{2}
	r.heardPeers[peer] = true
	r.heardPackets[peer] = 12
	// No ACK receipts: nobody ever confirmed our packets.
	if got := r.verdict(0); !bytes.Contains([]byte(got), []byte("RECEIVE ONLY?")) {
		t.Fatalf("verdict with 0 ACKs = %q, want a receive-only warning", got)
	}
	if got := r.verdict(10); !bytes.Contains([]byte(got), []byte("TRANSMIT + RECEIVE OK")) {
		t.Fatalf("verdict with ACKs = %q, want a bidirectional confirmation", got)
	}
}

func TestGraceWaitShort(t *testing.T) {
	start := time.Now()
	waitGrace(0)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitGrace(0) slept %v", elapsed)
	}
}
