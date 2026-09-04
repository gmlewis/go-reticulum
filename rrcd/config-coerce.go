// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file implements the Python-semantics coercions applied to raw
// configuration values at their use sites: Python stores whatever TOML
// produced in the dataclass field and converts only when the value is
// used (float(...), int(...), truthiness), so these helpers mirror that
// per-use-site behavior for the Go typed fields.

package rrcd

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gmlewis/go-reticulum/rrcd/toml"
)

// configFloat converts a raw config value the way Python's float() does:
// bools map to 1.0/0.0, integers and floats pass through, numeric text
// parses, and unparseable input yields 0. Python raises for unparseable
// input (killing the calling worker loop); Go reports 0 so the loop keeps
// running with the feature disabled — a necessary mechanical divergence.
func configFloat(v any) float64 {
	switch n := v.(type) {
	case bool:
		if n {
			return 1
		}
		return 0
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case uint64:
		return float64(n)
	case float64:
		return n
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0
		}
		return f
	}
	return 0
}

// configInt converts a raw config value the way Python's int() does:
// bools map to 1/0, integers pass through, floats truncate toward zero,
// integer text parses, and unparseable input yields 0. Python raises for
// unparseable input (aborting the calling path); Go reports 0 — a
// necessary mechanical divergence for values TOML cannot normally
// produce.
func configInt(v any) int {
	switch n := v.(type) {
	case bool:
		if n {
			return 1
		}
		return 0
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		if n > math.MaxInt {
			return math.MaxInt
		}
		return int(n)
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0
		}
		return int(math.Trunc(n))
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		if err != nil {
			return 0
		}
		return int(parsed)
	}
	return 0
}

// configIntOr converts a raw config value the way Python's int() does,
// falling back when int() would raise (the normalize_nick pattern, which
// catches the conversion failure and uses its default).
func configIntOr(v any, fallback int) int {
	switch n := v.(type) {
	case bool, int, int64, uint64:
		return configInt(n)
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return fallback
		}
		return configInt(n)
	case string:
		if _, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err != nil {
			return fallback
		}
		return configInt(n)
	}
	return fallback
}

// configTruthy reports Python bool() of a raw config value: 0/0.0/empty
// text/empty containers are false, everything else is true (including
// non-empty strings like "false" and NaN floats).
func configTruthy(v any) bool {
	switch n := v.(type) {
	case nil:
		return false
	case bool:
		return n
	case int:
		return n != 0
	case int64:
		return n != 0
	case uint64:
		return n != 0
	case float64:
		return n != 0
	case string:
		return n != ""
	case []any:
		return len(n) > 0
	}
	return true
}

// pythonScalarStr renders a raw config value the way Python's str() does
// for the scalar classes a TOML value can produce: True/False for bools,
// decimal digits for ints, Python repr for floats, and bare text for
// strings.
func pythonScalarStr(v any) string {
	switch n := v.(type) {
	case nil:
		return "None"
	case bool:
		if n {
			return "True"
		}
		return "False"
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case uint64:
		return strconv.FormatUint(n, 10)
	case float64:
		return toml.FormatFloat(n)
	case string:
		return n
	case []any:
		return "len=" + strconv.Itoa(len(n))
	}
	return fmt.Sprintf("%v", v)
}
