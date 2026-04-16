// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "testing"

func TestLiveHardwareGateParsing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want liveHardwareGates
	}{
		{
			name: "defaults disabled",
			want: liveHardwareGates{},
		},
		{
			name: "writes enabled",
			env: map[string]string{
				"GORNODECONF_LIVE_ALLOW_WRITES": "1",
			},
			want: liveHardwareGates{allowWrites: true},
		},
		{
			name: "destructive implies writes",
			env: map[string]string{
				"GORNODECONF_LIVE_ALLOW_DESTRUCTIVE": "1",
			},
			want: liveHardwareGates{allowWrites: true, allowDestructive: true},
		},
		{
			name: "whitespace is trimmed",
			env: map[string]string{
				"GORNODECONF_LIVE_ALLOW_WRITES":      " 1 ",
				"GORNODECONF_LIVE_ALLOW_DESTRUCTIVE": "\t1\n",
			},
			want: liveHardwareGates{allowWrites: true, allowDestructive: true},
		},
		{
			name: "non one values ignored",
			env: map[string]string{
				"GORNODECONF_LIVE_ALLOW_WRITES":      "true",
				"GORNODECONF_LIVE_ALLOW_DESTRUCTIVE": "yes",
			},
			want: liveHardwareGates{},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseLiveHardwareGates(envLookup(tc.env))
			if got != tc.want {
				t.Fatalf("parseLiveHardwareGates() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestLiveHardwarePortResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "missing port",
			want: "",
		},
		{
			name: "trimmed port",
			env: map[string]string{
				"GORNODECONF_LIVE_SERIAL_PORT": "  /dev/cu.usbmodem21101\t",
			},
			want: "/dev/cu.usbmodem21101",
		},
		{
			name: "linux port",
			env: map[string]string{
				"GORNODECONF_LIVE_SERIAL_PORT": "/dev/serial/by-id/usb-RNode_JTAG-if00",
			},
			want: "/dev/serial/by-id/usb-RNode_JTAG-if00",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveLiveHardwarePort(envLookup(tc.env))
			if got != tc.want {
				t.Fatalf("resolveLiveHardwarePort() = %q, want %q", got, tc.want)
			}
		})
	}
}

func envLookup(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
