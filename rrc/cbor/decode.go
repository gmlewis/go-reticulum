// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package cbor

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"
)

// maxDecodeDepth bounds recursion so adversarial nesting fails instead of
// exhausting the stack; Python's decoder fails similarly via its recursion
// limit, and both failures surface as a decode error to the caller.
const maxDecodeDepth = 64

// errTruncated reports input that ends before a complete value; the message
// mirrors Python cbor2's premature-end-of-stream failure class.
var errTruncated = errors.New("cbor: premature end of stream")

// errIndefinite reports an indefinite-length head where a definite
// length is required.
var errIndefinite = errors.New("cbor: indefinite length")

// Decode decodes exactly one CBOR value and ignores any trailing bytes,
// matching Python's cbor2.loads. Maps decode into *Map (insertion order
// preserved, bool keys equal to the matching int as in Python). Indefinite
// lengths are accepted for tolerance; tags are skipped and the tagged value
// is returned; 16/32/64-bit floats widen to float64. Empty input is an
// error.
func Decode(data []byte) (any, error) {
	if len(data) == 0 {
		return nil, errTruncated
	}
	p := &parser{src: &bytesSource{data: data}}
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Decoder reads and decodes CBOR values sequentially from an input stream.
type Decoder struct {
	br  *bufio.Reader
	src *streamSource
}

// NewDecoder returns a new Decoder that reads from r.
func NewDecoder(r io.Reader) *Decoder {
	var br *bufio.Reader
	if b, ok := r.(*bufio.Reader); ok {
		br = b
	} else {
		br = bufio.NewReader(r)
	}
	return &Decoder{
		br:  br,
		src: &streamSource{br: br},
	}
}

// Decode reads the next CBOR value from its input stream.
// It returns nil, io.EOF if the input stream contains no more values.
func (d *Decoder) Decode() (any, error) {
	_, err := d.br.Peek(1)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, err
	}
	p := &parser{src: d.src}
	return p.value(0)
}

type byteSource interface {
	byteAt() (byte, error)
	take(n int) ([]byte, error)
	peekByte() (byte, error)
	remaining() int
}

type bytesSource struct {
	data []byte
	off  int
}

func (s *bytesSource) byteAt() (byte, error) {
	if s.off >= len(s.data) {
		return 0, errTruncated
	}
	b := s.data[s.off]
	s.off++
	return b, nil
}

func (s *bytesSource) take(n int) ([]byte, error) {
	if n < 0 || s.off+n > len(s.data) || s.off+n < s.off {
		return nil, errTruncated
	}
	out := s.data[s.off : s.off+n]
	s.off += n
	return out, nil
}

func (s *bytesSource) peekByte() (byte, error) {
	if s.off >= len(s.data) {
		return 0, errTruncated
	}
	return s.data[s.off], nil
}

func (s *bytesSource) remaining() int {
	return len(s.data) - s.off
}

type streamSource struct {
	br *bufio.Reader
}

func (s *streamSource) byteAt() (byte, error) {
	b, err := s.br.ReadByte()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return 0, errTruncated
		}
		return 0, err
	}
	return b, nil
}

func (s *streamSource) take(n int) ([]byte, error) {
	if n < 0 {
		return nil, errTruncated
	}
	buf := make([]byte, n)
	_, err := io.ReadFull(s.br, buf)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, errTruncated
		}
		return nil, err
	}
	return buf, nil
}

func (s *streamSource) peekByte() (byte, error) {
	b, err := s.br.Peek(1)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return 0, errTruncated
		}
		return 0, err
	}
	return b[0], nil
}

func (s *streamSource) remaining() int {
	return -1
}

type parser struct {
	src byteSource
}

func (p *parser) byteAt() (byte, error) {
	return p.src.byteAt()
}

func (p *parser) take(n int) ([]byte, error) {
	return p.src.take(n)
}

