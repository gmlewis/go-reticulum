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

func tempDir(t *testing.T) (string, func()) {
	t.Helper()
	baseDir := ""
	if runtime.GOOS == "darwin" {
		baseDir = "/tmp"
	}
	dir, err := os.MkdirTemp(baseDir, "gornid-test-")
	if err != nil {
		t.Fatalf("tempDir error: %v", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	return dir, cleanup
}

func runGornid(t *testing.T, args ...string) (string, error) {
	t.Helper()
	fullArgs := append([]string{"run", "."}, args...)
	cmd := exec.Command("go", fullArgs...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestVersionOutput(t *testing.T) {
	t.Parallel()
	out, err := runGornid(t, "--version")
	if err != nil {
		t.Fatalf("gornid --version failed: %v\n%v", err, out)
	}
	want := "gornid " + rns.VERSION
	got := strings.TrimSpace(out)
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestNoIdentityError(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := tempDir(t)
	defer cleanup()

	// Build the binary first (go run doesn't preserve exit codes reliably)
	binPath := filepath.Join(tmpDir, "gornid")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Dir = "."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	cmd := exec.Command(binPath)
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
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	idFile := filepath.Join(tmpDir, "test.id")

	out, err := runGornid(t, "--config", tmpDir, "-g", idFile)
	if err != nil {
		t.Fatalf("generate failed: %v\n%v", err, out)
	}
	if _, err := os.Stat(idFile); err != nil {
		t.Fatalf("identity file not created: %v", err)
	}

	out, err = runGornid(t, "--config", tmpDir, "-i", idFile, "-p")
	if err != nil {
		t.Fatalf("print identity failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "Public Key") {
		t.Errorf("output missing 'Public Key', got: %v", out)
	}
}

func TestImportExportRoundTrip(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	idFile := filepath.Join(tmpDir, "test.id")

	// Generate identity
	out, err := runGornid(t, "--config", tmpDir, "-g", idFile)
	if err != nil {
		t.Fatalf("generate failed: %v\n%v", err, out)
	}

	// Export identity
	out, err = runGornid(t, "--config", tmpDir, "-i", idFile, "-x")
	if err != nil {
		t.Fatalf("export failed: %v\n%v", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
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
		t.Fatalf("could not find exported identity in output: %v", out)
	}

	// Import identity (does not need --config, exits before NewReticulum)
	out, err = runGornid(t, "-m", exportedHex, "-P")
	if err != nil {
		t.Fatalf("import failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "Public Key") {
		t.Errorf("import output missing 'Public Key', got: %v", out)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	idFile := filepath.Join(tmpDir, "test.id")
	plainFile := filepath.Join(tmpDir, "plain.txt")
	encFile := filepath.Join(tmpDir, "plain.txt.rfe")
	decFile := filepath.Join(tmpDir, "decrypted.txt")

	out, err := runGornid(t, "--config", tmpDir, "-g", idFile)
	if err != nil {
		t.Fatalf("generate failed: %v\n%v", err, out)
	}

	plaintext := "Hello, Reticulum encryption test!"
	if err := os.WriteFile(plainFile, []byte(plaintext), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err = runGornid(t, "--config", tmpDir, "-i", idFile, "-e", plainFile)
	if err != nil {
		t.Fatalf("encrypt failed: %v\n%v", err, out)
	}
	if _, err := os.Stat(encFile); err != nil {
		t.Fatalf("encrypted file not created: %v", err)
	}

	out, err = runGornid(t, "--config", tmpDir, "-i", idFile, "-d", encFile, "-w", decFile)
	if err != nil {
		t.Fatalf("decrypt failed: %v\n%v", err, out)
	}
	got, err := os.ReadFile(decFile)
	mustTest(t, err)
	if string(got) != plaintext {
		t.Errorf("decrypted content = %q, want %q", string(got), plaintext)
	}
}

func TestSignValidateRoundTrip(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	idFile := filepath.Join(tmpDir, "test.id")
	dataFile := filepath.Join(tmpDir, "data.txt")

	out, err := runGornid(t, "--config", tmpDir, "-g", idFile)
	if err != nil {
		t.Fatalf("generate failed: %v\n%v", err, out)
	}

	if err := os.WriteFile(dataFile, []byte("sign this data"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err = runGornid(t, "--config", tmpDir, "-i", idFile, "-s", dataFile)
	if err != nil {
		t.Fatalf("sign failed: %v\n%v", err, out)
	}
	sigFile := dataFile + ".rsg"
	if _, err := os.Stat(sigFile); err != nil {
		t.Fatalf("signature file not created: %v", err)
	}

	out, err = runGornid(t, "--config", tmpDir, "-i", idFile, "-V", sigFile, "-r", dataFile)
	if err != nil {
		t.Fatalf("validate failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "is valid") {
		t.Errorf("expected 'is valid' in output, got: %v", out)
	}
}

func TestValidateBadSignature(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	idFile := filepath.Join(tmpDir, "test.id")
	dataFile := filepath.Join(tmpDir, "data.txt")
	sigFile := filepath.Join(tmpDir, "bad.rsg")
	binPath := filepath.Join(tmpDir, "gornid")

	// Build the binary first (go run doesn't preserve exit codes reliably)
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Dir = "."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	out, err := exec.Command(binPath, "--config", tmpDir, "-g", idFile).CombinedOutput()
	if err != nil {
		t.Fatalf("generate failed: %v\n%v", err, string(out))
	}

	if err := os.WriteFile(dataFile, []byte("some data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigFile, []byte("not-a-real-signature-padding-to-64-bytes-0123456789abcdef01234567"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "--config", tmpDir, "-i", idFile, "-V", sigFile, "-r", dataFile)
	_, err = cmd.CombinedOutput()
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
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	idFile := filepath.Join(tmpDir, "test.id")

	out, err := runGornid(t, "--config", tmpDir, "-g", idFile)
	if err != nil {
		t.Fatalf("generate failed: %v\n%v", err, out)
	}

	out, err = runGornid(t, "--config", tmpDir, "-i", idFile, "-H", "app.aspect")
	if err != nil {
		t.Fatalf("hash failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "app.aspect") {
		t.Errorf("output missing 'app.aspect', got: %v", out)
	}
	if !strings.Contains(out, "destination for this Identity is") {
		t.Errorf("output missing destination hash, got: %v", out)
	}
	if !strings.Contains(out, "full destination specifier") {
		t.Errorf("output missing full specifier, got: %v", out)
	}
}
