// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package entropy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// ESP32C5RNGDataReg is the memory-mapped register address of the hardware RNG
// peripheral on the Espressif ESP32-C5 SoC.
const ESP32C5RNGDataReg = uintptr(0x600B2800)

// ErrMMIONotSupported is reported when direct MMIO access is attempted on an
// unsupported operating system or platform.
var ErrMMIONotSupported = errors.New("esp32c5 mmio read requires bare-metal runtime")

// WordReader reads a single 32-bit hardware word from an entropy peripheral.
type WordReader func() (uint32, error)

// DefaultWordReader is the default word reader. On hosted platforms it fails
// with ErrMMIONotSupported; bare-metal runtimes override it with the peripheral
// MMIO reader.
var DefaultWordReader WordReader = func() (uint32, error) {
	return 0, ErrMMIONotSupported
}

// ESP32C5Source adapts the ESP32-C5 on-chip hardware random number generator
// to the Source interface.
//
// The ESP32-C5 RNG peripheral samples thermal noise from the SAR ADC and clock
// jitter from RC_FAST. It requires the entropy source to be enabled (such as
// via bootloader_random_enable() or an active RF subsystem) before drawing
// samples.
type ESP32C5Source struct {
	mu       sync.Mutex
	label    string
	readWord WordReader
	rem      [4]byte
	remLen   int
}

var _ Source = (*ESP32C5Source)(nil)

// NewESP32C5Source returns a hardware entropy source for the ESP32-C5 using the
// provided WordReader. If readWord is nil, DefaultWordReader is used.
func NewESP32C5Source(readWord WordReader) *ESP32C5Source {
	if readWord == nil {
		readWord = DefaultWordReader
	}
	return &ESP32C5Source{
		label:    "esp32c5-sar-adc",
		readWord: readWord,
	}
}

// Name identifies this source in logs and error messages.
func (s *ESP32C5Source) Name() string {
	return s.label
}

// Read fills b completely with raw samples from the hardware generator.
// If the reader cannot fill b completely, an error is returned.
func (s *ESP32C5Source) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0
	// 1. Drain any remaining bytes from previous word read.
	if s.remLen > 0 {
		start := 4 - s.remLen
		avail := s.rem[start:4]
		copied := copy(b[n:], avail)
		n += copied
		s.remLen -= copied
	}

	// 2. Read full 32-bit words until b is satisfied.
	var wordBuf [4]byte
	for n < len(b) {
		w, err := s.readWord()
		if err != nil {
			return n, fmt.Errorf("esp32c5 entropy read: %w", err)
		}
		binary.LittleEndian.PutUint32(wordBuf[:], w)

		need := len(b) - n
		if need >= 4 {
			copy(b[n:], wordBuf[:])
			n += 4
		} else {
			copy(b[n:], wordBuf[:need])
			n += need
			// Save remainder
			s.remLen = 4 - need
			copy(s.rem[4-s.remLen:], wordBuf[need:])
		}
	}

	return n, nil
}
