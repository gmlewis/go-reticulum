// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestReplCreation(t *testing.T) {
	t.Parallel()

	repl := newREPL(nil, nil, strings.NewReader(""), io.Discard)
	if repl == nil {
		t.Fatal("newREPL returned nil")
	}
	if repl.ret != nil {
		t.Fatalf("repl.ret = %v, want nil", repl.ret)
	}
	if repl.logger != nil {
		t.Fatalf("repl.logger = %v, want nil", repl.logger)
	}
	if repl.in == nil {
		t.Fatal("repl.in = nil, want reader")
	}
	if repl.out == nil {
		t.Fatal("repl.out = nil, want writer")
	}
}

func TestReplCmdHelp(t *testing.T) {
	t.Parallel()
	repl := newREPL(nil, nil, strings.NewReader(""), io.Discard)
	got := repl.cmdHelp()
	for _, want := range []string{"help", "version", "status", "interfaces", "loglevel", "quit", "exit"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cmdHelp() missing %q in %q", want, got)
		}
	}
}

func TestReplCmdVersion(t *testing.T) {
	t.Parallel()
	repl := newREPL(nil, nil, strings.NewReader(""), io.Discard)
	if got, want := repl.cmdVersion(), "gornsd "+rns.VERSION; got != want {
		t.Fatalf("cmdVersion() = %q, want %q", got, want)
	}
}

func TestReplCmdStatus(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, "gornsd-repl-status-")
	defer cleanup()
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte("[reticulum]\nshare_instance = No\n\n[interfaces]\n"), 0o600); err != nil {
		t.Fatalf("write config error: %v", err)
	}

	t.Run("nil reticulum", func(t *testing.T) {
		repl := newREPL(nil, nil, strings.NewReader(""), io.Discard)
		if got, want := repl.cmdStatus(), "(no reticulum instance)"; got != want {
			t.Fatalf("cmdStatus() = %q, want %q", got, want)
		}
	})

	t.Run("standalone instance", func(t *testing.T) {
		ret, err := rns.NewReticulum(rns.NewTransportSystem(rns.NewLogger()), configDir)
		if err != nil {
			t.Fatalf("NewReticulum error: %v", err)
		}
		t.Cleanup(func() {
			if closeErr := ret.Close(); closeErr != nil {
				t.Fatalf("ret.Close error: %v", closeErr)
			}
		})
		repl := newREPL(ret, nil, strings.NewReader(""), io.Discard)
		got := repl.cmdStatus()
		if !strings.Contains(got, "standalone") {
			t.Fatalf("cmdStatus() = %q, want standalone status", got)
		}
	})

	t.Run("shared and connected instances", func(t *testing.T) {
		sharedConfigDir, sharedCleanup := testutils.TempDir(t, "gornsd-repl-shared-")
		defer sharedCleanup()
		sharedPort := reserveTCPPort(t)
		rpcPort := reserveTCPPort(t)
		config := fmt.Sprintf(`[reticulum]
instance_name = gornsd-repl
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v

[interfaces]
`, sharedPort, rpcPort)
		if err := os.WriteFile(filepath.Join(sharedConfigDir, "config"), []byte(config), 0o600); err != nil {
			t.Fatalf("write config error: %v", err)
		}

		shared, err := rns.NewReticulum(rns.NewTransportSystem(rns.NewLogger()), sharedConfigDir)
		if err != nil {
			t.Fatalf("shared NewReticulum error: %v", err)
		}
		t.Cleanup(func() {
			if closeErr := shared.Close(); closeErr != nil {
				t.Fatalf("shared.Close error: %v", closeErr)
			}
		})

		connected, err := rns.NewReticulum(rns.NewTransportSystem(rns.NewLogger()), sharedConfigDir)
		if err != nil {
			t.Fatalf("connected NewReticulum error: %v", err)
		}
		t.Cleanup(func() {
			if closeErr := connected.Close(); closeErr != nil {
				t.Fatalf("connected.Close error: %v", closeErr)
			}
		})

		if got := newREPL(shared, nil, strings.NewReader(""), io.Discard).cmdStatus(); !strings.Contains(got, "shared") {
			t.Fatalf("shared cmdStatus() = %q, want shared status", got)
		}
		if got := newREPL(connected, nil, strings.NewReader(""), io.Discard).cmdStatus(); !strings.Contains(got, "connected") {
			t.Fatalf("connected cmdStatus() = %q, want connected status", got)
		}
	})
}

func TestReplCmdInterfaces(t *testing.T) {
	t.Parallel()
	repl := newREPL(nil, nil, strings.NewReader(""), io.Discard)
	if got, want := repl.cmdInterfaces(), "(no interfaces)"; got != want {
		t.Fatalf("cmdInterfaces() = %q, want %q", got, want)
	}
}

func TestReplCmdLogLevel(t *testing.T) {
	t.Parallel()
	logger := rns.NewLogger()
	repl := newREPL(nil, logger, strings.NewReader(""), io.Discard)
	if got := repl.cmdLogLevel(nil); !strings.Contains(got, "3") || !strings.Contains(got, "Notice") {
		t.Fatalf("cmdLogLevel() = %q, want current level and name", got)
	}
	if got := repl.cmdLogLevel([]string{"6"}); !strings.Contains(got, "6") || !strings.Contains(got, "Debug") {
		t.Fatalf("cmdLogLevel([6]) = %q, want confirmation", got)
	}
	if got := repl.cmdLogLevel([]string{"bogus"}); !strings.Contains(got, "invalid") {
		t.Fatalf("cmdLogLevel([bogus]) = %q, want invalid-input error", got)
	}
	if got := newREPL(nil, nil, strings.NewReader(""), io.Discard).cmdLogLevel(nil); !strings.Contains(got, "no logger") {
		t.Fatalf("cmdLogLevel(nil logger) = %q, want no-logger error", got)
	}
}
