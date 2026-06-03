// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package rns

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFormatParityPrettySize(t *testing.T) {
	pyDir := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR")
	if pyDir == "" {
		pyDir = filepath.Join(os.Getenv("HOME"), "src", "github.com", "markqvist", "Reticulum")
	}
	if _, err := os.Stat(pyDir); err != nil {
		t.Skipf("original Reticulum repo not found at %v: %v", pyDir, err)
	}

	values := []float64{0, 1, 999, 1000, 1500, 1e6, 1.5e6, 1e9, 1e12, 1e15, 1e18, 1e21, 1e24, 1e27}
	suffixes := []string{"B", "b"}

	pyScript := `
import sys, json
sys.path.insert(0, sys.argv[1])
import RNS
values = json.loads(sys.argv[2])
suffixes = json.loads(sys.argv[3])
results = {}
for suffix in suffixes:
    for v in values:
        key = f"{v:.0f}|{suffix}"
        results[key] = RNS.prettysize(v, suffix=suffix)
print(json.dumps(results))
`
	tmpDir, err := os.MkdirTemp("/tmp", "prettysize-parity-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "ps.py")
	if err := os.WriteFile(scriptPath, []byte(pyScript), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	valJSON, _ := json.Marshal(values)
	sufJSON, _ := json.Marshal(suffixes)

	cmd := exec.Command("python3", scriptPath, pyDir, string(valJSON), string(sufJSON))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python3 failed: %v\n%s", err, out)
	}

	var pyResults map[string]string
	if err := json.Unmarshal(out, &pyResults); err != nil {
		t.Fatalf("json unmarshal: %v\nraw: %s", err, out)
	}

	for _, suffix := range suffixes {
		for _, v := range values {
			key := fmt.Sprintf("%.0f|%s", v, suffix)
			pyWant, ok := pyResults[key]
			if !ok {
				t.Errorf("no Python result for key %q", key)
				continue
			}
			goGot := PrettySize(v, suffix)
			if goGot != pyWant {
				t.Errorf("PrettySize(%v, %q) = %q, want %q (Python)", v, suffix, goGot, pyWant)
			}
		}
	}
}

func TestFormatParityPrettySpeed(t *testing.T) {
	pyDir := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR")
	if pyDir == "" {
		pyDir = filepath.Join(os.Getenv("HOME"), "src", "github.com", "markqvist", "Reticulum")
	}
	if _, err := os.Stat(pyDir); err != nil {
		t.Skipf("original Reticulum repo not found at %v: %v", pyDir, err)
	}

	values := []float64{0, 8, 8000, 8e6, 8e9, 8e12, 8e15, 8e18, 8e21, 8e24, 8e27}

	pyScript := `
import sys, json
sys.path.insert(0, sys.argv[1])
import RNS
values = json.loads(sys.argv[2])
results = {}
for v in values:
    key = f"{v:.0f}"
    results[key] = RNS.prettyspeed(v)
print(json.dumps(results))
`
	tmpDir, err := os.MkdirTemp("/tmp", "prettyspeed-parity-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "ps.py")
	if err := os.WriteFile(scriptPath, []byte(pyScript), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	valJSON, _ := json.Marshal(values)

	cmd := exec.Command("python3", scriptPath, pyDir, string(valJSON))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python3 failed: %v\n%s", err, out)
	}

	var pyResults map[string]string
	if err := json.Unmarshal(out, &pyResults); err != nil {
		t.Fatalf("json unmarshal: %v\nraw: %s", err, out)
	}

	for _, v := range values {
		key := fmt.Sprintf("%.0f", v)
		pyWant, ok := pyResults[key]
		if !ok {
			t.Errorf("no Python result for key %q", key)
			continue
		}
		goGot := PrettySpeed(v)
		if goGot != pyWant {
			t.Errorf("PrettySpeed(%v) = %q, want %q (Python)", v, goGot, pyWant)
		}
	}
}
