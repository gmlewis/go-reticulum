// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"reflect"
	"testing"
)

func TestNewListenerSessionInitialState(t *testing.T) {
	t.Parallel()

	s := newListenerSession(listenerSessionConfig{})
	if s.state != lsStateWaitIdent {
		t.Fatalf("state=%v, want %v", s.state, lsStateWaitIdent)
	}

	s2 := newListenerSession(listenerSessionConfig{AllowAll: true})
	if s2.state != lsStateWaitVers {
		t.Fatalf("state=%v, want %v", s2.state, lsStateWaitVers)
	}
}

func TestSessionVersionFlow(t *testing.T) {
	t.Parallel()

	s := newListenerSession(listenerSessionConfig{SoftwareVersion: "go-test"})
	if err := s.onInitiatorIdentified([]byte{0x01}, true); err != nil {
		t.Fatalf("onInitiatorIdentified error: %v", err)
	}
	resp, err := s.handleVersion(versionInfoMessage{SoftwareVersion: "py", ProtocolVersion: protocolVersion})
	if err != nil {
		t.Fatalf("handleVersion error: %v", err)
	}
	if resp == nil || resp.ProtocolVersion != protocolVersion || resp.SoftwareVersion != "go-test" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if s.state != lsStateWaitCmd {
		t.Fatalf("state=%v, want %v", s.state, lsStateWaitCmd)
	}
}

func TestSessionRejectsIncompatibleProtocol(t *testing.T) {
	t.Parallel()

	s := newListenerSession(listenerSessionConfig{AllowAll: true})
	if _, err := s.handleVersion(versionInfoMessage{ProtocolVersion: 999}); err == nil {
		t.Fatal("expected incompatible protocol error")
	}
	if s.state != lsStateError {
		t.Fatalf("state=%v, want %v", s.state, lsStateError)
	}
}

func TestSessionExecutePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     listenerSessionConfig
		remote  []string
		want    []string
		wantErr bool
	}{
		{
			name:   "default command used",
			cfg:    listenerSessionConfig{AllowAll: true, AllowRemoteCommand: true, DefaultCommand: []string{"/bin/sh"}},
			remote: nil,
			want:   []string{"/bin/sh"},
		},
		{
			name:    "remote command denied",
			cfg:     listenerSessionConfig{AllowAll: true, AllowRemoteCommand: false, DefaultCommand: []string{"/bin/sh"}},
			remote:  []string{"id"},
			wantErr: true,
		},
		{
			name:   "remote command as args",
			cfg:    listenerSessionConfig{AllowAll: true, AllowRemoteCommand: true, RemoteCmdAsArgs: true, DefaultCommand: []string{"/bin/sh", "-lc"}},
			remote: []string{"echo", "hi"},
			want:   []string{"/bin/sh", "-lc", "echo", "hi"},
		},
		{
			name:   "remote command replaces default",
			cfg:    listenerSessionConfig{AllowAll: true, AllowRemoteCommand: true, DefaultCommand: []string{"/bin/sh"}},
			remote: []string{"/usr/bin/env", "true"},
			want:   []string{"/usr/bin/env", "true"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newListenerSession(tc.cfg)
			if s.state != lsStateWaitVers {
				t.Fatalf("state=%v, want wait_vers", s.state)
			}
			if _, err := s.handleVersion(versionInfoMessage{ProtocolVersion: protocolVersion}); err != nil {
				t.Fatalf("handleVersion: %v", err)
			}

			cmd, err := s.handleExecute(executeCommandMessage{CommandLine: tc.remote})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if !reflect.DeepEqual(cmd, tc.want) {
				t.Fatalf("cmd=%v, want %v", cmd, tc.want)
			}
			if s.state != lsStateRunning {
				t.Fatalf("state=%v, want %v", s.state, lsStateRunning)
			}
		})
	}
}

