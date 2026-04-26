// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"cmp"
	"errors"
	"io"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/compress/bzip2"
	"github.com/gmlewis/go-reticulum/rns"
)

type flakySender struct {
	failCount int
	calls     int
}

func (f *flakySender) Send(msg rns.Message) (*rns.Envelope, error) {
	f.calls++
	if f.calls <= f.failCount {
		return nil, errors.New("transient send failure")
	}
	return nil, nil
}

type fakeChannelSession struct {
	mu       sync.Mutex
	handlers []func(rns.Message) bool
	onSend   func(rns.Message)
	mdu      int
}

type timedEvent struct {
	delay time.Duration
	send  func(*fakeChannelSession, chan<- struct{})
}

func (f *fakeChannelSession) Send(msg rns.Message) (*rns.Envelope, error) {
	if f.onSend != nil {
		f.onSend(msg)
	}
	return nil, nil
}

func (f *fakeChannelSession) AddMessageHandler(handler func(rns.Message) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers = append(f.handlers, handler)
}

func (f *fakeChannelSession) MDU() int {
	if f.mdu <= 0 {
		return 4096
	}
	return f.mdu
}

func (f *fakeChannelSession) emit(msg rns.Message) {
	f.mu.Lock()
	handlers := append([]func(rns.Message) bool{}, f.handlers...)
	f.mu.Unlock()
	for _, handler := range handlers {
		handler(msg)
	}
}

func setIsTTYFileForTest(t *testing.T, fn func(*os.File) bool) {
	t.Helper()
	previous := isTTYFile
	isTTYFile = fn
	t.Cleanup(func() {
		isTTYFile = previous
	})
}

func TestInitiatorSessionDecompressesCompressedStreams(t *testing.T) {
	t.Parallel()

	var compressed bytes.Buffer
	writer, err := bzip2.NewWriter(&compressed, nil)
	if err != nil {
		t.Fatalf("bzip2.NewWriter() error: %v", err)
	}
	if _, err := writer.Write([]byte("compressed hello")); err != nil {
		t.Fatalf("writer.Write() error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error: %v", err)
	}

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	s := newInitiatorChannelSession(stdoutWriter, io.Discard)
	s.state = initiatorWaitExit
	t.Cleanup(func() {
		_ = stdoutWriter.Close()
		_ = stdoutReader.Close()
	})

	if !s.handleMessage(&streamDataMessage{StreamID: streamIDStdout, Data: compressed.Bytes(), Compressed: true}) {
		t.Fatal("compressed stdout stream not handled")
	}

	decoded := make([]byte, len("compressed hello"))
	if _, err := io.ReadFull(stdoutReader, decoded); err != nil {
		t.Fatalf("stdout ReadFull error: %v", err)
	}
	if got := string(decoded); got != "compressed hello" {
		t.Fatalf("stdout=%q, want %q", got, "compressed hello")
	}
	if got := s.stdout.String(); got != "compressed hello" {
		t.Fatalf("buffered stdout=%q, want %q", got, "compressed hello")
	}
}

func TestCompressAdaptiveStreamData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     []byte
		maxSize   int
		wantComp  bool
		wantBytes []byte
	}{
		{
			name:      "small stays raw",
			input:     []byte("abc"),
			maxSize:   4096,
			wantComp:  false,
			wantBytes: []byte("abc"),
		},
		{
			name:     "repetitive compresses",
			input:    bytes.Repeat([]byte("aaaaabbbbbccccc"), 64),
			maxSize:  4096,
			wantComp: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotBytes, gotComp, err := compressAdaptiveStreamData(tc.input, tc.maxSize)
			if err != nil {
				t.Fatalf("compressAdaptiveStreamData() error: %v", err)
			}
			if gotComp != tc.wantComp {
				t.Fatalf("compressed=%v, want %v", gotComp, tc.wantComp)
			}
			if tc.wantComp {
				if len(gotBytes) >= len(tc.input) {
					t.Fatalf("compressed size=%v, want smaller than %v", len(gotBytes), len(tc.input))
				}
				return
			}
			if !bytes.Equal(gotBytes, tc.wantBytes) {
				t.Fatalf("bytes=%q, want %q", string(gotBytes), string(tc.wantBytes))
			}
		})
	}
}

type recordingSender struct {
	mu       sync.Mutex
	msgs     []rns.Message
	failOnce bool
}

func (s *recordingSender) Send(msg rns.Message) (*rns.Envelope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOnce {
		s.failOnce = false
		return nil, errors.New("forced send failure")
	}
	s.msgs = append(s.msgs, msg)
	return nil, nil
}

type failingSender struct{}

func (s *failingSender) Send(msg rns.Message) (*rns.Envelope, error) {
	return nil, errors.New("forced send failure")
}

func (s *recordingSender) messages() []rns.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]rns.Message, len(s.msgs))
	copy(out, s.msgs)
	return out
}

