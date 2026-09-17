// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// TestResolvePathsUsesTheHomeOverride asserts Home moves all three state files
// together, and that the per-file options win over it.
func TestResolvePathsUsesTheHomeOverride(t *testing.T) {
	t.Setenv("GORRCBOT_HOME", "")

	paths := resolvePaths(&Options{Home: "/tmp/gorrcbot-home"})
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

	paths = resolvePaths(&Options{
		Home:      "/tmp/gorrcbot-home",
		BotConfig: "/tmp/elsewhere/bot.toml",
		Identity:  "/tmp/elsewhere/bot_identity",
	})
	if paths.ConfigPath != "/tmp/elsewhere/bot.toml" {
		t.Errorf("ConfigPath = %q, want the per-file override", paths.ConfigPath)
	}
	if paths.IdentityPath != "/tmp/elsewhere/bot_identity" {
		t.Errorf("IdentityPath = %q, want the per-file override", paths.IdentityPath)
	}
	if paths.StorageDir != filepath.Join("/tmp/gorrcbot-home", defaultStorageDirName) {
		t.Errorf("StorageDir = %q, want it to stay under Home", paths.StorageDir)
	}
}

// TestResolvePathsHonorsGORRCBOTHome asserts the environment variable selects the
// home when no option overrides it.
func TestResolvePathsHonorsGORRCBOTHome(t *testing.T) {
	t.Setenv("GORRCBOT_HOME", "/tmp/gorrcbot-env-home")

	paths := resolvePaths(&Options{})
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

// TestConfigSummaryDescribesWhatTheBotWouldDo asserts CheckConfig is a real dry
// run: it names the files, the identity hash, the trigger, and every hub with
// its rooms.
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

// TestApplyLoggingConfiguresTheSharedLogger asserts the two log options reach
// the logger every part of the bot shares, since one switch has to quiet the
// whole process and not just one component.
func TestApplyLoggingConfiguresTheSharedLogger(t *testing.T) {
	t.Parallel()

	logger := rns.NewLogger()
	logPath := filepath.Join(tempDir(t), "gorrcbot.log")

	opts := Options{LogLevel: "WARNING", LogFile: logPath}
	if err := applyLogging(logger, &opts); err != nil {
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
	if err := applyLogging(quiet, &Options{}); err != nil {
		t.Fatalf("applyLogging: %v", err)
	}
	if got := quiet.GetLogLevel(); got != before {
		t.Errorf("log level = %v, want the default %v", got, before)
	}

	// A bad level is refused rather than ignored.
	if err := applyLogging(rns.NewLogger(), &Options{LogLevel: "loud"}); err == nil {
		t.Error("a bad log level was accepted")
	}

	// A logger is required, so a caller cannot silently configure nothing.
	if err := applyLogging(nil, &Options{}); err == nil {
		t.Error("a nil logger was accepted")
	}
}

// TestApplyConfiguredPathsHonourTheConfigFile asserts the configuration file can
// move the identity and the storage directory, and that an explicit command-line
// option still wins. The keys were parsed and documented but never consulted, so
// a deliberate override silently used the default path.
func TestApplyConfiguredPathsHonourTheConfigFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts Options
		cfg  *BotConfig
		// An empty want means "whatever the options alone resolved to".
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
			name:     "an explicit identity option beats the config file",
			opts:     Options{Identity: "/tmp/flag_identity"},
			cfg:      &BotConfig{IdentityPath: "/etc/gorrcbot/id", StorageDir: "/var/lib/gorrcbot"},
			wantID:   "/tmp/flag_identity",
			wantStor: "/var/lib/gorrcbot",
		},
		{
			name: "the home option wins over a configured storage directory",
			opts: Options{Home: "/tmp/gorrcbot-home"},
			cfg:  &BotConfig{StorageDir: "/var/lib/gorrcbot"},
		},
		{
			name:     "the config file alone still moves the identity",
			cfg:      &BotConfig{IdentityPath: "/etc/gorrcbot/id"},
			wantID:   "/etc/gorrcbot/id",
			wantStor: "",
		},
		{
			name: "a nil configuration leaves the paths alone",
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

// TestConfigSummaryReportsTheCompass asserts CheckConfig names the compass the
// way it names the GNSS source, so an operator can see which heading device the
// direction-finding answers will use before leaving the house.
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

// TestStartPProfIsOffByDefault asserts an empty address starts no listener and
// that the returned stop function is always safe to call, so a caller never has
// to track whether the debug server came up.
func TestStartPProfIsOffByDefault(t *testing.T) {
	t.Parallel()

	stop := startPProf("")
	stop()
	stop()
}
