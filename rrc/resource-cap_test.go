// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

// TestResourceAdvertisedHonoursTheConfiguredCap pins [rrc]
// max_accepted_resource_size. Python reads the application's value at
// advertisement time and falls back to 262144 when the app has none
// (RRC.py:1206-1223); a value of zero or less refuses every resource. The port
// hard-coded 262144 at this call site, so a configured cap could not take effect
// on the client — where the key is read from the app, not from rrcd's own config.
func TestResourceAdvertisedHonoursTheConfiguredCap(t *testing.T) {
	t.Parallel()

	adv := func(dataSize int64) *rns.ResourceAdvertisement {
		return &rns.ResourceAdvertisement{D: dataSize, T: dataSize}
	}

	tests := []struct {
		name string
		cap  int // bytes; 0 means "leave the manager's default"
		size int64
		want bool
	}{
		{"the default cap accepts a small resource", 0, 1024, true},
		{"the default cap accepts exactly 262144", 0, 262144, true},
		{"the default cap refuses one byte more", 0, 262145, false},
		{"a raised cap accepts what the default refuses", 1048576, 600000, true},
		{"a lowered cap refuses what the default accepts", 4096, 8192, false},
		{"a lowered cap still accepts what fits", 4096, 4096, true},
		{"a zero cap refuses everything", -1, 1, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mgr := NewManager(t.TempDir(), func() []byte { return []byte("ownhash") })
			if tc.cap != 0 {
				// A negative tc.cap models Python's `max_accepted_resource_size = 0`,
				// which resolves to a zero-byte cap that refuses every transfer.
				mgr.SetMaxAcceptedResourceBytes(max(tc.cap, 0))
			}
			hub := mgr.AddHub([]byte("hubhash"), "rrc.chat", "TestHub")

			if got := hub.resourceAdvertised(adv(tc.size)); got != tc.want {
				t.Errorf("resourceAdvertised(size=%v) with cap %v = %v, want %v",
					tc.size, mgr.MaxAcceptedResourceBytes(), got, tc.want)
			}
		})
	}
}

// TestMaxAcceptedResourceBytesDefaultsAndRoundTrips pins the accessor the hub reads
// per advertisement: the default when nothing was configured, and the exact value
// once set.
func TestMaxAcceptedResourceBytesDefaultsAndRoundTrips(t *testing.T) {
	t.Parallel()

	mgr := NewManager(t.TempDir(), func() []byte { return []byte("ownhash") })
	if got := mgr.MaxAcceptedResourceBytes(); got != DefaultMaxAcceptedResourceBytes {
		t.Errorf("default cap = %v, want %v", got, DefaultMaxAcceptedResourceBytes)
	}
	if DefaultMaxAcceptedResourceBytes != 262144 {
		t.Errorf("DefaultMaxAcceptedResourceBytes = %v, want the reference's 262144", DefaultMaxAcceptedResourceBytes)
	}

	mgr.SetMaxAcceptedResourceBytes(12345)
	if got := mgr.MaxAcceptedResourceBytes(); got != 12345 {
		t.Errorf("cap after Set = %v, want 12345", got)
	}
}
