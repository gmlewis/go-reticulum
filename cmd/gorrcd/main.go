// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// gorrcd is an RRC (Reticulum Relay Chat) hub daemon compatible with the
// Python rrcd 0.3.2 wire protocol and on-disk storage.
//
// It brings up the hub in three steps:
//   - First-run bootstrap: creates any missing state files (rrcd.toml,
//     hub_identity, rooms.toml) under RRCD_HOME (default ~/.rrcd) from the
//     byte-exact Python templates and exits 0 so the operator can edit the
//     configuration.
//   - Configuration chain: the TOML config file is applied over the path
//     seeds (TOML keys override --configdir/--identity/--room-registry,
//     while config_path and dest_name never come from TOML), then the CLI
//     overrides apply in the same order as Python's argparse.
//   - Hub run: logging is configured, the hub service announces on rrc.hub
//     and serves the RRC protocol until SIGINT or SIGTERM.
//
// Usage:
//
//	gorrcd [--config CONFIG] [--configdir CONFIGDIR]
//	       [--identity IDENTITY] [--room-registry ROOM_REGISTRY]
//	       [--no-announce] [--announce-period SECONDS]
//	       [--hub-name NAME] [--greeting TEXT]
//	       [--include-joined-member-list] [--max-rooms N]
//	       [--max-nick-bytes N] [--max-room-name-bytes N]
//	       [--rate-limit-msgs-per-minute N] [--max-msg-body-bytes N]
//	       [--ping-interval SECONDS] [--ping-timeout SECONDS]
//	       [--log-level LEVEL] [--log-file PATH] [--version]
//
// State paths honor the RRCD_HOME environment variable (used literally when
// truthy, no expansion) with ~/.rrcd as the default home.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd"
)

func main() {
	log.SetFlags(0)

	opts, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, errHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if opts.version {
		fmt.Printf("gorrcd %v\n", rns.VERSION)
		os.Exit(0)
	}

	configPath := opts.config
	identityPath := opts.identity
	roomRegistryPath := opts.roomRegistry

	if ensureFirstRunFiles(configPath, identityPath, roomRegistryPath, nil) {
		fmt.Fprint(os.Stderr, firstRunMessage(configPath, identityPath, roomRegistryPath))
		os.Exit(0)
	}

	// The base config seeds the path options; the TOML load then
	// overrides them (TOML keys win over the path seeds); the CLI
	// overrides apply last, in the Python order.
	cfg, err := buildConfig(opts, configPath, identityPath, roomRegistryPath)
	if err != nil {
		log.Fatalf("Failed to load config from %v: %v", configPath, err)
	}

	// One RNS logger instance carries the [logging] rns_level into the
	// live stack; the hub owns the logging state and shares the logger,
	// mirroring Python's configure_logging before start.
	rnsLogger := rns.NewLogger()

	svc := rrcd.NewHubService(cfg)
	svc.SetLogger(rnsLogger)
	svc.ConfigureLogging(opts.logLevel, opts.logFile)
	if err := svc.Start(); err != nil {
		log.Fatal(err)
	}

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		svc.Stop()
	}()

	// Run until the shutdown signal closes the hub channel.
	<-svc.Shutdown
}

// buildConfig applies the precedence chain: the path seeds, then the TOML
// load (TOML keys override the path seeds; config_path and dest_name never
// come from TOML), then the CLI overrides in the Python order.
func buildConfig(opts *gorrcdOptions, configPath, identityPath, roomRegistryPath string) (rrcd.HubConfig, error) {
	cfg := rrcd.DefaultHubConfig()
	cfg.Configdir = opts.configdir
	cfg.IdentityPath = &identityPath
	cfg.ConfigPath = &configPath
	cfg.RoomRegistryPath = &roomRegistryPath

	if configPath != "" {
		data, err := loadConfigTOMLFile(configPath)
		if err != nil {
			return cfg, err
		}
		cfg = rrcd.ApplyConfigData(cfg, data)
	}

	if opts.noAnnounce {
		cfg.AnnounceOnStart = false
		cfg.OverrideRawConfigValue("announce_on_start", false)
	}
	if opts.announcePeriod != nil {
		cfg.AnnouncePeriodS = *opts.announcePeriod
		cfg.OverrideRawConfigValue("announce_period_s", *opts.announcePeriod)
	}
	if opts.hubName != nil {
		cfg.HubName = *opts.hubName
	}
	if opts.greeting != nil {
		cfg.Greeting = opts.greeting
	}
	if opts.includeJoined {
		cfg.IncludeJoinedMemberList = true
		cfg.OverrideRawConfigValue("include_joined_member_list", true)
	}
	if opts.maxRooms != nil {
		cfg.MaxRoomsPerSession = *opts.maxRooms
		cfg.OverrideRawConfigValue("max_rooms_per_session", int64(*opts.maxRooms))
	}
	if opts.maxNickBytes != nil {
		cfg.MaxNickBytes = *opts.maxNickBytes
		cfg.OverrideRawConfigValue("max_nick_bytes", int64(*opts.maxNickBytes))
	}
	if opts.maxRoomNameBytes != nil {
		cfg.MaxRoomNameBytes = *opts.maxRoomNameBytes
		cfg.OverrideRawConfigValue("max_room_name_bytes", int64(*opts.maxRoomNameBytes))
	}
	if opts.rateLimit != nil {
		cfg.RateLimitMsgsPerMinute = *opts.rateLimit
		cfg.OverrideRawConfigValue("rate_limit_msgs_per_minute", int64(*opts.rateLimit))
	}
	if opts.maxMsgBodyBytes != nil {
		cfg.MaxMsgBodyBytes = *opts.maxMsgBodyBytes
		cfg.OverrideRawConfigValue("max_msg_body_bytes", int64(*opts.maxMsgBodyBytes))
	}
	if opts.pingInterval != nil {
		cfg.PingIntervalS = *opts.pingInterval
		cfg.OverrideRawConfigValue("ping_interval_s", *opts.pingInterval)
	}
	if opts.pingTimeout != nil {
		cfg.PingTimeoutS = *opts.pingTimeout
		cfg.OverrideRawConfigValue("ping_timeout_s", *opts.pingTimeout)
	}
	if opts.logLevel != nil {
		cfg.LogLevel = *opts.logLevel
	}
	if opts.logFile != nil {
		if *opts.logFile == "" {
			cfg.LogFile = nil
		} else {
			file := *opts.logFile
			cfg.LogFile = &file
		}
	}
	return cfg, nil
}