func TestInitiatorSessionVersionAck(t *testing.T) {
	t.Parallel()

	s := newInitiatorChannelSession(io.Discard, io.Discard)
	if !s.handleMessage(&versionInfoMessage{SoftwareVersion: "x", ProtocolVersion: protocolVersion}) {
		t.Fatal("expected version message handled")
	}

	select {
	case <-s.versionAckCh:
	default:
		t.Fatal("expected version ack signal")
	}

	if s.state != initiatorWaitExit {
		t.Fatalf("state=%v, want initiatorWaitExit", s.state)
	}
}

func TestInitiatorSessionRejectsIncompatibleVersion(t *testing.T) {
	t.Parallel()

	s := newInitiatorChannelSession(io.Discard, io.Discard)
	s.handleMessage(&versionInfoMessage{SoftwareVersion: "x", ProtocolVersion: protocolVersion + 1})

	select {
	case err := <-s.errCh:
		if !errors.Is(err, errors.New("incompatible protocol")) && err.Error() != "incompatible protocol" {
			t.Fatalf("unexpected error: %v", err)
		}
	default:
		t.Fatal("expected protocol error")
	}
}

func TestInitiatorSessionCollectsStreamsAndExit(t *testing.T) {
	t.Parallel()

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() stdout error: %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() stderr error: %v", err)
	}
	s := newInitiatorChannelSession(stdoutWriter, stderrWriter)
	s.state = initiatorWaitExit
	t.Cleanup(func() {
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
		_ = stdoutReader.Close()
		_ = stderrReader.Close()
	})

	if !s.handleMessage(&streamDataMessage{StreamID: streamIDStdout, Data: []byte("out")}) {
		t.Fatal("stdout stream not handled")
	}
	stdoutData := make([]byte, 3)
	if _, err := io.ReadFull(stdoutReader, stdoutData); err != nil {
		t.Fatalf("stdout ReadFull error: %v", err)
	}
	if string(stdoutData) != "out" {
		t.Fatalf("stdout data=%q, want out", string(stdoutData))
	}
	select {
	case code := <-s.doneCh:
		t.Fatalf("doneCh fired early with code %v", code)
	default:
	}

	if !s.handleMessage(&streamDataMessage{StreamID: streamIDStderr, Data: []byte("err")}) {
		t.Fatal("stderr stream not handled")
	}
	stderrData := make([]byte, 3)
	if _, err := io.ReadFull(stderrReader, stderrData); err != nil {
		t.Fatalf("stderr ReadFull error: %v", err)
	}
	if string(stderrData) != "err" {
		t.Fatalf("stderr data=%q, want err", string(stderrData))
	}
	select {
	case code := <-s.doneCh:
		t.Fatalf("doneCh fired early with code %v", code)
	default:
	}

	if !s.handleMessage(&commandExitedMessage{ReturnCode: 7}) {
		t.Fatal("command exited not handled")
	}

	if got := s.stdout.String(); got != "out" {
		t.Fatalf("stdout=%q, want out", got)
	}
	if got := s.stderr.String(); got != "err" {
		t.Fatalf("stderr=%q, want err", got)
	}

	select {
	case code := <-s.doneCh:
		if code != 7 {
			t.Fatalf("exit code=%v, want 7", code)
		}
	default:
		t.Fatal("expected done signal")
	}
}

func TestBuildExecuteCommandMessageNoTTY(t *testing.T) {
	setIsTTYFileForTest(t, func(*os.File) bool { return true })

	opts := options{noTTY: true, commandLine: []string{"/bin/sh", "-lc", "echo hi"}}
	msg := buildExecuteCommandMessage(opts)

	if !msg.PipeStdin || !msg.PipeStdout || !msg.PipeStderr {
		t.Fatalf("expected pipe mode enabled, got %+v", msg)
	}
	if msg.Term != nil || msg.Rows != nil || msg.Cols != nil {
		t.Fatalf("expected nil term/size in no-tty mode, got term=%v rows=%v cols=%v", msg.Term, msg.Rows, msg.Cols)
	}
}

func TestBuildExecuteCommandMessageTTYFromEnv(t *testing.T) {
	setIsTTYFileForTest(t, func(*os.File) bool { return true })

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LINES", "48")
	t.Setenv("COLUMNS", "160")

	opts := options{noTTY: false, commandLine: []string{"/bin/sh"}}
	msg := buildExecuteCommandMessage(opts)

	if msg.PipeStdin || msg.PipeStdout || msg.PipeStderr {
		t.Fatalf("expected tty mode (pipe false), got %+v", msg)
	}
	if msg.Term == nil || *msg.Term != "xterm-256color" {
		t.Fatalf("unexpected term=%v", msg.Term)
	}
	if msg.Rows == nil || *msg.Rows != 48 {
		t.Fatalf("unexpected rows=%v", msg.Rows)
	}
	if msg.Cols == nil || *msg.Cols != 160 {
		t.Fatalf("unexpected cols=%v", msg.Cols)
	}
}

