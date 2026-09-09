// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "time"

// maxAnnounceTimebaseSkew bounds how far in the future an announce emission
// may sit relative to local time before it is treated as garbage. Real
// deployments tolerate some clock skew; a day is generous while still keeping
// the poisoned values observed on the fleet (10^10..10^12) firmly excluded.
const maxAnnounceTimebaseSkew = 24 * time.Hour

// plausibleAnnounceTimebase reports whether tb — the uint40 big-endian
// emission timestamp decoded from an announce random blob's bytes [5:10] — is
// a plausible timestamp. Real emissions from standard nodes sit near local
// time, while embedded devices without battery-backed RTCs (e.g. ESP32, LoRa
// repeaters) emit timestamps counting up from zero or system uptime (such as
// 83,450 or 766,445). Blobs written by pre-fix binaries that misparsed
// truncated announces carried values from ~6e10 to ~1.1e12 (far in the
// future); because path replacement requires a newer emission than the stored
// maximum, one such future blob would block every future announce for the
// destination until the entry expired. Any non-future timestamp is plausible.
func plausibleAnnounceTimebase(tb uint64, now time.Time) bool {
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
