// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "github.com/gmlewis/go-reticulum/rns/msgpack"

// RequestDataBytes normalizes a request's data element into the MessagePack
// byte form that byte-oriented request handlers decode.
//
// Link.handleRequest hands request_data to the generator verbatim, whatever
// MessagePack type the peer sent (Python Link.py:803-808), so a peer that packs
// its own payload arrives as bytes while a browser submitting Micron form
// fields arrives as a decoded map. Handlers such as rnx, rngit, and rncp decode
// a packed payload, so they normalize their data element with this function
// before decoding. Data that already arrived as bytes is returned unchanged, so
// the round trip through a handler's own Unpack observes exactly the value the
// peer sent.
func RequestDataBytes(data any) []byte {
	switch value := data.(type) {
	case nil:
		return nil
	case []byte:
		return value
	default:
		packed, err := msgpack.Pack(value)
		if err != nil {
			return nil
		}
		return packed
	}
}
