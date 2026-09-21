// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package entropy

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// fakeSource is a Source whose output is scripted by a function, so a test can
// present the gate with dead silicon, a failing peripheral, or a working
// generator without any hardware.
type fakeSource struct {
	name string
	read func(b []byte) (int, error)
}

func (f fakeSource) Name() string               { return f.name }
func (f fakeSource) Read(b []byte) (int, error) { return f.read(b) }

// rampSource yields every byte value in turn. It is balanced, so it passes the
// health tests, and deterministic, so its results never move between runs.
func rampSource(name string) Source {
	var next byte
	return fakeSource{name: name, read: func(b []byte) (int, error) {
		for i := range b {
			b[i] = next
			next++
		}
		return len(b), nil
	}}
}

// constantSource is a generator whose register has stopped moving.
func constantSource(name string, value byte) Source {
	return fakeSource{name: name, read: func(b []byte) (int, error) {
		for i := range b {
			b[i] = value
		}
		return len(b), nil
	}}
}

// brokenSource is a peripheral that answers with an error.
func brokenSource(name string, err error) Source {
	return fakeSource{name: name, read: func(b []byte) (int, error) {
		return 0, err
	}}
}

func TestReaderProducesOutput(t *testing.T) {
	t.Parallel()

	r, err := NewReader(rampSource("test-ramp"), Options{Salt: []byte("device-0001")})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}

	out := make([]byte, 64)
	n, err := r.Read(out)
	if err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if n != len(out) {
		t.Fatalf("Read() = %v bytes, want %v", n, len(out))
	}

	stats := r.Stats()
	if !stats.Seeded {
		t.Error("Stats().Seeded = false after a successful read")
	}
	if stats.CreditedBits < DefaultMinEntropyBits {
		t.Errorf("Stats().CreditedBits = %v, want at least %v", stats.CreditedBits, DefaultMinEntropyBits)
	}
	if !stats.Healthy {
		t.Error("Stats().Healthy = false for a balanced source")
	}
	if stats.SourceName != "test-ramp" {
		t.Errorf("Stats().SourceName = %v, want test-ramp", stats.SourceName)
	}
}

// TestReaderConditionsTheSource checks that the gate is not a passthrough: the
// bytes a caller receives must not be the bytes the register produced.
func TestReaderConditionsTheSource(t *testing.T) {
	t.Parallel()

	r, err := NewReader(rampSource("test-ramp"), Options{})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}
	out := make([]byte, 64)
	if _, err := r.Read(out); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	raw := make([]byte, 64)
	for i := range raw {
		raw[i] = byte(i)
	}
	if bytes.Equal(out, raw) {
		t.Error("the gate returned the raw source samples unchanged")
	}
}

// TestReaderFailsClosedOnStuckSource is the test the package exists for. A
// generator stuck at one value must produce an error, must not produce bytes,
// and must not be quietly replaced by anything.
func TestReaderFailsClosedOnStuckSource(t *testing.T) {
	t.Parallel()

	for _, value := range []byte{0x00, 0xFF} {
		t.Run(fmt.Sprintf("%#x", value), func(t *testing.T) {
			t.Parallel()

			r, err := NewReader(constantSource("stuck-register", value), Options{Salt: []byte("device-0001")})
			if err != nil {
				t.Fatalf("NewReader() = %v", err)
			}

			out := make([]byte, 64)
			n, err := r.Read(out)
			if !errors.Is(err, ErrHealthTestFailed) {
				t.Fatalf("Read() error = %v, want ErrHealthTestFailed", err)
			}
			if n != 0 {
				t.Errorf("Read() = %v bytes, want 0", n)
			}
			if !bytes.Equal(out, make([]byte, len(out))) {
				t.Errorf("Read() wrote %#x over the buffer while failing", out)
			}
			if stats := r.Stats(); stats.Seeded {
				t.Error("Stats().Seeded = true after a health-test failure")
			}
			if stats := r.Stats(); stats.Healthy {
				t.Error("Stats().Healthy = true after a health-test failure")
			}

			// A second attempt must fail the same way: a device that cannot get
			// entropy does not get one lucky retry that produces a key.
			if _, err := r.Read(make([]byte, 64)); !errors.Is(err, ErrHealthTestFailed) {
				t.Errorf("second Read() error = %v, want ErrHealthTestFailed", err)
			}
		})
	}
}

