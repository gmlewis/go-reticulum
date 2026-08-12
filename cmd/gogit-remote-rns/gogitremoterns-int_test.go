// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

// gogitremoterns-int_test.go implements an end-to-end integration test for
// git-remote-rns: it starts a gorngit node subprocess on paired 127.0.0.1 UDP
// interfaces, builds the helper binary as git-remote-rns, prepends its
// directory to PATH, and drives a full clone/commit/push/re-clone round-trip
// through `git`, asserting tree equality between the original and re-cloned
// working trees.

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

const integrationAnnounceTimeout = 30 * time.Second

// gorngitBinaryPath is the built gorngit server binary, built in TestMain.
var gorngitBinaryPath string

// helperBinaryPath is the directory containing the built git-remote-rns
// helper binary, prepended to PATH for the git subprocesses.
var helperBinaryDir string

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(0)
	}

	if _, err := exec.LookPath("git"); err != nil {
		log.Printf("git not in PATH, skipping gogit-remote-rns integration suite: %s", err)
		os.Exit(0)
	}

	binDir, cleanup := testutils.TempDirMain("gogit-remote-rns-bin-")

	helperBinaryDir = binDir
	helperPath := filepath.Join(binDir, "git-remote-rns")
	buildHelper := exec.Command("go", "build", "-o", helperPath, ".")
	buildHelper.Stdout = os.Stdout
	buildHelper.Stderr = os.Stderr
	if err := buildHelper.Run(); err != nil {
		log.Fatalf("failed to build git-remote-rns binary: %s\n", err)
	}

	gorngitBinaryPath = filepath.Join(binDir, "gorngit")
	buildServer := exec.Command("go", "build", "-o", gorngitBinaryPath, "../gorngit")
	buildServer.Stdout = os.Stdout
	buildServer.Stderr = os.Stderr
	if err := buildServer.Run(); err != nil {
		log.Fatalf("failed to build gorngit binary: %s\n", err)
	}

	exitCode := m.Run()

	cleanup()
	out, err := exec.Command("/usr/bin/pkill", "-f", binDir).CombinedOutput()
	if err != nil && err.Error() != "exit status 1" {
		log.Printf("pkill -f %q failed: %s\n%s", binDir, err, out)
	}

	os.Exit(exitCode)
}

// prepareDirectUDPConfigPair creates a pair of RNS config directories with
// paired UDP interfaces on 127.0.0.1, mirroring the gorngit integration test
// pattern.
func prepareDirectUDPConfigPair(t *testing.T, prefix string) (string, string) {
	t.Helper()

	listenerConfigDir := testutils.TempDir(t, prefix+"listener-")
	initiatorConfigDir := testutils.TempDir(t, prefix+"initiator-")

	listenerPort := testutils.ReserveUDPPort(t)
	initiatorPort := testutils.ReserveUDPPort(t)
	prepareDirectUDPConfig(t, listenerConfigDir, "gogit-listener-"+filepath.Base(listenerConfigDir), listenerPort, initiatorPort)
	prepareDirectUDPConfig(t, initiatorConfigDir, "gogit-initiator-"+filepath.Base(initiatorConfigDir), initiatorPort, listenerPort)

	return listenerConfigDir, initiatorConfigDir
}

// prepareDirectUDPConfig writes a minimal RNS config with one UDP interface,
// mirroring prepareGorngitDirectUDPConfig.
func prepareDirectUDPConfig(t *testing.T, configDir, instanceName string, listenPort, forwardPort int) {
	t.Helper()

	configText := strings.Join([]string{
		"[reticulum]",
		"enable_transport = Yes",
		"share_instance = No",
		"instance_name = " + instanceName,
		"",
		"[logging]",
		"loglevel = 4",
		"",
		"[interfaces]",
		"  [[Default Interface]]",
		"    type = UDPInterface",
		"    enabled = Yes",
		"    listen_ip = 127.0.0.1",
		"    listen_port = " + fmt.Sprintf("%d", listenPort),
		"    forward_ip = 127.0.0.1",
		"    forward_port = " + fmt.Sprintf("%d", forwardPort),
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(configText), 0o600); err != nil {
		t.Fatalf("failed to write direct UDP config: %s", err)
	}
}

// prepareNodeConfig writes a gorngit node config in configDir that serves the
// repository group "main" from repoRoot.
func prepareNodeConfig(t *testing.T, configDir, repoRoot string) {
	t.Helper()

	configText := strings.Join([]string{
		"[rngit]",
		"announce_interval = 1",
		"",
		"[repositories]",
		"main = " + repoRoot,
		"",
		"[logging]",
		"loglevel = 4",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(configText), 0o600); err != nil {
		t.Fatalf("failed to write gorngit node config: %s", err)
	}
}

// seedBareRepo creates a bare git repository at repoPath and seeds it with a
// single commit on refs/heads/main via a temporary work repo, all through
// os/exec. Returns the commit SHA.
func seedBareRepo(t *testing.T, repoPath string) string {
	t.Helper()

	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir bare repo: %s", err)
	}
	runGit(t, repoPath, "init", "--bare")
	runGit(t, repoPath, "symbolic-ref", "HEAD", "refs/heads/main")

	workDir := testutils.TempDir(t, "gogit-seed-work-")
	runGit(t, workDir, "init")
	runGit(t, workDir, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# test repo\n"), 0o644); err != nil {
		t.Fatalf("write README: %s", err)
	}
	runGit(t, workDir, "add", "README.md")
	runGit(t, workDir, "config", "user.email", "test@example.com")
	runGit(t, workDir, "config", "user.name", "Test")
	runGit(t, workDir, "commit", "-m", "initial commit")
	runGit(t, workDir, "push", repoPath, "refs/heads/main")

	shaOut := runGit(t, workDir, "rev-parse", "refs/heads/main")
	sha := strings.TrimSpace(shaOut)
	if sha == "" {
		t.Fatal("could not resolve commit SHA")
	}
	return sha
}

// runGit runs a git command in dir and returns stdout, failing the test on
// error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s in %s failed: %s\nstderr: %s", strings.Join(args, " "), dir, err, stderr.String())
	}
	return stdout.String()
}

