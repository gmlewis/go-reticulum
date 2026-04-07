// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type initiatorSessionState int

const (
	initiatorWaitVersion initiatorSessionState = iota + 1
	initiatorWaitExit
	initiatorDone
)

type initiatorChannelSession struct {
	mu sync.Mutex

	state initiatorSessionState

	versionAckCh chan struct{}
	doneCh       chan int
	errCh        chan error

	stdout bytes.Buffer
	stderr bytes.Buffer

	terminated bool
	lastExit   *int
	lastErr    error
}

type initiatorTerminalSnapshot struct {
	terminated bool
	lastExit   *int
	lastErr    error
}

type channelSession interface {
	messageSender
	AddMessageHandler(func(rns.Message) bool)
}

func newInitiatorChannelSession() *initiatorChannelSession {
	return &initiatorChannelSession{
		state:        initiatorWaitVersion,
		versionAckCh: make(chan struct{}, 1),
		doneCh:       make(chan int, 1),
		errCh:        make(chan error, 1),
	}
}

func (s *initiatorChannelSession) handleMessage(message rns.Message) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch msg := message.(type) {
	case *versionInfoMessage:
		if s.state != initiatorWaitVersion {
			return true
		}
		if msg.ProtocolVersion != protocolVersion {
			s.emitErrLocked(errors.New("incompatible protocol"))
			return true
		}
		s.state = initiatorWaitExit
		s.emitVersionAckLocked()
		return true

	case *streamDataMessage:
		if s.state != initiatorWaitExit {
			return true
		}
		s.appendStreamLocked(msg)
		return true

	case *errorMessage:
		if msg.Fatal {
			err := fmt.Errorf("remote error: %v", msg.Message)
			s.lastErr = err
			s.state = initiatorDone
			s.emitErrLocked(err)
			s.terminated = true
			return true
		}
		if msg.Message != "" {
			s.stderr.WriteString("remote warning: ")
			s.stderr.WriteString(msg.Message)
			s.stderr.WriteString("\n")
		}
		return true

	case *commandExitedMessage:
		if s.state != initiatorWaitExit {
			return true
		}
		s.state = initiatorDone
		s.terminated = true
		exit := msg.ReturnCode
		s.lastExit = &exit
		s.emitDoneLocked(msg.ReturnCode)
		return true

	default:
		return false
	}
}

func (s *initiatorChannelSession) appendStreamLocked(msg *streamDataMessage) {
	switch msg.StreamID {
	case streamIDStdout:
		_, _ = os.Stdout.Write(msg.Data)
		s.stdout.Write(msg.Data)
	case streamIDStderr:
		_, _ = os.Stderr.Write(msg.Data)
		s.stderr.Write(msg.Data)
	}
}

func (s *initiatorChannelSession) emitVersionAckLocked() {
	select {
	case s.versionAckCh <- struct{}{}:
	default:
	}
}

func (s *initiatorChannelSession) emitDoneLocked(exitCode int) {
	select {
	case s.doneCh <- exitCode:
	default:
	}
}

func (s *initiatorChannelSession) emitErrLocked(err error) {
	select {
	case s.errCh <- err:
	default:
	}
}

func (s *initiatorChannelSession) terminalSnapshot() initiatorTerminalSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	var lastExit *int
	if s.lastExit != nil {
		exit := *s.lastExit
		lastExit = &exit
	}

	return initiatorTerminalSnapshot{
		terminated: s.terminated,
		lastExit:   lastExit,
		lastErr:    s.lastErr,
	}
}

func runInitiatorChannelSession(link *rns.Link, opts options) (int, error) {
	return runInitiatorChannelSessionWithLogger(nil, link, opts)
}

func runInitiatorChannelSessionWithLogger(logger *rns.Logger, link *rns.Link, opts options) (int, error) {
	channel := link.GetChannel()
	registerProtocolMessageTypes(channel)
	stopCh := make(chan struct{})
	defer close(stopCh)

	linkClosedCh := make(chan struct{}, 1)
	link.SetLinkClosedCallback(func(l *rns.Link) {
		select {
		case linkClosedCh <- struct{}{}:
		default:
		}
	})

	exitCode, session, err := (&runtimeT{logger: logger}).runInitiatorProtocolFlow(channel, opts, linkClosedCh, stopCh, true)
	if session != nil {
		writeInitiatorStreams(session)
	}
	return exitCode, err
}

func runInitiatorProtocolFlow(channel channelSession, opts options, linkClosedCh <-chan struct{}, stopCh <-chan struct{}, pumpInput bool) (int, *initiatorChannelSession, error) {
	return runInitiatorProtocolFlowWithLogger(nil, channel, opts, linkClosedCh, stopCh, pumpInput)
}

func runInitiatorProtocolFlowWithLogger(_ *rns.Logger, channel channelSession, opts options, linkClosedCh <-chan struct{}, stopCh <-chan struct{}, pumpInput bool) (int, *initiatorChannelSession, error) {
	return (&runtimeT{}).runInitiatorProtocolFlow(channel, opts, linkClosedCh, stopCh, pumpInput)
}

