// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// TestCheckFrameLen verifies the HDLC frame-length gate parity with Python
// RNS/Interfaces/{TCP,Backbone}Interface.py check_frame_len (v1.4.0): a frame
// at or below HEADER_MINSIZE (cannot carry a valid packet header) or above
// HW_MTU+ifac_size is rejected (returns false); everything in between is
// accepted.
func TestCheckFrameLen(t *testing.T) {
	t.Parallel()
	const hwmtu = 262144
	const ifac = 16
	cases := []struct {
		name     string
		frameLen int
		want     bool
	}{
		{"zero", 0, false},
		{"one byte", 1, false},
		{"at header min (19)", HDLCHeaderMinSize, false},
		{"just above header min", HDLCHeaderMinSize + 1, true},
		{"well within bounds", 500, true},
		{"at max (hwmtu+ifac)", hwmtu + ifac, true},
		{"one over max", hwmtu + ifac + 1, false},
		{"far over max", hwmtu + ifac + 1000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CheckFrameLen(tc.frameLen, hwmtu, ifac); got != tc.want {
				t.Fatalf("CheckFrameLen(%d, %d, %d) = %v, want %v", tc.frameLen, hwmtu, ifac, got, tc.want)
			}
		})
	}

	// With no IFAC configured (ifac_size == 0, matching Python's
	// `(self.ifac_size or 0)`), the ceiling is exactly HW_MTU.
	if CheckFrameLen(hwmtu+1, hwmtu, 0) {
		t.Fatalf("CheckFrameLen(hwmtu+1, hwmtu, 0) with no IFAC should be false")
	}
	if !CheckFrameLen(hwmtu, hwmtu, 0) {
		t.Fatalf("CheckFrameLen(hwmtu, hwmtu, 0) should be true at the boundary")
	}
}

// TestHDLCFrameLenValidationDropsInvalidFrames feeds raw HDLC-framed bytes
// through a real TCP server interface and asserts that sub-header and
// over-length frames are dropped before inboundHandler is invoked, while a
// valid-length frame is delivered. This mirrors Python's read_loop calling
// check_frame_len before process_incoming
// (RNS/Interfaces/{TCP,Backbone}Interface.py, v1.4.0).
func TestHDLCFrameLenValidationDropsInvalidFrames(t *testing.T) {
	t.Parallel()
	port := reserveTCPPort(t)

	var mu sync.Mutex
	calls := 0
	var last []byte
	handler := func(data []byte, _ Interface) {
		mu.Lock()
		calls++
		last = append(last[:0], data...)
		mu.Unlock()
	}

	server := mustTestNewTCPServerInterface(t, "server", "127.0.0.1", port, handler)
	defer func() {
		if err := server.Detach(); err != nil {
			t.Fatalf("server detach failed: %v", err)
		}
	}()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	// Let the server accept the connection and start the spawned readLoop.
	time.Sleep(100 * time.Millisecond)

	writeFrame := func(payload []byte) {
		frame := append([]byte{HDLCFlag}, HDLCEscape(payload)...)
		frame = append(frame, HDLCFlag)
		if err := writeAll(conn, frame); err != nil {
			t.Fatalf("writeAll: %v", err)
		}
	}

	// Sub-header frame: payload (5 bytes) shorter than HDLCHeaderMinSize (19).
	// Must be dropped before inboundHandler.
	writeFrame(bytes.Repeat([]byte{0x11}, 5))
	time.Sleep(100 * time.Millisecond)

	// Over-length frame: unescaped length (TCPHWMTU+1) exceeds HW_MTU. Must be
	// dropped before inboundHandler. 0x22 needs no HDLC escaping, so the
	// unescaped length equals the payload length.
	writeFrame(bytes.Repeat([]byte{0x22}, TCPHWMTU+1))
	time.Sleep(250 * time.Millisecond)

	mu.Lock()
	if calls != 0 {
		t.Fatalf("after invalid frames, inboundHandler calls = %d, want 0", calls)
	}
	mu.Unlock()

	// Valid frame: length > HDLCHeaderMinSize and <= TCPHWMTU. Must be delivered.
	valid := bytes.Repeat([]byte{0xAA}, 64)
	writeFrame(valid)
	time.Sleep(250 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("after valid frame, inboundHandler calls = %d, want 1", calls)
	}
	if !bytes.Equal(last, valid) {
		t.Fatalf("received %v, want %v", last, valid)
	}
}

