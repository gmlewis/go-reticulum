// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"bytes"
	"context"
	"io"
	"math"
	"testing"
	"time"
)

func TestOpenGPSFromStream(t *testing.T) {
	t.Parallel()

	// Canonical GNRMC sentence from gps_test.go
	sentence := testRMCSentence + "\r\n"
	pr, pw := io.Pipe()

	reader := OpenGPSFromStream(pr)
	if reader == nil {
		t.Fatalf("OpenGPSFromStream() = nil")
	}
	t.Cleanup(func() {
		_ = reader.Close()
	})

	go func() {
		_, _ = pw.Write([]byte(sentence))
		_ = pw.Close()
	}()

	// Wait briefly for the sentence to be consumed
	var fix GPSFix
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fix = reader.LastFix()
		if fix.Valid {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !fix.Valid {
		t.Fatalf("fix.Valid = false after reading stream")
	}

	// testRMCSentence lat is 3745.31926 N -> 37 + 45.31926/60
	wantLat := 37.0 + 45.31926/60.0
	if math.Abs(fix.Lat-wantLat) > 0.0001 {
		t.Errorf("fix.Lat = %v, want %v", fix.Lat, wantLat)
	}
}

func TestOpenCompassFromStream(t *testing.T) {
	t.Parallel()

	// Canonical HCHDG sentence from compass_test.go
	sentence := hdgEastSentence + "\r\n"
	buf := bytes.NewBufferString(sentence)

	reader := OpenCompassFromStream(buf)
	if reader == nil {
		t.Fatalf("OpenCompassFromStream() = nil")
	}
	t.Cleanup(func() {
		_ = reader.Close()
	})

	var heading CompassHeading
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		heading = reader.LastHeading()
		if heading.Valid {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !heading.Valid {
		t.Fatalf("heading.Valid = false after reading stream")
	}
	if math.Abs(heading.MagneticDeg-101.1) > 0.01 {
		t.Errorf("heading.MagneticDeg = %v, want 101.1", heading.MagneticDeg)
	}
	if math.Abs(heading.TrueDeg-114.2) > 0.01 {
		t.Errorf("heading.TrueDeg = %v, want 114.2", heading.TrueDeg)
	}
}

func TestStartPeriodicMagnetometer(t *testing.T) {
	t.Parallel()

	bus := newMockI2CBus()
	sensor := NewQMC5883L(bus, QMC5883LDefaultI2CAddr)
	if err := sensor.Init(); err != nil {
		t.Fatalf("sensor.Init() error = %v", err)
	}
	bus.setVector(0, 1000, 0) // East: 90 degrees

	compassReader := NewCompassReader(nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	StartPeriodicMagnetometer(ctx, sensor, compassReader, 10*time.Millisecond)

	var heading CompassHeading
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		heading = compassReader.LastHeading()
		if heading.Valid {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !heading.Valid {
		t.Fatalf("heading.Valid = false from periodic magnetometer")
	}
	if math.Abs(heading.MagneticDeg-90.0) > 0.01 {
		t.Errorf("heading.MagneticDeg = %v, want 90.0", heading.MagneticDeg)
	}
	if heading.Cardinal != "E" {
		t.Errorf("heading.Cardinal = %q, want %q", heading.Cardinal, "E")
	}
}
