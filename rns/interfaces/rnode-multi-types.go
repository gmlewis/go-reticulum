// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// RNodeMultiSubinterfaceConfig encapsulates the rigorous, granular hardware tuning parameters required for an individual child radio within a multiplexed RNode cluster.
// It dictates essential RF characteristics such as frequency, bandwidth, and spread spectrum variables for localized optimization.
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
