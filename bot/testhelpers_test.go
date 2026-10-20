// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import "math"

// closeWithin reports whether got is within tolerance of want. Many tests in
// this package compare floating-point results.
func closeWithin(got, want, tolerance float64) bool {
	return math.Abs(got-want) <= tolerance
}
