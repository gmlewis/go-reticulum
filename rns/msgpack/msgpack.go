// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package msgpack provides a minimal MessagePack serializer and deserializer
// required by the Reticulum Network Stack.
//
// This implementation focuses on the subset of the MessagePack specification
// used by Reticulum for internal state persistence and communication protocols.
// It supports nil, booleans, integers, floats, strings, byte slices, arrays, and maps.
package msgpack

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"reflect"
)

// MessagePack format constants
const (
	// posFixIntMin = 0x00
	posFixIntMax = 0x7f
	fixMapMin    = 0x80
	fixMapMax    = 0x8f
	fixArrayMin  = 0x90
	fixArrayMax  = 0x9f
	fixStrMin    = 0xa0
	fixStrMax    = 0xbf
	nilVal       = 0xc0
	falseVal     = 0xc2
	trueVal      = 0xc3
	bin8         = 0xc4
	bin16        = 0xc5
	bin32        = 0xc6
	// ext8         = 0xc7
	// ext16        = 0xc8
	// ext32        = 0xc9
	float32Val = 0xca
	float64Val = 0xcb
	uint8Val   = 0xcc
	uint16Val  = 0xcd
	uint32Val  = 0xce
	uint64Val  = 0xcf
	int8Val    = 0xd0
	int16Val   = 0xd1
	int32Val   = 0xd2
	int64Val   = 0xd3
	// fixExt1      = 0xd4
	// fixExt2      = 0xd5
	// fixExt4      = 0xd6
	// fixExt8      = 0xd7
	// fixExt16     = 0xd8
	str8         = 0xd9
	str16        = 0xda
	str32        = 0xdb
	array16      = 0xdc
	array32      = 0xdd
	map16        = 0xde
	map32        = 0xdf
	negFixIntMin = 0xe0
	// negFixIntMax = 0xff
)

// Pack serializes the provided Go data structure into a compact MessagePack byte slice.
// It uses reflection to dynamically determine the most efficient MessagePack encoding format for the given value, ensuring optimized data payloads for Reticulum network transmissions.
func Pack(v any) ([]byte, error) {
	var buf bytes.Buffer
	err := pack(&buf, reflect.ValueOf(v))
	return buf.Bytes(), err
}

func pack(w io.Writer, v reflect.Value) error {
	if !v.IsValid() {
		_, err := w.Write([]byte{nilVal})
		return err
	}

	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			_, err := w.Write([]byte{nilVal})
			return err
		}
		return pack(w, v.Elem())
	case reflect.Bool:
		if v.Bool() {
			_, err := w.Write([]byte{trueVal})
			return err
		}
		_, err := w.Write([]byte{falseVal})
		return err
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return packInt(w, v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return packUint(w, v.Uint())
	case reflect.Float32:
		_, err := w.Write([]byte{float32Val})
		if err != nil {
			return err
		}
		return binary.Write(w, binary.BigEndian, float32(v.Float()))
	case reflect.Float64:
		_, err := w.Write([]byte{float64Val})
		if err != nil {
			return err
		}
		return binary.Write(w, binary.BigEndian, v.Float())
	case reflect.String:
		s := v.String()
		return packStr(w, s)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return packBin(w, v.Bytes())
		}
		return packArray(w, v)
	case reflect.Array:
		return packArray(w, v)
	case reflect.Map:
		return packMap(w, v)
	default:
		return fmt.Errorf("unsupported type: %v", v.Kind())
	}
}

func packInt(w io.Writer, i int64) error {
	if i >= -32 && i <= 127 {
		_, err := w.Write([]byte{byte(i)})
		return err
	}
	if i >= math.MinInt8 && i <= math.MaxInt8 {
		_, err := w.Write([]byte{int8Val, byte(i)})
		return err
	}
	if i >= math.MinInt16 && i <= math.MaxInt16 {
		_, err := w.Write([]byte{int16Val})
		if err != nil {
			return err
		}
		return binary.Write(w, binary.BigEndian, int16(i))
	}
	if i >= math.MinInt32 && i <= math.MaxInt32 {
		_, err := w.Write([]byte{int32Val})
		if err != nil {
			return err
		}
		return binary.Write(w, binary.BigEndian, int32(i))
	}
	_, err := w.Write([]byte{int64Val})
	if err != nil {
		return err
	}
	return binary.Write(w, binary.BigEndian, i)
}

