// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"time"
)

// QMC5883L I2C Constants and Registers.
const (
	// QMC5883LDefaultI2CAddr is the standard 7-bit I2C slave address for the QMC5883L.
	QMC5883LDefaultI2CAddr = 0x0D

	qmc5883lRegDataOutputXLSB = 0x00
	qmc5883lRegDataOutputXMSB = 0x01
	qmc5883lRegDataOutputYLSB = 0x02
	qmc5883lRegDataOutputYMSB = 0x03
	qmc5883lRegDataOutputZLSB = 0x04
	qmc5883lRegDataOutputZMSB = 0x05
	qmc5883lRegStatus         = 0x06
	qmc5883lRegControl1       = 0x09
	qmc5883lRegControl2       = 0x0A
	qmc5883lRegSetResetPeriod = 0x0B

	// qmc5883lControl1Default: ODR=200Hz, RNG=8G, OSR=512, Mode=Continuous -> 0x1D
	qmc5883lControl1Default = 0x1D
)

// QMC5883L is a driver for the QMC5883L 3-axis electronic magnetometer connected via I2C.
// It samples raw flux vectors, applies hard-iron calibration offsets, computes horizontal
// azimuth heading, and formats heading data for the Reticulum navigation and direction-finding tools.
type QMC5883L struct {
	bus  I2CBus
	addr uint8

	mu             sync.Mutex
	offsetX        float64
	offsetY        float64
	declination    float64
	hasDeclination bool
}

// NewQMC5883L returns a new QMC5883L driver instance on the given I2C bus and device address.
func NewQMC5883L(bus I2CBus, addr uint8) *QMC5883L {
	if addr == 0 {
		addr = QMC5883LDefaultI2CAddr
	}
	return &QMC5883L{
		bus:  bus,
		addr: addr,
	}
}

// Init configures the sensor for continuous measurement mode:
// 1. Issues soft reset.
// 2. Configures SET/RESET period register to 0x01.
// 3. Sets Control Register 1 for continuous mode at 200 Hz with 512 oversampling and 8 Gauss full scale.
func (q *QMC5883L) Init() error {
	// Soft reset
	if err := q.bus.WriteRegister(q.addr, qmc5883lRegControl2, []byte{0x80}); err != nil {
		return fmt.Errorf("qmc5883l reset: %w", err)
	}

	// Set SET/RESET period
	if err := q.bus.WriteRegister(q.addr, qmc5883lRegSetResetPeriod, []byte{0x01}); err != nil {
		return fmt.Errorf("qmc5883l set period: %w", err)
	}

	// Set continuous measurement mode
	if err := q.bus.WriteRegister(q.addr, qmc5883lRegControl1, []byte{qmc5883lControl1Default}); err != nil {
		return fmt.Errorf("qmc5883l set control 1: %w", err)
	}

	return nil
}

// SetCalibration configures the hard-iron offset values to subtract from raw X and Y sensor readings.
func (q *QMC5883L) SetCalibration(offsetX, offsetY float64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.offsetX = offsetX
	q.offsetY = offsetY
}

// SetDeclination sets the local magnetic declination in degrees (+ East, - West).
func (q *QMC5883L) SetDeclination(declinationDeg float64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.declination = declinationDeg
	q.hasDeclination = true
}

// ReadRaw reads the raw 16-bit 2's complement magnetic flux values for X, Y, and Z axes.
func (q *QMC5883L) ReadRaw() (x, y, z int16, err error) {
	var buf [6]byte
	if err := q.bus.ReadRegister(q.addr, qmc5883lRegDataOutputXLSB, buf[:]); err != nil {
		return 0, 0, 0, fmt.Errorf("qmc5883l read raw: %w", err)
	}
	x = int16(binary.LittleEndian.Uint16(buf[0:2]))
	y = int16(binary.LittleEndian.Uint16(buf[2:4]))
	z = int16(binary.LittleEndian.Uint16(buf[4:6]))
	return x, y, z, nil
}

// ReadHeading reads the raw magnetic vector, applies calibration and declination,
// and returns a validated CompassHeading.
func (q *QMC5883L) ReadHeading() (CompassHeading, error) {
	x, y, _, err := q.ReadRaw()
	if err != nil {
		return CompassHeading{}, err
	}

	q.mu.Lock()
	ox, oy := q.offsetX, q.offsetY
	decl, hasDecl := q.declination, q.hasDeclination
	q.mu.Unlock()

	xCal := float64(x) - ox
	yCal := float64(y) - oy

	headingRad := math.Atan2(yCal, xCal)
	headingDeg := headingRad * (180.0 / math.Pi)
	if headingDeg < 0 {
		headingDeg += 360.0
	}
	headingDeg = math.Mod(headingDeg, 360.0)

	h := CompassHeading{
		Valid:          true,
		HasMagnetic:    true,
		MagneticDeg:    headingDeg,
		Cardinal:       CardinalDirection(headingDeg),
		TimeUTC:        time.Now().UTC(),
		HasDeclination: hasDecl,
		DeclinationDeg: decl,
	}

	if hasDecl {
		h.HasTrue = true
		h.TrueDeg = normalizeDegrees(headingDeg + decl)
	}

	return h, nil
}

// FormatNMEA formats the current heading into a standard NMEA-0183 $HCHDG sentence.
func (q *QMC5883L) FormatNMEA() (string, error) {
	h, err := q.ReadHeading()
	if err != nil {
		return "", err
	}

	declDir := "E"
	declVal := h.DeclinationDeg
	if declVal < 0 {
		declDir = "W"
		declVal = -declVal
	}

	body := fmt.Sprintf("HCHDG,%.1f,,,%.1f,%v", h.MagneticDeg, declVal, declDir)
	checksum := nmeaXOR(body)
	return fmt.Sprintf("$%v*%02X\r\n", body, checksum), nil
}
