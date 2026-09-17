// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"flag"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
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

// TestResolvePathsUsesTheHomeOverride asserts -home moves all three state files
// together, and that the per-file flags win over it.
func TestResolvePathsUsesTheHomeOverride(t *testing.T) {
	t.Setenv("GORRCBOT_HOME", "")

	opts, err := parseFlags([]string{"-home", "/tmp/gorrcbot-home"}, &strings.Builder{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	paths := resolvePaths(opts)
	if paths.Home != "/tmp/gorrcbot-home" {
		t.Errorf("Home = %q, want the override", paths.Home)
	}
	for name, got := range map[string]string{
		"config":   paths.ConfigPath,
		"identity": paths.IdentityPath,
		"storage":  paths.StorageDir,
	} {
		if filepath.Dir(got) != "/tmp/gorrcbot-home" {
			t.Errorf("%v = %q, want it under the overridden home", name, got)
		}
	}

	opts, err = parseFlags([]string{
		"-home", "/tmp/gorrcbot-home",
		"-bot-config", "/tmp/elsewhere/bot.toml",
		"-identity", "/tmp/elsewhere/bot_identity",
	}, &strings.Builder{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	paths = resolvePaths(opts)
	if paths.ConfigPath != "/tmp/elsewhere/bot.toml" {
		t.Errorf("ConfigPath = %q, want the per-file override", paths.ConfigPath)
	}
	if paths.IdentityPath != "/tmp/elsewhere/bot_identity" {
		t.Errorf("IdentityPath = %q, want the per-file override", paths.IdentityPath)
	}
	if paths.StorageDir != filepath.Join("/tmp/gorrcbot-home", defaultStorageDirName) {
		t.Errorf("StorageDir = %q, want it to stay under -home", paths.StorageDir)
	}
}

// TestResolvePathsHonorsGORRCBOTHome asserts the environment variable selects the
// home when no flag overrides it.
func TestResolvePathsHonorsGORRCBOTHome(t *testing.T) {
	t.Setenv("GORRCBOT_HOME", "/tmp/gorrcbot-env-home")

	opts, err := parseFlags(nil, &strings.Builder{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	paths := resolvePaths(opts)
	if paths.Home != "/tmp/gorrcbot-env-home" {
		t.Fatalf("Home = %q, want GORRCBOT_HOME", paths.Home)
	}
	if paths.ConfigPath != filepath.Join("/tmp/gorrcbot-env-home", defaultConfigFileName) {
		t.Errorf("ConfigPath = %q, want it under GORRCBOT_HOME", paths.ConfigPath)
	}
	if paths.IdentityPath != filepath.Join("/tmp/gorrcbot-env-home", defaultIdentityFileName) {
		t.Errorf("IdentityPath = %q, want it under GORRCBOT_HOME", paths.IdentityPath)
	}
}

// TestConfigSummaryDescribesWhatTheBotWouldDo asserts --check-config is a real
// dry run: it names the files, the identity hash, the trigger, and every hub
// with its rooms.
func TestConfigSummaryDescribesWhatTheBotWouldDo(t *testing.T) {
	t.Parallel()

	cfg, _, err := DecodeBotConfig("config.toml", defaultConfigContent(BotPaths{
		Home:         "/tmp/gorrcbot-summary",
		ConfigPath:   "/tmp/gorrcbot-summary/config.toml",
		IdentityPath: "/tmp/gorrcbot-summary/bot_identity",
		StorageDir:   "/tmp/gorrcbot-summary/storage",
	}))
	if err != nil {
		t.Fatalf("DecodeBotConfig: %v", err)
	}
	paths := BotPaths{
		Home:         "/tmp/gorrcbot-summary",
		ConfigPath:   "/tmp/gorrcbot-summary/config.toml",
		IdentityPath: "/tmp/gorrcbot-summary/bot_identity",
		StorageDir:   "/tmp/gorrcbot-summary/storage",
	}
	summary := configSummary(paths, cfg, mustHex(fakeHubTwo))

	for _, want := range []string{
		"gorrcbot " + rns.VERSION,
		"config:     /tmp/gorrcbot-summary/config.toml",
		"identity:   /tmp/gorrcbot-summary/bot_identity (" + fakeHubTwo + ")",
		"storage:    /tmp/gorrcbot-summary/storage",
		"nick:       gobot",
		"trigger:    @gobot or @" + fakeHubTwo[:12],
		"hubs:       1",
		"gonomadnet Public Hub (" + fakeHubOne + ")",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary does not contain %q:\n%v", want, summary)
		}
	}
	// Every room must be listed, or an operator cannot see a typo.
	for _, room := range []string{"general"} {
		if !strings.Contains(summary, "room: "+room) {
			t.Errorf("the summary does not list room %q", room)
		}
	}
	// A configured kjv text file is reported, so an operator can see where the
	// Bible lookup will read from.
	cfg.KJVTxtFile = "/tmp/gorrcbot-summary/kjv.txt"
	if got := configSummary(paths, cfg, mustHex(fakeHubTwo)); !strings.Contains(got, "kjv:        /tmp/gorrcbot-summary/kjv.txt") {
		t.Errorf("the summary does not name the configured kjv text file:\n%v", got)
	}
}

// TestGreetingNamesTheBotAndHowToAddressIt asserts the self-introduction says
// what the bot is and how to talk to it, in one line.
func TestGreetingNamesTheBotAndHowToAddressIt(t *testing.T) {
	t.Parallel()

	session, _ := newReplySession(t, defaultTestConfig())
	line := greeting(session, "general")
	if strings.Contains(line, "\n") {
		t.Errorf("greeting = %q, want a single line", line)
	}
	for _, want := range []string{"gorrcbot", "@gorrcbot help"} {
		if !strings.Contains(line, want) {
			t.Errorf("greeting = %q, want it to mention %q", line, want)
		}
	}
	if fits, err := noticeFits(session.bot.ownHash, "general", "gorrcbot", line); err != nil {
		t.Fatalf("noticeFits: %v", err)
	} else if !fits {
		t.Errorf("greeting = %q, want it to fit one envelope", line)
	}

	// A per-hub nick is honoured, so the greeting matches what clients see.
	nickSession := newSessionWithHubNick(t, defaultTestConfig(), "helper")
	if line = greeting(nickSession, "general"); !strings.Contains(line, "@helper help") {
		t.Errorf("greeting = %q, want it to use the hub's nick", line)
	}
}

// newSessionWithHubNick builds a connected, joined session whose hub advertises
// the given nick.
func newSessionWithHubNick(t *testing.T, cfg *BotConfig, nick string) *hubSession {
	t.Helper()
	hubCfg := &HubConfig{
		Name:        "One",
		Destination: fakeHubOne,
		Nick:        nick,
		Rooms:       []RoomConfig{{Name: "general"}},
	}
	fake := newFakeHub(hubCfg)
	fake.rooms["general"] = true
	fake.setStatus(rrc.StatusConnected)
	b := newBot(cfg, BotPaths{}, nil, mustHex(replyOwnHash), nil, botHooks{})
	s := newHubSession(b, hubCfg, fake)
	s.joinedOK["general"] = true
	return s
}

// TestParseLogLevelNames asserts every documented level name maps to the RNS
// logger's level, and that a bad name is refused instead of silently ignored.
func TestParseLogLevelNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want int
	}{
		{"CRITICAL", rns.LogCritical},
		{"error", rns.LogError},
		{"Warning", rns.LogWarning},
		{"warn", rns.LogWarning},
		{"notice", rns.LogNotice},
		{"INFO", rns.LogInfo},
		{"verbose", rns.LogVerbose},
		{"debug", rns.LogDebug},
		{"pathing", rns.LogPathing},
		{"EXTREME", rns.LogExtreme},
		{"NONE", rns.LogNone},
		{"off", rns.LogNone},
		{"4", rns.LogInfo},
		{" -1 ", rns.LogNone},
	}
	for _, tc := range cases {
		got, err := parseLogLevel(tc.name)
		if err != nil {
			t.Errorf("parseLogLevel(%q): %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseLogLevel(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}

	for _, bad := range []string{"", "loud", "9", "-2", "INFOO"} {
		if _, err := parseLogLevel(bad); err == nil {
			t.Errorf("parseLogLevel(%q) was accepted, want an error", bad)
		}
	}
}

// TestApplyLoggingConfiguresTheSharedLogger asserts the two log flags reach the
// logger every part of the bot shares, since one switch has to quiet the whole
// process and not just one component.
func TestApplyLoggingConfiguresTheSharedLogger(t *testing.T) {
	t.Parallel()

	logger := rns.NewLogger()
	logPath := filepath.Join(tempDir(t), "gorrcbot.log")

	opts, err := parseFlags([]string{"-log-level", "WARNING", "-log-file", logPath},
		&strings.Builder{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if err := applyLogging(logger, opts); err != nil {
		t.Fatalf("applyLogging: %v", err)
	}
	if got := logger.GetLogLevel(); got != rns.LogWarning {
		t.Errorf("log level = %v, want %v", got, rns.LogWarning)
	}
	if got := logger.GetLogFilePath(); got != logPath {
		t.Errorf("log file = %q, want %q", got, logPath)
	}
	if got := logger.GetLogDest(); got != rns.LogDestFile {
		t.Errorf("log destination = %v, want the file destination", got)
	}

	// Defaults must leave the logger alone rather than imposing a level.
	quiet := rns.NewLogger()
	before := quiet.GetLogLevel()
	opts, err = parseFlags(nil, &strings.Builder{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if err := applyLogging(quiet, opts); err != nil {
		t.Fatalf("applyLogging: %v", err)
	}
	if got := quiet.GetLogLevel(); got != before {
		t.Errorf("log level = %v, want the default %v", got, before)
	}

	// A bad level is refused rather than ignored.
	opts, err = parseFlags([]string{"-log-level", "loud"}, &strings.Builder{})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if err := applyLogging(rns.NewLogger(), opts); err == nil {
		t.Error("a bad log level was accepted")
	}
}

// TestApplyConfiguredPathsHonourTheConfigFile asserts the configuration file can
// move the identity and the storage directory, and that an explicit command-line
// flag still wins. The keys were parsed and documented but never consulted, so a
// deliberate override silently used the default path.
func TestApplyConfiguredPathsHonourTheConfigFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts botOptions
		cfg  *BotConfig
		// An empty want means "whatever the command line alone resolved to".
		wantID   string
		wantStor string
	}{
		{
			name: "a default configuration changes nothing",
		},
		{
			name:     "the config file moves both",
			cfg:      &BotConfig{IdentityPath: "/etc/gorrcbot/id", StorageDir: "/var/lib/gorrcbot"},
			wantID:   "/etc/gorrcbot/id",
			wantStor: "/var/lib/gorrcbot",
		},
		{
			name:     "an explicit identity flag beats the config file",
			opts:     botOptions{identity: "/tmp/flag_identity"},
			cfg:      &BotConfig{IdentityPath: "/etc/gorrcbot/id", StorageDir: "/var/lib/gorrcbot"},
			wantID:   "/tmp/flag_identity",
			wantStor: "/var/lib/gorrcbot",
		},
		{
			name: "the home flag wins over a configured storage directory",
			opts: botOptions{home: "/tmp/gorrcbot-home"},
			cfg:  &BotConfig{StorageDir: "/var/lib/gorrcbot"},
		},
		{
			name:     "the config file alone still moves the identity",
			cfg:      &BotConfig{IdentityPath: "/etc/gorrcbot/id"},
			wantID:   "/etc/gorrcbot/id",
			wantStor: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolved := resolvePaths(&tt.opts)
			got := applyConfiguredPaths(resolved, tt.cfg, &tt.opts)
			wantID, wantStor := tt.wantID, tt.wantStor
			if wantID == "" {
				wantID = resolved.IdentityPath
			}
			if wantStor == "" {
				wantStor = resolved.StorageDir
			}
			if got.IdentityPath != wantID {
				t.Errorf("IdentityPath = %q, want %q", got.IdentityPath, wantID)
			}
			if got.StorageDir != wantStor {
				t.Errorf("StorageDir = %q, want %q", got.StorageDir, wantStor)
			}
		})
	}
}

// TestConfigSummaryReportsTheCompass asserts --check-config names the compass
// the way it names the GNSS source, so an operator can see which heading device
// the direction-finding answers will use before leaving the house.
func TestConfigSummaryReportsTheCompass(t *testing.T) {
	t.Parallel()

	paths := BotPaths{
		Home:         "/tmp/gorrcbot-summary",
		ConfigPath:   "/tmp/gorrcbot-summary/config.toml",
		IdentityPath: "/tmp/gorrcbot-summary/bot_identity",
		StorageDir:   "/tmp/gorrcbot-summary/storage",
	}
	cases := []struct {
		name string
		cfg  BotConfig
		want string
	}{
		{"streaming compass", BotConfig{CompassPort: "/dev/ttyUSB1"}, "compass:    reading /dev/ttyUSB1"},
		{"static heading", BotConfig{CompassHeading: "042"}, "compass:    static heading 042"},
		{"no compass", BotConfig{}, "compass:    (no compass)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tc.cfg
			summary := configSummary(paths, &cfg, mustHex(fakeHubTwo))
			if !strings.Contains(summary, tc.want) {
				t.Errorf("the summary does not contain %q:\n%v", tc.want, summary)
			}
		})
	}
}
