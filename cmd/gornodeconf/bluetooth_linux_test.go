// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"errors"
	"testing"
)

type bluetoothWriter struct {
	writes     [][]byte
	shortWrite bool
	err        error
}

func (w *bluetoothWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), data...))
	if w.err != nil {
		return 0, w.err
	}
	if w.shortWrite {
		return len(data) - 1, nil
	}
	return len(data), nil
}

func TestRNodeEnableBluetoothWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &bluetoothWriter{}
	state := &bluetoothState{name: "rnode", writer: writer}
	if err := state.enableBluetooth(); err != nil {
		t.Fatalf("enableBluetooth returned error: %v", err)
	}

	want := []byte{0xc0, 0x46, 0x01, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("bluetooth enable frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeEnableBluetoothReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &bluetoothWriter{shortWrite: true}
	state := &bluetoothState{name: "rnode", writer: writer}
	err := state.enableBluetooth()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending bluetooth enable command to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeEnableBluetoothReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &bluetoothWriter{err: wantErr}
	state := &bluetoothState{name: "rnode", writer: writer}
	if err := state.enableBluetooth(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}

func TestRNodeDisableBluetoothWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &bluetoothWriter{}
	state := &bluetoothState{name: "rnode", writer: writer}
	if err := state.disableBluetooth(); err != nil {
		t.Fatalf("disableBluetooth returned error: %v", err)
	}

	want := []byte{0xc0, 0x46, 0x00, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("bluetooth disable frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeDisableBluetoothReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &bluetoothWriter{shortWrite: true}
	state := &bluetoothState{name: "rnode", writer: writer}
	err := state.disableBluetooth()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending bluetooth disable command to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeDisableBluetoothReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &bluetoothWriter{err: wantErr}
	state := &bluetoothState{name: "rnode", writer: writer}
	if err := state.disableBluetooth(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}

func TestRNodeBluetoothPairWritesPythonFrame(t *testing.T) {
	t.Parallel()

	writer := &bluetoothWriter{}
	state := &bluetoothState{name: "rnode", writer: writer}
	if err := state.bluetoothPair(); err != nil {
		t.Fatalf("bluetoothPair returned error: %v", err)
	}

	want := []byte{0xc0, 0x46, 0x02, 0xc0}
	if len(writer.writes) != 1 || !bytes.Equal(writer.writes[0], want) {
		t.Fatalf("bluetooth pair frame mismatch:\n got: %#v\nwant: %x", writer.writes, want)
	}
}

func TestRNodeBluetoothPairReturnsErrorOnShortWrite(t *testing.T) {
	t.Parallel()

	writer := &bluetoothWriter{shortWrite: true}
	state := &bluetoothState{name: "rnode", writer: writer}
	err := state.bluetoothPair()
	if err == nil {
		t.Fatalf("expected error on short write")
	}
	want := "An IO error occurred while sending bluetooth pair command to device"
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}

func TestRNodeBluetoothPairReturnsWriterError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	writer := &bluetoothWriter{err: wantErr}
	state := &bluetoothState{name: "rnode", writer: writer}
	if err := state.bluetoothPair(); !errors.Is(err, wantErr) {
		t.Fatalf("expected writer error, got %v", err)
	}
}
