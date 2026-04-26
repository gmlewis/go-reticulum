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

	"github.com/gmlewis/go-reticulum/compress/bzip2"
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

	stdout       bytes.Buffer
	stderr       bytes.Buffer
	stdoutWriter io.Writer
	stderrWriter io.Writer

	terminated bool
	lastExit   *int
	lastErr    error
	sawStdout  bool
	sawStderr  bool
	stdoutEOF  bool
	stderrEOF  bool
}

type initiatorTerminalSnapshot struct {
	terminated         bool
	lastExit           *int
	lastErr            error
	streamEOFsComplete bool
	sawOutput          bool
}

var (
	initiatorStdinFile  = os.Stdin
	initiatorStdoutFile = os.Stdout
	initiatorStderrFile = os.Stderr
	isTTYFile           = fileIsTTY
)

type channelSession interface {
	messageSender
	AddMessageHandler(func(rns.Message) bool)
}

func newInitiatorChannelSession(stdout, stderr io.Writer) *initiatorChannelSession {
	return &initiatorChannelSession{
		state:        initiatorWaitVersion,
		versionAckCh: make(chan struct{}, 1),
		doneCh:       make(chan int, 1),
		errCh:        make(chan error, 1),
		stdoutWriter: stdout,
		stderrWriter: stderr,
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
		if msg.Compressed {
			decoded, err := decodeCompressedStreamData(msg.Data)
			if err != nil {
				s.emitErrLocked(err)
				return true
			}
			msg = &streamDataMessage{StreamID: msg.StreamID, Data: decoded, EOF: msg.EOF, Compressed: false}
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
		s.sawStdout = true
		if msg.EOF {
			s.stdoutEOF = true
		}
		if s.stdoutWriter != nil {
			_, _ = s.stdoutWriter.Write(msg.Data)
		}
		s.stdout.Write(msg.Data)
	case streamIDStderr:
		s.sawStderr = true
		if msg.EOF {
			s.stderrEOF = true
		}
		if s.stderrWriter != nil {
			_, _ = s.stderrWriter.Write(msg.Data)
		}
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
		terminated:         s.terminated,
		lastExit:           lastExit,
		lastErr:            s.lastErr,
		streamEOFsComplete: s.outputStreamEOFsCompleteLocked(),
		sawOutput:          s.sawStdout || s.sawStderr,
	}
}

func (s *initiatorChannelSession) outputStreamEOFsCompleteLocked() bool {
	if !s.sawStdout && !s.sawStderr {
		return false
	}
	if s.sawStdout && !s.stdoutEOF {
		return false
	}
	if s.sawStderr && !s.stderrEOF {
		return false
	}
	return true
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

	exitCode, session, err := (&runtimeT{logger: logger, stdout: os.Stdout, stderr: os.Stderr}).runInitiatorProtocolFlow(channel, opts, linkClosedCh, stopCh, true)
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
	session := newInitiatorChannelSession(rt.stdout, rt.stderr)
	channel.AddMessageHandler(session.handleMessage)
	timeout := time.Duration(opts.timeoutSec) * time.Second

	versionMessage := &versionInfoMessage{SoftwareVersion: "gornsh " + rns.VERSION, ProtocolVersion: protocolVersion}
	if err := sendMessageWithRetry(channel, versionMessage, time.Now().Add(timeout), rt.retrySleep); err != nil {
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
	if err := sendMessageWithRetry(channel, executeMessage, time.Now().Add(timeout), rt.retrySleep); err != nil {
		return 1, session, fmt.Errorf("failed to send execute command: %w", err)
	}

	if pumpInput && !opts.noTTY {
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
			// Give it a tiny bit of time for final messages to be processed.
			time.Sleep(rt.linkClosedGrace)
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
			if snapshot.streamEOFsComplete {
				if opts.mirror {
					return 0, session, nil
				}
				return 0, session, nil
			}
			if snapshot.sawOutput {
				return 0, session, nil
			}
			return 1, session, errors.New("link closed before command completed")
		}
	}
}

func buildExecuteCommandMessage(opts options) *executeCommandMessage {
	pipeStdin := opts.noTTY || !isTTYFile(initiatorStdinFile)
	pipeStdout := opts.noTTY || !isTTYFile(initiatorStdoutFile)
	pipeStderr := opts.noTTY || !isTTYFile(initiatorStderrFile)
	term := determineTerminalName(opts.noTTY)
	rows, cols := determineTerminalSize(opts.noTTY)

	return &executeCommandMessage{
		CommandLine: opts.commandLine,
		PipeStdin:   pipeStdin,
		PipeStdout:  pipeStdout,
		PipeStderr:  pipeStderr,
		TCFlags:     nil,
		Term:        term,
		Rows:        rows,
		Cols:        cols,
		HPix:        nil,
		VPix:        nil,
	}
}

func fileIsTTY(file *os.File) bool {
	if file == nil {
		return false
	}
	restorer, err := newTTYRestorer(int(file.Fd()))
	if err != nil || restorer == nil {
		return false
	}
	return restorer.active
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
	tty, _ := newTTYRestorer(int(os.Stdin.Fd()))
	useTTY := !rt.opts.noTTY && tty != nil && tty.active
	state := &initiatorTTYInputState{}
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if useTTY {
				payload, action := processInitiatorTTYInputChunk(chunk, state)
				if len(payload) > 0 {
					if sendErr := sendMessageWithRetry(sender, &streamDataMessage{StreamID: streamIDStdin, Data: payload, EOF: false, Compressed: false}, time.Now().Add(2*time.Second), defaultRetrySleep); sendErr != nil {
						return
					}
				}
				if action.help {
					writeInitiatorEscapeHelp(rt.stdout)
				}
				if action.toggleLine {
					if state.lineMode {
						_, _ = fmt.Fprint(rt.stdout, "\n\rLine-interactive mode enabled\n\r")
					} else {
						_, _ = fmt.Fprint(rt.stdout, "\n\rLine-interactive mode disabled\n\r")
					}
				}
				if action.stop {
					return
				}
				continue
			}

			payload, compressed, compressErr := compressAdaptiveStreamData(chunk, messageSenderMDU(sender))
			if compressErr != nil {
				rt.sendProtocolErrorToSender(sender, compressErr.Error(), true)
				return
			}
			if sendErr := sendMessageWithRetry(sender, &streamDataMessage{StreamID: streamIDStdin, Data: payload, EOF: false, Compressed: compressed}, time.Now().Add(2*time.Second), defaultRetrySleep); sendErr != nil {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				rt.sendProtocolErrorToSender(sender, err.Error(), true)
			}
			_ = sendMessageWithRetry(sender, &streamDataMessage{StreamID: streamIDStdin, Data: nil, EOF: true, Compressed: false}, time.Now().Add(2*time.Second), defaultRetrySleep)
			return
		}
	}
}

