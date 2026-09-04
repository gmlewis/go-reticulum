// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rrcd"
	"github.com/gmlewis/go-reticulum/testutils"
)

// G12.1 parseFlags mirrors the Python argparse flag set: the path
// defaults are RRCD_HOME-aware, every Python flag exists, unset optional
// flags stay nil, and unknown flags error.
func TestParseFlags(t *testing.T) {
	t.Parallel()

	// Defaults come from the paths helper.
	opts, err := parseFlags([]string{}, nil)
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if opts.config != rrcd.DefaultConfigPath() ||
		opts.identity != rrcd.DefaultIdentityPath() ||
		opts.roomRegistry != rrcd.DefaultRoomRegistryPath() {
		t.Errorf("path defaults = %v/%v/%v, want the RRCD_HOME-aware defaults",
			opts.config, opts.identity, opts.roomRegistry)
	}

	full := []string{
		"--config", "/tmp/c.toml", "--configdir", "/tmp/rns", "--identity", "/tmp/id",
		"--room-registry", "/tmp/rooms.toml", "--no-announce", "--announce-period", "1.5",
		"--hub-name", "TestHub", "--greeting", "hi there", "--include-joined-member-list",
		"--max-rooms", "8", "--max-nick-bytes", "16", "--max-room-name-bytes", "24",
		"--rate-limit-msgs-per-minute", "60", "--max-msg-body-bytes", "200",
		"--ping-interval", "10", "--ping-timeout", "5", "--log-level", "DEBUG",
		"--log-file", "/tmp/rrcd.log",
	}
	opts, err = parseFlags(full, nil)
	if err != nil {
		t.Fatalf("parseFlags(full) error: %v", err)
	}
	if opts.config != "/tmp/c.toml" || opts.configdir == nil || *opts.configdir != "/tmp/rns" ||
		opts.identity != "/tmp/id" || opts.roomRegistry != "/tmp/rooms.toml" {
		t.Errorf("path options = %v/%v/%v/%v", opts.config, opts.configdir, opts.identity, opts.roomRegistry)
	}
	if !opts.noAnnounce || opts.announcePeriod == nil || *opts.announcePeriod != 1.5 {
		t.Errorf("announce options = %v/%v", opts.noAnnounce, opts.announcePeriod)
	}
	if opts.hubName == nil || *opts.hubName != "TestHub" || opts.greeting == nil || *opts.greeting != "hi there" {
		t.Errorf("identity field options = %v/%v", opts.hubName, opts.greeting)
	}
	if !opts.includeJoined {
		t.Error("include-joined-member-list was not set")
	}
	if opts.maxRooms == nil || *opts.maxRooms != 8 ||
		opts.maxNickBytes == nil || *opts.maxNickBytes != 16 ||
		opts.maxRoomNameBytes == nil || *opts.maxRoomNameBytes != 24 ||
		opts.rateLimit == nil || *opts.rateLimit != 60 ||
		opts.maxMsgBodyBytes == nil || *opts.maxMsgBodyBytes != 200 {
		t.Errorf("limit options = %v/%v/%v/%v/%v", opts.maxRooms, opts.maxNickBytes,
			opts.maxRoomNameBytes, opts.rateLimit, opts.maxMsgBodyBytes)
	}
	if opts.pingInterval == nil || *opts.pingInterval != 10 ||
		opts.pingTimeout == nil || *opts.pingTimeout != 5 {
		t.Errorf("ping options = %v/%v", opts.pingInterval, opts.pingTimeout)
	}
	if opts.logLevel == nil || *opts.logLevel != "DEBUG" || opts.logFile == nil || *opts.logFile != "/tmp/rrcd.log" {
		t.Errorf("log options = %v/%v", opts.logLevel, opts.logFile)
	}

	// Unset optionals stay nil.
	opts, err = parseFlags([]string{"--hub-name", "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.announcePeriod != nil || opts.maxRooms != nil || opts.logLevel != nil {
		t.Errorf("unset optionals = %v/%v/%v, want nil", opts.announcePeriod, opts.maxRooms, opts.logLevel)
	}

	// An unknown flag errors, and --dest-name specifically does not
	// exist (argparse rejects it in Python).
	if _, err := parseFlags([]string{"--dest-name", "rrc.hub"}, nil); err == nil {
		t.Error("parseFlags(--dest-name) = nil error, want an unknown-flag error")
	}
	if _, err := parseFlags([]string{"--frobnicate"}, nil); err == nil {
		t.Error("parseFlags(unknown) = nil error, want an error")
	}
	if _, err := parseFlags([]string{"--announce-period", "notafloat"}, nil); err == nil {
		t.Error("parseFlags(bad float) = nil error, want an error")
	}
	if _, err := parseFlags([]string{"--max-rooms", "notanint"}, nil); err == nil {
		t.Error("parseFlags(bad int) = nil error, want an error")
	}
	// Positional arguments are rejected.
	if _, err := parseFlags([]string{"extra"}, nil); err == nil {
		t.Error("parseFlags(extra) = nil error, want an unrecognized-arguments error")
	}

	// Help: the sentinel plus the usage text on the output writer.
	var usage bytes.Buffer
	if _, err := parseFlags([]string{"--help"}, &usage); err == nil || err.Error() != errHelp.Error() {
		t.Errorf("parseFlags(--help) error = %v, want the help sentinel", err)
	}
	if !strings.Contains(usage.String(), "usage: gorrcd") {
		t.Errorf("help usage output = %q, want the usage text", usage.String())
	}
}

// G12.2 ensureFirstRunFiles creates the missing state files and reports
// them; an existing set is left untouched.
func TestEnsureFirstRunFiles(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "firstrun")
	configPath := filepath.Join(dir, "rrcd.toml")
	identityPath := filepath.Join(dir, "hub_identity")
	roomsPath := filepath.Join(dir, "rooms.toml")

	created := ensureFirstRunFiles(configPath, identityPath, roomsPath, nil)
	if !created {
		t.Fatal("ensureFirstRunFiles = false, want true for a fresh dir")
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("config file missing: %v", err)
	}
	if info, err := os.Stat(identityPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("identity file stat = %v, %v; want mode 0o600", info, err)
	}
	if info, err := os.Stat(roomsPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("rooms file stat = %v, %v; want mode 0o600", info, err)
	}
	// The interpolated identity path appears in the config.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "'"+identityPath+"'") {
		t.Errorf("config lacks the interpolated identity path: %q", string(data))
	}

	// A second run creates nothing.
	created = ensureFirstRunFiles(configPath, identityPath, roomsPath, nil)
	if created {
		t.Error("second ensureFirstRunFiles = true, want false")
	}
}

