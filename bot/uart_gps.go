// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"context"
	"io"
	"time"
)

// OpenGPSFromStream builds a streaming GNSS reader over an arbitrary byte source,
// such as a hardware UART stream or mock buffer, and starts the background scan
// goroutine. The caller owns the returned reader and must Close it.
func OpenGPSFromStream(src io.Reader) *GPSReader {
	if src == nil {
		return nil
	}
	reader := NewGPSReader(src)
	reader.Start(context.Background())
	return reader
}

// OpenCompassFromStream builds a streaming electronic compass reader over an
// arbitrary byte source, such as a hardware serial port or mock buffer, and
// starts the background scan goroutine. The caller owns the returned reader and
// must Close it.
func OpenCompassFromStream(src io.Reader) *CompassReader {
	if src == nil {
		return nil
	}
	reader := NewCompassReader(src)
	reader.Start(context.Background())
	return reader
}

// StartPeriodicMagnetometer launches a background goroutine that polls the magnetometer
// at the given interval and updates compassReader with the latest heading until ctx is canceled.
func StartPeriodicMagnetometer(ctx context.Context, sensor *QMC5883L, compassReader *CompassReader, interval time.Duration) {
	if sensor == nil || compassReader == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if heading, err := sensor.ReadHeading(); err == nil {
					compassReader.SetHeading(heading)
				}
			}
		}
	}()
}