func messageSenderMDU(sender messageSender) int {
	type mduProvider interface {
		MDU() int
	}
	if provider, ok := sender.(mduProvider); ok {
		return provider.MDU()
	}
	return 0
}

func compressAdaptiveStreamData(data []byte, maxSize int) ([]byte, bool, error) {
	if len(data) == 0 {
		return data, false, nil
	}

	var compressed bytes.Buffer
	writer, err := bzip2.NewWriter(&compressed, nil)
	if err != nil {
		return nil, false, err
	}
	if _, err := writer.Write(data); err != nil {
		return nil, false, err
	}
	if err := writer.Close(); err != nil {
		return nil, false, err
	}
	if compressed.Len() < len(data) && (maxSize <= 0 || compressed.Len() <= maxSize) {
		return compressed.Bytes(), true, nil
	}
	return append([]byte(nil), data...), false, nil
}

type initiatorTTYInputState struct {
	preEscape bool
	escape    bool
	lineMode  bool
}

type initiatorTTYInputAction struct {
	stop       bool
	help       bool
	toggleLine bool
}

func processInitiatorTTYInputChunk(data []byte, state *initiatorTTYInputState) ([]byte, initiatorTTYInputAction) {
	output := make([]byte, 0, len(data))
	action := initiatorTTYInputAction{}
	if state == nil {
		state = &initiatorTTYInputState{}
	}
	for _, b := range data {
		if state.escape {
			state.escape = false
			switch b {
			case '~':
				output = append(output, '~')
			case '.':
				action.stop = true
				return output, action
			case 'L':
				state.lineMode = !state.lineMode
				action.toggleLine = true
			case '?':
				action.help = true
			default:
				output = append(output, '~', b)
			}
			continue
		}

		if state.preEscape && b == '~' {
			state.preEscape = false
			state.escape = true
			continue
		}

		if b == '\n' || b == '\r' {
			state.preEscape = true
			output = append(output, b)
			continue
		}

		state.preEscape = false
		output = append(output, b)
	}
	return output, action
}

func writeInitiatorEscapeHelp(w io.Writer) {
	_, _ = w.Write([]byte(`

Supported rnsh escape sequences:")
  ~~  Send the escape character by typing it twice")
  ~.  Terminate session and exit immediately")
  ~L  Toggle line-interactive mode")
  ~?  Display this quick reference
(Escape sequences are only recognized immediately after newline)
`))
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
			if err := sendMessageWithRetry(sender, &windowSizeMessage{Rows: rows, Cols: cols, HPix: nil, VPix: nil}, time.Now().Add(2*time.Second), defaultRetrySleep); err != nil {
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

func decodeCompressedStreamData(data []byte) ([]byte, error) {
	reader, err := bzip2.NewReader(bytes.NewReader(data), nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = reader.Close()
	}()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func sendMessageWithRetry(sender messageSender, msg rns.Message, deadline time.Time, retrySleep time.Duration) error {
	for {
		_, err := sender.Send(msg)
		if err == nil {
			return nil
		}
		if !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(retrySleep)
	}
}
