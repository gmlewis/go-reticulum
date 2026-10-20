// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestParseFixedMTU pins the TCP fixed_mtu key parsing (TCPInterface.py:78-82):
// absent and zero are unset, and a positive value below Reticulum.MTU is an
// error.
func TestParseFixedMTU(t *testing.T) {
	t.Parallel()
	tests := []struct {
		props   map[string]string
		want    int
		wantErr bool
	}{
		{props: map[string]string{}, want: 0},
		{props: map[string]string{"fixed_mtu": "9000"}, want: 9000},
		{props: map[string]string{"fixed_mtu": " 9000 "}, want: 9000},
		{props: map[string]string{"fixed_mtu": "0"}, want: 0},
		{props: map[string]string{"fixed_mtu": "not-a-number"}, want: 0},
		{props: map[string]string{"fixed_mtu": "499"}, wantErr: true},
		{props: map[string]string{"fixed_mtu": "500"}, want: interfaces.ReticulumMTU},
	}
	for _, tt := range tests {
		sub := &ConfigSection{Properties: tt.props}
		got, err := parseFixedMTU(sub)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseFixedMTU(%v) error = %v, wantErr %v", tt.props, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("parseFixedMTU(%v) = %v, want %v", tt.props, got, tt.want)
		}
	}
}

// TestFixedMTUConfigReachesTCPServer drives the config path end to end: a
// configured fixed_mtu overrides the TCP interface's autoconfiguration, and a
// value below the Reticulum MTU leaves the interface unregistered.
func TestFixedMTUConfigReachesTCPServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		fixedMTU   string
		wantMtu    int
		wantFixed  bool
		registered bool
	}{
		{name: "fixed mtu applies", fixedMTU: "9000", wantMtu: 9000, wantFixed: true, registered: true},
		{name: "autoconfigured without the key", wantMtu: interfaces.OptimiseHardwareMTU(interfaces.TCPBitrateGuess), wantFixed: false, registered: true},
		{name: "too small is rejected", fixedMTU: "499", registered: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			configDir := testutils.TempDir(t, tempDirPrefix)
			port := reserveTCPPort(t)
			body := "[reticulum]\nshare_instance = No\n\n[logging]\nloglevel = 4\n\n[interfaces]\n"
			body += "[[test]]\ntype = TCPServerInterface\ninterface_enabled = yes\nlisten_ip = 127.0.0.1\n"
			body += "listen_port = " + strconv.Itoa(port) + "\n"
			if tt.fixedMTU != "" {
				body += "fixed_mtu = " + tt.fixedMTU + "\n"
			}
			if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(body), 0o600); err != nil {
				t.Fatalf("WriteFile(config) error = %v", err)
			}

			ts := NewTransportSystem(nil)
			r := mustTestNewReticulum(t, ts, configDir)
			defer closeReticulum(t, r)

			var found interfaces.Interface
			for _, iface := range ts.GetInterfaces() {
				if iface.Name() == "test" {
					found = iface
					break
				}
			}
			if !tt.registered {
				if found != nil {
					t.Fatalf("interface with fixed_mtu %q was registered; want it rejected", tt.fixedMTU)
				}
				return
			}
			if found == nil {
				t.Fatalf("TCP server interface was not registered")
			}
			model, ok := found.(interface {
				HardwareMTU() int
				FixedMTU() bool
			})
			if !ok {
				t.Fatalf("registered interface does not carry the MTU model")
			}
			if model.FixedMTU() != tt.wantFixed || model.HardwareMTU() != tt.wantMtu {
				t.Errorf("interface model = fixed=%v hw=%v, want fixed=%v hw=%v",
					model.FixedMTU(), model.HardwareMTU(), tt.wantFixed, tt.wantMtu)
			}
		})
	}
}
