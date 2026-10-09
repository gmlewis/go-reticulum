// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package utils

import (
	"errors"
	"testing"
	"time"
)

// zoneProperty answers with one fixed property value, as a device would.
func zoneProperty(value string, err error) func(string) (string, error) {
	return func(name string) (string, error) {
		if name != androidZoneProperty {
			return "", errors.New("unexpected property")
		}
		return value, err
	}
}

// loadZone answers with one fixed location, as a zoneinfo lookup would.
func loadZone(loc *time.Location, err error) func(string) (*time.Location, error) {
	return func(string) (*time.Location, error) { return loc, err }
}

func TestResolveZone(t *testing.T) {
	t.Parallel()

	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("this machine has no zoneinfo for America/New_York: %v", err)
	}

	tests := []struct {
		name      string
		property  func(string) (string, error)
		load      func(string) (*time.Location, error)
		wantName  string
		wantOK    bool
		wantZone  *time.Location
		wantAsked []string
	}{
		{
			name:      "a device that names its zone",
			property:  zoneProperty("America/New_York\n", nil),
			load:      loadZone(ny, nil),
			wantName:  "America/New_York",
			wantOK:    true,
			wantZone:  ny,
			wantAsked: []string{"America/New_York"},
		},
		{
			// A device whose property is empty is a device that has not decided,
			// which is not a reason to look up an empty zone name.
			name:      "a device that names nothing",
			property:  zoneProperty("", nil),
			load:      loadZone(ny, nil),
			wantAsked: nil,
		},
		{
			name:      "whitespace is not a zone",
			property:  zoneProperty("   \n", nil),
			load:      loadZone(ny, nil),
			wantAsked: nil,
		},
		{
			// The property is external input: a path in it is a file read chosen
			// by whoever set the property, and must never be looked up.
			name:      "an absolute path is refused",
			property:  zoneProperty("/etc/localtime", nil),
			load:      loadZone(ny, nil),
			wantAsked: nil,
		},
		{
			name:      "a path walking upwards is refused",
			property:  zoneProperty("../../etc/passwd", nil),
			load:      loadZone(ny, nil),
			wantAsked: nil,
		},
		{
			name:      "an inner space is refused",
			property:  zoneProperty("America/New York", nil),
			load:      loadZone(ny, nil),
			wantAsked: nil,
		},
		{
			// A device already on UTC has nothing to be improved, and reporting
			// that it was changed would be a lie.
			name:      "UTC is left alone",
			property:  zoneProperty("UTC", nil),
			load:      loadZone(ny, nil),
			wantAsked: nil,
		},
		{
			name:      "a property that cannot be read leaves the zone alone",
			property:  zoneProperty("", errors.New("no getprop here")),
			load:      loadZone(ny, nil),
			wantAsked: nil,
		},
		{
			// The name resolving is the zone being usable; a name this platform
			// cannot load is no better than UTC.
			name:      "a name with no rules leaves the zone alone",
			property:  zoneProperty("Mars/Olympus_Mons", nil),
			load:      loadZone(nil, errors.New("unknown time zone")),
			wantAsked: []string{"Mars/Olympus_Mons"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var asked []string
			load := func(name string) (*time.Location, error) {
				asked = append(asked, name)
				return tt.load(name)
			}

			name, loc, ok := resolveZone(tt.property, load)

			if ok != tt.wantOK {
				t.Fatalf("resolveZone() ok = %v, want %v", ok, tt.wantOK)
			}
			if name != tt.wantName {
				t.Errorf("resolveZone() name = %q, want %q", name, tt.wantName)
			}
			if ok && loc != tt.wantZone {
				t.Errorf("resolveZone() zone = %v, want %v", loc, tt.wantZone)
			}
			if len(asked) != len(tt.wantAsked) {
				t.Fatalf("looked up %v, want %v", asked, tt.wantAsked)
			}
			for i, want := range tt.wantAsked {
				if asked[i] != want {
					t.Errorf("lookup %v = %q, want %q", i, asked[i], want)
				}
			}
		})
	}
}

func TestUsableZoneName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want bool
	}{
		{"America/New_York", true},
		{"Europe/Isle_of_Man", true},
		{"Etc/GMT+5", true},
		{"", false},
		{"UTC", false},
		{"Local", false},
		{"/etc/localtime", false},
		{"../zoneinfo/UTC", false},
		{"America/New\tYork", false},
		{"Mars\x00/Olympus", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := usableZoneName(tt.name); got != tt.want {
				t.Errorf("usableZoneName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestUseSystemZoneLeavesLocalAloneWhereThereIsNoDeviceZone is the whole safety
// property of the helper: on a machine whose zone the runtime already resolved, or
// one that has no Android property to read, time.Local is exactly what it was.
func TestUseSystemZoneLeavesLocalAloneWhereThereIsNoDeviceZone(t *testing.T) {
	t.Parallel()

	before := time.Local
	if got := UseSystemZone(); got != "" {
		t.Errorf("UseSystemZone() = %q on a machine with no Android property, want %q", got, "")
	}
	if time.Local != before {
		t.Errorf("time.Local changed from %v to %v", before, time.Local)
	}
}
