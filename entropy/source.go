// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package entropy

import crand "crypto/rand"

// NewOSSource returns the source a hosted build sits behind: the operating
// system's CSPRNG.
//
// On a workstation this is the hardware-backed generator, already seeded and
// health-tested by the kernel, so the gate over it is redundant — and it is
// still worth installing, because it means the same code path that a bare-metal
// device exercises in the field is the one the desktop is testing, rather than a
// path that only exists on the device.
func NewOSSource() Source {
	return SourceFunc{Label: "os-csprng", ReadFunc: crand.Read}
}

// SourceFunc adapts a read function to a Source, so a bare-metal call site can
// wrap its register read in three lines rather than declaring a type:
//
//	entropy.SourceFunc{
//		Label:    "esp32c5-sar-adc",
//		ReadFunc: soc.ReadRNGData,
//	}
type SourceFunc struct {
	// Label is the name reported by Name. Use something an operator can act on.
	Label string

	// ReadFunc fills the buffer with raw samples from the device. It must fill
	// the buffer completely or return an error.
	ReadFunc func(b []byte) (int, error)
}

// Name identifies the source in error messages and logs.
func (s SourceFunc) Name() string { return s.Label }

// Read fills b with raw samples from the device.
func (s SourceFunc) Read(b []byte) (int, error) { return s.ReadFunc(b) }
