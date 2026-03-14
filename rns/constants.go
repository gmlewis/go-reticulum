// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
	"math"
)

// Log levels
const (
	LogNone     = -1
	LogCritical = 0
	LogError    = 1
	LogWarning  = 2
	LogNotice   = 3
	LogInfo     = 4
	LogVerbose  = 5
	LogDebug    = 6
	LogExtreme  = 7
)

// Log destinations
const (
	LogStdout   = 0x91
	LogDestFile = 0x92
	LogCallback = 0x93
)

const (
	LogMaxSize = 5 * 1024 * 1024
)

// Default MTU
const (
	MTU = 500
)

const (
	ReticulumHopsMax = 20
)

const (
	NameHashLength      = 80
	TruncatedHashLength = 128
	HeaderMinSize       = 2 + 1 + (TruncatedHashLength/8)*1
	HeaderMaxSize       = 2 + 1 + (TruncatedHashLength/8)*2
	IFACMinSize         = 1
	MDU                 = MTU - HeaderMaxSize - IFACMinSize
)

// Version represents the Reticulum version.
const Version = "1.1.3" // To be updated based on the original repo's version.

// LogLevelName returns the string representation of a log level.
func LogLevelName(level int) string {
	switch level {
	case LogCritical:
		return "[Critical]"
	case LogError:
		return "[Error]   "
	case LogWarning:
		return "[Warning] "
	case LogNotice:
		return "[Notice]  "
	case LogInfo:
		return "[Info]    "
	case LogVerbose:
		return "[Verbose] "
	case LogDebug:
		return "[Debug]   "
	case LogExtreme:
		return "[Extra]   "
	default:
		return "[Unknown] "
	}
}

// PrettySize formats a byte count into a human-readable string.
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

// PrettyTime formats a duration in seconds into a human-readable string.
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
		displayed++
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
