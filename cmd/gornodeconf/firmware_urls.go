// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

const (
	firmwareVersionURL         = "https://github.com/markqvist/rnode_firmware/releases/latest/download/release.json"
	fallbackFirmwareVersionURL = "https://github.com/markqvist/rnode_firmware/releases/latest/download/release.json"
	firmwareUpdateURL          = "https://github.com/markqvist/RNode_Firmware/releases/download/"
)

func firmwareReleaseInfoURL(fwURL, selectedVersion string) string {
	if fwURL == "" {
		if selectedVersion == "" {
			return firmwareVersionURL
		}
		return firmwareVersionURL
	}
	if selectedVersion == "" {
		return fwURL + "latest/download/release.json"
	}
	return fwURL + "download/" + selectedVersion + "/release.json"
}

func firmwareBinaryURL(fwURL, selectedVersion, fwFilename string) string {
	if fwURL != "" {
		return fwURL + "download/" + selectedVersion + "/" + fwFilename
	}
	return firmwareUpdateURL + selectedVersion + "/" + fwFilename
}
