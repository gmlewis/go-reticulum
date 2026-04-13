// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && linux

package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"testing"
	"time"
)

var ptyLinePattern = regexp.MustCompile(`PTY is (.+)$`)

type loopbackProbeDetector struct {
	hostFile        *os.File
	responseTimeout time.Duration
	detected        bool
	mu              sync.Mutex
}

func (d *loopbackProbeDetector) Detect() error {
	responseSeen := make(chan struct{}, 1)
	go d.waitForDetectResponse(responseSeen)

	if err := rnodeDetect(d.hostFile, "device"); err != nil {
		return err
	}

	select {
	case <-responseSeen:
		d.mu.Lock()
		d.detected = true
		d.mu.Unlock()
	case <-time.After(d.responseTimeout):
		d.mu.Lock()
		d.detected = false
		d.mu.Unlock()
	}

	return nil
}

func (d *loopbackProbeDetector) Detected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.detected
}

func (d *loopbackProbeDetector) waitForDetectResponse(responseSeen chan struct{}) {
	var buffer []byte
	readBuf := make([]byte, 32)
	want := []byte{kissFend, 0x08, 0x46, kissFend}

	for {
		n, err := d.hostFile.Read(readBuf)
		if n > 0 {
			buffer = append(buffer, readBuf[:n]...)
			if bytes.Contains(buffer, want) {
				select {
				case responseSeen <- struct{}{}:
				default:
				}
				return
			}
		}
		if err != nil {
			return
		}
	}
}

type loopbackSleeper struct {
	calls []time.Duration
}

func (s *loopbackSleeper) Sleep(duration time.Duration) {
	s.calls = append(s.calls, duration)
}

func TestDeviceProbeSucceedsOverSocatLoopback(t *testing.T) {
	t.Parallel()
	hostPath, devicePath := startSocatPTYPair(t)

	hostPort, err := rnodeOpenSerial(hostPath)
	if err != nil {
		t.Fatalf("open host serial: %v", err)
	}
	hostFile, ok := hostPort.(*os.File)
	if !ok {
		t.Fatalf("expected *os.File from rnodeOpenSerial, got %T", hostPort)
	}
	t.Cleanup(func() {
		_ = hostFile.Close()
	})

	deviceFile, err := os.OpenFile(devicePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open device serial: %v", err)
	}
	t.Cleanup(func() {
		_ = deviceFile.Close()
	})

	responseErr := make(chan error, 1)
	go func() {
		responseErr <- respondToDetectRequest(deviceFile)
	}()

	detector := &loopbackProbeDetector{hostFile: hostFile, responseTimeout: 500 * time.Millisecond}
	sleeper := &loopbackSleeper{}
	probe := newDeviceProbeState(detector, sleeper)
	got, err := probe.probe()
	if err != nil {
		t.Fatalf("probe returned error: %v", err)
	}
	if !got {
		t.Fatalf("expected probe success")
	}
	if len(sleeper.calls) != 2 || sleeper.calls[0] != deviceProbeInitialDelay || sleeper.calls[1] != deviceProbeFinalDelay {
		t.Fatalf("sleep calls mismatch: %#v", sleeper.calls)
	}
	if err := <-responseErr; err != nil {
		t.Fatalf("detect response worker failed: %v", err)
	}
}

func TestDeviceProbeFailsOverSocatLoopbackWithoutResponse(t *testing.T) {
	t.Parallel()
	hostPath, _ := startSocatPTYPair(t)

	hostPort, err := rnodeOpenSerial(hostPath)
	if err != nil {
		t.Fatalf("open host serial: %v", err)
	}
	hostFile, ok := hostPort.(*os.File)
	if !ok {
		t.Fatalf("expected *os.File from rnodeOpenSerial, got %T", hostPort)
	}
	t.Cleanup(func() {
		_ = hostFile.Close()
	})

	detector := &loopbackProbeDetector{hostFile: hostFile, responseTimeout: 100 * time.Millisecond}
	sleeper := &loopbackSleeper{}
	probe := newDeviceProbeState(detector, sleeper)
	got, err := probe.probe()
	if err == nil {
		t.Fatalf("expected probe failure")
	}
	if got {
		t.Fatalf("expected probe failure")
	}
	if err.Error() != "Got invalid response while detecting device" {
		t.Fatalf("error mismatch: got %q", err.Error())
	}
	if len(sleeper.calls) != 2 {
		t.Fatalf("sleep calls mismatch: %#v", sleeper.calls)
	}
}

func startSocatPTYPair(t *testing.T) (string, string) {
	t.Helper()

	if _, err := exec.LookPath("socat"); err != nil {
		t.Skip("socat not installed")
	}

	cmd := exec.Command("socat", "-d", "-d", "pty,raw,echo=0", "pty,raw,echo=0")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start socat: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	scanner := bufio.NewScanner(stderr)
	paths := make([]string, 0, 2)
	for scanner.Scan() {
		line := scanner.Text()
		match := ptyLinePattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		paths = append(paths, match[1])
		if len(paths) == 2 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read socat output: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected two PTY paths from socat, got %#v", paths)
	}

	return paths[0], paths[1]
}

func respondToDetectRequest(deviceFile *os.File) error {

	buf := make([]byte, 0, 64)
	readBuf := make([]byte, 32)
	want := []byte{kissFend, 0x08, 0x73, kissFend}
	response := []byte{kissFend, 0x08, 0x46, kissFend}

	for {
		n, err := deviceFile.Read(readBuf)
		if n > 0 {
			buf = append(buf, readBuf[:n]...)
			if bytes.Contains(buf, want) {
				if _, err := deviceFile.Write(response); err != nil {
					return err
				}
				return nil
			}
		}
		if err != nil {
			return nil
		}
	}
}
