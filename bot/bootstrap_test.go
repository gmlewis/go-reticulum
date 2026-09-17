// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tempDir returns a short-lived directory under /tmp. t.TempDir() is unusable
// here: on macOS its path is too long for the Unix domain sockets Reticulum
// binds.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "gorrcbot-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestDefaultBotPathsUsesGorrcbotHome asserts GORRCBOT_HOME overrides the state
// directory and that the three state files hang off it.
func TestDefaultBotPathsUsesGorrcbotHome(t *testing.T) {
	home := tempDir(t)
	t.Setenv("GORRCBOT_HOME", home)

	paths := DefaultBotPaths()
	if paths.Home != home {
		t.Errorf("Home = %q, want %q", paths.Home, home)
	}
	if want := filepath.Join(home, "config.toml"); paths.ConfigPath != want {
		t.Errorf("ConfigPath = %q, want %q", paths.ConfigPath, want)
	}
	if want := filepath.Join(home, "bot_identity"); paths.IdentityPath != want {
		t.Errorf("IdentityPath = %q, want %q", paths.IdentityPath, want)
	}
	if want := filepath.Join(home, "storage"); paths.StorageDir != want {
		t.Errorf("StorageDir = %q, want %q", paths.StorageDir, want)
	}
}

// TestDefaultBotPathsFallsBackToHomeDir asserts the default is ~/.gorrcbot when
// no override is set.
func TestDefaultBotPathsFallsBackToHomeDir(t *testing.T) {
	t.Setenv("GORRCBOT_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}

	paths := DefaultBotPaths()
	if want := filepath.Join(home, ".gorrcbot"); paths.Home != want {
		t.Errorf("Home = %q, want %q", paths.Home, want)
	}
}

// TestEnsureFirstRunCreatesConfigAndIdentity asserts a first run creates a
// valid, self-documenting config and a 64-byte identity with mode 0o600.
func TestEnsureFirstRunCreatesConfigAndIdentity(t *testing.T) {
	home := tempDir(t)
	t.Setenv("GORRCBOT_HOME", home)
	paths := DefaultBotPaths()

	created, err := EnsureFirstRun(paths)
	if err != nil {
		t.Fatalf("EnsureFirstRun: %v", err)
	}
	if !created {
		t.Fatal("EnsureFirstRun reported nothing created on a first run")
	}

	info, err := os.Stat(paths.IdentityPath)
	if err != nil {
		t.Fatalf("stat identity: %v", err)
	}
	if got := info.Size(); got != 64 {
		t.Errorf("identity size = %v bytes, want 64", got)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("identity mode = %v, want 0o600", perm)
	}

	raw, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(raw), "[[hubs]]") {
		t.Error("generated config carries no [[hubs]] template entry")
	}

	cfg, warnings, err := DecodeBotConfig(paths.ConfigPath, string(raw))
	if err != nil {
		t.Fatalf("the generated template does not parse: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("the generated template produced warnings: %v", warnings)
	}
	// The default configuration talks to exactly one hub: the local
	// development hub.
	if len(cfg.Hubs) != 1 {
		t.Errorf("template hubs = %v, want 1 live hub", len(cfg.Hubs))
	}
	if len(cfg.Hubs) > 0 {
		if got := cfg.Hubs[0].Name; got != "gonomadnet Public Hub" {
			t.Errorf("template hub name = %q, want the local hub", got)
		}
		if got := cfg.Hubs[0].Destination; got != "a012129c10205c0b9441fcd2b755b2a7" {
			t.Errorf("template hub destination = %q, want the local hub's hash", got)
		}
	}
	// The public RNS Community Hub must not appear in the generated config at
	// all, neither live nor as a copy-pasteable example: it does not work, and
	// a first run must never reach it.
	if strings.Contains(string(raw), "28c7c1a68c735693aa8e6b8193ed44b2") {
		t.Error("the generated config still carries the public RNS Community Hub hash")
	}
	if strings.Contains(string(raw), "RNS Community") {
		t.Error("the generated config still names the public RNS Community Hub")
	}
	for _, hub := range cfg.Hubs {
		if hub.Destination == "28c7c1a68c735693aa8e6b8193ed44b2" {
			t.Error("the public RNS Community Hub is live in the default configuration")
		}
	}
	if cfg.Nick != "gobot" {
		t.Errorf("template nick = %q, want %q", cfg.Nick, "gobot")
	}
	// The template offers the kjv command's text file, empty by default, with
	// the comment that says what it is for.
	if !strings.Contains(string(raw), "kjv_txt_file = \"\"") {
		t.Error("the generated config does not offer the empty kjv_txt_file key")
	}
	if !strings.Contains(string(raw), "the 'kjv' command") {
		t.Error("the generated config does not document what kjv_txt_file is for")
	}
	if cfg.KJVTxtFile != "" {
		t.Errorf("template kjv_txt_file = %q, want empty", cfg.KJVTxtFile)
	}
	// The template offers the tower command's local dataset, pointed at the
	// state directory so an operator can simply drop the file there, and
	// documents the column order it expects.
	if !strings.Contains(string(raw), "towers_path = ") {
		t.Error("the generated config does not offer the towers_path key")
	}
	if !strings.Contains(string(raw), "id,name,type,lat,lng,freq,offset,tone,operator,city,region,country,elev") {
		t.Error("the generated config does not document the towers.csv column order")
	}
	if want := paths.TowersPath; cfg.TowersPath != want {
		t.Errorf("template towers_path = %q, want %q", cfg.TowersPath, want)
	}
}

// TestEnsureFirstRunIsIdempotent asserts a second run leaves both files
// byte-identical, so restarting the bot never rotates its identity.
func TestEnsureFirstRunIsIdempotent(t *testing.T) {
	home := tempDir(t)
	t.Setenv("GORRCBOT_HOME", home)
	paths := DefaultBotPaths()

	if _, err := EnsureFirstRun(paths); err != nil {
		t.Fatalf("first EnsureFirstRun: %v", err)
	}
	configBefore, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	identityBefore, err := os.ReadFile(paths.IdentityPath)
	if err != nil {
		t.Fatalf("read identity: %v", err)
	}

	created, err := EnsureFirstRun(paths)
	if err != nil {
		t.Fatalf("second EnsureFirstRun: %v", err)
	}
	if created {
		t.Error("EnsureFirstRun reported created=true on an existing state directory")
	}

	configAfter, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatalf("re-read config: %v", err)
	}
	identityAfter, err := os.ReadFile(paths.IdentityPath)
	if err != nil {
		t.Fatalf("re-read identity: %v", err)
	}
	if string(configAfter) != string(configBefore) {
		t.Error("config.toml changed on the second run")
	}
	if string(identityAfter) != string(identityBefore) {
		t.Error("bot_identity changed on the second run")
	}
}

