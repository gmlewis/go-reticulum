// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/hex"
	"fmt"
	"reflect"
)

// PrettyHexRep returns a lowercase hexadecimal representation of b wrapped in
// angle brackets.
func PrettyHexRep(b []byte) string {
	return fmt.Sprintf("<%v>", hex.EncodeToString(b))
}

func asAnyMap(v any) map[any]any {
	switch m := v.(type) {
	case map[any]any:
		return m
	case map[string]any:
		out := make(map[any]any, len(m))
		for k, val := range m {
			out[k] = val
		}
		return out
	default:
		return nil
	}
}

func lookupAny(m map[any]any, key string) (any, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m[key]
	if ok {
		return v, true
	}
	for mk, mv := range m {
		if ks, ok := mk.(string); ok && ks == key {
			return mv, true
		}
	}
	return nil, false
}

func lookupAnyValue(m map[any]any, key string) any {
	v, _ := lookupAny(m, key)
	return v
}

func hasPythonEquivalentNonStringKey(m map[any]any, key string) bool {
	if m == nil {
		return false
	}
	for mk := range m {
		if _, ok := mk.(string); ok {
			continue
		}
		rv := reflect.ValueOf(mk)
		if rv.IsValid() && rv.Kind() == reflect.String && rv.String() == key {
			return true
		}
	}
	return false
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return ""
	}
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int:
		return t != 0
	case int64:
		return t != 0
	case uint64:
		return t != 0
	default:
		return false
	}
}

func asInt(v any) int {
	if i, ok := numericIntValue(v); ok {
		return i
	}
	return 0
}

func asFloat64(v any) float64 {
	if f, ok := numericFloat64Value(v); ok {
		return f
	}
	return 0
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func numericIntValue(v any) (int, bool) {
	if b, ok := v.(bool); ok {
		return boolToInt(b), true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return int(rv.Float()), true
	default:
		return 0, false
	}
}

func numericFloat64Value(v any) (float64, bool) {
	if b, ok := v.(bool); ok {
		return float64(boolToInt(b)), true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	default:
		return 0, false
	}
}

func lookupOptFloat64(m map[any]any, key string) *float64 {
	v, ok := lookupAny(m, key)
	if !ok || v == nil {
		return nil
	}
	f := asFloat64(v)
	return &f
}

func lookupOptInt(m map[any]any, key string) *int {
	v, ok := lookupAny(m, key)
	if !ok || v == nil {
		return nil
	}
	i := asInt(v)
	return &i
}

func lookupOptString(m map[any]any, key string) *string {
	v, ok := lookupAny(m, key)
	if !ok || v == nil {
		return nil
	}
	s := asString(v)
	return &s
}

func lookupOptBool(m map[any]any, key string) *bool {
	v, ok := lookupAny(m, key)
	if !ok || v == nil {
		return nil
	}
	b := asBool(v)
	return &b
}

func asBytes(v any) []byte {
	switch t := v.(type) {
	case []byte:
		return t
	case string:
		return []byte(t)
	default:
		return nil
	}
}

func lookupOptBytes(m map[any]any, key string) []byte {
	v, ok := lookupAny(m, key)
	if !ok || v == nil {
		return nil
	}
	return asBytes(v)
}

func asUint64(v any) uint64 {
	switch t := v.(type) {
	case uint64:
		return t
	case uint32:
		return uint64(t)
	case int:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case int64:
		if t < 0 {
			return 0
		}
		return uint64(t)
	case float64:
		if t < 0 {
			return 0
		}
		return uint64(t)
	default:
		return 0
	}
}
