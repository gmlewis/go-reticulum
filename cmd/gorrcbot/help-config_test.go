// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
)

// helpLineCount is how many lines "help <name>" yields for one command under
// cfg: the summary line, the detail lines, and the configuration hint when the
// command's provider is unconfigured.
func helpLineCount(cmd *command, cfg *BotConfig) int {
	n := 1 + len(cmd.detail)
	if len(cmd.configHint) > 0 && cmd.configured != nil && cfg != nil && !cmd.configured(cfg) {
		n += len(cmd.configHint)
	}
	return n
}

// TestHelpHidesConfigurationGuidanceWhenConfigured asserts "help <command>"
// prints the operator-facing configuration hint only while the command's
// provider is actually unconfigured. A user asking about a feature the operator
// has already enabled needs its usage, not instructions for installing it.
func TestHelpHidesConfigurationGuidanceWhenConfigured(t *testing.T) {
	t.Parallel()

	cases := []struct {
		command string
		needle  string
		enable  func(*BotConfig)
	}{
		{"weather", "Needs weather_url in config.toml", func(c *BotConfig) {
			c.WeatherURL = "https://wx.example.invalid/{place}"
		}},
		{"launches", "Needs launch_url in config.toml", func(c *BotConfig) {
			c.LaunchURL = "https://ll.example.invalid/2.3.0/launches/{mode}/?limit={limit}"
		}},
		{"flight", "Needs flight_url", func(c *BotConfig) {
			c.FlightURL = "https://fl.example.invalid/{flight}"
		}},
		{"msg", "Needs lxmf_enabled = true in config.toml", func(c *BotConfig) {
			c.LXMFEnabled = true
		}},
		{"kjv", "Needs kjv_txt_file in config.toml", func(c *BotConfig) {
			c.KJVTxtFile = "/tmp/kjv.txt"
		}},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			t.Parallel()

			// Unconfigured: the guidance is exactly what the asker needs, since
			// the command cannot answer yet.
			reg, session, _ := commandFixture(t, defaultTestConfig())
			lines := runLines(t, reg, session, "help "+tc.command)
			if joined := strings.Join(lines, "\n"); !strings.Contains(joined, tc.needle) {
				t.Errorf("unconfigured help %v = %q, want %q", tc.command, joined, tc.needle)
			}

			// Configured: configuration is the operator's concern, so the
			// guidance must be gone rather than repeated to every user.
			cfg := defaultTestConfig()
			tc.enable(cfg)
			reg, session, _ = commandFixture(t, cfg)
			lines = runLines(t, reg, session, "help "+tc.command)
			joined := strings.Join(lines, "\n")
			if strings.Contains(joined, tc.needle) {
				t.Errorf("configured help %v still names the config key: %q", tc.command, joined)
			}
			if strings.Contains(joined, "config.toml") {
				t.Errorf("configured help %v mentions config.toml: %q", tc.command, joined)
			}
		})
	}
}

// TestNoCommandHelpMentionsConfigWhenConfigured is the invariant behind the test
// above: once every configurable provider is set, no command's help may talk
// about configuration at all.
func TestNoCommandHelpMentionsConfigWhenConfigured(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.WeatherURL = "https://wx.example.invalid/{place}"
	cfg.LaunchURL = "https://ll.example.invalid/2.3.0/launches/{mode}/?limit={limit}"
	cfg.FlightURL = "https://fl.example.invalid/{flight}"
	cfg.LXMFEnabled = true
	cfg.KJVTxtFile = "/tmp/kjv.txt"

	reg, session, _ := commandFixture(t, cfg)
	for _, cmd := range reg.commands {
		lines := runLines(t, reg, session, "help "+cmd.name)
		if joined := strings.Join(lines, "\n"); strings.Contains(joined, "config.toml") {
			t.Errorf("help %v mentions config.toml while configured: %q", cmd.name, joined)
		}
	}
}