// startGorngitNode starts a gorngit node subprocess with the given RNS config
// and node config directories. Returns the process and a cleanup function.
func startGorngitNode(t *testing.T, rnsConfigDir, nodeConfigDir string) (*exec.Cmd, func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, gorngitBinaryPath, "--rnsconfig", rnsConfigDir, "--config", nodeConfigDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("failed to start gorngit node: %s", err)
	}

	cleanup := func() {
		cancel()
		_ = cmd.Wait()
		if stderr.Len() > 0 {
			t.Logf("gorngit node stderr: %s", stderr.String())
		}
		if stdout.Len() > 0 {
			t.Logf("gorngit node stdout: %s", stdout.String())
		}
	}
	return cmd, cleanup
}

// waitForGorngitAnnounce waits for the gorngit node's repositories destination
// to be announced and reachable from the initiator config, mirroring
// waitForGorngitAnnounce in the gorngit integration suite.
func waitForGorngitAnnounce(t *testing.T, initiatorConfigDir, serverConfigDir string) []byte {
	t.Helper()

	identityPath := filepath.Join(serverConfigDir, "repositories_identity")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(identityPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	serverLogger := rns.NewLogger()
	serverLogger.SetLogLevel(rns.LogCritical)
	serverIdentity, err := rns.FromFile(identityPath, serverLogger)
	if err != nil {
		t.Fatalf("could not load server identity: %s", err)
	}
	destHash := rns.CalculateHash(serverIdentity, appName, repoAspect)

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulum(ts, initiatorConfigDir)
	if err != nil {
		t.Fatalf("failed to initialize Reticulum for path wait: %s", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close Reticulum after path wait: %s", err)
		}
	}()

	announceDeadline := time.Now().Add(integrationAnnounceTimeout)
	for time.Now().Before(announceDeadline) {
		if ret.Transport().HasPath(destHash) {
			return destHash
		}
		_ = ret.Transport().RequestPath(destHash)
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for announce of %x within %s", destHash, integrationAnnounceTimeout)
	return nil
}

// gitEnv returns a copy of os.Environ() with the helper binary directory
// prepended to PATH, RNGIT_CONFIG pointed at clientConfigDir, RNS_CONFIG
// pointed at rnsConfigDir, and git set to non-interactive mode. The git
// subprocess inherits this so the git-remote-rns helper it spawns can find
// its config and the helper binary on PATH.
func gitEnv(clientConfigDir, rnsConfigDir string) []string {
	env := os.Environ()
	found := false
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + helperBinaryDir + string(os.PathListSeparator) + e[len("PATH="):]
			found = true
			break
		}
	}
	if !found {
		env = append(env, "PATH="+helperBinaryDir)
	}
	env = append(env,
		"RNGIT_CONFIG="+clientConfigDir,
		"RNS_CONFIG="+rnsConfigDir,
		"GIT_TERMINAL_PROMPT=0",
	)
	return env
}

// runGitWithEnv runs a git command with the given environment and working
// directory, returning combined output and any error. Used for clone/push
// invocations that must inherit the helper-aware environment.
func runGitWithEnv(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	out, err := runGitWithEnvChecked(dir, env, args...)
	if err != nil {
		t.Fatalf("git %s failed: %s\nstdout: %s\nstderr: %s",
			strings.Join(args, " "), err, out.stdout, out.stderr)
	}
	return out.stdout
}

// gitOutput holds the captured stdout and stderr of a git command.
type gitOutput struct {
	stdout string
	stderr string
}

