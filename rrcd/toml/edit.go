// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package toml

// TablePath returns the table at the dotted path, creating any missing
// tables along the way (tomlkit's get-or-create behavior). Created tables
// are synthetic; they render with tomlkit's blank-line separators and an
// empty synthetic parent with sub-tables is elided.
func (d *Doc) TablePath(path ...string) *Table {
	return tablePath(d.root, path)
}

func tablePath(root *Table, path []string) *Table {
	cur := root
	for _, seg := range path {
		var next *Table
		for _, sub := range cur.Tables {
			if len(sub.Path) > 0 && sub.Path[len(sub.Path)-1] == seg {
				next = sub
				break
			}
		}
		if next == nil {
			subPath := appendPath(cur.Path, seg)
			next = &Table{Path: subPath, Synthetic: true}
			cur.Tables = append(cur.Tables, next)
		}
		cur = next
	}
	return cur
}

func appendPath(path []string, seg string) []string {
	out := make([]string, len(path)+1)
	copy(out, path)
	out[len(path)] = seg
	return out
}

// Set stores value under key: an existing key is updated in place (keeping
// its position; the value re-renders in tomlkit's default style), a new key
// appends after the existing entries.
func (t *Table) Set(key string, val Value) {
	for i := range t.Keys {
		kv := &t.Keys[i]
		if !kv.IsRaw && kv.Key == key {
			kv.Value = val
			kv.Dirty = true
			return
		}
	}
	t.Keys = append(t.Keys, KeyVal{Key: key, Value: val, Dirty: true})
}

// Delete removes key and reports whether it was present.
func (t *Table) Delete(key string) bool {
	for i := range t.Keys {
		if !t.Keys[i].IsRaw && t.Keys[i].Key == key {
			t.Keys = append(t.Keys[:i], t.Keys[i+1:]...)
			return true
		}
	}
	return false
}

// Get returns the value stored under key.
func (t *Table) Get(key string) (Value, bool) {
	for i := range t.Keys {
		kv := &t.Keys[i]
		if !kv.IsRaw && kv.Key == key {
			return kv.Value, true
		}
	}
	return Value{}, false
}

// Has reports whether key is present.
func (t *Table) Has(key string) bool {
	_, ok := t.Get(key)
	return ok
}

// SetTable returns the sub-table under key, creating it when missing. Like
// assigning a dict in tomlkit, assigning over an existing value converts it
// to a sub-table and replaces the previous content.
func (t *Table) SetTable(key string) *Table {
	for _, sub := range t.Tables {
		if len(sub.Path) > 0 && sub.Path[len(sub.Path)-1] == key {
			// Replace the previous content entirely (tomlkit re-assign
			// semantics), keeping the table in place.
			sub.Keys = nil
			sub.Tables = nil
			return sub
		}
	}
	// An existing non-table value is replaced by the sub-table.
	for i := range t.Keys {
		kv := &t.Keys[i]
		if !kv.IsRaw && kv.Key == key {
			t.Keys = append(t.Keys[:i], t.Keys[i+1:]...)
			break
		}
	}
	sub := &Table{Path: appendPath(t.Path, key), Synthetic: true}
	t.Tables = append(t.Tables, sub)
	return sub
}

// DeleteTable removes the sub-table under key and reports whether it was
// present.
func (t *Table) DeleteTable(key string) bool {
	for i, sub := range t.Tables {
		if len(sub.Path) > 0 && sub.Path[len(sub.Path)-1] == key {
			t.Tables = append(t.Tables[:i], t.Tables[i+1:]...)
			return true
		}
	}
	return false
}

// StringValue builds a double-quoted string value for edits.
func StringValue(s string) Value { return Value{Kind: KindString, Str: s} }

// IntValue builds an integer value for edits.
func IntValue(n int64) Value { return Value{Kind: KindInt, Int: n} }

// FloatValue builds a float value for edits (rendered in Python repr form).
func FloatValue(f float64) Value { return Value{Kind: KindFloat, Flt: f} }

// BoolValue builds a boolean value for edits.
func BoolValue(b bool) Value { return Value{Kind: KindBool, Bool: b} }

// ArrayValue builds a value array for edits (rendered as ["a", "b"]).
func ArrayValue(items ...Value) Value {
	return Value{Kind: KindArray, Arr: items}
}

// StringArrayValue builds an array of double-quoted strings for edits.
func StringArrayValue(items []string) Value {
	arr := make([]Value, len(items))
	for i, s := range items {
		arr[i] = StringValue(s)
	}
	return ArrayValue(arr...)
}

// LookupTable returns the table at the dotted path without creating
// anything, nil when missing.
func (d *Doc) LookupTable(path ...string) *Table {
	cur := d.root
	for _, seg := range path {
		var next *Table
		for _, sub := range cur.Tables {
			if len(sub.Path) > 0 && sub.Path[len(sub.Path)-1] == seg {
				next = sub
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}