func TestBuildExecuteCommandMessageTTYUsesDefaultTERM(t *testing.T) {
	setIsTTYFileForTest(t, func(*os.File) bool { return true })

	t.Setenv("TERM", "")
	t.Setenv("LINES", "25")
	t.Setenv("COLUMNS", "90")

	opts := options{noTTY: false, commandLine: []string{"/bin/sh"}}
	msg := buildExecuteCommandMessage(opts)

	if msg.Term == nil || *msg.Term != "xterm" {
		t.Fatalf("unexpected default term=%v", msg.Term)
	}
	if msg.Rows == nil || *msg.Rows != 25 {
		t.Fatalf("unexpected rows=%v", msg.Rows)
	}
	if msg.Cols == nil || *msg.Cols != 90 {
		t.Fatalf("unexpected cols=%v", msg.Cols)
	}
}

func TestBuildExecuteCommandMessageAutoDetectsPerStreamPipes(t *testing.T) {
	setIsTTYFileForTest(t, func(file *os.File) bool {
		return file == initiatorStdinFile
	})

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LINES", "48")
	t.Setenv("COLUMNS", "160")

	opts := options{noTTY: false, commandLine: []string{"/bin/sh"}}
	msg := buildExecuteCommandMessage(opts)

	if msg.PipeStdin {
		t.Fatalf("expected stdin tty mode, got %+v", msg)
	}
	if !msg.PipeStdout || !msg.PipeStderr {
		t.Fatalf("expected stdout/stderr pipe mode, got %+v", msg)
	}
	if msg.Term == nil || *msg.Term != "xterm-256color" {
		t.Fatalf("unexpected term=%v", msg.Term)
	}
}

func TestOptionalIntHelpers(t *testing.T) {
	t.Parallel()

	if !optionalIntEqual(nil, nil) {
		t.Fatal("nil should equal nil")
	}
	one := 1
	two := 2
	if optionalIntEqual(&one, &two) {
		t.Fatal("different values should not be equal")
	}
	copyOne := cloneOptionalInt(&one)
	if copyOne == nil || *copyOne != one {
		t.Fatalf("cloneOptionalInt failed: %v", copyOne)
	}
}

func TestInitiatorSessionNonFatalErrorBecomesWarning(t *testing.T) {
	t.Parallel()

	s := newInitiatorChannelSession(io.Discard, io.Discard)
	s.state = initiatorWaitExit
	if !s.handleMessage(&errorMessage{Message: "temporary issue", Fatal: false}) {
		t.Fatal("expected non-fatal error message handled")
	}

	if got := s.stderr.String(); got != "remote warning: temporary issue\n" {
		t.Fatalf("stderr=%q", got)
	}

	select {
	case err := <-s.errCh:
		t.Fatalf("did not expect fatal error signal, got %v", err)
	default:
	}
}

func TestInitiatorSessionFatalErrorTerminates(t *testing.T) {
	t.Parallel()

	s := newInitiatorChannelSession(io.Discard, io.Discard)
	s.state = initiatorWaitExit
	if !s.handleMessage(&errorMessage{Message: "fatal issue", Fatal: true}) {
		t.Fatal("expected fatal error message handled")
	}
	if !s.terminated {
		t.Fatal("expected session terminated")
	}
	if s.lastErr == nil || s.lastErr.Error() != "remote error: fatal issue" {
		t.Fatalf("unexpected lastErr: %v", s.lastErr)
	}
}

func TestInitiatorSessionFatalErrorPrecedesCommandExit(t *testing.T) {
	t.Parallel()

	s := newInitiatorChannelSession(io.Discard, io.Discard)
	s.state = initiatorWaitExit

	if !s.handleMessage(&errorMessage{Message: "fatal issue", Fatal: true}) {
		t.Fatal("expected fatal error message handled")
	}
	if !s.handleMessage(&commandExitedMessage{ReturnCode: 9}) {
		t.Fatal("expected command exited handled")
	}

	if s.lastErr == nil || s.lastErr.Error() != "remote error: fatal issue" {
		t.Fatalf("unexpected lastErr: %v", s.lastErr)
	}
	if s.lastExit != nil {
		t.Fatalf("expected lastExit nil after fatal error, got %v", *s.lastExit)
	}

	select {
	case <-s.doneCh:
		t.Fatal("did not expect done signal after fatal error")
	default:
	}
}

func TestInitiatorSessionTracksExitCode(t *testing.T) {
	t.Parallel()

	s := newInitiatorChannelSession(io.Discard, io.Discard)
	s.state = initiatorWaitExit
	if !s.handleMessage(&commandExitedMessage{ReturnCode: 9}) {
		t.Fatal("expected command exited handled")
	}
	if s.lastExit == nil || *s.lastExit != 9 {
		t.Fatalf("unexpected lastExit: %v", s.lastExit)
	}
}

