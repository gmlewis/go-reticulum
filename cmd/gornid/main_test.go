// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func tempDir(t *testing.T) string {
	t.Helper()
	baseDir := ""
	if runtime.GOOS == "darwin" {
		baseDir = "/tmp"
	}
	dir, err := os.MkdirTemp(baseDir, "gornid-test-")
	if err != nil {
		t.Fatalf("tempDir error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func buildGornid(t *testing.T) string {
	t.Helper()
	tmpDir := tempDir(t)
	bin := filepath.Join(tmpDir, "gornid")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build gornid: %v\n%v", err, string(out))
	}
	return bin
}

func TestVersionOutput(t *testing.T) {
	t.Parallel()
	bin := buildGornid(t)
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornid --version failed: %v\n%v", err, string(out))
	}
	want := "gornid " + rns.VERSION
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestNoIdentityError(t *testing.T) {
	t.Parallel()
	bin := buildGornid(t)
	cmd := exec.Command(bin)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error exit, got success")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() != 2 {
		t.Errorf("exit code = %v, want 2", exitErr.ExitCode())
	}
	if !strings.Contains(string(out), "No identity provided") {
		t.Errorf("output missing expected message, got: %v", string(out))
	}
}

func TestGenerateRoundTrip(t *testing.T) {
	t.Parallel()
	bin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")

	out, err := exec.Command(bin, "--config", tmpDir, "-g", idFile).CombinedOutput()
	if err != nil {
		t.Fatalf("generate failed: %v\n%v", err, string(out))
	}
	if _, err := os.Stat(idFile); err != nil {
		t.Fatalf("identity file not created: %v", err)
	}

	out, err = exec.Command(bin, "--config", tmpDir, "-i", idFile, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("print identity failed: %v\n%v", err, string(out))
	}
	if !strings.Contains(string(out), "Public Key") {
		t.Errorf("output missing 'Public Key', got: %v", string(out))
	}
}

func TestImportExportRoundTrip(t *testing.T) {
	t.Parallel()
	bin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")

	// Generate identity
	if out, err := exec.Command(bin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("generate failed: %v\n%v", err, string(out))
	}

	// Export identity
	out, err := exec.Command(bin, "--config", tmpDir, "-i", idFile, "-x").CombinedOutput()
	if err != nil {
		t.Fatalf("export failed: %v\n%v", err, string(out))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var exportedHex string
	for _, line := range lines {
		if strings.Contains(line, "Exported Identity") {
			parts := strings.SplitN(line, ": ", 2)
			if len(parts) == 2 {
				exportedHex = strings.TrimSpace(parts[1])
			}
		}
	}
	if exportedHex == "" {
		t.Fatalf("could not find exported identity in output: %v", string(out))
	}

	// Import identity (does not need --config, exits before NewReticulum)
	out, err = exec.Command(bin, "-m", exportedHex, "-P").CombinedOutput()
	if err != nil {
		t.Fatalf("import failed: %v\n%v", err, string(out))
	}
	if !strings.Contains(string(out), "Public Key") {
		t.Errorf("import output missing 'Public Key', got: %v", string(out))
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()
	bin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")
	plainFile := filepath.Join(tmpDir, "plain.txt")
	encFile := filepath.Join(tmpDir, "plain.txt.rfe")
	decFile := filepath.Join(tmpDir, "decrypted.txt")

	if out, err := exec.Command(bin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("generate failed: %v\n%v", err, string(out))
	}

	plaintext := "Hello, Reticulum encryption test!"
	if err := os.WriteFile(plainFile, []byte(plaintext), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command(bin, "--config", tmpDir, "-i", idFile, "-e", plainFile).CombinedOutput(); err != nil {
		t.Fatalf("encrypt failed: %v\n%v", err, string(out))
	}
	if _, err := os.Stat(encFile); err != nil {
		t.Fatalf("encrypted file not created: %v", err)
	}

	if out, err := exec.Command(bin, "--config", tmpDir, "-i", idFile, "-d", encFile, "-w", decFile).CombinedOutput(); err != nil {
		t.Fatalf("decrypt failed: %v\n%v", err, string(out))
	}
	got, err := os.ReadFile(decFile)
	mustTest(t, err)
	if string(got) != plaintext {
		t.Errorf("decrypted content = %q, want %q", string(got), plaintext)
	}
}

func TestSignValidateRoundTrip(t *testing.T) {
	t.Parallel()
	bin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")
	dataFile := filepath.Join(tmpDir, "data.txt")

	if out, err := exec.Command(bin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("generate failed: %v\n%v", err, string(out))
	}

	if err := os.WriteFile(dataFile, []byte("sign this data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command(bin, "--config", tmpDir, "-i", idFile, "-s", dataFile).CombinedOutput(); err != nil {
		t.Fatalf("sign failed: %v\n%v", err, string(out))
	}
	sigFile := dataFile + ".rsg"
	if _, err := os.Stat(sigFile); err != nil {
		t.Fatalf("signature file not created: %v", err)
	}

	out, err := exec.Command(bin, "--config", tmpDir, "-i", idFile, "-V", sigFile, "-r", dataFile).CombinedOutput()
	if err != nil {
		t.Fatalf("validate failed: %v\n%v", err, string(out))
	}
	if !strings.Contains(string(out), "is valid") {
		t.Errorf("expected 'is valid' in output, got: %v", string(out))
	}
}

func TestValidateBadSignature(t *testing.T) {
	t.Parallel()
	bin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")
	dataFile := filepath.Join(tmpDir, "data.txt")
	sigFile := filepath.Join(tmpDir, "bad.rsg")

	if out, err := exec.Command(bin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("generate failed: %v\n%v", err, string(out))
	}

	if err := os.WriteFile(dataFile, []byte("some data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigFile, []byte("not-a-real-signature-padding-to-64-bytes-0123456789abcdef01234567"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "--config", tmpDir, "-i", idFile, "-V", sigFile, "-r", dataFile)
	_, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error exit for bad signature")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() != 22 {
		t.Errorf("exit code = %v, want 22", exitErr.ExitCode())
	}
}

func TestHashOutput(t *testing.T) {
	t.Parallel()
	bin := buildGornid(t)
	tmpDir := tempDir(t)
	idFile := filepath.Join(tmpDir, "test.id")

	if out, err := exec.Command(bin, "--config", tmpDir, "-g", idFile).CombinedOutput(); err != nil {
		t.Fatalf("generate failed: %v\n%v", err, string(out))
	}

	out, err := exec.Command(bin, "--config", tmpDir, "-i", idFile, "-H", "app.aspect").CombinedOutput()
	if err != nil {
		t.Fatalf("hash failed: %v\n%v", err, string(out))
	}
	output := string(out)
	if !strings.Contains(output, "app.aspect") {
		t.Errorf("output missing 'app.aspect', got: %v", output)
	}
	if !strings.Contains(output, "destination for this Identity is") {
		t.Errorf("output missing destination hash, got: %v", output)
	}
	if !strings.Contains(output, "full destination specifier") {
		t.Errorf("output missing full specifier, got: %v", output)
	}
}