func (p *parser) value(depth int) (any, error) {
	if depth > maxDecodeDepth {
		return nil, fmt.Errorf("cbor: nesting too deep (limit %d)", maxDecodeDepth)
	}
	b, err := p.byteAt()
	if err != nil {
		return nil, err
	}
	major := b >> 5
	info := b & 0x1f

	switch major {
	case 0: // unsigned integer
		arg, err := p.argOfDirect(info)
		if err != nil {
			return nil, err
		}
		return uintValue(arg), nil
	case 1: // negative integer: value = -1 - arg
		arg, err := p.argOfDirect(info)
		if err != nil {
			return nil, err
		}
		if arg >= 1<<63 {
			// Python handles arbitrary precision; such magnitudes never
			// occur in valid RRC traffic.
			return nil, fmt.Errorf("cbor: negative integer overflow")
		}
		return -1 - int64(arg), nil
	case 2, 3: // byte / text string
		return p.stringOf(major, info)
	case 4: // array
		return p.arrayOf(info, depth)
	case 5: // map
		return p.mapOf(info, depth)
	case 6: // tag: bignum tags decode as integers, the rest pass through
		if info == 31 {
			return nil, fmt.Errorf("cbor: indefinite tags are not supported")
		}
		tagNum, err := p.argOfDirect(info)
		if err != nil {
			return nil, err
		}
		inner, err := p.value(depth)
		if err != nil {
			return nil, err
		}
		if (tagNum == 2 || tagNum == 3) && inner != nil {
			if payload, ok := inner.([]byte); ok {
				return BigUint{
					Negative:  tagNum == 3,
					Magnitude: payload,
				}, nil
			}
		}
		return inner, nil
	case 7:
		return p.simpleOf(info)
	}
	return nil, fmt.Errorf("cbor: unsupported major type %d", major)
}

// argOfDirect reads a definite head argument without consuming anything for
// inline values (info <= 23); the head byte was already consumed by value().
func (p *parser) argOfDirect(info byte) (uint64, error) {
	switch {
	case info <= 23:
		return uint64(info), nil
	case info == 24:
		v, err := p.take(1)
		if err != nil {
			return 0, err
		}
		return uint64(v[0]), nil
	case info == 25:
		v, err := p.take(2)
		if err != nil {
			return 0, err
		}
		return uint64(v[0])<<8 | uint64(v[1]), nil
	case info == 26:
		v, err := p.take(4)
		if err != nil {
			return 0, err
		}
		return uint64(v[0])<<24 | uint64(v[1])<<16 |
			uint64(v[2])<<8 | uint64(v[3]), nil
	case info == 27:
		v, err := p.take(8)
		if err != nil {
			return 0, err
		}
		var arg uint64
		for _, c := range v {
			arg = arg<<8 | uint64(c)
		}
		return arg, nil
	case info == 31:
		return 0, errIndefinite
	default:
		return 0, fmt.Errorf("cbor: reserved additional info %d", info)
	}
}

func uintValue(v uint64) any {
	if v <= 1<<63-1 {
		return int64(v)
	}
	return v
}

func (p *parser) stringOf(major, info byte) (any, error) {
	if info == 31 {
		// Indefinite: concatenate definite chunks until the break.
		var chunks []byte
		for {
			b, err := p.byteAt()
			if err != nil {
				return nil, err
			}
			if b == 0xff {
				if major == 2 {
					return chunks, nil
				}
				return string(chunks), nil
			}
			if b>>5 != major {
				return nil, fmt.Errorf("cbor: mismatched indefinite chunk type")
			}
			n, err := p.argOfDirect(b & 0x1f)
			if err != nil {
				return nil, err
			}
			c, err := p.take(int(n))
			if err != nil {
				return nil, err
			}
			chunks = append(chunks, c...)
		}
	}
	n, err := p.argOfDirect(info)
	if err != nil {
		return nil, err
	}
	c, err := p.take(int(n))
	if err != nil {
		return nil, err
	}
	if major == 2 {
		return append([]byte(nil), c...), nil
	}
	// Python's cbor2 rejects text strings that are not valid UTF-8
	// (UnicodeDecodeError wrapped in CBORDecodeError, "error decoding
	// text string"); accepting them would re-broadcast text the client
	// decoders drop.
	if !utf8.Valid(c) {
		return nil, errors.New("cbor: error decoding text string")
	}
	return string(c), nil
}

