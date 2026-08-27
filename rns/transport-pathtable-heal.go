// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "time"

// minPlausibleAnnounceTimebase is the floor for a plausible announce-emission
// unix timestamp (2020-09-13, predating the first RNS releases). Announce
// random blobs embed their emission time as a 5-byte big-endian unix seconds
// value, so anything below this floor cannot be a real emission time.
const minPlausibleAnnounceTimebase = uint64(1_600_000_000)

// maxAnnounceTimebaseSkew bounds how far in the future an announce emission
// may sit relative to local time before it is treated as garbage. Real
// deployments tolerate some clock skew; a day is generous while still keeping
// the poisoned values observed on the fleet (10^10..10^12) firmly excluded.
const maxAnnounceTimebaseSkew = 24 * time.Hour

// plausibleAnnounceTimebase reports whether tb — the uint40 big-endian
// emission timestamp decoded from an announce random blob's bytes [5:10] — is
// a plausible unix timestamp. Real emissions are always < 2^32 until 2106
// (their first byte is 0x00) and sit near local time. Blobs written by
// pre-fix binaries that misparsed truncated announces carry values from
// ~6e10 to ~1.1e12; because path replacement requires a newer emission than
// the stored maximum, one such blob would otherwise block every future
// announce for the destination until the entry expired — the fleet bug where
// a node stopped seeing its peers and could not be linked to.
func plausibleAnnounceTimebase(tb uint64, now time.Time) bool {
	if tb < minPlausibleAnnounceTimebase {
		return false
	}
	if tb > uint64(now.Unix())+uint64(maxAnnounceTimebaseSkew/time.Second) {
		return false
	}
	return true
}

// randomBlobTimebase decodes the 5-byte big-endian emission timebase from a
// random blob's bytes [5:10], returning 0 for blobs too short to carry one.
func randomBlobTimebase(blob []byte) uint64 {
	if len(blob) < 10 {
		return 0
	}
	var emitted uint64
	for _, x := range blob[5:10] {
		emitted = (emitted << 8) | uint64(x)
	}
	return emitted
}
