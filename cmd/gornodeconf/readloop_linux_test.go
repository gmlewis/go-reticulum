// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"testing"
)

func TestRNodeReadLoopFrameAssemblyAndUnescape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stream  []byte
		wantCmd byte
		want    []byte
	}{
		{
			name:    "rom-read",
			stream:  []byte{kissFend, rnodeKISSCommandROMRead, 0x11, kissFesc, kissTfend, 0x22, kissFend},
			wantCmd: rnodeKISSCommandROMRead,
			want:    []byte{0x11, kissFend, 0x22},
		},
		{
			name:    "cfg-read",
			stream:  []byte{kissFend, rnodeKISSCommandCFGRead, kissFesc, kissTfesc, 0x33, kissFend},
			wantCmd: rnodeKISSCommandCFGRead,
			want:    []byte{kissFesc, 0x33},
		},
		{
			name:    "data",
			stream:  []byte{kissFend, rnodeKISSCommandData, 0x44, kissFesc, kissTfend, kissFesc, kissTfesc, 0x55, kissFend},
			wantCmd: rnodeKISSCommandData,
			want:    []byte{0x44, kissFend, kissFesc, 0x55},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := newRnodeReadLoopState()
			var gotFrames []rnodeReadLoopFrame
			for _, b := range tt.stream {
				frame, ok := state.feedByte(b)
				if ok {
					gotFrames = append(gotFrames, frame)
				}
			}

			if len(gotFrames) != 1 {
				t.Fatalf("expected 1 completed frame, got %v", len(gotFrames))
			}
			got := gotFrames[0]
			if got.command != tt.wantCmd {
				t.Fatalf("command mismatch: got %#x want %#x", got.command, tt.wantCmd)
			}
			if !bytes.Equal(got.payload, tt.want) {
				t.Fatalf("payload mismatch:\n got: %x\nwant: %x", got.payload, tt.want)
			}
		})
	}
}
