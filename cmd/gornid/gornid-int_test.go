// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func minimalConfig(dir string) string {
	return fmt.Sprintf("[reticulum]\nshare_instance = No\ninstance_name = %v\n", filepath.Base(dir))
}

func buildGornid(t *testing.T) string {
	t.Helper()
	tmpDir := testutils.TempDirWithConfig(t, "gornid-test-", minimalConfig)
	bin := filepath.Join(tmpDir, "gornid")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build gornid: %v\n%v", err, string(out))
	}
	return bin
}

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

func TestParity_Base64ImportExport(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := testutils.TempDirWithConfig(t, "gornid-test-", minimalConfig)
	idFile := filepath.Join(tmpDir, "test.id")

	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	goOut, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-x", "-b").CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -x -b failed: %v\n%v", err, string(goOut))
	}

	pyOut, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-x", "-b").CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -x -b failed: %v\n%v", err, string(pyOut))
	}

	goKeys := extractKeyLines(string(goOut), "Public Identity Keys  :")
	pyKeys := extractKeyLines(string(pyOut), "Public Identity Keys  :")
	if goKeys["Public Identity Keys  :"] != pyKeys["Public Identity Keys  :"] {
		t.Fatalf("base64 export mismatch:\n  Go:     %v\n  Python: %v", goKeys["Public Identity Keys  :"], pyKeys["Public Identity Keys  :"])
	}

	goImportOut, err := exec.Command(gornidBin, "--config", tmpDir, "-m="+goKeys["Public Identity Keys  :"], "-b", "-p", "-P").CombinedOutput()
	if err != nil {
		t.Fatalf("gornid base64 import failed: %v\n%v", err, string(goImportOut))
	}
	pyImportOut, err := exec.Command(rnidBin, "--config", tmpDir, "-m="+pyKeys["Public Identity Keys  :"], "-b", "-p", "-P").CombinedOutput()
	if err != nil {
		t.Fatalf("rnid base64 import failed: %v\n%v", err, string(pyImportOut))
	}

	goImportKeys := extractKeyLines(string(goImportOut), "Public Key    :", "Private Key   :")
	pyImportKeys := extractKeyLines(string(pyImportOut), "Public Key    :", "Private Key   :")
	for _, label := range []string{"Public Key    :", "Private Key   :"} {
		if goImportKeys[label] != pyImportKeys[label] {
			t.Fatalf("base64 import mismatch for %v:\n  Go:     %v\n  Python: %v", label, goImportKeys[label], pyImportKeys[label])
		}
	}
}

func TestParity_PrintIdentity(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := testutils.TempDirWithConfig(t, "gornid-test-", minimalConfig)
	idFile := filepath.Join(tmpDir, "test.id")

	// Generate identity with Go.
	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	// Print identity with Go.
	goOut, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-p", "-P").CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -p failed: %v\n%v", err, string(goOut))
	}

	// Print identity with Python.
	pyOut, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-p", "-P").CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -p failed: %v\n%v", err, string(pyOut))
	}

	labels := []string{"Public Key    :", "Private Key   :"}
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
	testutils.SkipShortIntegration(t)
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := testutils.TempDirWithConfig(t, "gornid-test-", minimalConfig)
	idFile := filepath.Join(tmpDir, "test.id")

	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	goOut, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-x").CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -x failed: %v\n%v", err, string(goOut))
	}

	pyOut, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-x").CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -x failed: %v\n%v", err, string(pyOut))
	}

	label := "Public Identity Keys  :"
	goKeys := extractKeyLines(string(goOut), label)
	pyKeys := extractKeyLines(string(pyOut), label)
	if goKeys[label] != pyKeys[label] {
		t.Errorf("export mismatch:\n  Go:     %v\n  Python: %v", goKeys[label], pyKeys[label])
	}
}

func TestParity_Hash(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := testutils.TempDirWithConfig(t, "gornid-test-", minimalConfig)
	idFile := filepath.Join(tmpDir, "test.id")

	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	aspect := "myapp.delivery"

	goOut, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-H", aspect).CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -H failed: %v\n%v", err, string(goOut))
	}

	pyOut, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-H", aspect).CombinedOutput()
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
	testutils.SkipShortIntegration(t)
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := testutils.TempDirWithConfig(t, "gornid-test-", minimalConfig)
	idFile := filepath.Join(tmpDir, "test.id")

	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	// Export private key with Go to get hex (use -X for private export).
	goExpOut, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-X").CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -X failed: %v\n%v", err, string(goExpOut))
	}
	label := "Private Identity Keys :"
	hexStr := extractKeyLines(string(goExpOut), label)[label]
	if hexStr == "" {
		t.Fatalf("could not extract exported hex from: %v", string(goExpOut))
	}

	// Import private key with Go (uses -M). Add -p to print identity info.
	goOut, err := exec.Command(gornidBin, "--config", tmpDir, "-M", hexStr, "-p", "-P").CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -M failed: %v\n%v", err, string(goOut))
	}

	// Import private key with Python (uses -M). Add -p to print identity info.
	pyOut, err := exec.Command(rnidBin, "--config", tmpDir, "-M", hexStr, "-p", "-P").CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -M failed: %v\n%v", err, string(pyOut))
	}

	labels := []string{"Public Key    :", "Private Key   :"}
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
	testutils.SkipShortIntegration(t)
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := testutils.TempDirWithConfig(t, "gornid-test-", minimalConfig)
	idFile := filepath.Join(tmpDir, "test.id")
	dataFile := filepath.Join(tmpDir, "data.txt")

	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}
	if err := os.WriteFile(dataFile, []byte("cross-validation test data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sign with Go.
	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-s", dataFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -s failed: %v\n%v", err, string(out))
	}
	sigFile := dataFile + ".rsg"

	// Validate with Python.
	pyOut, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-V", sigFile, "-r", dataFile).CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -V failed (Go signature rejected by Python): %v\n%v", err, string(pyOut))
	}
	if !strings.Contains(string(pyOut), "is valid") {
		t.Errorf("Python validation output missing 'is valid': %v", string(pyOut))
	}
}