func TestInitiatorSessionAcceptsLateStreamAfterExit(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	s := newInitiatorChannelSession(&stdout, io.Discard)
	s.state = initiatorWaitExit

	if !s.handleMessage(&commandExitedMessage{ReturnCode: 0}) {
		t.Fatal("expected command exited handled")
	}
	if !s.handleMessage(&streamDataMessage{StreamID: streamIDStdout, Data: []byte("late"), EOF: true}) {
		t.Fatal("expected late stdout handled")
	}

	if got := stdout.String(); got != "late" {
		t.Fatalf("stdout=%q, want %q", got, "late")
	}
	if got := s.stdoutString(); got != "late" {
		t.Fatalf("session stdout=%q, want %q", got, "late")
	}
}

func TestInitiatorSessionTerminalSnapshotCopiesExit(t *testing.T) {
	t.Parallel()

	s := newInitiatorChannelSession(io.Discard, io.Discard)
	exit := 7
	s.lastExit = &exit
	s.terminated = true

	snapshot := s.terminalSnapshot()
	if snapshot.lastExit == nil || *snapshot.lastExit != 7 {
		t.Fatalf("unexpected snapshot lastExit=%v", snapshot.lastExit)
	}
	if !snapshot.terminated {
		t.Fatal("expected snapshot terminated")
	}

	*s.lastExit = 9
	if *snapshot.lastExit != 7 {
		t.Fatalf("snapshot exit mutated to %v", *snapshot.lastExit)
	}
}

func TestProcessInitiatorTTYInputChunk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      []byte
		wantOut    string
		wantStop   bool
		wantHelp   bool
		wantToggle bool
		wantLine   bool
	}{
		{name: "terminate", input: []byte("\r~."), wantOut: "\r", wantStop: true},
		{name: "literal tilde", input: []byte("\r~~"), wantOut: "\r~"},
		{name: "help", input: []byte("\r~?"), wantOut: "\r", wantHelp: true},
		{name: "toggle line mode", input: []byte("\r~L"), wantOut: "\r", wantToggle: true, wantLine: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := &initiatorTTYInputState{}
			gotOut, action := processInitiatorTTYInputChunk(tc.input, state)
			if string(gotOut) != tc.wantOut {
				t.Fatalf("output=%q, want %q", string(gotOut), tc.wantOut)
			}
			if action.stop != tc.wantStop {
				t.Fatalf("stop=%v, want %v", action.stop, tc.wantStop)
			}
			if action.help != tc.wantHelp {
				t.Fatalf("help=%v, want %v", action.help, tc.wantHelp)
			}
			if action.toggleLine != tc.wantToggle {
				t.Fatalf("toggleLine=%v, want %v", action.toggleLine, tc.wantToggle)
			}
			if state.lineMode != tc.wantLine {
				t.Fatalf("lineMode=%v, want %v", state.lineMode, tc.wantLine)
			}
		})
	}
}

func TestWriteInitiatorStreamsResetsBuffers(t *testing.T) {
	t.Parallel()

	s := newInitiatorChannelSession(io.Discard, io.Discard)
	s.stdout.WriteString("hello")
	s.stderr.WriteString("warn")

	writeInitiatorStreams(s)

	if s.stdout.Len() != 0 || s.stderr.Len() != 0 {
		t.Fatalf("buffers not reset: stdout=%v stderr=%v", s.stdout.Len(), s.stderr.Len())
	}
}

func TestSendMessageWithRetryEventuallySucceeds(t *testing.T) {
	t.Parallel()

	sender := &flakySender{failCount: 2}
	err := sendMessageWithRetry(sender, &noopMessage{}, time.Now().Add(200*time.Millisecond), 10*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected retry error: %v", err)
	}
	if sender.calls < 3 {
		t.Fatalf("expected retries, got calls=%v", sender.calls)
	}
}

func TestSendMessageWithRetryTimesOut(t *testing.T) {
	t.Parallel()

	sender := &flakySender{failCount: 1000}
	err := sendMessageWithRetry(sender, &noopMessage{}, time.Now().Add(20*time.Millisecond), 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRunInitiatorProtocolFlowSuccess(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			go fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			go func() {
				fake.emit(&streamDataMessage{StreamID: streamIDStdout, Data: []byte("ok"), EOF: true})
				fake.emit(&commandExitedMessage{ReturnCode: 3})
			}()
		}
	}

	opts := options{timeoutSec: 1, mirror: true, noTTY: true}
	code, session, err := runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err != nil {
		t.Fatalf("runInitiatorProtocolFlow error: %v", err)
	}
	if code != 3 {
		t.Fatalf("exit code=%v, want 3", code)
	}
	if session == nil || session.stdoutString() != "ok" {
		t.Fatalf("session stdout=%q", session.stdoutString())
	}
}

func TestRunInitiatorProtocolFlowLinkClosedWithoutOutputFails(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		if _, ok := msg.(*versionInfoMessage); ok {
			go fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
			go func() {
				linkClosedCh <- struct{}{}
			}()
		}
	}

	opts := options{timeoutSec: 1, mirror: false, noTTY: true}
	_, _, err := runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err == nil || err.Error() != "link closed before command completed" {
		t.Fatalf("unexpected err=%v", err)
	}
}

