// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"reflect"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// DisplayNameFromAppData extracts the display name from an LXMF announce [appData]
// payload, providing a seamless way to identify peers within the Reticulum network.
//
// It understands both the v0.5.0+ MessagePack list format and the original raw
// UTF-8 string format used by earlier LXMF versions.
func DisplayNameFromAppData(appData []byte) string {
	if len(appData) == 0 {
		return ""
	}

	// v0.5.0+ format: msgpack fixarray (0x90-0x9f) or array16 (0xdc)
	if (appData[0] >= 0x90 && appData[0] <= 0x9f) || appData[0] == 0xdc {
		result, err := msgpack.Unpack(appData)
		if err != nil {
			return ""
		}
		peerData, ok := result.([]any)
		if !ok || len(peerData) < 1 {
			return ""
		}
		switch dn := peerData[0].(type) {
		case string:
			return dn
		case []byte:
			return string(dn)
		default:
			return ""
		}
	}

	// Original format: raw UTF-8
	return string(appData)
}

// StampCostFromAppData extracts the announced outbound stamp cost from an LXMF
// announce payload.
func StampCostFromAppData(appData []byte) (int, bool) {
	if len(appData) == 0 {
		return 0, false
	}

	if (appData[0] < 0x90 || appData[0] > 0x9f) && appData[0] != 0xdc {
		return 0, false
	}

	result, err := msgpack.Unpack(appData)
	if err != nil {
		return 0, false
	}
	peerData, ok := result.([]any)
	if !ok || len(peerData) < 2 {
		return 0, false
	}

	rv := reflect.ValueOf(peerData[1])
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(rv.Uint()), true
	default:
		return 0, false
	}
}
