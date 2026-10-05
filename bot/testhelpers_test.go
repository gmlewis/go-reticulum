// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import "math"

// closeWithin reports whether got is within tolerance of want. The geodesy
// engine's own tests moved to package geo with the implementation, and many
// tests in this package compare floating-point results, so the helper lives
// here as well.
func closeWithin(got, want, tolerance float64) bool {
	return math.Abs(got-want) <= tolerance
}
