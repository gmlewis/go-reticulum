// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"unicode/utf8"

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
			panic(err)
		}
		peerData, ok := result.([]any)
		if !ok || len(peerData) < 1 {
			return ""
		}
		switch dn := peerData[0].(type) {
		case []byte:
			if !utf8.Valid(dn) {
				return ""
			}
			return string(dn)
		default:
			return ""
		}
	}

	// Original format: raw UTF-8
	if !utf8.Valid(appData) {
		panic("invalid UTF-8 in LXMF announce app data")
	}
	return string(appData)
}

func stampCostFromAppDataOutcome(appData []byte) (any, bool, bool, error) {
	if len(appData) == 0 {
		return nil, false, true, nil
	}

	if (appData[0] < 0x90 || appData[0] > 0x9f) && appData[0] != 0xdc {
		return nil, false, true, nil
	}

	result, err := msgpack.Unpack(appData)
	if err != nil {
		return nil, false, false, err
	}
	peerData, ok := result.([]any)
	if !ok || len(peerData) < 2 {
		return nil, false, true, nil
	}
	if peerData[1] == nil {
		return nil, false, true, nil
	}

	return cloneStampCostValue(peerData[1]), true, false, nil
}

func stampCostFromAppDataDetailed(appData []byte) (int, bool, error) {
	stampCost, ok, _, err := stampCostFromAppDataOutcome(appData)
	if err != nil || !ok {
		return 0, false, err
	}
	converted, convertedOK := stampCostAsInt(stampCost)
	return converted, convertedOK, nil
}

// StampCostFromAppData extracts the announced outbound stamp cost from an LXMF
// announce payload.
func StampCostFromAppData(appData []byte) (int, bool) {
	stampCost, ok, err := stampCostFromAppDataDetailed(appData)
	if err != nil {
		panic(err)
	}
	return stampCost, ok
}
