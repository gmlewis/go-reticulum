// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"io"
	"testing"
)

func TestAppFlags(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{"--config", "/tmp/config", "--silent", "--allow-fetch", "--jail", "/home", "--save", "/tmp/save", "--overwrite", "-b", "5", "-a", "abc123", "--no-auth", "--print-identity", "--phy-rates", "-w", "30", "--version"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if app.configDir != "/tmp/config" || !app.silent || !app.allowFetch || app.jail != "/home" || app.savePath != "/tmp/save" || !app.overwrite || app.announceInterval != 5 || len(app.allowed) != 1 || app.allowed[0] != "abc123" || !app.noAuth || !app.printIdentity || !app.phyRates || app.timeoutSec != 30 || !app.version {
		t.Fatalf("unexpected app state: %+v", app)
	}
}

func TestSilentFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantSilent bool
	}{
		{"no flag", []string{}, false},
		{"short flag", []string{"-S"}, true},
		{"long flag", []string{"--silent"}, true},
		{"both flags", []string{"-S", "--silent"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			silent := fs.Bool("S", false, "disable transfer progress output")
			silentLong := fs.Bool("silent", false, "disable transfer progress output")

			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			got := *silent || *silentLong
			if got != tt.wantSilent {
				t.Errorf("silent = %v, want %v", got, tt.wantSilent)
			}
		})
	}
}

func TestAllowFetchFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		args           []string
		wantAllowFetch bool
	}{
		{"no flag", []string{}, false},
		{"short flag", []string{"-F"}, true},
		{"long flag", []string{"--allow-fetch"}, true},
		{"both flags", []string{"-F", "--allow-fetch"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			allowFetch := fs.Bool("F", false, "allow authenticated clients to fetch files")
			allowFetchLong := fs.Bool("allow-fetch", false, "allow authenticated clients to fetch files")

			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			got := *allowFetch || *allowFetchLong
			if got != tt.wantAllowFetch {
				t.Errorf("allowFetch = %v, want %v", got, tt.wantAllowFetch)
			}
		})
	}
}

func TestJailFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantJail string
	}{
		{"no flag", []string{}, ""},
		{"short flag", []string{"-j", "/tmp"}, "/tmp"},
		{"long flag", []string{"--jail", "/home"}, "/home"},
		{"both flags", []string{"-j", "/tmp", "--jail", "/home"}, "/home"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			jail := fs.String("j", "", "restrict fetch requests to specified path")
			jailLong := fs.String("jail", "", "restrict fetch requests to specified path")

			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			got := *jail
			if *jailLong != "" {
				got = *jailLong
			}
			if got != tt.wantJail {
				t.Errorf("jail = %v, want %v", got, tt.wantJail)
			}
		})
	}
}

func TestSaveFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantSave string
	}{
		{"no flag", []string{}, ""},
		{"short flag", []string{"-s", "/tmp"}, "/tmp"},
		{"long flag", []string{"--save", "/home"}, "/home"},
		{"both flags", []string{"-s", "/tmp", "--save", "/home"}, "/home"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			save := fs.String("s", "", "save received files in specified path")
			saveLong := fs.String("save", "", "save received files in specified path")

			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			got := *save
			if *saveLong != "" {
				got = *saveLong
			}
			if got != tt.wantSave {
				t.Errorf("save = %v, want %v", got, tt.wantSave)
			}
		})
	}
}

func TestOverwriteFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		args          []string
		wantOverwrite bool
	}{
		{"no flag", []string{}, false},
		{"short flag", []string{"-O"}, true},
		{"long flag", []string{"--overwrite"}, true},
		{"both flags", []string{"-O", "--overwrite"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			overwrite := fs.Bool("O", false, "allow overwriting received files")
			overwriteLong := fs.Bool("overwrite", false, "allow overwriting received files")

			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			got := *overwrite || *overwriteLong
			if got != tt.wantOverwrite {
				t.Errorf("overwrite = %v, want %v", got, tt.wantOverwrite)
			}
		})
	}
}

func TestAnnounceFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		wantAnnounce int
	}{
		{"no flag", []string{}, -1},
		{"zero", []string{"-b", "0"}, 0},
		{"five", []string{"-b", "5"}, 5},
		{"negative one", []string{"-b", "-1"}, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			announce := fs.Int("b", -1, "announce interval (0=once, >0=seconds)")

			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			got := *announce
			if got != tt.wantAnnounce {
				t.Errorf("announce = %v, want %v", got, tt.wantAnnounce)
			}
		})
	}
}

func TestAllowedFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantAllowed []string
	}{
		{"no flag", []string{}, nil},
		{"single", []string{"-a", "abc123"}, []string{"abc123"}},
		{"multiple", []string{"-a", "abc123", "-a", "def456"}, []string{"abc123", "def456"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			var allowed []string
			fs.Func("a", "allow identity hash", func(s string) error {
				allowed = append(allowed, s)
				return nil
			})

			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			if len(allowed) != len(tt.wantAllowed) {
				t.Errorf("allowed length = %v, want %v", len(allowed), len(tt.wantAllowed))
				return
			}
			for i := range allowed {
				if allowed[i] != tt.wantAllowed[i] {
					t.Errorf("allowed[%d] = %v, want %v", i, allowed[i], tt.wantAllowed[i])
				}
			}
		})
	}
}

func TestParseFlagsHelp(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	_, err := parseFlags([]string{"--help"}, &buf)
	if err != errHelp {
		t.Fatalf("parseFlags error = %v, want %v", err, errHelp)
	}
	if got := buf.String(); got != usageText {
		t.Fatalf("help output mismatch:\n--- got ---\n%v\n--- want ---\n%v", got, usageText)
	}
}

func TestParseFlagsVersion(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{"--version"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if !app.version {
		t.Fatal("version = false, want true")
	}
}

func TestNoAuthFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantNoAuth bool
	}{
		{"no flag", []string{}, false},
		{"short flag", []string{"-n"}, true},
		{"long flag", []string{"--no-auth"}, true},
		{"both flags", []string{"-n", "--no-auth"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			noAuth := fs.Bool("n", false, "accept requests from anyone")
			noAuthLong := fs.Bool("no-auth", false, "accept requests from anyone")

			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			got := *noAuth || *noAuthLong
			if got != tt.wantNoAuth {
				t.Errorf("noAuth = %v, want %v", got, tt.wantNoAuth)
			}
		})
	}
}

func TestPrintIdentityFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		args              []string
		wantPrintIdentity bool
	}{
		{"no flag", []string{}, false},
		{"short flag", []string{"-p"}, true},
		{"long flag", []string{"--print-identity"}, true},
		{"both flags", []string{"-p", "--print-identity"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			printIdentity := fs.Bool("p", false, "print identity and destination info and exit")
			printIdentityLong := fs.Bool("print-identity", false, "print identity and destination info and exit")

			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			got := *printIdentity || *printIdentityLong
			if got != tt.wantPrintIdentity {
				t.Errorf("printIdentity = %v, want %v", got, tt.wantPrintIdentity)
			}
		})
	}
}

func TestPhyRatesFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		wantPhyRates bool
	}{
		{"no flag", []string{}, false},
		{"short flag", []string{"-P"}, true},
		{"long flag", []string{"--phy-rates"}, true},
		{"both flags", []string{"-P", "--phy-rates"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			phyRates := fs.Bool("P", false, "display physical layer transfer rates")
			phyRatesLong := fs.Bool("phy-rates", false, "display physical layer transfer rates")

			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			got := *phyRates || *phyRatesLong
			if got != tt.wantPhyRates {
				t.Errorf("phyRates = %v, want %v", got, tt.wantPhyRates)
			}
		})
	}
}

func TestTimeoutFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantTimeout float64
	}{
		{"default", []string{}, 15.0},
		{"30 seconds", []string{"-w", "30"}, 30.0},
		{"5 seconds", []string{"-w", "5"}, 5.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			timeout := fs.Float64("w", 15.0, "sender timeout seconds")

			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			got := *timeout
			if got != tt.wantTimeout {
				t.Errorf("timeout = %v, want %v", got, tt.wantTimeout)
			}
		})
	}
}

func TestVersionFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantVersion bool
	}{
		{"no flag", []string{}, false},
		{"version", []string{"--version"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			version := fs.Bool("version", false, "show version")

			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse failed: %v", err)
			}

			got := *version
			if got != tt.wantVersion {
				t.Errorf("version = %v, want %v", got, tt.wantVersion)
			}
		})
	}
}
