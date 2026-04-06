// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package cmd_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	usageTextRE = regexp.MustCompile(`(?m)^\s*(const|var)\s+usageText\b`)
)

func TestCLIContract(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pkgDir := filepath.Join(".", entry.Name())
		mainPath := filepath.Join(pkgDir, "main.go")
		flagsPath := filepath.Join(pkgDir, "flags.go")
		if _, err := os.Stat(mainPath); err != nil {
			continue
		}
		if _, err := os.Stat(flagsPath); err != nil {
			continue
		}

		t.Run(entry.Name(), func(t *testing.T) {
			t.Parallel()

			mainSrc := readText(t, mainPath)
			flagsSrc := readText(t, flagsPath)

			if !usageTextRE.MatchString(flagsSrc) {
				t.Fatalf("%v: flags.go must define usageText", entry.Name())
			}
			if strings.Contains(mainSrc, "flag.CommandLine") || strings.Contains(flagsSrc, "flag.CommandLine") {
				t.Fatalf("%v: command parsing must not use flag.CommandLine", entry.Name())
			}
			if !strings.Contains(mainSrc, "parseFlags(") && !strings.Contains(mainSrc, "parseOptions(") && !strings.Contains(mainSrc, "parseArgs(") {
				t.Fatalf("%v: main.go must call a package-local parser", entry.Name())
			}
			if !strings.Contains(mainSrc, "errHelp") && !strings.Contains(mainSrc, "utils.ErrHelp") && !strings.Contains(mainSrc, "flag.ErrHelp") {
				t.Fatalf("%v: main.go must handle the help sentinel", entry.Name())
			}
		})
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%v) failed: %v", path, err)
	}
	return string(data)
}
