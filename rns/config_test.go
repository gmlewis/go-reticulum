// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"strings"
	"testing"
)

func TestParseConfig(t *testing.T) {
	input := `
[reticulum]
enable_transport = False
share_instance = Yes

[[interface_name]]
type = UDPInterface
port = 37428

[logging]
loglevel = 4
`
	config, err := ParseConfig(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	section, ok := config.GetSection("reticulum")
	if !ok {
		t.Fatal("section [reticulum] not found")
	}

	if v, _ := section.GetProperty("enable_transport"); v != "False" {
		t.Errorf("expected enable_transport = False, got %v", v)
	}

	sub, ok := section.Subsections["interface_name"]
	if !ok {
		t.Fatal("subsection [[interface_name]] not found")
	}

	if v, _ := sub.GetProperty("type"); v != "UDPInterface" {
		t.Errorf("expected type = UDPInterface, got %v", v)
	}

	logSection, ok := config.GetSection("logging")
	if !ok {
		t.Fatal("section [logging] not found")
	}

	if v, _ := logSection.GetProperty("loglevel"); v != "4" {
		t.Errorf("expected loglevel = 4, got %v", v)
	}
}

func TestParseConfigNestedSubsections(t *testing.T) {
	input := `
[interfaces]
[[RNode Multi]]
type = RNodeMultiInterface
port = /dev/ttyUSB0

[[[sub0]]]
interface_enabled = Yes
vport = 0
frequency = 433050000
bandwidth = 125000
txpower = 10
spreadingfactor = 7
codingrate = 5
`

	config, err := ParseConfig(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	interfacesSection, ok := config.GetSection("interfaces")
	if !ok {
		t.Fatal("section [interfaces] not found")
	}

	multi, ok := interfacesSection.Subsections["RNode Multi"]
	if !ok {
		t.Fatal("subsection [[RNode Multi]] not found")
	}

	if v, _ := multi.GetProperty("type"); v != "RNodeMultiInterface" {
		t.Fatalf("expected type = RNodeMultiInterface, got %q", v)
	}

	sub0, ok := multi.Subsections["sub0"]
	if !ok {
		t.Fatal("nested subsection [[[sub0]]] not found")
	}

	if v, _ := sub0.GetProperty("frequency"); v != "433050000" {
		t.Fatalf("expected frequency = 433050000, got %q", v)
	}
	if v, _ := sub0.GetProperty("interface_enabled"); strings.ToLower(v) != "yes" {
		t.Fatalf("expected interface_enabled = Yes, got %q", v)
	}
}
