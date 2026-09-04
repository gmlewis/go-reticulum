// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file is the gorrcd daemon entry point, mirroring Python's cli.main
// flow: the first-run bootstrap, the config chain (TOML overrides the path
// seeds), the CLI overrides, logging, and the hub bring-up.

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

	rrcd.ConfigureLogging(cfg, rns.NewLogger(), opts.logLevel, opts.logFile)

	svc := rrcd.NewHubService(cfg)
	svc.SetLogger(rns.NewLogger())
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
	}
	if opts.announcePeriod != nil {
		cfg.AnnouncePeriodS = *opts.announcePeriod
	}
	if opts.hubName != nil {
		cfg.HubName = *opts.hubName
	}
	if opts.greeting != nil {
		cfg.Greeting = opts.greeting
	}
	if opts.includeJoined {
		cfg.IncludeJoinedMemberList = true
	}
	if opts.maxRooms != nil {
		cfg.MaxRoomsPerSession = *opts.maxRooms
	}
	if opts.maxNickBytes != nil {
		cfg.MaxNickBytes = *opts.maxNickBytes
	}
	if opts.maxRoomNameBytes != nil {
		cfg.MaxRoomNameBytes = *opts.maxRoomNameBytes
	}
	if opts.rateLimit != nil {
		cfg.RateLimitMsgsPerMinute = *opts.rateLimit
	}
	if opts.maxMsgBodyBytes != nil {
		cfg.MaxMsgBodyBytes = *opts.maxMsgBodyBytes
	}
	if opts.pingInterval != nil {
		cfg.PingIntervalS = *opts.pingInterval
	}
	if opts.pingTimeout != nil {
		cfg.PingTimeoutS = *opts.pingTimeout
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
