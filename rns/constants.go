// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
	"math"
)

const (
	// MTU defines the Maximum Transmission Unit for generic packets across the Reticulum network.
	MTU = 500
)

const (
	// ReticulumHopsMax limits the absolute maximum number of network hops a packet can traverse.
	ReticulumHopsMax = 20
)

const (
	// NameHashLength establishes the fixed length in bytes for Reticulum name hashes.
	NameHashLength = 80
	// TruncatedHashLength establishes the fixed length in bits for truncated identifiers.
	TruncatedHashLength = 128
	// HeaderMinSize specifies the absolute minimum number of bytes required for a packet header.
	HeaderMinSize = 2 + 1 + (TruncatedHashLength/8)*1
	// HeaderMaxSize specifies the maximum possible size in bytes for a packet header.
	HeaderMaxSize = 2 + 1 + (TruncatedHashLength/8)*2
	// IFACMinSize specifies the minimum number of bytes allocated for interface control data.
	IFACMinSize = 1
	// MDU defines the Maximum Data Unit payload size after subtracting theoretical maximum header overhead.
	MDU = MTU - HeaderMaxSize - IFACMinSize
)

// PrettySize dynamically formats a precise byte count into an easily readable string with magnitude suffixes.
func PrettySize(num float64, suffix string) string {
	units := []string{"", "K", "M", "G", "T", "P", "E", "Z"}
	lastUnit := "Y"

	if suffix == "b" {
		num *= 8
	}

	for _, unit := range units {
		if math.Abs(num) < 1000.0 {
			if unit == "" {
				return fmt.Sprintf("%.0f %v%v", num, unit, suffix)
			}
			return fmt.Sprintf("%.2f %v%v", num, unit, suffix)
		}
		num /= 1000.0
	}

	return fmt.Sprintf("%.2f %v%v", num, lastUnit, suffix)
}

// PrettySpeed formats a bit rate (in bits per second) into a human-readable
// string like "1.00 Kbps" or "500.00 Mbps".
func PrettySpeed(num float64) string {
	return PrettySize(num/8, "b") + "ps"
}

// PrettyTime calculates and formats a raw duration in seconds into a human-readable temporal string representation.
func PrettyTime(seconds float64, verbose bool, compact bool) string {
	neg := false
	if seconds < 0 {
		seconds = math.Abs(seconds)
		neg = true
	}

	days := int(seconds) / (24 * 3600)
	rem := int(seconds) % (24 * 3600)
	hours := rem / 3600
	rem %= 3600
	minutes := rem / 60
	rem %= 60
	secs := float64(rem) + (seconds - math.Floor(seconds))

	if compact {
		secs = math.Floor(secs)
	}

	var components []string
	displayed := 0

	if days > 0 && (!compact || displayed < 2) {
		label := "d"
		if verbose {
			label = " day"
			if days != 1 {
				label += "s"
			}
		}
		components = append(components, fmt.Sprintf("%v%v", days, label))
		displayed++
	}

	if hours > 0 && (!compact || displayed < 2) {
		label := "h"
		if verbose {
			label = " hour"
			if hours != 1 {
				label += "s"
			}
		}
		components = append(components, fmt.Sprintf("%v%v", hours, label))
		displayed++
	}

	if minutes > 0 && (!compact || displayed < 2) {
		label := "m"
		if verbose {
			label = " minute"
			if minutes != 1 {
				label += "s"
			}
		}
		components = append(components, fmt.Sprintf("%v%v", minutes, label))
		displayed++
	}

	if secs > 0 && (!compact || displayed < 2) {
		label := "s"
		if verbose {
			label = " second"
			if secs != 1 {
				label += "s"
			}
		}
		if math.Floor(secs) == secs {
			components = append(components, fmt.Sprintf("%.0f%v", secs, label))
		} else {
			components = append(components, fmt.Sprintf("%.2f%v", secs, label))
		}
	}

	if len(components) == 0 {
		return "0s"
	}

	result := ""
	for i, c := range components {
		if i > 0 {
			if i == len(components)-1 {
				result += " and "
			} else {
				result += ", "
			}
		}
		result += c
	}

	if neg {
		return "-" + result
	}
	return result
}

// PrettyFrequency formats a frequency value (in Hz as a float where
// 1.0 = 1 Hz) into a human-readable string with SI unit prefixes.
// The input is multiplied by 1e6 and iterated through units
// ["µ","m","","K","M","G","T","P","E","Z"] with suffix "Hz".
func PrettyFrequency(hz float64) string {
	num := hz * 1e6
	units := []string{"µ", "m", "", "K", "M", "G", "T", "P", "E", "Z"}
	lastUnit := "Y"
	suffix := "Hz"

	for _, unit := range units {
		if math.Abs(num) < 1000.0 {
			return fmt.Sprintf("%.2f %v%v", num, unit, suffix)
		}
		num /= 1000.0
	}

	return fmt.Sprintf("%.2f %v%v", num, lastUnit, suffix)
}

// PrettyHex returns a bracketed hex representation of the provided data, matching Python's prettyhexrep.
func PrettyHex(data []byte) string {
	return fmt.Sprintf("<%x>", data)
}

// PrettyHexFromString formats a hex string with angle brackets,
// matching Python's prettyhexrep output format.
func PrettyHexFromString(hexStr string) string {
	return "<" + hexStr + ">"
}
