// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// PrettyDistance formats a distance in metres as a human-readable string,
// mirroring Python RNS.prettydistance (RNS/__init__.py:226-239).
func PrettyDistance(m float64, suffix string) string {
	num := m * 1e6
	units := []string{"µ", "m", "c", ""}
	lastUnit := "K"

	for _, unit := range units {
		divisor := 1000.0
		switch unit {
		case "m":
			divisor = 10
		case "c":
			divisor = 100
		}
		if math.Abs(num) < divisor {
			return fmt.Sprintf("%.2f %v%v", num, unit, suffix)
		}
		num /= divisor
	}
	return fmt.Sprintf("%.2f %v%v", num, lastUnit, suffix)
}

// PrettyShortTime formats a duration in seconds using seconds/milliseconds/
// microseconds, mirroring Python RNS.prettyshorttime
// (RNS/__init__.py:294-338).
func PrettyShortTime(seconds float64, verbose, compact bool) string {
	time := seconds * 1e6
	neg := false
	if time < 0 {
		time = math.Abs(time)
		neg = true
	}

	secs := int(math.Floor(time / 1e6))
	time = math.Mod(time, 1e6)
	millis := int(math.Floor(time / 1e3))
	time = math.Mod(time, 1e3)

	var micros float64
	if compact {
		micros = math.Trunc(time)
	} else {
		micros = roundTo2(time)
	}

	var components []string
	displayed := 0
	appendComponent := func(text string) {
		components = append(components, text)
		displayed++
	}

	if secs > 0 && (!compact || displayed < 2) {
		label := strconv.Itoa(secs) + "s"
		if verbose {
			label = strconv.Itoa(secs) + " second"
			if secs != 1 {
				label += "s"
			}
		}
		appendComponent(label)
	}
	if millis > 0 && (!compact || displayed < 2) {
		label := strconv.Itoa(millis) + "ms"
		if verbose {
			label = strconv.Itoa(millis) + " millisecond"
			if millis != 1 {
				label += "s"
			}
		}
		appendComponent(label)
	}
	if micros > 0 && (!compact || displayed < 2) {
		label := pythonFloatString(micros) + "µs"
		if verbose {
			label = pythonFloatString(micros) + " microsecond"
			if micros != 1 {
				label += "s"
			}
		}
		appendComponent(label)
	}

	var b strings.Builder
	for i, c := range components {
		switch {
		case i == 0:
		case i < len(components)-1:
			b.WriteString(", ")
		default:
			b.WriteString(" and ")
		}
		b.WriteString(c)
	}

	tstr := b.String()
	if tstr == "" {
		return "0us"
	}
	if neg {
		return "-" + tstr
	}
	return tstr
}

// roundTo2 reproduces Python's round(v, 2) on the binary value: the decimal
// representation rounded to two places and parsed back, so ties follow
// round-half-even on the represented float rather than Go's math.Round.
func roundTo2(v float64) float64 {
	r, err := strconv.ParseFloat(strconv.FormatFloat(v, 'f', 2, 64), 64)
	if err != nil {
		return v
	}
	return r
}

// pythonFloatString formats a float the way Python's str(float) does: shortest
// round-trip representation, always carrying a decimal point or exponent
// ("1" → "1.0", "0.5" → "0.5").
func pythonFloatString(v float64) string {
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// HostOS reports the host platform using Python's get_platform names
// (RNS/vendor/platformutils.py:24-31): "android" when an Android environment
// variable is set, "win32" on Windows, else the Go platform name (darwin,
// linux, freebsd, …). Python returns sys.platform, which is "win32" on
// Windows and the bare OS name elsewhere.
func HostOS() string {
	if os.Getenv("ANDROID_ARGUMENT") != "" || os.Getenv("ANDROID_ROOT") != "" {
		return "android"
	}
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

// PhyParams renders the physical-layer parameters Python's RNS.phyparams
// prints (RNS/__init__.py:340-347).
func PhyParams() string {
	return fmt.Sprintf(
		"Required Physical Layer MTU : %v bytes\n"+
			"Plaintext Packet MDU        : %v bytes\n"+
			"Encrypted Packet MDU        : %v bytes\n"+
			"Link Curve                  : %v\n"+
			"Link Packet MDU             : %v bytes\n"+
			"Link Public Key Size        : %v bits\n"+
			"Link Private Key Size       : %v bits",
		MTU, PacketPlainMDU, PacketEncryptedMDU, IdentityCurve, MDU,
		LinkECPubSize*8, LinkKeySize*8)
}
