// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package cbor

import "testing"

// keys returns the map's keys in order as test-friendly strings.
func keys(m *Map) []any {
	out := []any{}
	for _, p := range m.Pairs() {
		out = append(out, p.Key)
	}
	return out
}

func TestSetAppendsNewKeys(t *testing.T) {
	t.Parallel()
	m := NewMap()
	m.Set(int64(0), int64(1))
	m.Set(int64(5), "room")
	m.Set(int64(6), []byte("body"))
	if got := keys(m); len(got) != 3 || got[0] != int64(0) || got[1] != int64(5) || got[2] != int64(6) {
		t.Fatalf("key order = %v, want [0 5 6]", got)
	}
	if m.Len() != 3 {
		t.Fatalf("Len = %v, want 3", m.Len())
	}
}

func TestSetUpdatesInPlacePreservingPosition(t *testing.T) {
	t.Parallel()
	m := NewMap()
	m.Set(int64(0), int64(1))
	m.Set(int64(4), []byte("old"))
	m.Set(int64(6), "body")
	m.Set(int64(4), []byte("new"))
	got := keys(m)
	if len(got) != 3 || got[1] != int64(4) {
		t.Fatalf("key order = %v, want position of 4 preserved at index 1", got)
	}
	v, ok := m.GetBytes(int64(4))
	if !ok || string(v) != "new" {
		t.Fatalf("GetBytes(4) = %v, %v; want new, true", v, ok)
	}
}

func TestDeletePreservesOrder(t *testing.T) {
	t.Parallel()
	m := NewMap()
	m.Set(int64(1), int64(10))
	m.Set(int64(2), int64(20))
	m.Set(int64(3), int64(30))
	m.Set(int64(4), int64(40))
	if !m.Delete(int64(2)) {
		t.Fatal("Delete(2) = false, want true")
	}
	if m.Delete(int64(99)) {
		t.Fatal("Delete(99) = true, want false")
	}
	got := keys(m)
	if len(got) != 3 || got[0] != int64(1) || got[1] != int64(3) || got[2] != int64(4) {
		t.Fatalf("key order after delete = %v, want [1 3 4]", got)
	}
}

func TestPopReturnsAndRemoves(t *testing.T) {
	t.Parallel()
	m := NewMap()
	m.Set(int64(5), "room")
	v, ok := m.Pop(int64(5))
	if !ok || v != "room" {
		t.Fatalf("Pop(5) = %v, %v; want room, true", v, ok)
	}
	if _, ok := m.Get(int64(5)); ok {
		t.Fatal("key 5 still present after Pop")
	}
	if _, ok := m.Pop(int64(5)); ok {
		t.Fatal("second Pop(5) succeeded, want false")
	}
}

func TestBoolKeyEqualsIntKey(t *testing.T) {
	t.Parallel()
	// Python: {True: 1} — a bool key is the same slot as the int key.
	m := NewMap()
	m.Set(true, int64(7))
	if v, ok := m.Get(int64(1)); !ok || v != int64(7) {
		t.Fatalf("Get(1) after Set(true, 7) = %v, %v; want 7, true", v, ok)
	}
	if m.Len() != 1 {
		t.Fatalf("Len = %v, want 1 (bool and int are one key)", m.Len())
	}
	// Updating via the int key keeps the original bool key representation.
	m.Set(int64(1), int64(8))
	got := keys(m)
	if len(got) != 1 || got[0] != true {
		t.Fatalf("keys after Set(1, 8) = %v, want [true] (original key kept)", got)
	}
	if v, _ := m.Get(int64(1)); v != int64(8) {
		t.Fatalf("value after Set(1, 8) = %v, want 8", v)
	}
}

func TestIntKeyMatchesBoolLookup(t *testing.T) {
	t.Parallel()
	m := NewMap()
	m.Set(int64(1), "one")
	if _, ok := m.Get(true); !ok {
		t.Fatal("Get(true) failed on int-keyed map; Python True == 1")
	}
	m.Set(false, "zero")
	if _, ok := m.Get(int64(0)); !ok {
		t.Fatal("Get(0) failed after Set(false, ...)")
	}
}

func TestGetIntTypedSemantics(t *testing.T) {
	t.Parallel()
	m := NewMap()
	m.Set(int64(0), true) // bool passes int checks as 1
	m.Set(int64(1), int64(5))
	m.Set(int64(2), 2.5) // float is not an integer
	if v, ok := m.GetInt(int64(0)); !ok || v != 1 {
		t.Fatalf("GetInt(0) = %v, %v; want 1, true", v, ok)
	}
	if v, ok := m.GetInt(int64(1)); !ok || v != 5 {
		t.Fatalf("GetInt(1) = %v, %v; want 5, true", v, ok)
	}
	if _, ok := m.GetInt(int64(2)); ok {
		t.Fatal("GetInt(2) succeeded on a float value; Python floats are not ints")
	}
	if _, ok := m.GetInt(int64(9)); ok {
		t.Fatal("GetInt on a missing key succeeded")
	}
}

func TestTypedSettersPreservePosition(t *testing.T) {
	t.Parallel()
	m := NewMap()
	m.SetInt(4, 1)
	m.SetString(6, "body")
	m.SetBytes(2, []byte("id"))
	m.SetInt(4, 99)
	m.SetString(6, "new-body")
	m.SetBytes(2, []byte("new-id"))
	got := keys(m)
	if len(got) != 3 || got[0] != int64(4) || got[1] != int64(6) || got[2] != int64(2) {
		t.Fatalf("key order = %v, want [4 6 2]", got)
	}
	if v, _ := m.GetInt(4); v != 99 {
		t.Fatalf("GetInt(4) = %v, want 99", v)
	}
	if v, _ := m.GetString(6); v != "new-body" {
		t.Fatalf("GetString(6) = %v, want new-body", v)
	}
	if v, _ := m.GetBytes(2); string(v) != "new-id" {
		t.Fatalf("GetBytes(2) = %v, want new-id", v)
	}
}

func TestGetMapReturnsNestedMap(t *testing.T) {
	t.Parallel()
	inner := NewMap()
	inner.SetInt(0, 1)
	m := NewMap()
	m.Set(int64(6), inner)
	got, ok := m.GetMap(int64(6))
	if !ok || got != inner {
		t.Fatalf("GetMap(6) = %v, %v; want the inner map", got, ok)
	}
	if _, ok := m.GetMap(int64(0)); ok {
		t.Fatal("GetMap(0) succeeded on a non-map entry")
	}
}

func TestPairsReturnsCopy(t *testing.T) {
	t.Parallel()
	m := NewMap()
	m.Set(int64(1), "a")
	pairs := m.Pairs()
	pairs[0].Val = "mutated"
	if v, _ := m.Get(int64(1)); v != "a" {
		t.Fatal("mutating Pairs() output changed the map")
	}
}

func TestNilMapSafe(t *testing.T) {
	t.Parallel()
	var m *Map
	if m.Len() != 0 {
		t.Fatal("nil map Len != 0")
	}
	if _, ok := m.Get(int64(0)); ok {
		t.Fatal("nil map Get succeeded")
	}
	m.Set(int64(0), int64(1)) // no-op
	if m.Has(int64(0)) {
		t.Fatal("nil map Has succeeded")
	}
}
