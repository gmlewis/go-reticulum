// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestPipeHardwareMTUSeam pins the seam that lets a test build an MTU-limited link. An
// in-memory pipe has no wire MTU, so it has always delivered every frame; that is still
// the default (PipeDefaultMTU). Setting a ceiling makes the RECEIVING interface drop
// frames over it, exactly as the TCP and Backbone receive loops do before dispatching an
// inbound frame (CheckFrameLen, mirroring Python's check_frame_len), which is what a
// resource-segmentation test needs.
func TestPipeHardwareMTUSeam(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		got [][]byte
	)
	a := NewPipeInterface("mtu-a", nil)
	b := NewPipeInterface("mtu-b", func(data []byte, _ Interface) {
		mu.Lock()
		got = append(got, append([]byte{}, data...))
		mu.Unlock()
	})
	a.SetOther(b)
	b.SetOther(a)
	t.Cleanup(func() {
		_ = a.Detach()
		_ = b.Detach()
	})

	// An untouched pipe is unlimited, so no existing test changes behaviour.
	if m := a.HardwareMTU(); m != PipeDefaultMTU {
		t.Fatalf("default MTU = %v, want %v", m, PipeDefaultMTU)
	}

	// The RECEIVER owns the ceiling.
	b.SetHardwareMTU(64)
	if m := b.HardwareMTU(); m != 64 {
		t.Fatalf("MTU after SetHardwareMTU = %v, want 64", m)
	}

	oversized := bytes.Repeat([]byte{0x11}, 1000)
	allowed := bytes.Repeat([]byte{0x22}, 20)

	if err := a.Send(oversized); err != nil {
		t.Fatalf("send oversized: %v", err)
	}
	if err := a.Send(allowed); err != nil {
		t.Fatalf("send allowed: %v", err)
	}

	// Delivery is in order on one queue, so waiting for the second frame also proves the
	// first was processed — and if it had been delivered it would be got[0].
	testutils.EventuallyFatal(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) >= 1
	}, "the allowed frame was never delivered")

	mu.Lock()
	delivered := append([][]byte{}, got...)
	mu.Unlock()

	if len(delivered) != 1 {
		t.Fatalf("%v frames delivered, want only the allowed one", len(delivered))
	}
	if !bytes.Equal(delivered[0], allowed) {
		t.Errorf("delivered frame = %v bytes, want the allowed frame", len(delivered[0]))
	}
}
