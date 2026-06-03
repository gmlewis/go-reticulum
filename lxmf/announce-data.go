// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"fmt"
	"unicode/utf8"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// DisplayNameFromAppData extracts the display name from an LXMF announce [appData]
// payload, providing a seamless way to identify peers within the Reticulum network.
//
// It understands both the v0.5.0+ MessagePack list format and the original raw
// UTF-8 string format used by earlier LXMF versions. If the appData contains
// a malformed MessagePack encoding or invalid UTF-8, it returns a non-nil error.
func DisplayNameFromAppData(appData []byte) (string, error) {
	if len(appData) == 0 {
		return "", nil
	}

	// v0.5.0+ format: msgpack fixarray (0x90-0x9f) or array16 (0xdc)
	if (appData[0] >= 0x90 && appData[0] <= 0x9f) || appData[0] == 0xdc {
		result, err := msgpack.Unpack(appData)
		if err != nil {
			return "", fmt.Errorf("unpack lxmf announce app data: %w", err)
		}
		peerData, ok := result.([]any)
		if !ok || len(peerData) < 1 {
			return "", nil
		}
		switch dn := peerData[0].(type) {
		case []byte:
			if !utf8.Valid(dn) {
				return "", nil
			}
			return string(dn), nil
		default:
			return "", nil
		}
	}

	// Original format: raw UTF-8
	if !utf8.Valid(appData) {
		return "", fmt.Errorf("invalid UTF-8 in LXMF announce app data")
	}
	return string(appData), nil
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
// announce payload. It returns the stamp cost, whether a stamp cost was present,
// and any error encountered during unpacking.
func StampCostFromAppData(appData []byte) (int, bool, error) {
	stampCost, ok, err := stampCostFromAppDataDetailed(appData)
	if err != nil {
		return 0, false, fmt.Errorf("unpack lxmf stamp cost from app data: %w", err)
	}
	return stampCost, ok, nil
}