// writeAll writes the entire buffer to w, retrying on short writes.
func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

// TestHDLCReassemble verifies the HDLC frame-buffer reassembly logic,
// including the v1.4.0 overflow guard (RNS/Interfaces/{TCP,Backbone}Interface.py
// read_loop): a partial frame (start flag with no end flag) whose buffer
// exceeds 2*hwmtu is dropped entirely rather than held, bounding memory against
// a peer that streams a start flag followed by unbounded flag-less junk.
func TestHDLCReassemble(t *testing.T) {
	t.Parallel()
	const hwmtu = 1024

	p := func(n int, b byte) []byte { return bytes.Repeat([]byte{b}, n) }
	// frame builds a complete HDLC frame: FLAG + payload + FLAG.
	frame := func(payload []byte) []byte {
		out := make([]byte, 0, len(payload)+2)
		out = append(out, HDLCFlag)
		out = append(out, payload...)
		out = append(out, HDLCFlag)
		return out
	}
	// partial builds a start flag + payload with no end flag.
	partial := func(payload []byte) []byte {
		out := make([]byte, 0, len(payload)+1)
		out = append(out, HDLCFlag)
		out = append(out, payload...)
		return out
	}

	cases := []struct {
		name     string
		in       []byte
		wantTail []byte
		wantN    int
	}{
		{
			name:     "no flag reset to empty",
			in:       p(500, 0x55),
			wantTail: nil,
			wantN:    0,
		},
		{
			name:     "start flag no end flag under limit kept",
			in:       partial(p(100, 0x55)),
			wantTail: partial(p(100, 0x55)),
			wantN:    0,
		},
		{
			name:     "start flag no end flag over 2x hwmtu dropped",
			in:       partial(p(2*hwmtu+1, 0x55)),
			wantTail: nil,
			wantN:    0,
		},
		{
			name:     "single complete frame",
			in:       frame(p(64, 0xAA)),
			wantTail: []byte{HDLCFlag}, // closing flag remains as next frame's opener
			wantN:    1,
		},
		{
			name:     "two complete frames",
			in:       append(frame(p(32, 0xAA)), frame(p(32, 0xBB))...),
			wantTail: []byte{HDLCFlag}, // final closing flag remains
			wantN:    2,
		},
		{
			name:     "complete frame then trailing partial kept",
			in:       append(frame(p(32, 0xAA)), partial(p(10, 0x55))...),
			wantTail: partial(p(10, 0x55)),
			wantN:    1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tail, frames := hdlcReassemble(tc.in, hwmtu)
			if len(frames) != tc.wantN {
				t.Fatalf("frames = %d, want %d", len(frames), tc.wantN)
			}
			if tc.wantTail == nil {
				if len(tail) != 0 {
					t.Fatalf("tail = %d bytes, want empty; tail=%v", len(tail), tail)
				}
			} else if !bytes.Equal(tail, tc.wantTail) {
				t.Fatalf("tail = %v, want %v", tail, tc.wantTail)
			}
		})
	}

	// Explicit overflow-guard assertion: a start flag followed by >2*hwmtu
	// flag-less bytes must yield an empty tail (the buffer was dropped), not
	// the unbounded partial frame.
	tail, _ := hdlcReassemble(append([]byte{HDLCFlag}, p(2*hwmtu+100, 0x55)...), hwmtu)
	if len(tail) != 0 {
		t.Fatalf("overflow guard: tail len = %d, want 0 (buffer must be dropped)", len(tail))
	}
}
