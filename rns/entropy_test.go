// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/entropy"
	"github.com/gmlewis/go-reticulum/rns/crypto"
	"github.com/gmlewis/go-reticulum/testutils"
)

// installStuckGenerator stands in for the device this package exists to catch: a
// chip whose hardware generator answers every read with the same value. It
// restores the default source when the test ends.
//
// The tests that install a source deliberately do not call t.Parallel: they
// replace process-wide state, and the sequential pass over the package's tests
// is what keeps them from overlapping with the tests that draw real key
// material.
func installStuckGenerator(t *testing.T, value byte) {
	t.Helper()
	t.Cleanup(crypto.ResetRandomSource)

	stuck := entropy.SourceFunc{
		Label: "stuck-register",
		ReadFunc: func(b []byte) (int, error) {
			for i := range b {
				b[i] = value
			}
			return len(b), nil
		},
	}
	if err := crypto.SetEntropySource(stuck, entropy.Options{Salt: []byte("device-0001")}); err != nil {
		t.Fatalf("SetEntropySource() = %v", err)
	}
}

// installGatedGenerator installs a gate over a healthy generator, with the
// given device identifier as the HKDF salt.
func installGatedGenerator(t *testing.T, salt string) {
	t.Helper()
	t.Cleanup(crypto.ResetRandomSource)

	var next byte
	ramp := entropy.SourceFunc{
		Label: "test-sar-adc",
		ReadFunc: func(b []byte) (int, error) {
			for i := range b {
				b[i] = next
				next++
			}
			return len(b), nil
		},
	}
	if err := crypto.SetEntropySource(ramp, entropy.Options{Salt: []byte(salt)}); err != nil {
		t.Fatalf("SetEntropySource() = %v", err)
	}
}

// TestIdentityCreationFailsClosed is the first-boot guarantee at the level a
// device hits it: a chip with a dead generator must not produce an identity.
func TestIdentityCreationFailsClosed(t *testing.T) {
	installStuckGenerator(t, 0x00)

	id, err := NewIdentity(true, nil)
	if !errors.Is(err, entropy.ErrHealthTestFailed) {
		t.Fatalf("NewIdentity(true, nil) error = %v, want ErrHealthTestFailed", err)
	}
	if id != nil {
		t.Error("NewIdentity returned an identity alongside the error")
	}
}

func TestRandomHashFailsClosed(t *testing.T) {
	installStuckGenerator(t, 0xFF)

	if hash, err := RandomHash(); !errors.Is(err, entropy.ErrHealthTestFailed) {
		t.Errorf("RandomHash() error = %v, want ErrHealthTestFailed", err)
	} else if hash != nil {
		t.Error("RandomHash returned a hash alongside the error")
	}
}

// TestTransportStartFailsClosed checks the same guarantee one level up, on the
// path a device actually executes at boot: bringing the transport up creates the
// persistent transport identity, and that must fail rather than persist a
// predictable one.
//
// Stop is not called on the failure path: a Start that fails this early has not
// reached the point where it starts its maintenance goroutines, so there is
// nothing to stop.
func TestTransportStartFailsClosed(t *testing.T) {
	ts := newTestTransportSystem(t)
	storage := testutils.TempDir(t, "rns-entropy-test-failclosed-*")

	installStuckGenerator(t, 0x00)

	if err := ts.Start(storage); !errors.Is(err, entropy.ErrHealthTestFailed) {
		t.Fatalf("Start() error = %v, want ErrHealthTestFailed", err)
	}

	identityPath := filepath.Join(storage, "transport_identity")
	if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
		t.Errorf("a transport identity was persisted despite the failure (stat %v)", err)
	}
}

// TestTransportStartUsesTheGateAndRecovers checks the other half: once the
// generator works, the same boot path produces an identity, and the identifier
// it is salted with is what keeps two units from colliding.
func TestTransportStartUsesTheGateAndRecovers(t *testing.T) {
	storageA := testutils.TempDir(t, "rns-entropy-test-device-a-*")
	storageB := testutils.TempDir(t, "rns-entropy-test-device-b-*")

	installGatedGenerator(t, "device-0001")
	tsA := newTestTransportSystem(t)
	if err := tsA.Start(storageA); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	t.Cleanup(tsA.Stop)
	crypto.ResetRandomSource()

	installGatedGenerator(t, "device-0002")
	tsB := newTestTransportSystem(t)
	if err := tsB.Start(storageB); err != nil {
		t.Fatalf("Start() = %v", err)
	}
	t.Cleanup(tsB.Stop)
	crypto.ResetRandomSource()

	keyA, err := os.ReadFile(filepath.Join(storageA, "transport_identity"))
	if err != nil {
		t.Fatalf("reading the first device's identity: %v", err)
	}
	keyB, err := os.ReadFile(filepath.Join(storageB, "transport_identity"))
	if err != nil {
		t.Fatalf("reading the second device's identity: %v", err)
	}
	if len(keyA) != IdentityKeySize/8 {
		t.Errorf("transport identity is %v bytes, want %v", len(keyA), IdentityKeySize/8)
	}
	if bytes.Equal(keyA, keyB) {
		t.Error("two devices with the same generator and different identifiers persisted the same identity")
	}
}

// TestIdentityCreationFollowsTheGate pins the wiring: identity creation must
// draw from the installed source, so a device whose generator is gated produces
// the identity its gate describes rather than one drawn from somewhere else.
func TestIdentityCreationFollowsTheGate(t *testing.T) {
	installGatedGenerator(t, "device-0001")
	first, err := NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(true, nil) = %v", err)
	}

	crypto.ResetRandomSource()
	installGatedGenerator(t, "device-0001")
	second, err := NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(true, nil) = %v", err)
	}

	if !bytes.Equal(first.GetPrivateKey(), second.GetPrivateKey()) {
		t.Error("identity creation did not follow the installed source")
	}

	crypto.ResetRandomSource()
	installGatedGenerator(t, "device-0002")
	other, err := NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(true, nil) = %v", err)
	}
	if bytes.Equal(first.GetPrivateKey(), other.GetPrivateKey()) {
		t.Error("a different device identifier produced the same identity")
	}
}
