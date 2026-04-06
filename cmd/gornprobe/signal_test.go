// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type interruptBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *interruptBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *interruptBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func TestGornprobeKeyboardInterrupt(t *testing.T) {
	t.Parallel()

	binDir := tempDir(t)
	binPath := filepath.Join(binDir, "gornprobe")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("failed to build gornprobe: %v\n%v", err, string(out))
	}

	configDir := tempDir(t)
	configText := `[reticulum]
share_instance = No
instance_control_port = 0

[logging]
loglevel = 4

[interfaces]
`
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(configText), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	buf := &interruptBuffer{}

	cmd := exec.Command(binPath, "--config", configDir, "gornprobe.test", "00112233445566778899aabbccddeeff")
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start gornprobe: %v", err)
	}

	<-time.After(200 * time.Millisecond)

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("failed to signal interrupt: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if got, want := exitErr.ExitCode(), 0; got != want {
				t.Fatalf("exit code = %v, want %v\n%v", got, want, buf.String())
			}
		} else {
			t.Fatalf("gornprobe interrupt wait failed: %v", err)
		}
	}

	out := buf.String()
	if !strings.Contains(out, "\n") {
		t.Fatalf("missing interrupt blank line: %q", out)
	}
}

func tempDir(t *testing.T) string {
	t.Helper()
	baseDir := ""
	if runtime.GOOS == "darwin" {
		baseDir = "/tmp"
	}
	dir, err := os.MkdirTemp(baseDir, "gornprobe-test-")
	if err != nil {
		t.Fatalf("tempDir error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}