// TestEnsureFirstRunReplacesTruncatedIdentity asserts a corrupt identity is
// detected with a clear error instead of being silently reused or panicking.
func TestEnsureFirstRunReplacesTruncatedIdentity(t *testing.T) {
	home := tempDir(t)
	t.Setenv("GORRCBOT_HOME", home)
	paths := DefaultBotPaths()

	if _, err := EnsureFirstRun(paths); err != nil {
		t.Fatalf("EnsureFirstRun: %v", err)
	}
	// Truncate the stored key material: a shorter-than-expected file cannot
	// be a valid identity.
	if err := os.WriteFile(paths.IdentityPath, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 0o600); err != nil {
		t.Fatalf("truncate identity: %v", err)
	}

	_, _, err := LoadBotIdentity(paths.IdentityPath)
	if err == nil {
		t.Fatal("LoadBotIdentity(truncated) = nil error, want a clear error")
	}
	if !strings.Contains(err.Error(), paths.IdentityPath) {
		t.Errorf("error = %q, want it to name the identity path", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "identity") {
		t.Errorf("error = %q, want it to explain the identity file", err)
	}
}

// TestLoadBotIdentityCreatesOnDemand asserts LoadBotIdentity creates the
// identity on first use and returns the same hash on every later load.
func TestLoadBotIdentityCreatesOnDemand(t *testing.T) {
	home := tempDir(t)
	t.Setenv("GORRCBOT_HOME", home)
	paths := DefaultBotPaths()

	first, created, err := LoadBotIdentity(paths.IdentityPath)
	if err != nil {
		t.Fatalf("LoadBotIdentity: %v", err)
	}
	if !created {
		t.Error("created = false on a missing identity file, want true")
	}
	if len(first.Hash) != 16 {
		t.Errorf("identity hash length = %v, want 16", len(first.Hash))
	}

	second, created, err := LoadBotIdentity(paths.IdentityPath)
	if err != nil {
		t.Fatalf("second LoadBotIdentity: %v", err)
	}
	if created {
		t.Error("created = true on an existing identity file, want false")
	}
	if string(second.Hash) != string(first.Hash) {
		t.Error("the identity hash changed between loads")
	}
}

// TestFirstRunMessageNamesThePaths asserts the first-run notice tells the
// operator what was created and what to do next.
func TestFirstRunMessageNamesThePaths(t *testing.T) {
	paths := BotPaths{
		Home:         "/home/u/.gorrcbot",
		ConfigPath:   "/home/u/.gorrcbot/config.toml",
		IdentityPath: "/home/u/.gorrcbot/bot_identity",
		StorageDir:   "/home/u/.gorrcbot/storage",
	}
	msg := firstRunMessage(paths)
	for _, want := range []string{"gorrcbot", paths.ConfigPath, paths.IdentityPath} {
		if !strings.Contains(msg, want) {
			t.Errorf("first-run message = %q, want it to mention %q", msg, want)
		}
	}
}

// TestBotPathsStorageIsADirectory asserts the one path the bot hands to the RRC
// client is named like the directory it is. It was called state.toml, so the
// client created a DIRECTORY with a file's name.
func TestBotPathsStorageIsADirectory(t *testing.T) {
	t.Parallel()

	home := tempDir(t)
	paths := BotPaths{
		Home:         home,
		ConfigPath:   filepath.Join(home, defaultConfigFileName),
		IdentityPath: filepath.Join(home, defaultIdentityFileName),
		StorageDir:   filepath.Join(home, defaultStorageDirName),
	}
	if filepath.Ext(paths.StorageDir) != "" {
		t.Errorf("StorageDir = %q, want a plain directory name with no extension",
			paths.StorageDir)
	}
	if !strings.HasSuffix(paths.StorageDir, string(filepath.Separator)+defaultStorageDirName) {
		t.Errorf("StorageDir = %q, want it under %q", paths.StorageDir, defaultStorageDirName)
	}

	created, err := EnsureFirstRun(paths)
	if err != nil {
		t.Fatalf("EnsureFirstRun: %v", err)
	}
	if !created {
		t.Error("EnsureFirstRun reported nothing created")
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".toml") && entry.Name() != defaultConfigFileName {
			t.Errorf("first run created %q, want only %q as a TOML file",
				entry.Name(), defaultConfigFileName)
		}
	}
}
