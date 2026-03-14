// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
)

type lsState int

const (
	lsStateWaitIdent lsState = iota + 1
	lsStateWaitVers
	lsStateWaitCmd
	lsStateRunning
	lsStateError
	lsStateTeardown
)

type listenerSessionConfig struct {
	AllowAll           bool
	AllowRemoteCommand bool
	RemoteCmdAsArgs    bool
	DefaultCommand     []string
	SoftwareVersion    string
}

type listenerSession struct {
	mu    sync.Mutex
	state lsState

	allowAll           bool
	allowRemoteCommand bool
	remoteCmdAsArgs    bool
	defaultCommand     []string
	softwareVersion    string

	remoteIdentity []byte
	cmdline        []string

	stdinIsPipe  bool
	stdoutIsPipe bool
	stderrIsPipe bool
	term         *string
	rows         *int
	cols         *int
	hpix         *int
	vpix         *int
}

func newListenerSession(cfg listenerSessionConfig) *listenerSession {
	state := lsStateWaitIdent
	if cfg.AllowAll {
		state = lsStateWaitVers
	}
	softwareVersion := cfg.SoftwareVersion
	if softwareVersion == "" {
		softwareVersion = "gornsh"
	}
	return &listenerSession{
		state:              state,
		allowAll:           cfg.AllowAll,
		allowRemoteCommand: cfg.AllowRemoteCommand,
		remoteCmdAsArgs:    cfg.RemoteCmdAsArgs,
		defaultCommand:     append([]string{}, cfg.DefaultCommand...),
		softwareVersion:    softwareVersion,
	}
}

func (s *listenerSession) stateNameLocked() string {
	switch s.state {
	case lsStateWaitIdent:
		return "LSSTATE_WAIT_IDENT"
	case lsStateWaitVers:
		return "LSSTATE_WAIT_VERS"
	case lsStateWaitCmd:
		return "LSSTATE_WAIT_CMD"
	case lsStateRunning:
		return "LSSTATE_RUNNING"
	case lsStateError:
		return "LSSTATE_ERROR"
	case lsStateTeardown:
		return "LSSTATE_TEARDOWN"
	default:
		return "LSSTATE_UNKNOWN"
	}
}

func (s *listenerSession) markTeardown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = lsStateTeardown
}

func (s *listenerSession) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == lsStateRunning
}

func (s *listenerSession) isTeardown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == lsStateTeardown
}

func (s *listenerSession) onInitiatorIdentified(identityHash []byte, isAllowed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != lsStateWaitIdent && s.state != lsStateWaitVers {
		return fmt.Errorf("protocol error (%v)", s.stateNameLocked())
	}
	if !s.allowAll && !isAllowed {
		s.state = lsStateError
		return errors.New("identity is not allowed")
	}
	if len(s.remoteIdentity) > 0 && !bytes.Equal(s.remoteIdentity, identityHash) {
		s.state = lsStateError
		return errors.New("remote identity changed during setup")
	}
	s.remoteIdentity = append([]byte{}, identityHash...)
	s.state = lsStateWaitVers
	return nil
}

func (s *listenerSession) handleVersion(msg versionInfoMessage) (*versionInfoMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state == lsStateWaitIdent {
		return nil, nil
	}
	if s.state != lsStateWaitVers {
		s.state = lsStateError
		return nil, fmt.Errorf("protocol error (%v)", s.stateNameLocked())
	}
	if msg.ProtocolVersion != protocolVersion {
		s.state = lsStateError
		return nil, errors.New("incompatible protocol")
	}
	s.state = lsStateWaitCmd
	return &versionInfoMessage{SoftwareVersion: s.softwareVersion, ProtocolVersion: protocolVersion}, nil
}

func (s *listenerSession) handleExecute(msg executeCommandMessage) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != lsStateWaitCmd {
		s.state = lsStateError
		return nil, fmt.Errorf("protocol error (%v)", s.stateNameLocked())
	}

	cmdline, err := s.resolveCommand(msg.CommandLine)
	if err != nil {
		s.state = lsStateError
		return nil, err
	}

	s.cmdline = append([]string{}, cmdline...)
	s.stdinIsPipe = msg.PipeStdin
	s.stdoutIsPipe = msg.PipeStdout
	s.stderrIsPipe = msg.PipeStderr
	s.term = msg.Term
	s.rows = msg.Rows
	s.cols = msg.Cols
	s.hpix = msg.HPix
	s.vpix = msg.VPix
	s.state = lsStateRunning

	return append([]string{}, s.cmdline...), nil
}

func (s *listenerSession) handleWindowSize(msg windowSizeMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != lsStateRunning {
		return fmt.Errorf("protocol error (%v)", s.stateNameLocked())
	}
	if msg.Rows != nil && *msg.Rows > 0 {
		rows := *msg.Rows
		s.rows = &rows
	}
	if msg.Cols != nil && *msg.Cols > 0 {
		cols := *msg.Cols
		s.cols = &cols
	}
	s.hpix = msg.HPix
	s.vpix = msg.VPix
	return nil
}

func (s *listenerSession) handleStreamData(msg streamDataMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != lsStateRunning {
		return fmt.Errorf("protocol error (%v)", s.stateNameLocked())
	}
	if msg.StreamID != streamIDStdin {
		s.state = lsStateError
		return fmt.Errorf("protocol error (%v)", s.stateNameLocked())
	}
	return nil
}

func (s *listenerSession) resolveCommand(remoteCommand []string) ([]string, error) {
	base := append([]string{}, s.defaultCommand...)
	if len(base) == 0 {
		base = []string{"/bin/sh"}
	}

	if len(remoteCommand) > 0 && !s.allowRemoteCommand {
		return nil, errors.New("remote command line not allowed by listener")
	}

	if len(remoteCommand) == 0 {
		return base, nil
	}

	if s.remoteCmdAsArgs {
		return append(base, remoteCommand...), nil
	}

	return append([]string{}, remoteCommand...), nil
}
