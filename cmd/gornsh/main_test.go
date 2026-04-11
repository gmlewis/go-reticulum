// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bufio"
	"bytes"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

const tempDirPrefix = "gornsh-test-"

func TestRunVersionOutput(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"--version"}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %v, want 0", exitCode)
	}
	if got, want := stdout.String(), "gornsh "+rns.VERSION+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunHelpOutput(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"--help"}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %v, want 0", exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	got := stdout.String()
	for _, want := range []string{"--listen", "--config", "--identity", "--service", "--announce", "--allowed", "--no-auth", "--no-id", "--mirror", "--timeout", "--no-tty", "--remote-command-as-args", "--no-remote-command", "--print-identity"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help output missing %q in:\n%v", want, got)
		}
	}
}

func TestRunUnknownFlag(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{"--unknown-flag-xyz"}, strings.NewReader(""), &stdout, &stderr)

	if exitCode == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if stderr.Len() == 0 {
		t.Fatal("stderr = empty, want error output")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunNoArguments(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run([]string{}, strings.NewReader(""), &stdout, &stderr)

	if exitCode == 0 {
		t.Fatal("exit code = 0, want non-zero")
	}
	if stdout.Len() == 0 && stderr.Len() == 0 {
		t.Fatal("expected usage output on stdout or stderr")
	}
	if !strings.Contains(stdout.String()+stderr.String(), "gornsh") {
		t.Fatalf("usage output missing gornsh hint:\nstdout=%q\nstderr=%q", stdout.String(), stderr.String())
	}
}

func TestParseAllowedIdentityHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantOK  bool
		wantLen int
	}{
		{name: "valid lowercase", input: "00112233445566778899aabbccddeeff", wantOK: true, wantLen: 16},
		{name: "valid uppercase", input: "00112233445566778899AABBCCDDEEFF", wantOK: true, wantLen: 16},
		{name: "invalid hex", input: "not-hex", wantOK: false, wantLen: 0},
		{name: "wrong length short", input: "0011", wantOK: false, wantLen: 0},
		{name: "empty", input: "", wantOK: false, wantLen: 0},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseAllowedIdentityHash(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tc.wantOK)
			}
			if len(got) != tc.wantLen {
				t.Fatalf("len=%v, want %v", len(got), tc.wantLen)
			}
		})
	}
}

func TestSplitAllowedFile(t *testing.T) {
	t.Parallel()

	input := "# comment\n  \n00112233445566778899aabbccddeeff\n aabbccddeeff00112233445566778899 \n"
	got := splitAllowedFile(input)
	want := []string{"00112233445566778899aabbccddeeff", "aabbccddeeff00112233445566778899"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitAllowedFile()=%v, want %v", got, want)
	}
}

func TestParseCommandResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		response   any
		wantExit   int
		wantStdout string
		wantStderr string
		wantErr    bool
	}{
		{
			name:       "valid response",
			response:   []any{true, int64(7), []byte("out"), []byte("err")},
			wantExit:   7,
			wantStdout: "out",
			wantStderr: "err",
		},
		{
			name:     "invalid response type",
			response: map[string]any{"bad": true},
			wantErr:  true,
		},
		{
			name:     "invalid exit code",
			response: []any{true, "nope", []byte("out"), []byte("err")},
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exitCode, stdout, stderr, err := parseCommandResponse(tc.response)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if exitCode != tc.wantExit {
				t.Fatalf("exitCode=%v, want %v", exitCode, tc.wantExit)
			}
			if string(stdout) != tc.wantStdout {
				t.Fatalf("stdout=%q, want %q", string(stdout), tc.wantStdout)
			}
			if string(stderr) != tc.wantStderr {
				t.Fatalf("stderr=%q, want %q", string(stderr), tc.wantStderr)
			}
		})
	}
}

func TestJoinCommandArgs(t *testing.T) {
	t.Parallel()

	if got := joinCommandArgs(nil); got != "" {
		t.Fatalf("joinCommandArgs(nil)=%q, want empty", got)
	}

	if got := joinCommandArgs([]string{"echo", "hello", "world"}); got != "echo hello world" {
		t.Fatalf("joinCommandArgs()=%q, want %q", got, "echo hello world")
	}
}