func TestRunInitiatorProtocolFlowLinkClosedWaitsForLateExitWithinGrace(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			go fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			go func() {
				linkClosedCh <- struct{}{}
				time.Sleep(20 * time.Millisecond)
				fake.emit(&commandExitedMessage{ReturnCode: 0})
			}()
		}
	}

	rt := &runtimeT{linkClosedGrace: 40 * time.Millisecond}
	opts := options{timeoutSec: 1, mirror: true, noTTY: true}
	code, _, err := rt.runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err != nil {
		t.Fatalf("runInitiatorProtocolFlow error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code=%v, want 0", code)
	}
}

func TestRunInitiatorProtocolFlowWaitsForLateStreamAfterExitWithinGrace(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			go fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			go func() {
				fake.emit(&commandExitedMessage{ReturnCode: 0})
				time.Sleep(20 * time.Millisecond)
				fake.emit(&streamDataMessage{StreamID: streamIDStdout, Data: []byte("late"), EOF: true})
			}()
		}
	}

	rt := &runtimeT{postExitDrainGrace: 40 * time.Millisecond, retrySleep: time.Millisecond}
	opts := options{timeoutSec: 1, mirror: false, noTTY: true}
	code, session, err := rt.runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err != nil {
		t.Fatalf("runInitiatorProtocolFlow error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code=%v, want 0", code)
	}
	if session == nil {
		t.Fatal("expected session")
	}
	if got := session.stdoutString(); got != "late" {
		t.Fatalf("session stdout=%q, want %q", got, "late")
	}
}

func TestRunInitiatorProtocolFlowLinkClosedAfterCompleteStreamEOFReturnsSuccess(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			go fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			go func() {
				fake.emit(&streamDataMessage{StreamID: streamIDStdout, Data: []byte("hello"), EOF: true})
				linkClosedCh <- struct{}{}
			}()
		}
	}

	rt := &runtimeT{linkClosedGrace: 40 * time.Millisecond}
	opts := options{timeoutSec: 1, mirror: false, noTTY: true}
	code, session, err := rt.runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err != nil {
		t.Fatalf("runInitiatorProtocolFlow error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code=%v, want 0", code)
	}
	if session == nil || session.stdoutString() != "hello" {
		t.Fatalf("session stdout=%q, want %q", session.stdoutString(), "hello")
	}
}

func TestRunInitiatorProtocolFlowLinkClosedBeforeStreamEOFReturnsSuccess(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			go fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			go func() {
				fake.emit(&streamDataMessage{StreamID: streamIDStdout, Data: []byte("partial"), EOF: false})
				linkClosedCh <- struct{}{}
			}()
		}
	}

	rt := &runtimeT{linkClosedGrace: 40 * time.Millisecond}
	opts := options{timeoutSec: 1, mirror: false, noTTY: true}
	code, session, err := rt.runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err != nil {
		t.Fatalf("runInitiatorProtocolFlow error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code=%v, want 0", code)
	}
	if session == nil || session.stdoutString() != "partial" {
		t.Fatalf("session stdout=%q, want %q", session.stdoutString(), "partial")
	}
}

func TestRunInitiatorProtocolFlowFatalErrorPrecedesExit(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			go fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			go func() {
				fake.emit(&errorMessage{Message: "fatal issue", Fatal: true})
				fake.emit(&commandExitedMessage{ReturnCode: 3})
			}()
		}
	}

	opts := options{timeoutSec: 1, mirror: true, noTTY: true}
	code, _, err := runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err == nil || err.Error() != "remote error: fatal issue" {
		t.Fatalf("unexpected err=%v", err)
	}
	if code != 1 {
		t.Fatalf("exit code=%v, want 1", code)
	}
}

func TestRunInitiatorProtocolFlowExitThenFatalStillReturnsFatal(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			fake.emit(&commandExitedMessage{ReturnCode: 3})
			fake.emit(&errorMessage{Message: "fatal issue", Fatal: true})
		}
	}

	opts := options{timeoutSec: 1, mirror: true, noTTY: true}
	code, _, err := runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err == nil || err.Error() != "remote error: fatal issue" {
		t.Fatalf("unexpected err=%v", err)
	}
	if code != 1 {
		t.Fatalf("exit code=%v, want 1", code)
	}
}

func TestRunInitiatorProtocolFlowFatalErrorPrecedesLinkClosed(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			go fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			go func() {
				fake.emit(&errorMessage{Message: "fatal issue", Fatal: true})
				linkClosedCh <- struct{}{}
			}()
		}
	}

	opts := options{timeoutSec: 1, mirror: true, noTTY: true}
	code, _, err := runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err == nil || err.Error() != "remote error: fatal issue" {
		t.Fatalf("unexpected err=%v", err)
	}
	if code != 1 {
		t.Fatalf("exit code=%v, want 1", code)
	}
}

