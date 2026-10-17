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

// PropagationNodeStateFromAppData reports whether a `lxmf.propagation` announce
// declares the announcing router as an active propagation node, reading the
// node-state element of the propagation announce payload.
//
// The payload is Python's get_propagation_node_app_data list
// (LXMF/LXMRouter.py:324-336, v1.1.1):
//
//	[ legacy_flag, timebase, node_state, per_transfer_limit,
//	  per_sync_limit, stamp_cost, metadata ]
//
// where element 2 (node_state) is the boolean "this node serves propagation"
// flag, and element 6 is the metadata map carrying the node name. A router that
// announces with node_state false still declares its propagation address — it
// says the address exists but is not serving, which is exactly what Python's
// disable_propagation announces (LXMRouter.py:692-694) and what every NomadNet
// node announce declares for its own address (Node.py:216-220).
//
// It returns (serving, ok): ok is false when the payload is missing, not a
// MessagePack list, or too short/invalid to carry a state — the same acceptance
// rules decodePropagationAnnounceData applies (Python pn_announce_data_is_valid
// requires all seven elements). Callers must treat ok=false as "no declaration"
// rather than "not serving".
func PropagationNodeStateFromAppData(appData []byte) (bool, bool) {
	if len(appData) == 0 {
		return false, false
	}
	// Propagation announce payloads are always non-empty MessagePack arrays
	// (fixarray 0x90-0x9f or array16 0xdc); anything else is a different
	// (legacy or foreign) format with no node-state element.
	if (appData[0] < 0x90 || appData[0] > 0x9f) && appData[0] != 0xdc {
		return false, false
	}
	result, err := msgpack.Unpack(appData)
	if err != nil {
		return false, false
	}
	items, ok := result.([]any)
	if !ok || len(items) < 7 {
		return false, false
	}
	serving, ok := items[2].(bool)
	if !ok {
		return false, false
	}
	return serving, true
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