func TestReaderFailsClosedOnSourceError(t *testing.T) {
	t.Parallel()

	sourceErr := errors.New("peripheral not responding")
	r, err := NewReader(brokenSource("dead-peripheral", sourceErr), Options{})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}

	if _, err := r.Read(make([]byte, 32)); !errors.Is(err, sourceErr) {
		t.Fatalf("Read() error = %v, want the source error", err)
	}
	if stats := r.Stats(); stats.Seeded {
		t.Error("Stats().Seeded = true after a source error")
	}
}

func TestReaderFailsClosedOnTwoValuedSource(t *testing.T) {
	t.Parallel()

	var flip bool
	source := fakeSource{name: "stuck-pair", read: func(b []byte) (int, error) {
		for i := range b {
			if flip {
				b[i] = 0xF0
			} else {
				b[i] = 0x0F
			}
			flip = !flip
		}
		return len(b), nil
	}}

	r, err := NewReader(source, Options{})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}
	if _, err := r.Read(make([]byte, 32)); !errors.Is(err, ErrHealthTestFailed) {
		t.Fatalf("Read() error = %v, want ErrHealthTestFailed", err)
	}
}

func TestReaderRejectsNilSource(t *testing.T) {
	t.Parallel()

	if _, err := NewReader(nil, Options{}); !errors.Is(err, ErrNoSource) {
		t.Errorf("NewReader(nil) error = %v, want ErrNoSource", err)
	}
}

func TestReaderRejectsImpossibleOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts Options
	}{
		{name: "negative floor", opts: Options{MinEntropyBits: -1}},
		{name: "negative credit rate", opts: Options{MinEntropyBitsPerByte: -1}},
		{name: "impossible health test", opts: Options{Health: Health{Alpha: 2}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewReader(rampSource("test-ramp"), tt.opts); !errors.Is(err, ErrBadParameter) {
				t.Errorf("NewReader() error = %v, want ErrBadParameter", err)
			}
		})
	}
}

// TestReaderUnreachableFloor checks that a source which is healthy but too slow
// to satisfy the caller's floor is reported as such, instead of spinning.
func TestReaderUnreachableFloor(t *testing.T) {
	t.Parallel()

	r, err := NewReader(rampSource("slow-ramp"), Options{MinEntropyBits: 1 << 20, MaxRounds: 2})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}
	if _, err := r.Read(make([]byte, 32)); !errors.Is(err, ErrInsufficientEntropy) {
		t.Errorf("Read() error = %v, want ErrInsufficientEntropy", err)
	}
}

// TestReaderRecoversAfterSourceFailure checks the other side of failing closed:
// once the generator works, the gate works. A device whose first collection
// attempt failed should not need a reboot to get an identity.
func TestReaderRecoversAfterSourceFailure(t *testing.T) {
	t.Parallel()

	broken := errors.New("warming up")
	remaining := 2
	source := fakeSource{name: "slow-start", read: func(b []byte) (int, error) {
		if remaining > 0 {
			remaining--
			return 0, broken
		}
		for i := range b {
			b[i] = byte(i)
		}
		return len(b), nil
	}}

	r, err := NewReader(source, Options{})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}
	if _, err := r.Read(make([]byte, 32)); !errors.Is(err, broken) {
		t.Fatalf("first Read() error = %v, want the source error", err)
	}
	if _, err := r.Read(make([]byte, 32)); !errors.Is(err, broken) {
		t.Fatalf("second Read() error = %v, want the source error", err)
	}
	if n, err := r.Read(make([]byte, 32)); err != nil || n != 32 {
		t.Fatalf("Read() after the source recovered = (%v, %v), want (32, nil)", n, err)
	}
}

// TestReaderSaltSeparatesStreams is the fleet-collision property end to end: two
// devices whose generators emit the same bytes must still come up with different
// key material.
func TestReaderSaltSeparatesStreams(t *testing.T) {
	t.Parallel()

	first, err := NewReader(rampSource("same-silicon"), Options{Salt: []byte("device-0001")})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}
	second, err := NewReader(rampSource("same-silicon"), Options{Salt: []byte("device-0002")})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}

	a := make([]byte, 64)
	b := make([]byte, 64)
	if _, err := first.Read(a); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if _, err := second.Read(b); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if bytes.Equal(a, b) {
		t.Error("two devices with identical generators and different identifiers produced the same key material")
	}
}