func TestRunInitiatorProtocolFlowWarningThenExitMirror(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			go fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			go func() {
				fake.emit(&errorMessage{Message: "temporary issue", Fatal: false})
				fake.emit(&commandExitedMessage{ReturnCode: 4})
			}()
		}
	}

	opts := options{timeoutSec: 1, mirror: true, noTTY: true}
	code, session, err := runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err != nil {
		t.Fatalf("runInitiatorProtocolFlow error: %v", err)
	}
	if code != 4 {
		t.Fatalf("exit code=%v, want 4", code)
	}
	if session == nil {
		t.Fatal("expected non-nil session")
	}
	if got := session.stderrString(); got != "remote warning: temporary issue\n" {
		t.Fatalf("stderr=%q", got)
	}
}

func TestRunInitiatorProtocolFlowWarningThenExitNoMirror(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			go fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			go func() {
				fake.emit(&errorMessage{Message: "temporary issue", Fatal: false})
				fake.emit(&commandExitedMessage{ReturnCode: 7})
			}()
		}
	}

	opts := options{timeoutSec: 1, mirror: false, noTTY: true}
	code, session, err := runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err != nil {
		t.Fatalf("runInitiatorProtocolFlow error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code=%v, want 0", code)
	}
	if session == nil {
		t.Fatal("expected non-nil session")
	}
	if got := session.stderrString(); got != "remote warning: temporary issue\n" {
		t.Fatalf("stderr=%q", got)
	}
}

func TestRunInitiatorProtocolFlowTTYExecuteMessageIncludesTerminalMetadata(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LINES", "44")
	t.Setenv("COLUMNS", "132")
	setIsTTYFileForTest(t, func(*os.File) bool { return true })

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}

	var sentExecute *executeCommandMessage
	fake.onSend = func(msg rns.Message) {
		switch typed := msg.(type) {
		case *versionInfoMessage:
			fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			copyMsg := *typed
			sentExecute = &copyMsg
			fake.emit(&commandExitedMessage{ReturnCode: 0})
		}
	}

	opts := options{timeoutSec: 1, mirror: true, noTTY: false, commandLine: []string{"/bin/sh", "-lc", "echo hi"}}
	code, _, err := runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err != nil {
		t.Fatalf("runInitiatorProtocolFlow error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code=%v, want 0", code)
	}
	if sentExecute == nil {
		t.Fatal("expected execute command message to be sent")
	}
	if sentExecute.PipeStdin || sentExecute.PipeStdout || sentExecute.PipeStderr {
		t.Fatalf("expected tty (non-pipe) execute flags, got %+v", sentExecute)
	}
	if sentExecute.Term == nil || *sentExecute.Term != "xterm-256color" {
		t.Fatalf("unexpected term=%v", sentExecute.Term)
	}
	if sentExecute.Rows == nil || *sentExecute.Rows != 44 {
		t.Fatalf("unexpected rows=%v", sentExecute.Rows)
	}
	if sentExecute.Cols == nil || *sentExecute.Cols != 132 {
		t.Fatalf("unexpected cols=%v", sentExecute.Cols)
	}
}

func TestPumpWindowSizeUpdatesSendsOnChange(t *testing.T) {
	t.Setenv("LINES", "24")
	t.Setenv("COLUMNS", "80")

	sender := &recordingSender{}
	stopCh := make(chan struct{})
	done := make(chan struct{})

	go func() {
		pumpWindowSizeUpdates(sender, stopCh, 10*time.Millisecond, nil, nil)
		close(done)
	}()

	time.Sleep(25 * time.Millisecond)
	t.Setenv("LINES", "40")
	t.Setenv("COLUMNS", "100")

	time.Sleep(35 * time.Millisecond)
	close(stopCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pumpWindowSizeUpdates did not stop")
	}

	msgs := sender.messages()
	if len(msgs) == 0 {
		t.Fatal("expected at least one windowSizeMessage")
	}

	foundUpdated := false
	for _, msg := range msgs {
		wm, ok := msg.(*windowSizeMessage)
		if !ok {
			continue
		}
		if wm.Rows != nil && wm.Cols != nil && *wm.Rows == 40 && *wm.Cols == 100 {
			foundUpdated = true
			break
		}
	}
	if !foundUpdated {
		t.Fatalf("expected updated window size message, got %v messages", len(msgs))
	}
}

func TestPumpWindowSizeUpdatesStopsOnSendError(t *testing.T) {
	t.Setenv("LINES", "31")
	t.Setenv("COLUMNS", "91")

	sender := &failingSender{}
	stopCh := make(chan struct{})
	done := make(chan struct{})
	initialRows := 30
	initialCols := 90

	go func() {
		pumpWindowSizeUpdates(sender, stopCh, 10*time.Millisecond, &initialRows, &initialCols)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("expected pumpWindowSizeUpdates to stop after send failure")
	}
}

