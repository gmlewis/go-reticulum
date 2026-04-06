// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type closeErrorWriteCloser struct{}

func (c *closeErrorWriteCloser) Write(p []byte) (int, error) {
	return len(p), nil
}

func (c *closeErrorWriteCloser) Close() error {
	return errors.New("close failed")
}

type fakeSender struct {
	mu   sync.Mutex
	msgs []rns.Message
}

func (f *fakeSender) Send(msg rns.Message) (*rns.Envelope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, msg)
	return nil, nil
}

func (f *fakeSender) messages() []rns.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]rns.Message, len(f.msgs))
	copy(out, f.msgs)
	return out
}

func TestStreamPipeSendsDataAndEOF(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	reader := io.NopCloser(bytesReader("hello"))
	streamPipe(rns.NewLogger(), sender, reader, streamIDStdout)

	msgs := sender.messages()
	if len(msgs) != 2 {
		t.Fatalf("message count=%v, want 2", len(msgs))
	}

	dataMsg, ok := msgs[0].(*streamDataMessage)
	if !ok {
		t.Fatalf("first message type=%T", msgs[0])
	}
	if string(dataMsg.Data) != "hello" || dataMsg.EOF {
		t.Fatalf("unexpected data msg: %+v", dataMsg)
	}

	eofMsg, ok := msgs[1].(*streamDataMessage)
	if !ok {
		t.Fatalf("second message type=%T", msgs[1])
	}
	if !eofMsg.EOF {
		t.Fatalf("expected EOF message, got %+v", eofMsg)
	}
}

func TestActiveCommandWriteStdin(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	cmd := &activeCommand{stdin: writer}

	readDone := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 3)
		_, _ = io.ReadFull(reader, buf)
		readDone <- buf
	}()

	if err := cmd.writeStdin([]byte("abc"), false); err != nil {
		t.Fatalf("writeStdin error: %v", err)
	}

	select {
	case got := <-readDone:
		if string(got) != "abc" {
			t.Fatalf("stdin read=%q", string(got))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for stdin read")
	}

	if err := cmd.writeStdin(nil, true); err != nil {
		t.Fatalf("writeStdin eof error: %v", err)
	}

	if err := cmd.writeStdin([]byte("x"), false); err == nil {
		t.Fatal("expected closed stdin error")
	}
}

func TestActiveCommandWriteStdinClosedErrorType(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	_ = reader.Close()
	cmd := &activeCommand{stdin: writer, closed: true}
	err := cmd.writeStdin([]byte("x"), false)
	if !errors.Is(err, errStdinClosed) {
		t.Fatalf("expected errStdinClosed, got %v", err)
	}
}

func TestActiveCommandWriteStdinClosedPipeErrorType(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	_ = reader.Close()
	cmd := &activeCommand{stdin: writer}
	err := cmd.writeStdin([]byte("x"), false)
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("expected io.ErrClosedPipe, got %v", err)
	}
}

func TestActiveCommandCloseKillsWhenNotFinished(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	defer func() {
		if err := reader.Close(); err != nil {
			t.Fatalf("reader close failed: %v", err)
		}
	}()

	killed := 0
	cmd := &activeCommand{
		stdin: writer,
		kill: func() error {
			killed++
			return nil
		},
	}

	cmd.close()
	if killed != 1 {
		t.Fatalf("kill called %v times, want 1", killed)
	}

	cmd.close()
	if killed != 1 {
		t.Fatalf("kill called %v times after repeated close, want 1", killed)
	}
}

func TestActiveCommandCloseDoesNotKillWhenFinished(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	defer func() {
		if err := reader.Close(); err != nil {
			t.Fatalf("reader close failed: %v", err)
		}
	}()

	killed := 0
	cmd := &activeCommand{
		stdin: writer,
		kill: func() error {
			killed++
			return nil
		},
	}
	cmd.markFinished()
	cmd.close()

	if killed != 0 {
		t.Fatalf("kill called %v times, want 0", killed)
	}
}

func TestActiveCommandCloseLogsThroughInjectedLogger(t *testing.T) {
	t.Parallel()

	var captured string
	logger := rns.NewLogger()
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(msg string) {
		captured += msg
	})
	logger.SetLogLevel(rns.LogWarning)

	cmd := &activeCommand{
		stdin:  &closeErrorWriteCloser{},
		kill:   func() error { return errors.New("kill failed") },
		logger: logger,
	}

	cmd.close()

	if !strings.Contains(captured, "Warning: Could not close stdin for active command") {
		t.Fatalf("missing stdin close warning in %q", captured)
	}
	if !strings.Contains(captured, "Warning: Could not kill active command properly") {
		t.Fatalf("missing kill warning in %q", captured)
	}
}

