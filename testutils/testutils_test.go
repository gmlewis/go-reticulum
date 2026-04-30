// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

const tempDirPrefix = "testutils-tempdir-"

func TestTempDirCreatesAndCleansUpDirectory(t *testing.T) {
	t.Parallel()

	dir, cleanup := TempDir(t, tempDirPrefix)

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("TempDir returned missing directory %q: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("TempDir returned non-directory path %q", dir)
	}
	if !strings.Contains(filepath.Base(dir), tempDirPrefix) {
		t.Fatalf("TempDir directory name %q does not contain prefix", filepath.Base(dir))
	}

	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("TempDir cleanup left directory behind: %v", err)
	}
}

func TestTempDirWithConfigCreatesConfigFile(t *testing.T) {
	t.Parallel()

	config := "[reticulum]\ninstance_name = test\n"
	dir, cleanup := TempDirWithConfig(t, tempDirPrefix, func(string) string { return config })

	configPath := filepath.Join(dir, "config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("TempDirWithConfig missing config file %q: %v", configPath, err)
	}
	if got := string(data); got != config {
		t.Fatalf("TempDirWithConfig config = %q, want %q", got, config)
	}

	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("TempDirWithConfig cleanup left directory behind: %v", err)
	}
}

func TestTempBaseDir(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "darwin" {
		if got := tempBaseDir(); got != "/tmp" {
			t.Fatalf("tempBaseDir() = %q, want %q", got, "/tmp")
		}
		return
	}

	if got := tempBaseDir(); got != "" {
		t.Fatalf("tempBaseDir() = %q, want empty on %v", got, runtime.GOOS)
	}
}

func TestReserveUDPPortReturnsDistinctBindablePorts(t *testing.T) {
	t.Parallel()

	first := ReserveUDPPort(t)
	second := ReserveUDPPort(t)
	if first == second {
		t.Fatalf("ReserveUDPPort() returned duplicate ports: %v", first)
	}

	for _, port := range []int{first, second} {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
		if err != nil {
			t.Fatalf("ListenUDP(%v) error = %v", port, err)
		}
		if err := conn.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
}

func TestIsRetriableRemoveAllError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "plain enotempty",
			err:  syscall.ENOTEMPTY,
			want: true,
		},
		{
			name: "wrapped ebusy",
			err: &os.PathError{
				Op:   "unlinkat",
				Path: "/tmp/test",
				Err:  syscall.EBUSY,
			},
			want: true,
		},
		{
			name: "other error",
			err:  errors.New("boom"),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isRetriableRemoveAllError(tc.err); got != tc.want {
				t.Fatalf("isRetriableRemoveAllError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