func TestParity_SignPythonValidateGo(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := testutils.TempDirWithConfig(t, "gornid-test-", minimalConfig)
	idFile := filepath.Join(tmpDir, "test.id")
	dataFile := filepath.Join(tmpDir, "data.txt")
	sigFile := filepath.Join(tmpDir, "data.txt.rsg")

	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}
	if err := os.WriteFile(dataFile, []byte("reverse cross-validation test"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sign with Python.
	if out, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-s", dataFile).CombinedOutput(); err != nil {
		t.Fatalf("rnid -s failed: %v\n%v", err, string(out))
	}

	// Validate with Go.
	goOut, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-V", sigFile, "-r", dataFile).CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -V failed (Python signature rejected by Go): %v\n%v", err, string(goOut))
	}
	if !strings.Contains(string(goOut), "is valid") {
		t.Errorf("Go validation output missing 'is valid': %v", string(goOut))
	}
}

func TestParity_EncryptGoDecryptPython(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := testutils.TempDirWithConfig(t, "gornid-test-", minimalConfig)
	idFile := filepath.Join(tmpDir, "test.id")
	plainFile := filepath.Join(tmpDir, "plain.txt")
	encFile := filepath.Join(tmpDir, "plain.txt.rfe")
	decFile := filepath.Join(tmpDir, "decrypted.txt")

	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	original := "Go encrypted, Python decrypted!"
	if err := os.WriteFile(plainFile, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// Encrypt with Go.
	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-e", plainFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -e failed: %v\n%v", err, string(out))
	}
	// Decrypt with Python.
	pyOut, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-d", encFile, "-w", decFile).CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -d failed (Go ciphertext rejected by Python): %v\n%v", err, string(pyOut))
	}

	got, err := os.ReadFile(decFile)
	mustTest(t, err)
	if string(got) != original {
		t.Errorf("Python decrypted = %q, want %q", string(got), original)
	}
}

func TestParity_EncryptPythonDecryptGo(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := testutils.TempDirWithConfig(t, "gornid-test-", minimalConfig)
	idFile := filepath.Join(tmpDir, "test.id")
	plainFile := filepath.Join(tmpDir, "plain.txt")
	encFile := filepath.Join(tmpDir, "plain.txt.rfe")
	decFile := filepath.Join(tmpDir, "decrypted.txt")

	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	original := "Python encrypted, Go decrypted!"
	if err := os.WriteFile(plainFile, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// Encrypt with Python.
	if out, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-e", plainFile).CombinedOutput(); err != nil {
		t.Fatalf("rnid -e failed: %v\n%v", err, string(out))
	}

	// Decrypt with Go.
	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-d", encFile, "-w", decFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -d failed (Python ciphertext rejected by Go): %v\n%v", err, string(out))
	}

	got, err := os.ReadFile(decFile)
	mustTest(t, err)
	if string(got) != original {
		t.Errorf("Go decrypted = %q, want %q", string(got), original)
	}
}

