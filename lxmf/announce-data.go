// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"fmt"
	"reflect"
	"strings"
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
			// Mirrors Python's display_name_from_app_data (LXMF.py:165,
			// v0.9.7+): the v0.5.0+ list-format name is NUL-stripped and
			// whitespace-trimmed after decoding. The original raw-string
			// format below is intentionally returned verbatim.
			return strings.TrimSpace(strings.ReplaceAll(string(dn), "\x00", "")), nil
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

// CompressionSupportFromAppData reports whether an LXMF peer's announce app-data
// signals support for auto-compressed message resources, mirroring Python
// LXMF.compression_support_from_app_data (lxmf/LXMF.py:154-166, v0.9.5).
//
// It returns (supported, present, err):
//   - present is false (and supported false) when appData is nil/empty, the
//     Python None outcome.
//   - supported is true when the peer uses the original raw-string announce
//     format, the v0.5.0+ list format with no functionality list
//     (peer_data shorter than 3 elements), or a non-list third element — all
//     default to "compression supported".
//   - supported is true when peer_data[2] is a list containing SFCompression,
//     false when that list omits it.
//
// A malformed MessagePack payload yields a non-nil error, mirroring the
// umsgpack exception that propagates from the Python helper.
func CompressionSupportFromAppData(appData []byte) (bool, bool, error) {
	if len(appData) == 0 {
		return false, false, nil
	}

	// v0.5.0+ format: msgpack fixarray (0x90-0x9f) or array16 (0xdc)
	if (appData[0] >= 0x90 && appData[0] <= 0x9f) || appData[0] == 0xdc {
		result, err := msgpack.Unpack(appData)
		if err != nil {
			return false, false, fmt.Errorf("unpack lxmf compression support from app data: %w", err)
		}
		peerData, ok := result.([]any)
		if !ok || len(peerData) < 3 {
			// No functionality list present: compression is supported by
			// default (Python `if len(peer_data) < 3: return True`).
			return true, true, nil
		}
		fnList, ok := peerData[2].([]any)
		if !ok {
			// Third element is not a functionality list: default supported.
			return true, true, nil
		}
		for _, fn := range fnList {
			if functionalityCodeEquals(fn, SFCompression) {
				return true, true, nil
			}
		}
		return false, true, nil
	}

	// Original format: raw UTF-8 string. Compression is supported.
	return true, true, nil
}

// functionalityCodeEquals reports whether a msgpack-unpacked functionality
// code equals target. Functionality codes are small positive fixints that
// unpack as int64 (and occasionally other numeric kinds), so a numeric
// comparison avoids type-mismatch false negatives.
func functionalityCodeEquals(code any, target int) bool {
	if code == nil {
		return false
	}
	rv := reflect.ValueOf(code)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(rv.Int()) == target
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(rv.Uint()) == target
	case reflect.Float32, reflect.Float64:
		return int(rv.Float()) == target
	default:
		return false
	}
}