func TestConcurrentPumpInitiatorStdinEOFAndWindowUpdates(t *testing.T) {
	t.Setenv("LINES", "24")
	t.Setenv("COLUMNS", "80")

	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	os.Stdin = reader
	defer func() {
		os.Stdin = oldStdin
	}()

	sender := &recordingSender{}
	stopCh := make(chan struct{})
	stdinDone := make(chan struct{})
	windowDone := make(chan struct{})

	rt := &runtimeT{}
	go func() {
		rt.pumpInitiatorStdin(sender)
		close(stdinDone)
	}()

	initialRows := 23
	initialCols := 79
	go func() {
		pumpWindowSizeUpdates(sender, stopCh, 10*time.Millisecond, &initialRows, &initialCols)
		close(windowDone)
	}()

	_, _ = writer.WriteString("hello")
	_ = writer.Close()

	select {
	case <-stdinDone:
	case <-time.After(2 * time.Second):
		t.Fatal("pumpInitiatorStdin did not finish")
	}

	t.Setenv("LINES", "40")
	t.Setenv("COLUMNS", "100")
	time.Sleep(40 * time.Millisecond)
	close(stopCh)

	select {
	case <-windowDone:
	case <-time.After(time.Second):
		t.Fatal("pumpWindowSizeUpdates did not stop")
	}

	msgs := sender.messages()
	var sawStdinData bool
	var sawStdinEOF bool
	var sawWindowUpdate bool

	for _, msg := range msgs {
		switch typed := msg.(type) {
		case *streamDataMessage:
			if typed.StreamID == streamIDStdin && string(typed.Data) == "hello" {
				sawStdinData = true
			}
			if typed.StreamID == streamIDStdin && typed.EOF {
				sawStdinEOF = true
			}
		case *windowSizeMessage:
			if typed.Rows != nil && typed.Cols != nil && *typed.Rows == 40 && *typed.Cols == 100 {
				sawWindowUpdate = true
			}
		}
	}

	if !sawStdinData {
		t.Fatal("expected stdin data message")
	}
	if !sawStdinEOF {
		t.Fatal("expected stdin EOF message")
	}
	if !sawWindowUpdate {
		t.Fatal("expected window size update message")
	}
}

func TestPumpWindowSizeUpdatesMultipleSequentialChanges(t *testing.T) {
	t.Setenv("LINES", "24")
	t.Setenv("COLUMNS", "80")

	sender := &recordingSender{}
	stopCh := make(chan struct{})
	done := make(chan struct{})
	initialRows := 23
	initialCols := 79

	go func() {
		pumpWindowSizeUpdates(sender, stopCh, 10*time.Millisecond, &initialRows, &initialCols)
		close(done)
	}()

	t.Setenv("LINES", "41")
	t.Setenv("COLUMNS", "101")
	time.Sleep(50 * time.Millisecond)
	t.Setenv("LINES", "42")
	t.Setenv("COLUMNS", "102")
	time.Sleep(50 * time.Millisecond)
	close(stopCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pumpWindowSizeUpdates did not stop")
	}

	msgs := sender.messages()
	sawFirst := false
	sawSecond := false
	for _, msg := range msgs {
		wm, ok := msg.(*windowSizeMessage)
		if !ok || wm.Rows == nil || wm.Cols == nil {
			continue
		}
		if *wm.Rows == 41 && *wm.Cols == 101 {
			sawFirst = true
		}
		if *wm.Rows == 42 && *wm.Cols == 102 {
			sawSecond = true
		}
	}

	if !sawFirst {
		t.Fatalf("expected first sequential resize update, got %v messages", len(msgs))
	}
	if !sawSecond {
		t.Fatalf("expected second sequential resize update, got %v messages", len(msgs))
	}
}

func TestRunInitiatorProtocolFlowMirrorOrderingJitterFatalFirst(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			runTimedEvents(fake, linkClosedCh,
				timedEvent{delay: 5 * time.Millisecond, send: func(ch *fakeChannelSession, _ chan<- struct{}) {
					ch.emit(&errorMessage{Message: "fatal issue", Fatal: true})
				}},
				timedEvent{delay: 10 * time.Millisecond, send: func(ch *fakeChannelSession, _ chan<- struct{}) {
					ch.emit(&commandExitedMessage{ReturnCode: 9})
				}},
				timedEvent{delay: 15 * time.Millisecond, send: func(_ *fakeChannelSession, closed chan<- struct{}) {
					closed <- struct{}{}
				}},
			)
		}
	}

	opts := options{timeoutSec: 1, mirror: true, noTTY: true}
	code, _, err := runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err == nil || err.Error() != "remote error: fatal issue" {
		t.Fatalf("unexpected err=%v", err)
	}
	if code != 1 {
		t.Fatalf("exit code=%v, want 1", code)
	}
}

