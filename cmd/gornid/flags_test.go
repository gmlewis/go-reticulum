// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"io"
	"reflect"
	"testing"
)

func TestCounter(t *testing.T) {
	t.Parallel()
	var c counter
	for range 3 {
		if err := c.Set("true"); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}
	if int(c) != 3 {
		t.Fatalf("counter = %v, want 3", int(c))
	}
}

func TestCounterIsBoolFlag(t *testing.T) {
	t.Parallel()
	var c counter
	if !c.IsBoolFlag() {
		t.Fatal("IsBoolFlag() = false, want true")
	}
}

func TestCounterString(t *testing.T) {
	t.Parallel()
	var c counter
	if c.String() != "0" {
		t.Fatalf("String() = %q, want %q", c.String(), "0")
	}
}

func TestAppFlags(t *testing.T) {
	t.Parallel()
	app := newApp()
	fs := flag.NewFlagSet("gornid", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	app.initFlags(fs)
	if err := fs.Parse([]string{"--verbose", "--quiet", "--version"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if app.verbose != 1 || app.quiet != 1 || !app.version {
		t.Fatalf("unexpected app state: %+v", app)
	}
}

func TestLongFormParserAliases(t *testing.T) {
	t.Parallel()
	app := newApp()
	fs := flag.NewFlagSet("gornid", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	app.initFlags(fs)
	if err := fs.Parse([]string{"--generate", "out.id", "--identity", "in.id", "--print-identity"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if app.generatePath != "out.id" || app.identityPath != "in.id" || !app.printIdentity {
		t.Fatalf("unexpected alias state: %+v", app)
	}
}

func TestParseFlags(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{"--verbose", "--quiet", "--version"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if app.verbose != 1 || app.quiet != 1 || !app.version {
		t.Fatalf("unexpected app state: %+v", app)
	}
}

func TestUsageText(t *testing.T) {
	t.Parallel()
	if got := bytes.NewBufferString(usageText).String(); got == "" || !bytes.Contains([]byte(got), []byte("Go Reticulum Identity & Encryption Utility")) {
		t.Fatalf("usageText missing expected content: %q", got)
	}
}

func TestMultiValueSignFlag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"repeated", []string{"-s", "a", "-s", "b", "-s", "c"}, []string{"a", "b", "c"}},
		{"positional", []string{"-s", "a", "b", "c"}, []string{"a", "b", "c"}},
	}
	for _, c := range cases {
		app, err := parseFlags(c.args, io.Discard)
		if err != nil {
			t.Fatalf("%s: parseFlags failed: %v", c.name, err)
		}
		if !reflect.DeepEqual(app.signList.vals, c.want) {
			t.Errorf("%s: signList.vals = %v, want %v", c.name, app.signList.vals, c.want)
		}
		if app.signFile != "a" {
			t.Errorf("%s: signFile = %q, want %q", c.name, app.signFile, "a")
		}
	}
}

func TestMultiValueEncryptFlag(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{"-e", "x", "y", "z"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if !reflect.DeepEqual(app.encryptList.vals, []string{"x", "y", "z"}) {
		t.Errorf("encryptList.vals = %v, want [x y z]", app.encryptList.vals)
	}
	if app.encryptFile != "x" {
		t.Errorf("encryptFile = %q, want %q", app.encryptFile, "x")
	}
}

func TestMultiValueDecryptFlag(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{"-d", "p", "q", "r"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if !reflect.DeepEqual(app.decryptList.vals, []string{"p", "q", "r"}) {
		t.Errorf("decryptList.vals = %v, want [p q r]", app.decryptList.vals)
	}
	if app.decryptFile != "p" {
		t.Errorf("decryptFile = %q, want %q", app.decryptFile, "p")
	}
}

func TestMultiValueValidateFlag(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{"-V", "m", "n", "o"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if !reflect.DeepEqual(app.validateList.vals, []string{"m", "n", "o"}) {
		t.Errorf("validateList.vals = %v, want [m n o]", app.validateList.vals)
	}
	if app.validateFile != "m" {
		t.Errorf("validateFile = %q, want %q", app.validateFile, "m")
	}
}

func TestNewFlagsParsed(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{
		"-S", "msg", "-E", "meta.txt", "--meta-spec", "spec", "--meta",
		"-U", "-F", "-N",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if app.signMessage != "msg" {
		t.Errorf("signMessage = %q, want %q", app.signMessage, "msg")
	}
	if app.embedMeta != "meta.txt" {
		t.Errorf("embedMeta = %q, want %q", app.embedMeta, "meta.txt")
	}
	if app.metaSpec != "spec" {
		t.Errorf("metaSpec = %q, want %q", app.metaSpec, "spec")
	}
	if !app.meta {
		t.Errorf("meta = false, want true")
	}
	if !app.useBase256 {
		t.Errorf("useBase256 = false, want true")
	}
	if !app.useHex {
		t.Errorf("useHex = false, want true")
	}
	if !app.noCache {
		t.Errorf("noCache = false, want true")
	}
}

func TestBase256HexFlagLongForms(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{"--base256", "--hex", "--no-cache"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if !app.useBase256 || !app.useHex || !app.noCache {
		t.Errorf("flags not set: base256=%v hex=%v noCache=%v", app.useBase256, app.useHex, app.noCache)
	}
}

func TestNoCacheFlagShortForm(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{"-N", "-U", "-F"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if !app.noCache || !app.useBase256 || !app.useHex {
		t.Errorf("short flags not set: noCache=%v base256=%v hex=%v", app.noCache, app.useBase256, app.useHex)
	}
}