// TestParity_SignMessageRoundTrip verifies that gornid -S creates an
// embedded .rsm that both gornid and rnid validate, and vice versa.
func TestParity_SignMessageRoundTrip(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := testutils.TempDirWithConfig(t, "gornid-test-", minimalConfig)
	idFile := filepath.Join(tmpDir, "test.id")

	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	// gornid signs a message.
	goOut := filepath.Join(tmpDir, "go_out")
	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-S", "hello world", "-w", goOut).CombinedOutput(); err != nil {
		t.Fatalf("gornid -S failed: %v\n%v", err, string(out))
	}
	goRSM := goOut + ".rsm"
	if _, err := os.Stat(goRSM); err != nil {
		t.Fatalf("gornid did not create %v: %v", goRSM, err)
	}

	// gornid validates its own .rsm.
	goValOut, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-V", goRSM).CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -V (own rsm) failed: %v\n%v", err, string(goValOut))
	}
	if !strings.Contains(string(goValOut), "Signature is valid") {
		t.Errorf("gornid validate own rsm missing 'Signature is valid': %v", string(goValOut))
	}

	// rnid signs a message with the same identity.
	pyOut := filepath.Join(tmpDir, "py_out")
	if out, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-S", "hello world", "-w", pyOut).CombinedOutput(); err != nil {
		t.Fatalf("rnid -S failed: %v\n%v", err, string(out))
	}
	pyRSM := pyOut + ".rsm"
	if _, err := os.Stat(pyRSM); err != nil {
		t.Fatalf("rnid did not create %v: %v", pyRSM, err)
	}

	// rnid validates its own .rsm.
	pyValOut, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-V", pyRSM).CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -V (own rsm) failed: %v\n%v", err, string(pyValOut))
	}
	if !strings.Contains(string(pyValOut), "Signature is valid") {
		t.Errorf("rnid validate own rsm missing 'Signature is valid': %v", string(pyValOut))
	}

	// Cross-validation: gornid validates rnid's .rsm.
	goCrossOut, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-V", pyRSM).CombinedOutput()
	if err != nil {
		t.Fatalf("gornid -V (rnid rsm) failed: %v\n%v", err, string(goCrossOut))
	}
	if !strings.Contains(string(goCrossOut), "Signature is valid") {
		t.Errorf("gornid cross-validate rnid rsm missing 'Signature is valid': %v", string(goCrossOut))
	}

	// Cross-validation: rnid validates gornid's .rsm.
	pyCrossOut, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-V", goRSM).CombinedOutput()
	if err != nil {
		t.Fatalf("rnid -V (gornid rsm) failed: %v\n%v", err, string(pyCrossOut))
	}
	if !strings.Contains(string(pyCrossOut), "Signature is valid") {
		t.Errorf("rnid cross-validate gornid rsm missing 'Signature is valid': %v", string(pyCrossOut))
	}
}

// TestParity_MultiFileSign verifies that gornid -s f1 f2 (multi-value)
// produces f1.rsg and f2.rsg that both validate, and the same for rnid.
func TestParity_MultiFileSign(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	rnidBin := findRnid(t)
	gornidBin := buildGornid(t)
	tmpDir := testutils.TempDirWithConfig(t, "gornid-test-", minimalConfig)
	idFile := filepath.Join(tmpDir, "test.id")

	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("gornid -g failed: %v\n%v", err, string(out))
	}

	f1 := filepath.Join(tmpDir, "f1.txt")
	f2 := filepath.Join(tmpDir, "f2.txt")
	if err := os.WriteFile(f1, []byte("first file content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("second file content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// gornid multi-file sign.
	if out, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-s", f1, f2).CombinedOutput(); err != nil {
		t.Fatalf("gornid -s f1 f2 failed: %v\n%v", err, string(out))
	}
	for _, f := range []string{f1, f2} {
		rsg := f + ".rsg"
		if _, err := os.Stat(rsg); err != nil {
			t.Fatalf("gornid did not create %v: %v", rsg, err)
		}
		valOut, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-V", rsg, "-r", f).CombinedOutput()
		if err != nil {
			t.Fatalf("gornid -V %v failed: %v\n%v", rsg, err, string(valOut))
		}
		if !strings.Contains(string(valOut), "is valid") {
			t.Errorf("gornid validate %v missing 'is valid': %v", rsg, string(valOut))
		}
	}

	// Remove gornid signatures so rnid can re-sign without overwrite errors.
	_ = os.Remove(f1 + ".rsg")
	_ = os.Remove(f2 + ".rsg")

	// rnid multi-file sign.
	if out, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-s", f1, f2).CombinedOutput(); err != nil {
		t.Fatalf("rnid -s f1 f2 failed: %v\n%v", err, string(out))
	}
	for _, f := range []string{f1, f2} {
		rsg := f + ".rsg"
		if _, err := os.Stat(rsg); err != nil {
			t.Fatalf("rnid did not create %v: %v", rsg, err)
		}
		valOut, err := exec.Command(rnidBin, "--config", tmpDir, "-i", idFile, "-V", rsg, "-r", f).CombinedOutput()
		if err != nil {
			t.Fatalf("rnid -V %v failed: %v\n%v", rsg, err, string(valOut))
		}
		if !strings.Contains(string(valOut), "is valid") {
			t.Errorf("rnid validate %v missing 'is valid': %v", rsg, string(valOut))
		}
	}

	// Cross-validation: gornid validates rnid-signed f1.rsg.
	_ = os.Remove(f2 + ".rsg") // cleanup not needed but tidy
	goCrossOut, err := exec.Command(gornidBin, "--config", tmpDir, "-i", idFile, "-V", f1+".rsg", "-r", f1).CombinedOutput()
	if err != nil {
		t.Fatalf("gornid cross-validate rnid f1.rsg failed: %v\n%v", err, string(goCrossOut))
	}
	if !strings.Contains(string(goCrossOut), "is valid") {
		t.Errorf("gornid cross-validate rnid f1.rsg missing 'is valid': %v", string(goCrossOut))
	}
}
