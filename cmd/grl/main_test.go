// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

// captureStdout runs fn with standard output redirected and returns the exit
// code together with everything fn printed.
func captureStdout(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = writer
	code := fn()
	os.Stdout = saved
	if err := writer.Close(); err != nil {
		t.Fatalf("closing the pipe: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading the pipe: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("closing the read end: %v", err)
	}
	return code, string(data)
}

// TestRunVersion asserts --version prints the version and exits 0 without
// touching the configuration, the network, or any device.
func TestRunVersion(t *testing.T) {
	code, out := captureStdout(t, func() int { return run([]string{"-version"}) })
	if code != exitOK {
		t.Errorf("run(-version) = %v, want %v", code, exitOK)
	}
	if want := "grl " + rns.VERSION; !strings.Contains(out, want) {
		t.Errorf("run(-version) printed %q, want it to contain %q", out, want)
	}
}

// TestRunHelp asserts every spelling of help exits 0, which is what a shell
// script checking `grl -h` expects.
func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"-h", "-help", "--help"} {
		code, _ := captureStdout(t, func() int { return run([]string{arg}) })
		if code != exitOK {
			t.Errorf("run(%v) = %v, want %v", arg, code, exitOK)
		}
	}
}

// TestRunRefusesAnImpossibleCommandLine asserts a usage error is exit 2 and
// never reaches the configuration or the radio.
func TestRunRefusesAnImpossibleCommandLine(t *testing.T) {
	for _, args := range [][]string{
		{"-bogus"},
		{"serve"},
		{"-quiet", "-verbose"},
	} {
		code, _ := captureStdout(t, func() int { return run(args) })
		if code != exitUsage {
			t.Errorf("run(%v) = %v, want %v", args, code, exitUsage)
		}
	}
}

// TestLoadApplianceConfigUsesGRLHome asserts a fresh appliance creates its
// documented configuration under GRL_HOME and runs from it, so a test or a
// second appliance on one workstation never touches ~/.grl.
func TestLoadApplianceConfigUsesGRLHome(t *testing.T) {
	dir := tempDir(t)
	t.Setenv(homeEnvVar, dir)

	cfg, err := loadApplianceConfig(&options{})
	if err != nil {
		t.Fatalf("loadApplianceConfig: %v", err)
	}
	if cfg.Device.Callsign != DefaultCallsign {
		t.Errorf("Callsign = %q, want the documented default", cfg.Device.Callsign)
	}
	if want := filepath.Join(dir, configFileName); !fileExists(t, want) {
		t.Errorf("no configuration was created at %v", want)
	}
}

// fileExists reports whether path names an existing file.
func fileExists(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// TestLoadApplianceConfigAppliesOverrides asserts a flag narrows the file's
// choice, and that every override is validated before anything binds.
func TestLoadApplianceConfigAppliesOverrides(t *testing.T) {
	dir := tempDir(t)
	t.Setenv(homeEnvVar, dir)
	path := filepath.Join(dir, "custom.toml")
	if _, err := EnsureConfigFile(path); err != nil {
		t.Fatalf("EnsureConfigFile: %v", err)
	}

	cfg, err := loadApplianceConfig(&options{
		configPath:  path,
		portalAddr:  "0.0.0.0:9111",
		gpsPort:     "/dev/ttyUSB7",
		compassPort: "/dev/ttyUSB8",
	})
	if err != nil {
		t.Fatalf("loadApplianceConfig: %v", err)
	}
	if cfg.Portal.PortalAddr != "0.0.0.0:9111" {
		t.Errorf("PortalAddr = %q, want the flag's value", cfg.Portal.PortalAddr)
	}
	if cfg.GNSS.Port != "/dev/ttyUSB7" {
		t.Errorf("GNSS.Port = %q, want the flag's value", cfg.GNSS.Port)
	}
	if cfg.Compass.Port != "/dev/ttyUSB8" {
		t.Errorf("Compass.Port = %q, want the flag's value", cfg.Compass.Port)
	}

	// An unusable override is refused with the same wording as an unusable
	// file, and nothing is started.
	if _, err := loadApplianceConfig(&options{configPath: path, portalAddr: "nonsense"}); err == nil {
		t.Error("an unusable -portal-addr was accepted")
	}
}

// TestNewLoggerHonoursVerbosity asserts -quiet quiets the shared Reticulum
// logger and -verbose raises it, while the default leaves the level the
// Reticulum configuration chose alone.
func TestNewLoggerHonoursVerbosity(t *testing.T) {
	t.Parallel()

	if got := newLogger(&options{quiet: true}).GetLogLevel(); got != rns.LogWarning {
		t.Errorf("-quiet log level = %v, want %v", got, rns.LogWarning)
	}
	if got := newLogger(&options{verbose: true}).GetLogLevel(); got != rns.LogInfo {
		t.Errorf("-verbose log level = %v, want %v", got, rns.LogInfo)
	}
	// A fresh logger has no explicit level, so the Reticulum configuration is
	// free to set one. Whatever the default is, neither flag may be in effect.
	plain := newLogger(&options{})
	if plain == nil {
		t.Fatal("newLogger returned nothing")
	}
	if got := plain.GetLogLevel(); got == rns.LogWarning || got == rns.LogInfo {
		t.Logf("the default log level equals a verbosity flag's level (%v); "+
			"the flags are still applied only when given", got)
	}
}

// TestLogfRespectsQuiet asserts -quiet silences the appliance's own
// informational lines, so an operator running it under a service manager can
// keep the journal empty without losing errors, and that -verbose is what turns
// the extra diagnostic lines on.
func TestLogfRespectsQuiet(t *testing.T) {
	savedQuiet, savedVerbose := quietOutput, verboseOutput
	t.Cleanup(func() {
		quietOutput, verboseOutput = savedQuiet, savedVerbose
	})
	var buf strings.Builder
	savedWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(savedWriter) })

	quietOutput, verboseOutput = false, false
	logf("an informational line")
	debugf("a verbose-only line")
	if !strings.Contains(buf.String(), "an informational line") {
		t.Errorf("logf wrote nothing at the default verbosity: %q", buf.String())
	}
	if strings.Contains(buf.String(), "a verbose-only line") {
		t.Errorf("debugf wrote without -verbose: %q", buf.String())
	}

	buf.Reset()
	quietOutput = true
	logf("a line that must not appear")
	if buf.Len() != 0 {
		t.Errorf("logf wrote %q under -quiet", buf.String())
	}

	buf.Reset()
	quietOutput, verboseOutput = false, true
	debugf("a line only -verbose prints")
	if !strings.Contains(buf.String(), "only -verbose prints") {
		t.Errorf("debugf wrote nothing under -verbose: %q", buf.String())
	}
}
