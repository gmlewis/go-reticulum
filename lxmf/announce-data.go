// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import "github.com/gmlewis/go-reticulum/rns/msgpack"

// DisplayNameFromAppData extracts the display name from an LXMF announce app_data payload, providing a seamless way to identify peers within the Reticulum network.
//
// It handles both the v0.5.0+ msgpack list format and the original raw
// UTF-8 string format to ensure backward compatibility across diverse LXMF deployments.
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
