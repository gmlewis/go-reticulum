// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// RNodeMultiSubinterfaceConfig describes the radio settings for one child
// interface within a multiplexed RNode configuration. It includes RF characteristics
// such as frequency, bandwidth, spreading factor, and coding rate for local
// optimization. [SpreadingFactor] and [CodingRate] are LoRa modulation settings.
//
// Vport is the virtual-port index the device reports via the CMD_INTERFACES
// serial response (RNodeMultiInterface.py:234 "vport"). The dynamic spawn
// matches each enabled config to the device's reported interface types by this
// index; a zero Vport means the first available virtual port (the legacy
// pre-create path ignores Vport and uses array order).
type RNodeMultiSubinterfaceConfig struct {
	Name            string
	Enabled         bool
	Vport           int
	Frequency       int
	Bandwidth       int
	TXPower         int
	SpreadingFactor int
	CodingRate      int
	FlowControl     bool
}
