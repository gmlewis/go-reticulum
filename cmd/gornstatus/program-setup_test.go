// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestProgramSetupExitsCleanly(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := testutils.TempDirWithConfig(t, "gornstatus-test-", func(dir string) string {
		instanceName := filepath.Base(dir)
		return "[reticulum]\nenable_transport = False\nshare_instance = Yes\ninstance_name = " + instanceName + "\n\n[logging]\nloglevel = 2\n"
	})
	defer cleanup()
	var buf bytes.Buffer
	logger := rns.NewLogger()
	ts := rns.NewTransportSystem(logger)
	r, err := rns.NewReticulumWithLogger(ts, tmpDir, logger)
	if err != nil {
		t.Fatalf("NewReticulum: %v", err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	exitCode := programSetup(programSetupParams{
		configDir:   tmpDir,
		mustExit:    true,
		rnsInstance: r,
		logger:      logger,
		writer:      &buf,
	})

	if exitCode != 0 {
		t.Errorf("programSetup exit code = %v, want 0; output: %v", exitCode, buf.String())
	}
}

func TestProgramSetupNoSharedInstance(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := rns.NewLogger()

	exitCode := programSetup(programSetupParams{
		configDir: "/nonexistent/path/that/should/fail",
		mustExit:  true,
		logger:    logger,
		writer:    &buf,
	})

	if exitCode != 1 {
		t.Errorf("programSetup exit code = %v, want 1; output: %v", exitCode, buf.String())
	}
}

func TestProgramSetupNoSharedInstanceNoExit(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := rns.NewLogger()

	exitCode := programSetup(programSetupParams{
		configDir: "/nonexistent/path/that/should/fail",
		mustExit:  false,
		logger:    logger,
		writer:    &buf,
	})

	if exitCode != 0 {
		t.Errorf("programSetup exit code = %v, want 0; output: %v", exitCode, buf.String())
	}
}

func boolPtr(b bool) *bool { return &b }

func TestShouldDisplayInterface(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		ifstat     rns.InterfaceStat
		dispAll    bool
		nameFilter string
		want       bool
	}{
		{
			name:   "normal interface shown",
			ifstat: rns.InterfaceStat{Name: "RNodeInterface[LoRa 915]"},
			want:   true,
		},
		{
			name:   "LocalInterface hidden",
			ifstat: rns.InterfaceStat{Name: "LocalInterface[Admin]"},
			want:   false,
		},
		{
			name:    "LocalInterface shown with dispAll",
			ifstat:  rns.InterfaceStat{Name: "LocalInterface[Admin]"},
			dispAll: true,
			want:    true,
		},
		{
			name:   "TCPInterface Client hidden",
			ifstat: rns.InterfaceStat{Name: "TCPInterface[Client on 127.0.0.1]"},
			want:   false,
		},
		{
			name:    "TCPInterface Client shown with dispAll",
			ifstat:  rns.InterfaceStat{Name: "TCPInterface[Client on 127.0.0.1]"},
			dispAll: true,
			want:    true,
		},
		{
			name:   "BackboneInterface Client hidden",
			ifstat: rns.InterfaceStat{Name: "BackboneInterface[Client on 10.0.0.1]"},
			want:   false,
		},
		{
			name:   "AutoInterfacePeer hidden",
			ifstat: rns.InterfaceStat{Name: "AutoInterfacePeer[node1]"},
			want:   false,
		},
		{
			name:   "WeaveInterfacePeer hidden",
			ifstat: rns.InterfaceStat{Name: "WeaveInterfacePeer[peer2]"},
			want:   false,
		},
		{
			name:   "I2PInterfacePeer Connected hidden",
			ifstat: rns.InterfaceStat{Name: "I2PInterfacePeer[Connected peer abc]"},
			want:   false,
		},
		{
			name:   "I2PInterface non-connectable hidden",
			ifstat: rns.InterfaceStat{Name: "I2PInterface[test]", I2PConnectable: boolPtr(false)},
			want:   false,
		},
		{
			name:    "I2PInterface non-connectable hidden even with dispAll",
			ifstat:  rns.InterfaceStat{Name: "I2PInterface[test]", I2PConnectable: boolPtr(false)},
			dispAll: true,
			want:    false,
		},
		{
			name:   "I2PInterface connectable shown",
			ifstat: rns.InterfaceStat{Name: "I2PInterface[test]", I2PConnectable: boolPtr(true)},
			want:   true,
		},
		{
			name:   "I2PInterface nil connectable shown",
			ifstat: rns.InterfaceStat{Name: "I2PInterface[test]"},
			want:   true,
		},
		{
			name:       "name filter matches",
			ifstat:     rns.InterfaceStat{Name: "RNodeInterface[LoRa 915]"},
			nameFilter: "lora",
			want:       true,
		},
		{
			name:       "name filter no match",
			ifstat:     rns.InterfaceStat{Name: "RNodeInterface[LoRa 915]"},
			nameFilter: "tcp",
			want:       false,
		},
		{
			name:   "Shared Instance shown",
			ifstat: rns.InterfaceStat{Name: "Shared Instance[37428]"},
			want:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shouldDisplayInterface(tc.ifstat, tc.dispAll, tc.nameFilter)
			if got != tc.want {
				t.Errorf("shouldDisplayInterface(%q, %v, %q) = %v, want %v",
					tc.ifstat.Name, tc.dispAll, tc.nameFilter, got, tc.want)
			}
		})
	}
}