func packUint(w io.Writer, i uint64) error {
	if i <= 127 {
		_, err := w.Write([]byte{byte(i)})
		return err
	}
	if i <= math.MaxUint8 {
		_, err := w.Write([]byte{uint8Val, byte(i)})
		return err
	}
	if i <= math.MaxUint16 {
		_, err := w.Write([]byte{uint16Val})
		if err != nil {
			return err
		}
		return binary.Write(w, binary.BigEndian, uint16(i))
	}
	if i <= math.MaxUint32 {
		_, err := w.Write([]byte{uint32Val})
		if err != nil {
			return err
		}
		return binary.Write(w, binary.BigEndian, uint32(i))
	}
	_, err := w.Write([]byte{uint64Val})
	if err != nil {
		return err
	}
	return binary.Write(w, binary.BigEndian, i)
}

func packStr(w io.Writer, s string) error {
	l := len(s)
	if l < 32 {
		_, err := w.Write([]byte{fixStrMin | byte(l)})
		if err != nil {
			return err
		}
	} else if l < 256 {
		_, err := w.Write([]byte{str8, byte(l)})
		if err != nil {
			return err
		}
	} else if l < 65536 {
		_, err := w.Write([]byte{str16})
		if err != nil {
			return err
		}
		err = binary.Write(w, binary.BigEndian, uint16(l))
		if err != nil {
			return err
		}
	} else {
		_, err := w.Write([]byte{str32})
		if err != nil {
			return err
		}
		err = binary.Write(w, binary.BigEndian, uint32(l))
		if err != nil {
			return err
		}
	}
	_, err := w.Write([]byte(s))
	return err
}

func packBin(w io.Writer, b []byte) error {
	l := len(b)
	if l < 256 {
		_, err := w.Write([]byte{bin8, byte(l)})
		if err != nil {
			return err
		}
	} else if l < 65536 {
		_, err := w.Write([]byte{bin16})
		if err != nil {
			return err
		}
		err = binary.Write(w, binary.BigEndian, uint16(l))
		if err != nil {
			return err
		}
	} else {
		_, err := w.Write([]byte{bin32})
		if err != nil {
			return err
		}
		err = binary.Write(w, binary.BigEndian, uint32(l))
		if err != nil {
			return err
		}
	}
	_, err := w.Write(b)
	return err
}

func packArray(w io.Writer, v reflect.Value) error {
	l := v.Len()
	if l < 16 {
		_, err := w.Write([]byte{fixArrayMin | byte(l)})
		if err != nil {
			return err
		}
	} else if l < 65536 {
		_, err := w.Write([]byte{array16})
		if err != nil {
			return err
		}
		err = binary.Write(w, binary.BigEndian, uint16(l))
		if err != nil {
			return err
		}
	} else {
		_, err := w.Write([]byte{array32})
		if err != nil {
			return err
		}
		err = binary.Write(w, binary.BigEndian, uint32(l))
		if err != nil {
			return err
		}
	}
	for i := 0; i < l; i++ {
		if err := pack(w, v.Index(i)); err != nil {
			return err
		}
	}
	return nil
}

func packMap(w io.Writer, v reflect.Value) error {
	l := v.Len()
	if l < 16 {
		_, err := w.Write([]byte{fixMapMin | byte(l)})
		if err != nil {
			return err
		}
	} else if l < 65536 {
		_, err := w.Write([]byte{map16})
		if err != nil {
			return err
		}
		err = binary.Write(w, binary.BigEndian, uint16(l))
		if err != nil {
			return err
		}
	} else {
		_, err := w.Write([]byte{map32})
		if err != nil {
			return err
		}
		err = binary.Write(w, binary.BigEndian, uint32(l))
		if err != nil {
			return err
		}
	}
	keys := v.MapKeys()
	for _, k := range keys {
		if err := pack(w, k); err != nil {
			return err
		}
		if err := pack(w, v.MapIndex(k)); err != nil {
			return err
		}
	}
	return nil
}

// Unpack deserializes a MessagePack encoded byte slice back into a native Go data structure.
// It analyzes the byte stream to infer the correct types, recursively reconstructing complex elements such as maps and arrays, and returns an any interface that the caller can safely type-assert.
func Unpack(data []byte) (any, error) {
	r := bytes.NewReader(data)
	return unpack(r)
}