// G12.4 The usage text matches the live Python argparse help after
// normalizing the go-prefix self-reference.
func TestHelpParity(t *testing.T) {
	t.Parallel()

	pythonHelp, err := capturePythonHelp()
	if err != nil {
		t.Skipf("python3 with rrcd unavailable: %v", err)
	}

	normalized := normalizeHelpText(usageText)
	if normalized != normalizeHelpText(pythonHelp) {
		t.Errorf("usage text mismatch:\n--- Go ---\n%v\n--- Python ---\n%v", normalized, normalizeHelpText(pythonHelp))
	}
}

// normalizeHelpText maps the go-prefix self-reference back to the Python
// tool name, following the cmd/gornsd pattern.
func normalizeHelpText(text string) string {
	text = strings.ReplaceAll(text, "gorrcd", "rrcd")
	return strings.ReplaceAll(text, "Go Reticulum", "Reticulum")
}

// capturePythonHelp runs the live Python rrcd argparse help, probing
// python3.14 first and skipping when no interpreter with the rrcd package
// is available.
func capturePythonHelp() (string, error) {
	script := strings.Join([]string{
		"import sys",
		"sys.path.insert(0, r\"/Users/glenn/src/github.com/kc1awv/rrcd\")",
		"from rrcd.cli import _build_arg_parser",
		"import io, contextlib",
		"buf = io.StringIO()",
		"with contextlib.redirect_stdout(buf):",
		"    _build_arg_parser().print_help()",
		"sys.stdout.write(buf.getvalue())",
	}, "\n")
	dir := "/tmp/rrcd-help-capture"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	scriptPath := filepath.Join(dir, "help_capture.py")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return "", err
	}
	defer func() {
		_ = os.RemoveAll(dir)
	}()
	var lastErr error
	for _, py := range []string{"python3.14", "python3"} {
		if _, err := exec.LookPath(py); err != nil {
			lastErr = err
			continue
		}
		cmd := exec.Command(py, scriptPath)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			lastErr = err
			continue
		}
		if stdout.Len() == 0 {
			lastErr = errors.New("empty help output")
			continue
		}
		return stdout.String(), nil
	}
	return "", lastErr
}