func TestReaderSaltIsCopied(t *testing.T) {
	t.Parallel()

	salt := []byte("device-0001")
	r, err := NewReader(rampSource("test-ramp"), Options{Salt: salt})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}
	copy(salt, []byte("device-9999"))

	before := make([]byte, 32)
	if _, err := r.Read(before); err != nil {
		t.Fatalf("Read() = %v", err)
	}

	fresh, err := NewReader(rampSource("test-ramp"), Options{Salt: []byte("device-0001")})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}
	want := make([]byte, 32)
	if _, err := fresh.Read(want); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if !bytes.Equal(before, want) {
		t.Error("the gate kept a reference to the caller's salt buffer")
	}
}

func TestReaderSeedIsExplicitAndIdempotent(t *testing.T) {
	t.Parallel()

	r, err := NewReader(rampSource("test-ramp"), Options{})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}
	if err := r.Seed(); err != nil {
		t.Fatalf("Seed() = %v", err)
	}
	seeded := r.Stats()
	if !seeded.Seeded {
		t.Fatal("Stats().Seeded = false after Seed()")
	}
	if err := r.Seed(); err != nil {
		t.Fatalf("second Seed() = %v", err)
	}
	if again := r.Stats(); again.CreditedBits != seeded.CreditedBits || again.Rounds != seeded.Rounds {
		t.Errorf("a second Seed() collected again: %+v, want %+v", again, seeded)
	}
	if _, err := r.Read(make([]byte, 32)); err != nil {
		t.Fatalf("Read() after Seed() = %v", err)
	}
}

func TestReaderReseedChangesStream(t *testing.T) {
	t.Parallel()

	r, err := NewReader(rampSource("test-ramp"), Options{})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}
	before := make([]byte, 64)
	if _, err := r.Read(before); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if err := r.Reseed(); err != nil {
		t.Fatalf("Reseed() = %v", err)
	}
	after := make([]byte, 64)
	if _, err := r.Read(after); err != nil {
		t.Fatalf("Read() = %v", err)
	}
	if bytes.Equal(before, after) {
		t.Error("the stream did not change after a reseed")
	}
}

// TestReaderReseedFailureKeepsWorkingGenerator checks that a transient fault
// cannot take a working device offline: a failed reseed reports the error and
// leaves the existing stream intact.
func TestReaderReseedFailureKeepsWorkingGenerator(t *testing.T) {
	t.Parallel()

	failing := false
	source := fakeSource{name: "flaky", read: func(b []byte) (int, error) {
		if failing {
			return 0, errors.New("transient fault")
		}
		for i := range b {
			b[i] = byte(i)
		}
		return len(b), nil
	}}

	r, err := NewReader(source, Options{})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}
	if err := r.Seed(); err != nil {
		t.Fatalf("Seed() = %v", err)
	}

	failing = true
	if err := r.Reseed(); err == nil {
		t.Fatal("Reseed() = nil, want the source error")
	}
	if _, err := r.Read(make([]byte, 32)); err != nil {
		t.Errorf("Read() after a failed reseed = %v, want nil", err)
	}
}

func TestReaderConcurrentReadsAreDistinct(t *testing.T) {
	t.Parallel()

	r, err := NewReader(rampSource("test-ramp"), Options{Salt: []byte("device-0001")})
	if err != nil {
		t.Fatalf("NewReader() = %v", err)
	}

	const readers = 16
	results := make([][]byte, readers)
	errs := make([]error, readers)
	done := make(chan int, readers)
	for i := range readers {
		go func(i int) {
			buf := make([]byte, 32)
			_, errs[i] = r.Read(buf)
			results[i] = buf
			done <- i
		}(i)
	}
	for range readers {
		<-done
	}

	seen := make(map[string]int, readers)
	for i, buf := range results {
		if errs[i] != nil {
			t.Fatalf("concurrent Read() = %v", errs[i])
		}
		if prev, dup := seen[string(buf)]; dup {
			t.Fatalf("readers %v and %v received identical bytes", prev, i)
		}
		seen[string(buf)] = i
	}
}

func TestNewOSSourceProducesOutput(t *testing.T) {
	t.Parallel()

	source := NewOSSource()
	if source.Name() == "" {
		t.Error("Name() is empty")
	}
	buf := make([]byte, 32)
	if n, err := source.Read(buf); n != len(buf) || err != nil {
		t.Fatalf("Read() = (%v, %v), want (%v, nil)", n, err, len(buf))
	}
	if bytes.Equal(buf, make([]byte, len(buf))) {
		t.Error("the OS source returned all zeros")
	}
}
