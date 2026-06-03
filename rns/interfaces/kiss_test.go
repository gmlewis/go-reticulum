// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"testing"
)

func TestKISS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  []byte
	}{
		{"simple", []byte("hello")},
		{"with-fend", []byte{KISSFend, 0x01, KISSFend}},
		{"with-fesc", []byte{KISSFesc, 0x01, KISSFesc}},
		{"mixed", []byte{KISSFend, KISSFesc, 0xC0, 0xDB}},
		{"empty", []byte{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			escaped := KISSEscape(tt.raw)
			unescaped := KISSUnescape(escaped)
			if !bytes.Equal(tt.raw, unescaped) {
				t.Fatalf("KISS roundtrip mismatch for %q: got %x, want %x", tt.name, unescaped, tt.raw)
			}
		})
	}
}

func TestKISSFrameUint32(t *testing.T) {
	t.Parallel()
	frame := KISSFrameUint32(KISSCmdFrequency, 433050000)
	parsed := KISSUnescape(frame[2 : len(frame)-1])
	if len(parsed) != 4 {
		t.Fatalf("expected 4 data bytes, got %d", len(parsed))
	}
	var got uint32
	got = uint32(parsed[0])<<24 | uint32(parsed[1])<<16 | uint32(parsed[2])<<8 | uint32(parsed[3])
	if got != 433050000 {
		t.Fatalf("expected frequency 433050000, got %d", got)
	}
	if frame[0] != KISSFend || frame[len(frame)-1] != KISSFend {
		t.Fatalf("frame must start and end with FEND")
	}
	if frame[1] != KISSCmdFrequency {
		t.Fatalf("frame command byte must be CMD_FREQUENCY, got 0x%02X", frame[1])
	}
}

func TestKISSFrameUint8(t *testing.T) {
	t.Parallel()
	frame := KISSFrameUint8(KISSCmdTXPower, 17)
	if frame[0] != KISSFend || frame[len(frame)-1] != KISSFend {
		t.Fatalf("frame must start and end with FEND")
	}
	if frame[1] != KISSCmdTXPower {
		t.Fatalf("frame command byte must be CMD_TXPOWER, got 0x%02X", frame[1])
	}
	parsed := KISSUnescape(frame[2 : len(frame)-1])
	if len(parsed) != 1 || parsed[0] != 17 {
		t.Fatalf("expected txpower 17, got %v", parsed)
	}
}

func TestKISSFrameUint16(t *testing.T) {
	t.Parallel()
	stALock := float64(15.5)
	at := int(stALock * 100)
	frame := KISSFrameUint16(KISSCmdSTALock, uint16(at))
	if frame[0] != KISSFend || frame[len(frame)-1] != KISSFend {
		t.Fatalf("frame must start and end with FEND")
	}
	if frame[1] != KISSCmdSTALock {
		t.Fatalf("frame command byte must be CMD_ST_ALOCK, got 0x%02X", frame[1])
	}
	parsed := KISSUnescape(frame[2 : len(frame)-1])
	if len(parsed) != 2 {
		t.Fatalf("expected 2 data bytes, got %d", len(parsed))
	}
	got := int(parsed[0])<<8 | int(parsed[1])
	if got != 1550 {
		t.Fatalf("expected 1550 (15.5%%), got %d", got)
	}
}

func TestKISSFrameSelectInt(t *testing.T) {
	t.Parallel()
	data := []byte{0x01, 0x02, 0x03, 0x04}
	frame := KISSFrameSelectInt(KISSCmdFrequency, 2, data)
	if frame[0] != KISSFend || frame[3] != KISSFend {
		t.Fatalf("frame select prefix must be FEND SEL_INT index FEND")
	}
	if frame[1] != KISSCmdSelInt || frame[2] != 2 {
		t.Fatalf("frame select prefix must be [FEND][CMD_SEL_INT][index][FEND]")
	}
	if frame[4] != KISSFend {
		t.Fatalf("second command must start with FEND")
	}
	if frame[5] != KISSCmdFrequency {
		t.Fatalf("command byte must be CMD_FREQUENCY, got 0x%02X", frame[5])
	}
}

func TestKISSFrameEscapesUint32(t *testing.T) {
	t.Parallel()
	value := uint32(0xC0DB0001)
	frame := KISSFrameUint32(KISSCmdFrequency, value)
	var rawInside []byte
	inFrame := false
	for i := 1; i < len(frame)-1; i++ {
		if i == 1 {
			continue
		}
		if !inFrame {
			inFrame = true
			continue
		}
		rawInside = append(rawInside, frame[i])
	}
	if !bytes.Contains(rawInside, []byte{KISSFesc, KISSTfend}) && !bytes.Contains(rawInside, []byte{KISSFesc, KISSTfesc}) {
		if bytes.Contains(rawInside, []byte{KISSFend}) || bytes.Contains(rawInside, []byte{KISSFesc}) {
			t.Fatalf("KISS escaping did not properly escape FEND/FESC bytes in frame")
		}
	}
	unescaped := KISSUnescape(frame[2 : len(frame)-1])
	var got uint32
	got = uint32(unescaped[0])<<24 | uint32(unescaped[1])<<16 | uint32(unescaped[2])<<8 | uint32(unescaped[3])
	if got != value {
		t.Fatalf("roundtrip expected 0x%08X, got 0x%08X", value, got)
	}
}