func TestConfigureLogger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		verbose   int
		quiet     int
		wantLevel int
	}{
		{name: "default", wantLevel: rns.LogInfo},
		{name: "verbose", verbose: 1, wantLevel: rns.LogVerbose},
		{name: "more verbose", verbose: 2, wantLevel: rns.LogDebug},
		{name: "quiet", quiet: 1, wantLevel: rns.LogNotice},
		{name: "more quiet", quiet: 2, wantLevel: rns.LogWarning},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := &runtimeT{}
			rt.configureLogger(tc.verbose, tc.quiet)
			if got := rt.logger.GetLogLevel(); got != tc.wantLevel {
				t.Fatalf("log level=%v, want %v", got, tc.wantLevel)
			}
		})
	}
}

func TestNewRuntime(t *testing.T) {
	t.Parallel()
	rt := newRuntime(options{verbose: 1})
	if rt == nil {
		t.Fatal("newRuntime returned nil")
	}
	if rt.logger == nil {
		t.Fatal("newRuntime returned nil logger")
	}
	if got := rt.logger.GetLogLevel(); got != rns.LogVerbose {
		t.Fatalf("log level=%v, want %v", got, rns.LogVerbose)
	}
}

func TestBuildAllowPolicyLogsThroughInjectedLogger(t *testing.T) {
	t.Parallel()

	var captured string
	logger := rns.NewLogger()
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(msg string) {
		captured += msg
	})
	logger.SetLogLevel(rns.LogWarning)

	rt := &runtimeT{logger: logger}
	mode, allowed := rt.buildAllowPolicy(options{allowHashes: []string{"not-a-hash"}})

	if mode != rns.AllowList {
		t.Fatalf("mode=%v, want %v", mode, rns.AllowList)
	}
	if len(allowed) != 0 {
		t.Fatalf("allowed=%v, want empty", allowed)
	}
	if !strings.Contains(captured, "Ignoring invalid allowed identity hash") {
		t.Fatalf("missing invalid-hash warning in %q", captured)
	}
	if !strings.Contains(captured, "Authentication enabled but no allowed identities configured") {
		t.Fatalf("missing empty-policy warning in %q", captured)
	}
}

func TestLogServiceName(t *testing.T) {
	t.Parallel()

	var captured string
	logger := rns.NewLogger()
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(msg string) {
		captured += msg
	})
	logger.SetLogLevel(rns.LogInfo)

	logServiceName(logger, "svc")

	if !strings.Contains(captured, "Using service name svc") {
		t.Fatalf("missing service-name log in %q", captured)
	}
}

func TestListeningReadyLine(t *testing.T) {
	t.Parallel()

	if got := listeningReadyLine(); got != "rnsh listening..." {
		t.Fatalf("listeningReadyLine()=%q, want %q", got, "rnsh listening...")
	}
}

func TestListeningDestinationLine(t *testing.T) {
	t.Parallel()

	if got := listeningDestinationLine([]byte{0xde, 0xad, 0xbe, 0xef}); got != "rnsh listening for commands on <deadbeef>" {
		t.Fatalf("listeningDestinationLine()=%q, want %q", got, "rnsh listening for commands on <deadbeef>")
	}
}

func TestInitiatorShutdownWatcher(t *testing.T) {
	t.Parallel()

	sigCh := make(chan os.Signal, 1)
	teardownCh := make(chan struct{}, 1)
	watcher, stop := startInitiatorShutdownWatcher(sigCh, func() {
		teardownCh <- struct{}{}
	})
	t.Cleanup(stop)

	sigCh <- syscall.SIGINT

	select {
	case <-teardownCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for teardown")
	}

	if !watcher.requested() {
		t.Fatal("watcher did not record shutdown request")
	}
}

func TestTTYRestorerNoOp(t *testing.T) {
	t.Parallel()

	restorer, err := newTTYRestorer(int(os.Stdin.Fd()))
	if err != nil {
		t.Fatalf("newTTYRestorer() error: %v", err)
	}
	if err := restorer.raw(); err != nil {
		t.Fatalf("raw() error: %v", err)
	}
	if err := restorer.restore(); err != nil {
		t.Fatalf("restore() error: %v", err)
	}
}

