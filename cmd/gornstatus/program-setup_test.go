// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

func mustMsgpackPack(v any) []byte {
	data, err := msgpack.Pack(v)
	if err != nil {
		panic(err)
	}
	return data
}

func TestProgramSetupDiscovery(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDirWithConfig(t, "gornstatus-discovery-", func(dir string) string {
		instanceName := filepath.Base(dir)
		return "[reticulum]\nenable_transport = False\nshare_instance = Yes\ninstance_name = " + instanceName + "\n\n[logging]\nloglevel = 2\n"
	})

	// Setup mock discovery data
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	mockData := map[string]any{
		"name":       "Mock Interface",
		"type":       "UDPInterface",
		"last_heard": now - 30,
		"value":      123,
	}
	data := mustMsgpackPack(mockData)
	if err := os.WriteFile(filepath.Join(storagePath, "mock.data"), data, 0o644); err != nil {
		t.Fatalf("failed to write mock data: %v", err)
	}

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
		configDir:        tmpDir,
		discoveredIfaces: true,
		rnsInstance:      r,
		logger:           logger,
		writer:           &buf,
	})

	if exitCode != 0 {
		t.Errorf("programSetup exit code = %v, want 0; output: %v", exitCode, buf.String())
	}

	got := buf.String()
	if !strings.Contains(got, "Mock Interface") {
		t.Errorf("output missing Mock Interface\ngot:\n%v", got)
	}
	if !strings.Contains(got, "✓ Available") {
		t.Errorf("output missing Available status\ngot:\n%v", got)
	}
}

func TestProgramSetupExitsCleanly(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDirWithConfig(t, "gornstatus-test-", func(dir string) string {
		instanceName := filepath.Base(dir)
		return "[reticulum]\nenable_transport = False\nshare_instance = Yes\ninstance_name = " + instanceName + "\n\n[logging]\nloglevel = 2\n"
	})
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

func TestShouldDisplayInterface(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		ifstat      rns.InterfaceStat
		dispAll     bool
		nameFilter  string
		burstFilter bool
		want        bool
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
			ifstat: rns.InterfaceStat{Name: "I2PInterface[test]", I2PConnectable: new(false)},
			want:   false,
		},
		{
			name:    "I2PInterface non-connectable hidden even with dispAll",
			ifstat:  rns.InterfaceStat{Name: "I2PInterface[test]", I2PConnectable: new(false)},
			dispAll: true,
			want:    false,
		},
		{
			name:   "I2PInterface connectable shown",
			ifstat: rns.InterfaceStat{Name: "I2PInterface[test]", I2PConnectable: new(true)},
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
		{
			name:        "burst filter hides idle interface",
			ifstat:      rns.InterfaceStat{Name: "RNodeInterface[LoRa 915]"},
			burstFilter: true,
			want:        false,
		},
		{
			name:        "burst filter shows announce-burst interface",
			ifstat:      rns.InterfaceStat{Name: "RNodeInterface[LoRa 915]", BurstActive: true, BurstActivated: 100.0},
			burstFilter: true,
			want:        true,
		},
		{
			name:        "burst filter shows pr-burst interface",
			ifstat:      rns.InterfaceStat{Name: "AutoInterface[test]", PrBurstActive: true, PrBurstActivated: 100.0},
			burstFilter: true,
			want:        true,
		},
		{
			name:        "burst filter with matching name shows interface",
			ifstat:      rns.InterfaceStat{Name: "RNodeInterface[LoRa 915]"},
			nameFilter:  "lora",
			burstFilter: true,
			want:        true,
		},
		{
			name:        "burst filter with non-matching name hides idle interface",
			ifstat:      rns.InterfaceStat{Name: "RNodeInterface[LoRa 915]"},
			nameFilter:  "tcp",
			burstFilter: true,
			want:        false,
		},
		{
			name:        "burst filter hides hidden interface even if bursting",
			ifstat:      rns.InterfaceStat{Name: "LocalInterface[Admin]", BurstActive: true, BurstActivated: 100.0},
			burstFilter: true,
			want:        false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shouldDisplayInterface(tc.ifstat, tc.dispAll, tc.nameFilter, tc.burstFilter)
			if got != tc.want {
				t.Errorf("shouldDisplayInterface(%q, %v, %q, %v) = %v, want %v",
					tc.ifstat.Name, tc.dispAll, tc.nameFilter, tc.burstFilter, got, tc.want)
			}
		})
	}
}
