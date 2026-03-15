// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"
	"time"
)

func TestTimeoutFlag(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"1", time.Second, false},
		{"0.5", 500 * time.Millisecond, false},
		{"1.5", 1500 * time.Millisecond, false},
		{"invalid", 0, true},
	}

	for _, tc := range tests {
		var d time.Duration
		tf := (*timeoutFlag)(&d)
		err := tf.Set(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("Set(%q) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && d != tc.expected {
			t.Errorf("Set(%q) got %v, want %v", tc.input, d, tc.expected)
		}
	}
}

func TestCountFlag(t *testing.T) {
	var c countFlag
	if c.String() != "0" {
		t.Errorf("expected 0, got %s", c.String())
	}

	if err := c.Set("true"); err != nil {
		t.Fatal(err)
	}
	if c.String() != "1" {
		t.Errorf("expected 1, got %s", c.String())
	}

	if err := c.Set("true"); err != nil {
		t.Fatal(err)
	}
	if c.String() != "2" {
		t.Errorf("expected 2, got %s", c.String())
	}
}