func TestDoListenHandlesSIGINT(t *testing.T) {
	configDir, err := os.MkdirTemp("", "gornsh-do-listen-sigint-*")
	if err != nil {
		t.Fatalf("os.MkdirTemp() error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(configDir)
	})

	rt := newRuntime(options{configDir: configDir, listen: true, noAuth: true})

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = r.Close()
	})

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- rt.doListen()
	}()

	readyCh := make(chan struct{}, 1)
	outputCh := make(chan string, 1)
	go func() {
		var output bytes.Buffer
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			output.WriteString(line)
			output.WriteByte('\n')
			if strings.Contains(line, "rnsh listening...") {
				readyCh <- struct{}{}
			}
		}
		outputCh <- output.String()
	}()

	select {
	case <-readyCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for readiness line")
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("syscall.Kill() error: %v", err)
	}

	select {
	case err := <-doneCh:
		if err != nil {
			t.Fatalf("doListen() error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for doListen to exit")
	}

	_ = w.Close()
	output := <-outputCh
	if !strings.Contains(output, "Shutting down") {
		t.Fatalf("listener output %q missing shutdown log", output)
	}
}

func TestPrintIdentityUsesPrettyHexDestination(t *testing.T) {
	configDir, err := os.MkdirTemp("", "gornsh-print-identity-*")
	if err != nil {
		t.Fatalf("os.MkdirTemp() error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(configDir)
	})

	rt := newRuntime(options{configDir: configDir, listen: true})

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
	})
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = oldStdout
	})

	outputCh := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		outputCh <- buf.String()
	}()

	if err := rt.printIdentity(); err != nil {
		t.Fatalf("printIdentity() error: %v", err)
	}
	_ = w.Close()

	output := <-outputCh
	if !strings.Contains(output, "Listening on : <") {
		t.Fatalf("printIdentity output %q missing pretty hex destination", output)
	}
	if !strings.Contains(output, ">") {
		t.Fatalf("printIdentity output %q missing closing angle bracket", output)
	}
}

type recordingAnnouncer struct {
	mu    sync.Mutex
	calls int
	ch    chan struct{}
}

func (a *recordingAnnouncer) Announce([]byte) error {
	a.mu.Lock()
	a.calls++
	ch := a.ch
	a.mu.Unlock()
	if ch != nil {
		ch <- struct{}{}
	}
	return nil
}

func (a *recordingAnnouncer) Count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

type fakeAnnouncementTicker struct {
	ch chan time.Time
}

func (t *fakeAnnouncementTicker) C() <-chan time.Time {
	return t.ch
}

func (t *fakeAnnouncementTicker) Stop() {}

func TestStartAnnouncements(t *testing.T) {
	oldTickerFactory := newAnnouncementTicker
	t.Cleanup(func() {
		newAnnouncementTicker = oldTickerFactory
	})

	tests := []struct {
		name       string
		announce   *int
		wantCalls  int
		withTicker bool
	}{
		{name: "unset", announce: nil, wantCalls: 1},
		{name: "startup only", announce: intPtr(0), wantCalls: 1},
		{name: "periodic", announce: intPtr(30), wantCalls: 2, withTicker: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			a := &recordingAnnouncer{}
			if tc.withTicker {
				called := make(chan struct{}, 2)
				a.ch = called
				fakeTicker := &fakeAnnouncementTicker{ch: make(chan time.Time, 1)}
				newAnnouncementTicker = func(time.Duration) announcementTicker {
					return fakeTicker
				}
				stop := startAnnouncements(a, tc.announce, rns.NewLogger())
				if got := a.Count(); got != 1 {
					t.Fatalf("initial announce count=%v, want 1", got)
				}
				select {
				case <-called:
				default:
					t.Fatal("missing initial announce signal")
				}
				fakeTicker.ch <- time.Now()
				select {
				case <-called:
				case <-time.After(time.Second):
					t.Fatal("timed out waiting for periodic announce")
				}
				stop()
				if got := a.Count(); got != tc.wantCalls {
					t.Fatalf("announce count=%v, want %v", got, tc.wantCalls)
				}
				return
			}

			startAnnouncements(a, tc.announce, rns.NewLogger())
			if got := a.Count(); got != tc.wantCalls {
				t.Fatalf("announce count=%v, want %v", got, tc.wantCalls)
			}
		})
	}
}

func intPtr(v int) *int {
	return &v
}
