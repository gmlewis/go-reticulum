// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

// TestParseFlagsDefaults asserts the defaults leave every choice to the
// configuration file.
func TestParseFlagsDefaults(t *testing.T) {
	t.Parallel()

	opts, err := parseFlags(nil, &strings.Builder{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.version || opts.checkConfig {
		t.Error("a bare command line asked for version or a config check")
	}
	for name, value := range map[string]string{
		"config":     opts.configDir,
		"bot-config": opts.botConfig,
		"identity":   opts.identity,
		"home":       opts.home,
		"nick":       opts.nick,
		"log-level":  opts.logLevel,
		"log-file":   opts.logFile,
		"pprof-addr": opts.pprofAddr,
	} {
		if value != "" {
			t.Errorf("-%v defaulted to %q, want the empty string", name, value)
		}
	}
}

// TestParseFlagsEveryOption asserts every documented flag reaches its field.
func TestParseFlagsEveryOption(t *testing.T) {
	t.Parallel()

	opts, err := parseFlags([]string{
		"-config", "/etc/reticulum",
		"-bot-config", "/tmp/bot.toml",
		"-identity", "/tmp/bot_identity",
		"-home", "/tmp/bothome",
		"-nick", "helper",
		"-log-level", "DEBUG",
		"-log-file", "/tmp/bot.log",
		"-pprof-addr", "127.0.0.1:0",
		"-check-config",
	}, &strings.Builder{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	for name, got := range map[string]string{
		"-config":     opts.configDir,
		"-bot-config": opts.botConfig,
		"-identity":   opts.identity,
		"-home":       opts.home,
		"-nick":       opts.nick,
		"-log-level":  opts.logLevel,
		"-log-file":   opts.logFile,
		"-pprof-addr": opts.pprofAddr,
	} {
		if got == "" {
			t.Errorf("%v did not reach its field", name)
		}
	}
	if !opts.checkConfig {
		t.Error("-check-config did not reach its field")
	}
}

// TestParseFlagsHelp asserts -h asks for the usage text, and that the text
// documents every flag.
func TestParseFlagsHelp(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	_, err := parseFlags([]string{"-h"}, &out)
	if !errors.Is(err, errHelp) {
		t.Fatalf("parseFlags(-h) error = %v, want %v", err, errHelp)
	}
	text := out.String()
	for _, want := range []string{
		"usage: gorrcbot",
		"--version", "--check-config", "--config", "--bot-config",
		"--identity", "--home", "--nick", "--log-level", "--log-file",
		"--pprof-addr", "GORRCBOT_HOME", "config.toml", "bot_identity",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the usage text does not mention %q", want)
		}
	}
}

// TestParseFlagsVersion asserts -version and -V both ask for the version.
func TestParseFlagsVersion(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"-version", "-V"} {
		opts, err := parseFlags([]string{arg}, &strings.Builder{})
		if err != nil {
			t.Fatalf("parseFlags(%v): %v", arg, err)
		}
		if !opts.version {
			t.Errorf("%v did not ask for the version", arg)
		}
	}
}

// TestParseFlagsBadInput asserts a command line that cannot mean anything is
// refused rather than silently ignored.
func TestParseFlagsBadInput(t *testing.T) {
	t.Parallel()

	t.Run("unknown flag", func(t *testing.T) {
		t.Parallel()
		if _, err := parseFlags([]string{"-bogus"}, &strings.Builder{}); err == nil {
			t.Fatal("an unknown flag was accepted")
		}
		if _, err := parseFlags([]string{"-bogus"}, &strings.Builder{}); errors.Is(err, errHelp) {
			t.Error("an unknown flag was reported as a help request")
		}
	})

	t.Run("positional argument", func(t *testing.T) {
		t.Parallel()
		_, err := parseFlags([]string{"join", "general"}, &strings.Builder{})
		if err == nil {
			t.Fatal("a positional argument was accepted")
		}
		if !strings.Contains(err.Error(), "unrecognized arguments") {
			t.Errorf("error = %v, want it to name the unrecognized arguments", err)
		}
	})
}

// TestFlagsDoNotUseTheGlobalFlagSet asserts the tool never touches
// flag.CommandLine, which every other CLI in this repository also avoids.
func TestFlagsDoNotUseTheGlobalFlagSet(t *testing.T) {
	t.Parallel()

	before := flag.CommandLine.NFlag()
	if _, err := parseFlags([]string{"-nick", "helper"}, &strings.Builder{}); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if flag.CommandLine.NFlag() != before {
		t.Error("parseFlags wrote to the global flag set")
	}
}