func unpack(r *bytes.Reader) (any, error) {
	b, err := r.ReadByte()
	if err != nil {
		return nil, err
	}

	switch {
	case b <= posFixIntMax:
		return int64(b), nil
	case b >= fixMapMin && b <= fixMapMax:
		return unpackMap(r, int(b&0x0f))
	case b >= fixArrayMin && b <= fixArrayMax:
		return unpackArray(r, int(b&0x0f))
	case b >= fixStrMin && b <= fixStrMax:
		return unpackStr(r, int(b&0x1f))
	case b == nilVal:
		return nil, nil
	case b == falseVal:
		return false, nil
	case b == trueVal:
		return true, nil
	case b == bin8:
		l, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		return unpackBin(r, int(l))
	case b == bin16:
		var l uint16
		if err := binary.Read(r, binary.BigEndian, &l); err != nil {
			return nil, err
		}
		return unpackBin(r, int(l))
	case b == bin32:
		var l uint32
		if err := binary.Read(r, binary.BigEndian, &l); err != nil {
			return nil, err
		}
		return unpackBin(r, int(l))
	case b == float32Val:
		var f float32
		if err := binary.Read(r, binary.BigEndian, &f); err != nil {
			return nil, err
		}
		return f, nil
	case b == float64Val:
		var f float64
		if err := binary.Read(r, binary.BigEndian, &f); err != nil {
			return nil, err
		}
		return f, nil
	case b == uint8Val:
		v, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		return int64(v), nil
	case b == uint16Val:
		var v uint16
		if err := binary.Read(r, binary.BigEndian, &v); err != nil {
			return nil, err
		}
		return int64(v), nil
	case b == uint32Val:
		var v uint32
		if err := binary.Read(r, binary.BigEndian, &v); err != nil {
			return nil, err
		}
		return int64(v), nil
	case b == uint64Val:
		var v uint64
		if err := binary.Read(r, binary.BigEndian, &v); err != nil {
			return nil, err
		}
		if v <= math.MaxInt64 {
			return int64(v), nil
		}
		return v, nil
	case b == int8Val:
		v, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		return int64(int8(v)), nil
	case b == int16Val:
		var v int16
		if err := binary.Read(r, binary.BigEndian, &v); err != nil {
			return nil, err
		}
		return int64(v), nil
	case b == int32Val:
		var v int32
		if err := binary.Read(r, binary.BigEndian, &v); err != nil {
			return nil, err
		}
		return int64(v), nil
	case b == int64Val:
		var v int64
		if err := binary.Read(r, binary.BigEndian, &v); err != nil {
			return nil, err
		}
		return v, nil
	case b == str8:
		l, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		return unpackStr(r, int(l))
	case b == str16:
		var l uint16
		if err := binary.Read(r, binary.BigEndian, &l); err != nil {
			return nil, err
		}
		return unpackStr(r, int(l))
	case b == str32:
		var l uint32
		if err := binary.Read(r, binary.BigEndian, &l); err != nil {
			return nil, err
		}
		return unpackStr(r, int(l))
	case b == array16:
		var l uint16
		if err := binary.Read(r, binary.BigEndian, &l); err != nil {
			return nil, err
		}
		return unpackArray(r, int(l))
	case b == array32:
		var l uint32
		if err := binary.Read(r, binary.BigEndian, &l); err != nil {
			return nil, err
		}
		return unpackArray(r, int(l))
	case b == map16:
		var l uint16
		if err := binary.Read(r, binary.BigEndian, &l); err != nil {
			return nil, err
		}
		return unpackMap(r, int(l))
	case b == map32:
		var l uint32
		if err := binary.Read(r, binary.BigEndian, &l); err != nil {
			return nil, err
		}
		return unpackMap(r, int(l))
	case b >= negFixIntMin:
		return int64(int8(b)), nil
	default:
		return nil, fmt.Errorf("unknown type: 0x%02x", b)
	}
}

func unpackStr(r *bytes.Reader, l int) (string, error) {
	if l == 0 {
		return "", nil
	}
	b := make([]byte, l)
	_, err := io.ReadFull(r, b)
	return string(b), err
}

func unpackBin(r *bytes.Reader, l int) ([]byte, error) {
	if l == 0 {
		return []byte{}, nil
	}
	b := make([]byte, l)
	_, err := io.ReadFull(r, b)
	return b, err
}

func unpackArray(r *bytes.Reader, l int) ([]any, error) {
	a := make([]any, l)
	for i := 0; i < l; i++ {
		v, err := unpack(r)
		if err != nil {
			return nil, err
		}
		a[i] = v
	}
	return a, nil
}

func unpackMap(r *bytes.Reader, l int) (map[any]any, error) {
	m := make(map[any]any, l)
	for i := 0; i < l; i++ {
		k, err := unpack(r)
		if err != nil {
			return nil, err
		}
		// []byte is not hashable in Go; convert to string for use as map key.
		if b, ok := k.([]byte); ok {
			k = string(b)
		}
		v, err := unpack(r)
		if err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, nil
}
