// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// extractKeyLines extracts lines containing well-known label prefixes
// from rnid/gornid output, stripping timestamps and log-level markers
// so that Python and Go outputs can be compared directly.
func extractKeyLines(output string, labels ...string) map[string]string {
	result := make(map[string]string, len(labels))
	// Strip common log prefixes: "[2026-03-15 18:38:59] " or similar.
	tsRe := regexp.MustCompile(`^\[[\d\- :\.]+\]\s*(\[[\w]+\]\s*)?`)
	for _, line := range strings.Split(output, "\n") {
		cleaned := tsRe.ReplaceAllString(strings.TrimSpace(line), "")
		for _, label := range labels {
			if strings.HasPrefix(cleaned, label) {
				result[label] = strings.TrimSpace(strings.TrimPrefix(cleaned, label))
			}
		}
	}
	return result
}

// findRnid returns the path to the Python rnid binary, or skips the test.
func findRnid(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("rnid")
	if err != nil {
		t.Skip("rnid not found in PATH, skipping Python/Go parity test")
	}
	return path
}

func TestParity_PrintIdentity(t *testing.T) {
	t.Parallel()
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")

	// Generate identity with Go.
	if out, err := exec.Command(gornidBin, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	// Print identity with Go.
	goOut, err := exec.Command(gornidBin, "-i", idFile, "-p", "-P").CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -p failed: %v\n%v", err, string(goOut))
	}

	// Print identity with Python.
	pyOut, err := exec.Command(rnidBin, "-i", idFile, "-p", "-P").CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -p failed: %v\n%v", err, string(pyOut))
	}

	labels := []string{"Public Key  :", "Private Key :"}
	goKeys := extractKeyLines(string(goOut), labels...)
	pyKeys := extractKeyLines(string(pyOut), labels...)

	for _, label := range labels {
		if goKeys[label] != pyKeys[label] {
			t.Errorf("%v mismatch:\n  Go:     %v\n  Python: %v", label, goKeys[label], pyKeys[label])
		}
	}
}

func TestParity_Export(t *testing.T) {
	t.Parallel()
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")

	if out, err := exec.Command(gornidBin, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	goOut, err := exec.Command(gornidBin, "-i", idFile, "-x").CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -x failed: %v\n%v", err, string(goOut))
	}

	pyOut, err := exec.Command(rnidBin, "-i", idFile, "-x").CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -x failed: %v\n%v", err, string(pyOut))
	}

	label := "Exported Identity :"
	goKeys := extractKeyLines(string(goOut), label)
	pyKeys := extractKeyLines(string(pyOut), label)
	if goKeys[label] != pyKeys[label] {
		t.Errorf("export mismatch:\n  Go:     %v\n  Python: %v", goKeys[label], pyKeys[label])
	}
}

func TestParity_Hash(t *testing.T) {
	t.Parallel()
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")

	if out, err := exec.Command(gornidBin, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	aspect := "myapp.delivery"

	goOut, err := exec.Command(gornidBin, "-i", idFile, "-H", aspect).CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -H failed: %v\n%v", err, string(goOut))
	}

	pyOut, err := exec.Command(rnidBin, "-i", idFile, "-H", aspect).CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -H failed: %v\n%v", err, string(pyOut))
	}

	destLabel := "The " + aspect + " destination for this Identity is"
	specLabel := "The full destination specifier is"
	goKeys := extractKeyLines(string(goOut), destLabel, specLabel)
	pyKeys := extractKeyLines(string(pyOut), destLabel, specLabel)

	for _, label := range []string{destLabel, specLabel} {
		if goKeys[label] != pyKeys[label] {
			t.Errorf("%v mismatch:\n  Go:     %v\n  Python: %v", label, goKeys[label], pyKeys[label])
		}
	}
}

func TestParity_ImportHex(t *testing.T) {
	t.Parallel()
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")

	if out, err := exec.Command(gornidBin, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	// Export with Go to get hex.
	goExpOut, err := exec.Command(gornidBin, "-i", idFile, "-x").CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -x failed: %v\n%v", err, string(goExpOut))
	}
	label := "Exported Identity :"
	hexStr := extractKeyLines(string(goExpOut), label)[label]
	if hexStr == "" {
		t.Fatalf("could not extract exported hex from: %v", string(goExpOut))
	}

	// Import with Go.
	goOut, err := exec.Command(gornidBin, "-m", hexStr, "-P").CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -m failed: %v\n%v", err, string(goOut))
	}

	// Import with Python.
	pyOut, err := exec.Command(rnidBin, "-m", hexStr, "-P").CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -m failed: %v\n%v", err, string(pyOut))
	}

	labels := []string{"Public Key  :", "Private Key :"}
	goKeys := extractKeyLines(string(goOut), labels...)
	pyKeys := extractKeyLines(string(pyOut), labels...)

	for _, l := range labels {
		if goKeys[l] != pyKeys[l] {
			t.Errorf("%v mismatch on import:\n  Go:     %v\n  Python: %v", l, goKeys[l], pyKeys[l])
		}
	}
}

