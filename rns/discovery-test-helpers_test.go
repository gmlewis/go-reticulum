// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "bytes"

// discoveryTestTransportID is a 16-byte transport_id (TruncatedHashLength/8)
// matching the length RNS/Discovery.py:309 requires of a received announce.
// Its hex encoding is discoveryTestTransportIDHex.
var discoveryTestTransportID = bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef}, 4)

// discoveryTestTransportIDHex is the hex encoding of discoveryTestTransportID.
const discoveryTestTransportIDHex = "deadbeefdeadbeefdeadbeefdeadbeef"

// discoveryTestTransportIDFromByte returns a distinct 16-byte transport_id
// whose first byte is b, so multiple test announces can coexist in the
// valid/invalid caches (keyed by full hash of the packed payload).
func discoveryTestTransportIDFromByte(b byte) []byte {
	id := make([]byte, 16)
	id[0] = b
	return id
}
