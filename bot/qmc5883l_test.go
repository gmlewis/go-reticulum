// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"encoding/binary"
	"math"
	"testing"
)

type mockI2CBus struct {
	registers map[uint8][]byte
	writes    map[uint8][]byte
}

func newMockI2CBus() *mockI2CBus {
	return &mockI2CBus{
		registers: make(map[uint8][]byte),
		writes:    make(map[uint8][]byte),
	}
}

func (m *mockI2CBus) ReadRegister(addr uint8, reg uint8, buf []byte) error {
	data, ok := m.registers[reg]
	if !ok {
		for i := range buf {
			buf[i] = 0
		}
		return nil
	}
	copy(buf, data)
	return nil
}

func (m *mockI2CBus) WriteRegister(addr uint8, reg uint8, data []byte) error {
	cpy := make([]byte, len(data))
	copy(cpy, data)
	m.writes[reg] = cpy
	m.registers[reg] = cpy
	return nil
}

func (m *mockI2CBus) setVector(x, y, z int16) {
	buf := make([]byte, 6)
	binary.LittleEndian.PutUint16(buf[0:2], uint16(x))
	binary.LittleEndian.PutUint16(buf[2:4], uint16(y))
	binary.LittleEndian.PutUint16(buf[4:6], uint16(z))
	m.registers[qmc5883lRegDataOutputXLSB] = buf
}

func TestQMC5883L_Init(t *testing.T) {
	t.Parallel()

	bus := newMockI2CBus()
	sensor := NewQMC5883L(bus, QMC5883LDefaultI2CAddr)

	if err := sensor.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Verify Control Register 1 was written with continuous mode config
	ctl1, ok := bus.writes[qmc5883lRegControl1]
	if !ok || len(ctl1) == 0 {
		t.Errorf("Control 1 register 0x09 was not written")
	}

	// Verify SET/RESET period register was written
	period, ok := bus.writes[qmc5883lRegSetResetPeriod]
	if !ok || len(period) == 0 || period[0] != 0x01 {
		t.Errorf("SET/RESET period register 0x0B = %v, want 0x01", period)
	}
}

func TestQMC5883L_Heading(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		x, y, z     int16
		wantHeading float64
		wantCard    string
	}{
		{"North", 1000, 0, 0, 0.0, "N"},
		{"NorthEast", 1000, 1000, 0, 45.0, "NE"},
		{"East", 0, 1000, 0, 90.0, "E"},
		{"SouthEast", -1000, 1000, 0, 135.0, "SE"},
		{"South", -1000, 0, 0, 180.0, "S"},
		{"SouthWest", -1000, -1000, 0, 225.0, "SW"},
		{"West", 0, -1000, 0, 270.0, "W"},
		{"NorthWest", 1000, -1000, 0, 315.0, "NW"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := newMockI2CBus()
			sensor := NewQMC5883L(bus, QMC5883LDefaultI2CAddr)
			if err := sensor.Init(); err != nil {
				t.Fatalf("Init() error = %v", err)
			}

			bus.setVector(tt.x, tt.y, tt.z)

			heading, err := sensor.ReadHeading()
			if err != nil {
				t.Fatalf("ReadHeading() error = %v", err)
			}
			if !heading.Valid {
				t.Errorf("heading.Valid = false, want true")
			}
			if math.Abs(heading.MagneticDeg-tt.wantHeading) > 0.01 {
				t.Errorf("MagneticDeg = %v, want %v", heading.MagneticDeg, tt.wantHeading)
			}
			if heading.Cardinal != tt.wantCard {
				t.Errorf("Cardinal = %q, want %q", heading.Cardinal, tt.wantCard)
			}
		})
	}
}

func TestQMC5883L_CalibrationAndDeclination(t *testing.T) {
	t.Parallel()

	bus := newMockI2CBus()
	sensor := NewQMC5883L(bus, QMC5883LDefaultI2CAddr)
	if err := sensor.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Set calibration offsets: X offset +100, Y offset +200
	sensor.SetCalibration(100, 200)

	// Set magnetic declination: +15 degrees
	sensor.SetDeclination(15.0)

	// Raw sensor reads X=1100, Y=200 -> after calibration: X=1000, Y=0 (due North magnetic)
	bus.setVector(1100, 200, 0)

	heading, err := sensor.ReadHeading()
	if err != nil {
		t.Fatalf("ReadHeading() error = %v", err)
	}

	if math.Abs(heading.MagneticDeg-0.0) > 0.01 {
		t.Errorf("MagneticDeg = %v, want 0.0", heading.MagneticDeg)
	}
	if !heading.HasTrue {
		t.Errorf("HasTrue = false, want true")
	}
	if math.Abs(heading.TrueDeg-15.0) > 0.01 {
		t.Errorf("TrueDeg = %v, want 15.0", heading.TrueDeg)
	}
	if heading.DeclinationDeg != 15.0 {
		t.Errorf("DeclinationDeg = %v, want 15.0", heading.DeclinationDeg)
	}
}

func TestQMC5883L_NMEASentence(t *testing.T) {
	t.Parallel()

	bus := newMockI2CBus()
	sensor := NewQMC5883L(bus, QMC5883LDefaultI2CAddr)
	if err := sensor.Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	bus.setVector(1000, 0, 0) // Due North
	sensor.SetDeclination(12.5)

	sentence, err := sensor.FormatNMEA()
	if err != nil {
		t.Fatalf("FormatNMEA() error = %v", err)
	}

	// Sentence should be valid NMEA sentence starting with $ and ending with \r\n
	if len(sentence) < 10 || sentence[0] != '$' {
		t.Errorf("invalid sentence format: %q", sentence)
	}

	// Check that CompassReader parses it
	reader := NewCompassReader(nil)
	reader.ConsumeSentence(sentence)

	last := reader.LastHeading()
	if !last.Valid {
		t.Errorf("LastHeading().Valid = false")
	}
	if math.Abs(last.MagneticDeg-0.0) > 0.01 {
		t.Errorf("LastHeading().MagneticDeg = %v, want 0.0", last.MagneticDeg)
	}
}
