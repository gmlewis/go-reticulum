// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package utils

import (
	"bytes"
	"errors"
	"flag"
	"reflect"
	"strings"
	"testing"
)

func TestErrHelp(t *testing.T) {
	t.Parallel()

	if ErrHelp == nil {
		t.Fatal("ErrHelp is nil")
	}
	if !errors.Is(ErrHelp, ErrHelp) {
		t.Fatal("ErrHelp does not compare equal to itself")
	}
	if got, want := ErrHelp.Error(), "help requested"; got != want {
		t.Fatalf("ErrHelp.Error() = %q, want %q", got, want)
	}
}

func TestPrintVersionLine(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	PrintVersion(&out, "gornprobe", "0.1.0")
	if got, want := out.String(), "gornprobe 0.1.0\n"; got != want {
		t.Fatalf("PrintVersion output = %q, want %q", got, want)
	}
}

func TestNewFlagSetInvokesUsageOnHelp(t *testing.T) {
	t.Parallel()

	var called bool
	fs := NewFlagSet("gornprobe", func() {
		called = true
	})
	if err := fs.Parse([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("Parse returned %v, want flag.ErrHelp", err)
	}
	if !called {
		t.Fatal("usage function was not called")
	}
}

func TestWriteText(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	WriteText(&out, strings.Repeat("x", 3))
	if got, want := out.String(), "xxx"; got != want {
		t.Fatalf("WriteText output = %q, want %q", got, want)
	}
}

func TestAsInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		v    any
		want int
		ok   bool
	}{
		{int(42), 42, true},
		{int64(42), 42, true},
		{uint64(42), 42, true},
		{float64(42), 42, true},
		{"42", 0, false},
		{nil, 0, false},
	}
	for _, tc := range tests {
		got, ok := AsInt(tc.v)
		if got != tc.want || ok != tc.ok {
			t.Errorf("AsInt(%v) = %v, %v; want %v, %v", tc.v, got, ok, tc.want, tc.ok)
		}
	}
}

func TestShlexSplit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		s       string
		want    []string
		wantErr bool
	}{
		{`ls -l`, []string{"ls", "-l"}, false},
		{`echo "hello world"`, []string{"echo", "hello world"}, false},
		{`echo 'hello world'`, []string{"echo", "hello world"}, false},
		{`cmd "arg with 'quotes'"`, []string{"cmd", "arg with 'quotes'"}, false},
		{`unclosed "quote`, nil, true},
		{`  multiple   spaces  `, []string{"multiple", "spaces"}, false},
	}
	for _, tc := range tests {
		got, err := ShlexSplit(tc.s)
		if (err != nil) != tc.wantErr {
			t.Errorf("ShlexSplit(%q) error = %v, wantErr %v", tc.s, err, tc.wantErr)
			continue
		}
		if err == nil {
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ShlexSplit(%q) = %v, want %v", tc.s, got, tc.want)
			}
		}
	}
}
