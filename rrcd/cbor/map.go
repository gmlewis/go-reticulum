// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package cbor

// Pair is a single key/value entry of an ordered map.
type Pair struct {
	Key any
	Val any
}

// Map is an insertion-ordered CBOR map. Key lookup uses Python dict equality
// semantics: a bool key equals the matching int (true == 1, false == 0), and
// integer keys of differing widths compare numerically. Setting an existing
// key updates its value in place and keeps the key's position (and original
// key representation); setting a new key appends it.
type Map struct {
	pairs []Pair
}

// NewMap returns an empty ordered map.
func NewMap() *Map { return &Map{} }

// Get returns the value stored under key.
func (m *Map) Get(key any) (any, bool) {
	if m == nil {
		return nil, false
	}
	for i := range m.pairs {
		if keyEqual(m.pairs[i].Key, key) {
			return m.pairs[i].Val, true
		}
	}
	return nil, false
}

// Set stores val under key, updating the existing entry in place (preserving
// its position and stored key) or appending a new pair.
func (m *Map) Set(key, val any) {
	if m == nil {
		return
	}
	for i := range m.pairs {
		if keyEqual(m.pairs[i].Key, key) {
			m.pairs[i].Val = val
			return
		}
	}
	m.pairs = append(m.pairs, Pair{Key: key, Val: val})
}

// Delete removes key and reports whether it was present. The relative order
// of the remaining entries is preserved.
func (m *Map) Delete(key any) bool {
	_, ok := m.Pop(key)
	return ok
}

// Pop removes key and returns its value, reporting whether it was present.
func (m *Map) Pop(key any) (any, bool) {
	if m == nil {
		return nil, false
	}
	for i := range m.pairs {
		if keyEqual(m.pairs[i].Key, key) {
			val := m.pairs[i].Val
			m.pairs = append(m.pairs[:i], m.pairs[i+1:]...)
			return val, true
		}
	}
	return nil, false
}

// Pairs returns a copy of the map's key/value pairs in insertion order.
func (m *Map) Pairs() []Pair {
	if m == nil {
		return nil
	}
	out := make([]Pair, len(m.pairs))
	copy(out, m.pairs)
	return out
}

// Len returns the number of entries.
func (m *Map) Len() int {
	if m == nil {
		return 0
	}
	return len(m.pairs)
}

// Has reports whether key is present.
func (m *Map) Has(key any) bool {
	_, ok := m.Get(key)
	return ok
}

// GetInt returns the entry as an integer. Bool values count as 1/0; float
// values are not integers. A uint64 beyond int64 range reports not-found.
func (m *Map) GetInt(key int64) (int64, bool) {
	v, ok := m.Get(key)
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case int64:
		return n, true
	case uint64:
		if n <= 1<<63-1 {
			return int64(n), true
		}
	}
	return 0, false
}

// GetString returns the entry as a text string.
func (m *Map) GetString(key int64) (string, bool) {
	v, ok := m.Get(key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// GetBytes returns the entry as a byte string.
func (m *Map) GetBytes(key int64) ([]byte, bool) {
	v, ok := m.Get(key)
	if !ok {
		return nil, false
	}
	b, ok := v.([]byte)
	return b, ok
}

// GetMap returns the entry as a nested ordered map.
func (m *Map) GetMap(key int64) (*Map, bool) {
	v, ok := m.Get(key)
	if !ok {
		return nil, false
	}
	sub, ok := v.(*Map)
	return sub, ok
}

// GetBool returns the entry as a boolean.
func (m *Map) GetBool(key int64) (bool, bool) {
	v, ok := m.Get(key)
	if !ok {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

// SetInt stores an integer value under an integer key, preserving the key's
// position when it already exists.
func (m *Map) SetInt(key, val int64) { m.Set(key, val) }

// SetString stores a text value under an integer key, preserving the key's
// position when it already exists.
func (m *Map) SetString(key int64, val string) { m.Set(key, val) }

// SetBytes stores a byte-string value under an integer key, preserving the
// key's position when it already exists.
func (m *Map) SetBytes(key int64, val []byte) { m.Set(key, val) }

// keyEqual reports Python dict-key equality between two values: bools and
// integers compare numerically (true == 1), as do floats; byte strings
// compare by content; text strings by value; all other types by identity.
func keyEqual(a, b any) bool {
	an, aok := numericValue(a)
	bn, bok := numericValue(b)
	if aok && bok {
		return an == bn
	}
	if aok != bok {
		return false
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case []byte:
		bv, ok := b.([]byte)
		return ok && string(av) == string(bv)
	case *Map:
		bv, ok := b.(*Map)
		return ok && av == bv
	case []any:
		bv, ok := b.([]any)
		return ok && len(av) == 0 && len(bv) == 0
	case nil:
		return b == nil
	}
	return false
}

// numericValue converts Python-int-like values to float64 for key equality.
// Precision loss beyond 2^53 is irrelevant for the tiny integer keys used on
// the wire; a bool key participates as 1/0 exactly as in Python.
func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