// runGitWithEnvChecked runs a git command and returns its captured output and
// exit error without failing the test, so callers can inspect non-zero exits.
func runGitWithEnvChecked(dir string, env []string, args ...string) (gitOutput, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return gitOutput{stdout: stdout.String(), stderr: stderr.String()}, err
}

// TestIntegrationClonePushReclone exercises the full git-remote-rns path:
// seed a bare repo on a gorngit node, clone it over RNS via the helper, add a
// commit, push it back, then re-clone into a second working tree and assert
// the two trees match.
func TestIntegrationClonePushReclone(t *testing.T) {
	if gorngitBinaryPath == "" || helperBinaryDir == "" {
		t.Skip("integration binaries not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareDirectUDPConfigPair(t, "gogit-clone-")

	nodeConfigDir := testutils.TempDir(t, "gogit-clone-nodecfg-")
	repoRoot := testutils.TempDir(t, "gogit-clone-repos-")
	repoName := "clonetest.git"
	seedBareRepo(t, filepath.Join(repoRoot, repoName))
	prepareNodeConfig(t, nodeConfigDir, repoRoot)

	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)
	t.Logf("Server destination hash: %x", destHash)

	clientConfigDir := testutils.TempDir(t, "gogit-clone-clientcfg-")
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), repoName)
	env := gitEnv(clientConfigDir, initiatorRNSConfig)

	// Clone the seeded repo into a fresh working tree.
	cloneDir := testutils.TempDir(t, "gogit-clone-work-")
	runGitWithEnv(t, "", env, "clone", remoteURL, cloneDir)

	// The cloned HEAD should point at refs/heads/main and contain the seed
	// commit's README.md.
	if _, err := os.Stat(filepath.Join(cloneDir, "README.md")); err != nil {
		t.Fatalf("cloned tree missing README.md: %s", err)
	}

	// Add a second commit and push it back over RNS.
	if err := os.WriteFile(filepath.Join(cloneDir, "file2.txt"), []byte("second file\n"), 0o644); err != nil {
		t.Fatalf("write file2: %s", err)
	}
	runGit(t, cloneDir, "config", "user.email", "test@example.com")
	runGit(t, cloneDir, "config", "user.name", "Test")
	runGit(t, cloneDir, "add", "file2.txt")
	runGit(t, cloneDir, "commit", "-m", "second commit")
	pushedSHAOut := runGit(t, cloneDir, "rev-parse", "refs/heads/main")
	pushedSHA := strings.TrimSpace(pushedSHAOut)
	t.Logf("Pushed SHA: %s", pushedSHA)

	// Push over RNS. Git may return a non-zero exit even when the push
	// itself succeeds (the helper reports ok <ref> and the server ref
	// advances) because the remote-tracking ref update for a remote-helper
	// push is best-effort on some git versions. Verify the push by checking
	// the server's bare repo ref advanced to the pushed SHA, rather than
	// relying on git's exit code alone.
	pushOut, pushErr := runGitWithEnvChecked(cloneDir, env, "push", remoteURL, "refs/heads/main")
	if pushErr != nil {
		t.Logf("git push exited non-zero (verifying via server ref): %s\nstderr: %s",
			pushErr, pushOut.stderr)
	} else {
		t.Logf("git push stderr: %s", pushOut.stderr)
	}

	// Verify the server's bare repo ref advanced to the pushed SHA.
	serverSHAOut := runGit(t, filepath.Join(repoRoot, repoName), "rev-parse", "refs/heads/main")
	serverSHA := strings.TrimSpace(serverSHAOut)
	if serverSHA != pushedSHA {
		t.Fatalf("server ref SHA = %q, want %q (push did not advance server ref)", serverSHA, pushedSHA)
	}

	// Re-clone into a second working tree and assert both trees match.
	recloneDir := testutils.TempDir(t, "gogit-reclone-work-")
	runGitWithEnv(t, "", env, "clone", remoteURL, recloneDir)
	recloneSHAOut := runGit(t, recloneDir, "rev-parse", "refs/heads/main")
	recloneSHA := strings.TrimSpace(recloneSHAOut)
	if recloneSHA != pushedSHA {
		t.Fatalf("re-cloned HEAD SHA = %q, want %q", recloneSHA, pushedSHA)
	}
	if _, err := os.Stat(filepath.Join(recloneDir, "file2.txt")); err != nil {
		t.Fatalf("re-cloned tree missing file2.txt: %s", err)
	}

	// Assert the full tree listing (paths) matches between clone and reclone.
	cloneTree := runGit(t, cloneDir, "ls-tree", "-r", "--name-only", "HEAD")
	recloneTree := runGit(t, recloneDir, "ls-tree", "-r", "--name-only", "HEAD")
	if cloneTree != recloneTree {
		t.Fatalf("tree mismatch:\nclone:\n%s\nreclone:\n%s", cloneTree, recloneTree)
	}
	t.Logf("Re-clone tree matches original (%d bytes of tree listing)", len(cloneTree))
}
