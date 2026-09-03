// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package toml

import (
	"math"
	"strconv"
	"strings"
)

// FormatFloat renders a float the way Python's repr does: shortest
// round-trip digits, integral values keep a ".0" suffix, and exponent form
// (two-digit zero-padded exponent, always signed) for abs < 1e-4 or
// >= 1e16. Go's strconv.FormatFloat does not match these rules.
func FormatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}

	// Shortest round-trip digits and decimal exponent, via the %e form
	// ("d.ddd e±ee"). For zero, digits are "0".
	e := strconv.FormatFloat(f, 'e', -1, 64)
	eIdx := strings.IndexByte(e, 'e')
	mant, expPart := e[:eIdx], e[eIdx+1:]
	exp, _ := strconv.Atoi(expPart)
	digits := strings.Replace(mant, ".", "", 1)
	neg := false
	if digits[0] == '-' {
		neg = true
		digits = digits[1:]
	}
	// FormatFloat never emits trailing zeros with precision -1 except for
	// an exact zero value.
	if digits == "0" {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}

	if exp >= 16 || exp <= -5 {
		return exponentForm(neg, digits, exp)
	}
	return plainForm(neg, digits, exp)
}

// exponentForm renders digits * 10^exp as Python's repr exponent form: a
// mantissa with a decimal point when more than one significant digit, and a
// signed, at-least-two-digit exponent.
func exponentForm(neg bool, digits string, exp int) string {
	var sb strings.Builder
	if neg {
		sb.WriteByte('-')
	}
	sb.WriteByte(digits[0])
	if len(digits) > 1 {
		sb.WriteByte('.')
		sb.WriteString(digits[1:])
	}
	sb.WriteByte('e')
	if exp < 0 {
		sb.WriteByte('-')
		exp = -exp
	} else {
		sb.WriteByte('+')
	}
	if exp < 10 {
		sb.WriteByte('0')
	}
	sb.WriteString(strconv.Itoa(exp))
	return sb.String()
}

// plainForm renders digits * 10^(exp-(len-1)) as a plain decimal, integral
// values with a trailing ".0".
func plainForm(neg bool, digits string, exp int) string {
	var sb strings.Builder
	if neg {
		sb.WriteByte('-')
	}
	switch {
	case exp >= len(digits):
		sb.WriteString(digits)
		for i := 0; i < exp-len(digits)+1; i++ {
			sb.WriteByte('0')
		}
		sb.WriteString(".0")
	case exp >= 0:
		sb.WriteString(digits[:exp+1])
		frac := digits[exp+1:]
		if frac == "" {
			sb.WriteString(".0")
		} else {
			sb.WriteString(".")
			sb.WriteString(frac)
		}
	default:
		sb.WriteString("0.")
		for i := 0; i < -exp-1; i++ {
			sb.WriteByte('0')
		}
		sb.WriteString(digits)
	}
	return sb.String()
}