func (rt *runtimeT) runInitiatorProtocolFlow(channel channelSession, opts options, linkClosedCh <-chan struct{}, stopCh <-chan struct{}, pumpInput bool) (int, *initiatorChannelSession, error) {
	session := newInitiatorChannelSession()
	channel.AddMessageHandler(session.handleMessage)
	timeout := time.Duration(opts.timeoutSec) * time.Second

	versionMessage := &versionInfoMessage{SoftwareVersion: "gornsh " + rns.VERSION, ProtocolVersion: protocolVersion}
	if err := sendMessageWithRetry(channel, versionMessage, time.Now().Add(timeout)); err != nil {
		return 1, session, fmt.Errorf("failed to send version info: %w", err)
	}

	select {
	case <-session.versionAckCh:
	case err := <-session.errCh:
		return 1, session, err
	case <-time.After(timeout):
		return 1, session, errors.New("version negotiation timed out")
	}

	executeMessage := buildExecuteCommandMessage(opts)
	if err := sendMessageWithRetry(channel, executeMessage, time.Now().Add(timeout)); err != nil {
		return 1, session, fmt.Errorf("failed to send execute command: %w", err)
	}

	if pumpInput {
		go rt.pumpInitiatorStdin(channel)
	}
	if pumpInput && !opts.noTTY {
		go pumpWindowSizeUpdates(channel, stopCh, 500*time.Millisecond, executeMessage.Rows, executeMessage.Cols)
	}

	for {
		select {
		case err := <-session.errCh:
			return 1, session, err
		case exitCode := <-session.doneCh:
			// Even if we got an exit code, a fatal error might have arrived
			// simultaneously and should be prioritized.
			select {
			case err := <-session.errCh:
				return 1, session, err
			default:
			}
			if opts.mirror {
				return exitCode, session, nil
			}
			return 0, session, nil
		case <-linkClosedCh:
			// Link closure is the lowest priority; check for errors or exits first.
			select {
			case err := <-session.errCh:
				return 1, session, err
			case exitCode := <-session.doneCh:
				if opts.mirror {
					return exitCode, session, nil
				}
				return 0, session, nil
			default:
			}

			snapshot := session.terminalSnapshot()
			if snapshot.lastErr != nil {
				return 1, session, snapshot.lastErr
			}
			if snapshot.lastExit != nil {
				if opts.mirror {
					return *snapshot.lastExit, session, nil
				}
				return 0, session, nil
			}
			if snapshot.terminated {
				if opts.mirror {
					return 0, session, nil
				}
				return 0, session, nil
			}
			return 1, session, errors.New("link closed before command completed")
		}
	}
}

func buildExecuteCommandMessage(opts options) *executeCommandMessage {
	pipeMode := opts.noTTY
	term := determineTerminalName(opts.noTTY)
	rows, cols := determineTerminalSize(opts.noTTY)

	return &executeCommandMessage{
		CommandLine: opts.commandLine,
		PipeStdin:   pipeMode,
		PipeStdout:  pipeMode,
		PipeStderr:  pipeMode,
		TCFlags:     nil,
		Term:        term,
		Rows:        rows,
		Cols:        cols,
		HPix:        nil,
		VPix:        nil,
	}
}

func determineTerminalName(noTTY bool) *string {
	if noTTY {
		return nil
	}
	value := os.Getenv("TERM")
	if value == "" {
		value = "xterm"
	}
	return &value
}

func determineTerminalSize(noTTY bool) (*int, *int) {
	if noTTY {
		return nil, nil
	}
	rows := parsePositiveEnvInt("LINES")
	cols := parsePositiveEnvInt("COLUMNS")
	return rows, cols
}

func parsePositiveEnvInt(name string) *int {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return nil
	}
	out := parsed
	return &out
}

func (rt *runtimeT) pumpInitiatorStdin(sender messageSender) {
	buf := make([]byte, 32*1024)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if sendErr := sendMessageWithRetry(sender, &streamDataMessage{StreamID: streamIDStdin, Data: chunk, EOF: false, Compressed: false}, time.Now().Add(2*time.Second)); sendErr != nil {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				rt.sendProtocolErrorToSender(sender, err.Error(), true)
			}
			_ = sendMessageWithRetry(sender, &streamDataMessage{StreamID: streamIDStdin, Data: nil, EOF: true, Compressed: false}, time.Now().Add(2*time.Second))
			return
		}
	}
}

func pumpWindowSizeUpdates(sender messageSender, stop <-chan struct{}, interval time.Duration, initialRows, initialCols *int) {
	lastRows := cloneOptionalInt(initialRows)
	lastCols := cloneOptionalInt(initialCols)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			rows, cols := determineTerminalSize(false)
			if optionalIntEqual(lastRows, rows) && optionalIntEqual(lastCols, cols) {
				continue
			}
			lastRows = cloneOptionalInt(rows)
			lastCols = cloneOptionalInt(cols)
			if err := sendMessageWithRetry(sender, &windowSizeMessage{Rows: rows, Cols: cols, HPix: nil, VPix: nil}, time.Now().Add(2*time.Second)); err != nil {
				return
			}
		}
	}
}

func cloneOptionalInt(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func optionalIntEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (s *initiatorChannelSession) stderrString() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stderr.String()
}

func (s *initiatorChannelSession) stdoutString() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stdout.String()
}

func writeInitiatorStreams(session *initiatorChannelSession) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.stdout.Reset()
	session.stderr.Reset()
}

func sendMessageWithRetry(sender messageSender, msg rns.Message, deadline time.Time) error {
	for {
		_, err := sender.Send(msg)
		if err == nil {
			return nil
		}
		if !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}