func (p *parser) arrayOf(info byte, depth int) (any, error) {
	var items []any
	if info == 31 {
		for {
			b, err := p.src.peekByte()
			if err != nil {
				return nil, err
			}
			if b == 0xff {
				_, _ = p.src.byteAt()
				return items, nil
			}
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			items = append(items, v)
		}
	}
	n, err := p.argOfDirect(info)
	if err != nil {
		return nil, err
	}
	rem := p.src.remaining()
	if rem >= 0 && n > uint64(rem) {
		return nil, errTruncated
	}
	if n > 1<<28 {
		return nil, errTruncated
	}
	items = make([]any, n)
	for i := range items {
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		items[i] = v
	}
	return items, nil
}

func (p *parser) mapOf(info byte, depth int) (any, error) {
	m := NewMap()
	if info == 31 {
		for {
			b, err := p.src.peekByte()
			if err != nil {
				return nil, err
			}
			if b == 0xff {
				_, _ = p.src.byteAt()
				return m, nil
			}
			key, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			val, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			m.Set(key, val)
		}
	}
	n, err := p.argOfDirect(info)
	if err != nil {
		return nil, err
	}
	rem := p.src.remaining()
	if rem >= 0 && n > uint64(rem) {
		return nil, errTruncated
	}
	if n > 1<<28 {
		return nil, errTruncated
	}
	for range n {
		key, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		val, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		m.Set(key, val)
	}
	return m, nil
}

// simpleOf decodes the simple-value major type. Documented divergence:
// Python's cbor2 yields its `undefined` sentinel for 0xf7 and a
// CBORSimpleValue for assigned simple values (the two-byte form only for
// values >= 32), while this decoder rejects every simple value other
// than null, true, and false — RRC bodies are never simple values, so a
// hostile envelope answers "bad message" instead of echoing the exotic
// value.
func (p *parser) simpleOf(info byte) (any, error) {
	switch {
	case info == 20:
		return false, nil
	case info == 21:
		return true, nil
	case info == 22:
		return nil, nil
	case info <= 23:
		return nil, fmt.Errorf("cbor: unassigned simple value %d", info)
	case info == 24:
		v, err := p.take(1)
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("cbor: unassigned simple value %d", v[0])
	case info == 25:
		b, err := p.take(2)
		if err != nil {
			return nil, err
		}
		return float64(halfFloat(b[0], b[1])), nil
	case info == 26:
		b, err := p.take(4)
		if err != nil {
			return nil, err
		}
		bits := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		return float64(math.Float32frombits(bits)), nil
	case info == 27:
		b, err := p.take(8)
		if err != nil {
			return nil, err
		}
		var bits uint64
		for _, c := range b {
			bits = bits<<8 | uint64(c)
		}
		return math.Float64frombits(bits), nil
	case info == 31:
		// A top-level break has no value to return; Python yields an
		// internal sentinel here, which callers can never use as a
		// message. Report it as malformed input.
		return nil, fmt.Errorf("cbor: unexpected break code")
	default:
		return nil, fmt.Errorf("cbor: unsupported float/simple form %d", info)
	}
}

// halfFloat widens an IEEE 754 binary16 value to float64.
func halfFloat(hi, lo byte) float64 {
	bits := uint16(hi)<<8 | uint16(lo)
	sign := uint64(bits&0x8000) << 48
	exp := int(bits>>10) & 0x1f
	frac := uint64(bits & 0x03ff)
	var out uint64
	switch exp {
	case 0:
		if frac == 0 {
			out = sign // zero
		} else {
			// Subnormal: value = frac * 2^-24; normalize to 1.m * 2^e.
			bits := 0
			for f := frac; f != 0; f >>= 1 {
				bits++
			}
			biased := uint64(998 + bits)
			mant := uint64(frac-(1<<(bits-1))) << (11 - bits)
			out = sign | biased<<52 | mant
		}
	case 0x1f:
		out = sign | 0x7ff<<52 | frac<<42 // inf / nan
	default:
		out = sign | uint64(exp-15+1023)<<52 | frac<<42
	}
	return math.Float64frombits(out)
}
