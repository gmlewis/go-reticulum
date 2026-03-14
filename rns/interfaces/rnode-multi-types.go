// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// RNodeMultiSubinterfaceConfig describes the hardware tuning parameters for a
// child radio within a multiplexed RNode cluster. It includes RF characteristics
// such as frequency, bandwidth, spreading factor, and coding rate for local
// optimization.
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