func TestSendProtocolErrorToSenderLogsThroughInjectedLogger(t *testing.T) {
	t.Parallel()

	var captured string
	logger := rns.NewLogger()
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(msg string) {
		captured += msg
	})
	logger.SetLogLevel(rns.LogWarning)

	sender := &failingSender{}
	sendProtocolErrorToSender(logger, sender, "boom", true)

	if !strings.Contains(captured, "Failed to send protocol error") {
		t.Fatalf("missing protocol error warning in %q", captured)
	}
}

func TestRunInitiatorSessionHandlesFatalError(t *testing.T) {
	t.Parallel()

	s := newInitiatorChannelSession()
	s.state = initiatorWaitExit
	if !s.handleMessage(&errorMessage{Message: "boom", Fatal: true}) {
		t.Fatal("expected error message handled")
	}
	select {
	case err := <-s.errCh:
		if err.Error() != "remote error: boom" {
			t.Fatalf("unexpected error: %v", err)
		}
	default:
		t.Fatal("expected fatal error signal")
	}
}

func TestBuildSessionCommandEnv(t *testing.T) {
	t.Parallel()

	term := "xterm-256color"
	rows := 40
	cols := 120
	execMsg := &executeCommandMessage{Term: &term, Rows: &rows, Cols: &cols}

	env := buildSessionCommandEnv([]string{"PATH=/usr/bin"}, nil, execMsg)
	joined := ""
	for _, value := range env {
		joined += value + "\n"
	}

	if !containsLine(joined, "TERM=xterm-256color") {
		t.Fatalf("expected TERM in env, got %q", joined)
	}
	if !containsLine(joined, "LINES=40") {
		t.Fatalf("expected LINES in env, got %q", joined)
	}
	if !containsLine(joined, "COLUMNS=120") {
		t.Fatalf("expected COLUMNS in env, got %q", joined)
	}
}

func TestBuildSessionCommandEnvOverridesExistingValues(t *testing.T) {
	t.Parallel()

	term := "xterm-256color"
	rows := 40
	cols := 120
	execMsg := &executeCommandMessage{Term: &term, Rows: &rows, Cols: &cols}

	base := []string{
		"PATH=/usr/bin",
		"TERM=dumb",
		"LINES=1",
		"COLUMNS=2",
		"RNS_REMOTE_IDENTITY=old",
		"TERM=stale-dup",
	}

	env := buildSessionCommandEnv(base, &rns.Identity{HexHash: "beef"}, execMsg)

	if countKey(env, "TERM") != 1 {
		t.Fatalf("TERM count=%v, want 1 (%v)", countKey(env, "TERM"), env)
	}
	if countKey(env, "LINES") != 1 {
		t.Fatalf("LINES count=%v, want 1 (%v)", countKey(env, "LINES"), env)
	}
	if countKey(env, "COLUMNS") != 1 {
		t.Fatalf("COLUMNS count=%v, want 1 (%v)", countKey(env, "COLUMNS"), env)
	}
	if countKey(env, "RNS_REMOTE_IDENTITY") != 1 {
		t.Fatalf("RNS_REMOTE_IDENTITY count=%v, want 1 (%v)", countKey(env, "RNS_REMOTE_IDENTITY"), env)
	}

	if !hasExact(env, "TERM=xterm-256color") {
		t.Fatalf("missing TERM override in env: %v", env)
	}
	if !hasExact(env, "LINES=40") {
		t.Fatalf("missing LINES override in env: %v", env)
	}
	if !hasExact(env, "COLUMNS=120") {
		t.Fatalf("missing COLUMNS override in env: %v", env)
	}
	if !hasExact(env, "RNS_REMOTE_IDENTITY=beef") {
		t.Fatalf("missing RNS_REMOTE_IDENTITY override in env: %v", env)
	}
}

func TestNormalizeCommandStartError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil error", err: nil, want: "command start failed"},
		{name: "plain error", err: errors.New("executable file not found"), want: "command start failed: executable file not found"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeCommandStartError(tc.err)
			if got != tc.want {
				t.Fatalf("normalizeCommandStartError()=%q, want %q", got, tc.want)
			}
		})
	}
}

func containsLine(all, line string) bool {
	return len(all) >= len(line) && (all == line+"\n" || containsSubstring(all, line+"\n"))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func countKey(env []string, key string) int {
	prefix := key + "="
	count := 0
	for _, entry := range env {
		if len(entry) >= len(prefix) && entry[:len(prefix)] == prefix {
			count++
		}
	}
	return count
}

func hasExact(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}

type stringReader struct {
	data []byte
	idx  int
}

func bytesReader(s string) *stringReader {
	return &stringReader{data: []byte(s)}
}

func (r *stringReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.idx:])
	r.idx += n
	return n, nil
}