// G12.3 buildConfig applies the precedence chain: CLI > TOML > defaults;
// the path seeds are overridden by the TOML keys, config_path and
// dest_name never come from TOML, and the log overrides pass through.
func TestBuildConfigPrecedence(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "precedence")
	cfgPath := filepath.Join(dir, "rrcd.toml")
	configText := strings.Join([]string{
		"[hub]",
		"hub_name = \"TOMLHub\"",
		"max_nick_bytes = 48",
		"identity_path = '/tomes/identity'",
		"room_registry_path = '/tomes/rooms.toml'",
		"configdir = '/tomes/rns'",
		"config_path = '/tomes/ignored.toml'",
		"dest_name = 'ignored.hub'",
		"",
	}, "\n")
	if err := os.WriteFile(cfgPath, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}

	opts, err := parseFlags([]string{
		"--config", cfgPath, "--identity", "/cli/identity", "--room-registry", "/cli/rooms.toml",
		"--configdir", "/cli/rns",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := buildConfig(opts, opts.config, opts.identity, opts.roomRegistry)
	if err != nil {
		t.Fatalf("buildConfig error: %v", err)
	}
	// TOML overrides the path seeds.
	if cfg.IdentityPath == nil || *cfg.IdentityPath != "/tomes/identity" {
		t.Errorf("identity path after TOML = %v, want /tomes/identity", cfg.IdentityPath)
	}
	if cfg.RoomRegistryPath == nil || *cfg.RoomRegistryPath != "/tomes/rooms.toml" {
		t.Errorf("room registry path after TOML = %v, want /tomes/rooms.toml", cfg.RoomRegistryPath)
	}
	if cfg.Configdir == nil || *cfg.Configdir != "/tomes/rns" {
		t.Errorf("configdir after TOML = %v, want /tomes/rns", cfg.Configdir)
	}
	// config_path and dest_name never come from TOML.
	if cfg.ConfigPath == nil || *cfg.ConfigPath != cfgPath {
		t.Errorf("config path after TOML = %v, want the CLI seed %v", cfg.ConfigPath, cfgPath)
	}
	if cfg.DestName != rrcd.HubDestName {
		t.Errorf("dest name after TOML = %q, want %q", cfg.DestName, rrcd.HubDestName)
	}
	// TOML wins over defaults.
	if cfg.HubName != "TOMLHub" || cfg.MaxNickBytes != 48 {
		t.Errorf("TOML values = %q/%v, want TOMLHub/48", cfg.HubName, cfg.MaxNickBytes)
	}

	// The CLI overrides win over the TOML values.
	opts2, err := parseFlags([]string{
		"--config", cfgPath, "--hub-name", "CLIHub", "--max-nick-bytes", "16",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg2, err := buildConfig(opts2, opts2.config, opts2.identity, opts2.roomRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.HubName != "CLIHub" || cfg2.MaxNickBytes != 16 {
		t.Errorf("CLI overrides = %q/%v, want CLIHub/16", cfg2.HubName, cfg2.MaxNickBytes)
	}
}