func TestRunInitiatorProtocolFlowMirrorOrderingJitterExitFirst(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			runTimedEvents(fake, linkClosedCh,
				timedEvent{delay: 5 * time.Millisecond, send: func(ch *fakeChannelSession, _ chan<- struct{}) {
					ch.emit(&commandExitedMessage{ReturnCode: 4})
				}},
				timedEvent{delay: 10 * time.Millisecond, send: func(ch *fakeChannelSession, _ chan<- struct{}) {
					ch.emit(&errorMessage{Message: "fatal issue", Fatal: true})
				}},
				timedEvent{delay: 15 * time.Millisecond, send: func(_ *fakeChannelSession, closed chan<- struct{}) {
					closed <- struct{}{}
				}},
			)
		}
	}

	opts := options{timeoutSec: 1, mirror: true, noTTY: true}
	code, _, err := runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err != nil {
		t.Fatalf("runInitiatorProtocolFlow error: %v", err)
	}
	if code != 4 {
		t.Fatalf("exit code=%v, want 4", code)
	}
}

func TestRunInitiatorProtocolFlowRetryWarningsThenExitMirror(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			runTimedEvents(fake, linkClosedCh,
				timedEvent{delay: 5 * time.Millisecond, send: func(ch *fakeChannelSession, _ chan<- struct{}) {
					ch.emit(&errorMessage{Message: "retry attempt 1", Fatal: false})
				}},
				timedEvent{delay: 10 * time.Millisecond, send: func(ch *fakeChannelSession, _ chan<- struct{}) {
					ch.emit(&errorMessage{Message: "retry attempt 2", Fatal: false})
				}},
				timedEvent{delay: 15 * time.Millisecond, send: func(ch *fakeChannelSession, _ chan<- struct{}) {
					ch.emit(&commandExitedMessage{ReturnCode: 6})
				}},
			)
		}
	}

	opts := options{timeoutSec: 1, mirror: true, noTTY: true}
	code, session, err := runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err != nil {
		t.Fatalf("runInitiatorProtocolFlow error: %v", err)
	}
	if code != 6 {
		t.Fatalf("exit code=%v, want 6", code)
	}
	if session == nil {
		t.Fatal("expected non-nil session")
	}
	// Allow remaining timed-event goroutines to deliver messages.
	time.Sleep(50 * time.Millisecond)
	wantStderr := "remote warning: retry attempt 1\nremote warning: retry attempt 2\n"
	if got := session.stderrString(); got != wantStderr {
		t.Fatalf("stderr=%q, want %q", got, wantStderr)
	}
}

func TestRunInitiatorProtocolFlowRetryWarningsThenFatalMirror(t *testing.T) {
	t.Parallel()

	linkClosedCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	defer close(stopCh)

	fake := &fakeChannelSession{}
	fake.onSend = func(msg rns.Message) {
		switch msg.(type) {
		case *versionInfoMessage:
			fake.emit(&versionInfoMessage{SoftwareVersion: "listener", ProtocolVersion: protocolVersion})
		case *executeCommandMessage:
			runTimedEvents(fake, linkClosedCh,
				timedEvent{delay: 5 * time.Millisecond, send: func(ch *fakeChannelSession, _ chan<- struct{}) {
					ch.emit(&errorMessage{Message: "retry attempt 1", Fatal: false})
				}},
				timedEvent{delay: 10 * time.Millisecond, send: func(ch *fakeChannelSession, _ chan<- struct{}) {
					ch.emit(&errorMessage{Message: "fatal issue", Fatal: true})
				}},
				timedEvent{delay: 15 * time.Millisecond, send: func(ch *fakeChannelSession, _ chan<- struct{}) {
					ch.emit(&commandExitedMessage{ReturnCode: 6})
				}},
			)
		}
	}

	opts := options{timeoutSec: 1, mirror: true, noTTY: true}
	code, session, err := runInitiatorProtocolFlow(fake, opts, linkClosedCh, stopCh, false)
	if err == nil || err.Error() != "remote error: fatal issue" {
		t.Fatalf("unexpected err=%v", err)
	}
	if code != 1 {
		t.Fatalf("exit code=%v, want 1", code)
	}
	if session == nil {
		t.Fatal("expected non-nil session")
	}
	// Allow remaining timed-event goroutines to deliver messages.
	time.Sleep(50 * time.Millisecond)
	wantPrefix := "remote warning: retry attempt 1\n"
	if got := session.stderrString(); got != wantPrefix {
		t.Fatalf("stderr=%q, want %q", got, wantPrefix)
	}
}

func runTimedEvents(fake *fakeChannelSession, linkClosedCh chan<- struct{}, events ...timedEvent) {
	// Sort events by delay to ensure we process them in order.
	slices.SortFunc(events, func(a, b timedEvent) int {
		return cmp.Compare(a.delay, b.delay)
	})

	go func() {
		var lastDelay time.Duration
		for _, ev := range events {
			time.Sleep(ev.delay - lastDelay)
			ev.send(fake, linkClosedCh)
			lastDelay = ev.delay
		}
	}()
}
