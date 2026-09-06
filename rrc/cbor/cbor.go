// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package cbor implements the subset of RFC 8949 CBOR used by the RRC
// protocol, matching the behavior of Python's cbor2 6.1.2 as used by the
// Python rrcd hub (non-canonical encoding, insertion-ordered maps, lax
// decode). Envelope maps decode into *Map, an insertion-ordered key/value
// structure whose key equality mirrors Python dict semantics (True == 1), so
// forwarded envelopes re-encode with the sender's key order, unknown keys,
// and key types preserved verbatim.
package cbor
