// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// RNodeMultiSubinterfaceConfig describes the radio settings for one child
// interface within a multiplexed RNode configuration. It includes RF characteristics
// such as frequency, bandwidth, spreading factor, and coding rate for local
// optimization. [SpreadingFactor] and [CodingRate] are LoRa modulation settings.
type RNodeMultiSubinterfaceConfig struct {
	Name            string
	Enabled         bool
	Frequency       int
	Bandwidth       int
	TXPower         int
	SpreadingFactor int
	CodingRate      int
	FlowControl     bool
}