func TestParity_SignGoValidatePython(t *testing.T) {
	t.Parallel()
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")
	dataFile := filepath.Join(tmpDir, "data.txt")

	if out, err := exec.Command(gornidBin, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}
	if err := os.WriteFile(dataFile, []byte("cross-validation test data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sign with Go.
	if out, err := exec.Command(gornidBin, "-i", idFile, "-s", dataFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -s failed: %v\n%v", err, string(out))
	}
	sigFile := dataFile + ".rsg"

	// Validate with Python.
	pyOut, err := exec.Command(rnidBin, "-i", idFile, "-V", sigFile, "-r", dataFile).CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -V failed (Go signature rejected by Python): %v\n%v", err, string(pyOut))
	}
	if !strings.Contains(string(pyOut), "is valid") {
		t.Errorf("Python validation output missing 'is valid': %v", string(pyOut))
	}
}

func TestParity_SignPythonValidateGo(t *testing.T) {
	t.Parallel()
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")
	dataFile := filepath.Join(tmpDir, "data.txt")
	sigFile := filepath.Join(tmpDir, "data.txt.rsg")

	if out, err := exec.Command(gornidBin, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}
	if err := os.WriteFile(dataFile, []byte("reverse cross-validation test"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sign with Python.
	if out, err := exec.Command(rnidBin, "-i", idFile, "-s", dataFile).CombinedOutput(); err != nil {
		t.Fatalf("rnid -s failed: %v\n%v", err, string(out))
	}

	// Validate with Go.
	goOut, err := exec.Command(gornidBin, "-i", idFile, "-V", sigFile, "-r", dataFile).CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -V failed (Python signature rejected by Go): %v\n%v", err, string(goOut))
	}
	if !strings.Contains(string(goOut), "is valid") {
		t.Errorf("Go validation output missing 'is valid': %v", string(goOut))
	}
}

func TestParity_EncryptGoDecryptPython(t *testing.T) {
	t.Parallel()
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")
	plainFile := filepath.Join(tmpDir, "plain.txt")
	encFile := filepath.Join(tmpDir, "plain.txt.rfe")
	decFile := filepath.Join(tmpDir, "decrypted.txt")

	if out, err := exec.Command(gornidBin, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	original := "Go encrypted, Python decrypted!"
	if err := os.WriteFile(plainFile, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// Encrypt with Go.
	if out, err := exec.Command(gornidBin, "-i", idFile, "-e", plainFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -e failed: %v\n%v", err, string(out))
	}

	// Decrypt with Python.
	if out, err := exec.Command(rnidBin, "-i", idFile, "-d", encFile, "-w", decFile).CombinedOutput(); err != nil {
		t.Fatalf("rnid -d failed (Go ciphertext rejected by Python): %v\n%v", err, string(out))
	}

	got, err := os.ReadFile(decFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("Python decrypted = %q, want %q", string(got), original)
	}
}

func TestParity_EncryptPythonDecryptGo(t *testing.T) {
	t.Parallel()
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")
	plainFile := filepath.Join(tmpDir, "plain.txt")
	encFile := filepath.Join(tmpDir, "plain.txt.rfe")
	decFile := filepath.Join(tmpDir, "decrypted.txt")

	if out, err := exec.Command(gornidBin, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	original := "Python encrypted, Go decrypted!"
	if err := os.WriteFile(plainFile, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// Encrypt with Python.
	if out, err := exec.Command(rnidBin, "-i", idFile, "-e", plainFile).CombinedOutput(); err != nil {
		t.Fatalf("rnid -e failed: %v\n%v", err, string(out))
	}

	// Decrypt with Go.
	if out, err := exec.Command(gornidBin, "-i", idFile, "-d", encFile, "-w", decFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -d failed (Python ciphertext rejected by Go): %v\n%v", err, string(out))
	}

	got, err := os.ReadFile(decFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("Go decrypted = %q, want %q", string(got), original)
	}
}