func TestSessionWindowSizeOnlyInRunning(t *testing.T) {
	t.Parallel()

	rows := 40
	s := newListenerSession(listenerSessionConfig{AllowAll: true})
	if err := s.handleWindowSize(windowSizeMessage{Rows: &rows}); err == nil {
		t.Fatal("expected protocol error before running")
	}

	if _, err := s.handleVersion(versionInfoMessage{ProtocolVersion: protocolVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.handleExecute(executeCommandMessage{}); err != nil {
		t.Fatal(err)
	}
	if err := s.handleWindowSize(windowSizeMessage{Rows: &rows}); err != nil {
		t.Fatalf("unexpected running window-size error: %v", err)
	}
	if s.rows == nil || *s.rows != 40 {
		t.Fatalf("rows=%v, want 40", s.rows)
	}
}

func TestSessionWindowSizeRetainsLastValidDimensions(t *testing.T) {
	t.Parallel()

	rows := 40
	cols := 120
	s := newListenerSession(listenerSessionConfig{AllowAll: true})
	if _, err := s.handleVersion(versionInfoMessage{ProtocolVersion: protocolVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.handleExecute(executeCommandMessage{}); err != nil {
		t.Fatal(err)
	}
	if err := s.handleWindowSize(windowSizeMessage{Rows: &rows, Cols: &cols}); err != nil {
		t.Fatal(err)
	}

	zero := 0
	if err := s.handleWindowSize(windowSizeMessage{Rows: nil, Cols: &zero}); err != nil {
		t.Fatal(err)
	}

	if s.rows == nil || *s.rows != 40 {
		t.Fatalf("rows=%v, want 40 retained", s.rows)
	}
	if s.cols == nil || *s.cols != 120 {
		t.Fatalf("cols=%v, want 120 retained", s.cols)
	}
}

func TestSessionWindowSizeAppliesValidSequentialUpdates(t *testing.T) {
	t.Parallel()

	s := newListenerSession(listenerSessionConfig{AllowAll: true})
	if _, err := s.handleVersion(versionInfoMessage{ProtocolVersion: protocolVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.handleExecute(executeCommandMessage{}); err != nil {
		t.Fatal(err)
	}

	rows1 := 41
	cols1 := 101
	if err := s.handleWindowSize(windowSizeMessage{Rows: &rows1, Cols: &cols1}); err != nil {
		t.Fatal(err)
	}
	rows2 := 42
	cols2 := 102
	if err := s.handleWindowSize(windowSizeMessage{Rows: &rows2, Cols: &cols2}); err != nil {
		t.Fatal(err)
	}

	if s.rows == nil || *s.rows != 42 {
		t.Fatalf("rows=%v, want 42", s.rows)
	}
	if s.cols == nil || *s.cols != 102 {
		t.Fatalf("cols=%v, want 102", s.cols)
	}
}

func TestSessionStateHelpersRunningAndTeardown(t *testing.T) {
	t.Parallel()

	s := newListenerSession(listenerSessionConfig{AllowAll: true})
	if s.isRunning() {
		t.Fatal("expected not running initially")
	}
	if s.isTeardown() {
		t.Fatal("expected not teardown initially")
	}

	if _, err := s.handleVersion(versionInfoMessage{ProtocolVersion: protocolVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.handleExecute(executeCommandMessage{}); err != nil {
		t.Fatal(err)
	}
	if !s.isRunning() {
		t.Fatal("expected running after execute")
	}

	s.markTeardown()
	if s.isRunning() {
		t.Fatal("expected not running after teardown")
	}
	if !s.isTeardown() {
		t.Fatal("expected teardown state after markTeardown")
	}
}

func TestSessionRepeatedIdentifySameIdentityAllowed(t *testing.T) {
	t.Parallel()

	s := newListenerSession(listenerSessionConfig{})
	id := []byte{0x01, 0x02}

	if err := s.onInitiatorIdentified(id, true); err != nil {
		t.Fatalf("first identify error: %v", err)
	}
	if err := s.onInitiatorIdentified(id, true); err != nil {
		t.Fatalf("second same-identity identify error: %v", err)
	}
	if s.state != lsStateWaitVers {
		t.Fatalf("state=%v, want %v", s.state, lsStateWaitVers)
	}
}

func TestSessionRepeatedIdentifyDifferentIdentityRejected(t *testing.T) {
	t.Parallel()

	s := newListenerSession(listenerSessionConfig{})
	if err := s.onInitiatorIdentified([]byte{0x01, 0x02}, true); err != nil {
		t.Fatalf("first identify error: %v", err)
	}
	err := s.onInitiatorIdentified([]byte{0x03, 0x04}, true)
	if err == nil || err.Error() != "remote identity changed during setup" {
		t.Fatalf("unexpected err=%v", err)
	}
	if s.state != lsStateError {
		t.Fatalf("state=%v, want %v", s.state, lsStateError)
	}
}
