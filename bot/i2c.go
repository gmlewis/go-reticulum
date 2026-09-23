// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

// I2CBus defines the minimal standard interface to communicate with an I2C slave device.
// This abstraction allows hardware drivers to run over hardware I2C peripherals
// (e.g. on bare-metal microcontrollers) or mock buses in unit tests without external dependencies.
type I2CBus interface {
	// ReadRegister reads len(buf) bytes from the register reg of the I2C device at addr.
	ReadRegister(addr uint8, reg uint8, buf []byte) error

	// WriteRegister writes data to the register reg of the I2C device at addr.
	WriteRegister(addr uint8, reg uint8, data []byte) error
}
