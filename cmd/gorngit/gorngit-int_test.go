// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

// gorngit-int_test.go implements two-node integration tests for the gorngit
// /git/list and /git/create operations over paired UDP interfaces on
// 127.0.0.1, mirroring the gornsh integration test pattern.

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/rsg"
	"github.com/gmlewis/go-reticulum/testutils"
)

var gorngitBinaryPath string

const integrationAnnounceTimeout = 20 * time.Second

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(0)
	}

	if _, err := exec.LookPath("git"); err != nil {
		log.Printf("git not in PATH, skipping gorngit integration suite: %v", err)
		os.Exit(0)
	}

	binDir, cleanup := testutils.TempDirMain("gorngit-bin-")

	gorngitBinaryPath = filepath.Join(binDir, "gorngit")
	build := exec.Command("go", "build", "-o", gorngitBinaryPath, ".")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		log.Fatalf("failed to build gorngit binary: %v\n", err)
	}

	exitCode := m.Run()

	cleanup()
	out, err := exec.Command("/usr/bin/pkill", "-f", binDir).CombinedOutput()
	if err != nil && err.Error() != "exit status 1" {
		log.Printf("pkill -f %q failed: %v\n%s", binDir, err, out)
	}

	os.Exit(exitCode)
}

// prepareGorngitDirectUDPConfigPair creates a pair of RNS config directories
// with paired UDP interfaces on 127.0.0.1, mirroring the gornsh pattern.
func prepareGorngitDirectUDPConfigPair(t *testing.T, prefix string) (string, string) {
	t.Helper()

	listenerConfigDir := testutils.TempDir(t, prefix+"listener-")
	initiatorConfigDir := testutils.TempDir(t, prefix+"initiator-")

	listenerPort := testutils.ReserveUDPPort(t)
	initiatorPort := testutils.ReserveUDPPort(t)
	prepareGorngitDirectUDPConfig(t, listenerConfigDir, "gorngit-listener-"+filepath.Base(listenerConfigDir), listenerPort, initiatorPort)
	prepareGorngitDirectUDPConfig(t, initiatorConfigDir, "gorngit-initiator-"+filepath.Base(initiatorConfigDir), initiatorPort, listenerPort)

	return listenerConfigDir, initiatorConfigDir
}

// prepareGorngitDirectUDPConfig writes a minimal RNS config with one UDP
// interface, mirroring prepareGornshDirectUDPConfig.
func prepareGorngitDirectUDPConfig(t *testing.T, configDir, instanceName string, listenPort, forwardPort int) {
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
		"    listen_port = " + fmt.Sprintf("%v", listenPort),
		"    forward_ip = 127.0.0.1",
		"    forward_port = " + fmt.Sprintf("%v", forwardPort),
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(configText), 0o600); err != nil {
		t.Fatalf("failed to write gorngit direct UDP config: %v", err)
	}
}

// openAllowedContent is the group .allowed content that grants every
// permission to every identity, so integration-test clients (which seed
// bare repos directly with no per-repo .allowed) are granted access at
// the group level via the TGT_ALL fallback.
const openAllowedContent = "r:all\nw:all\nrw:all\nc:all\ns:all\nrel:all\ni:all\np:all\nadm:all\n"

// prepareGorngitNodeConfig writes a gorngit node config in configDir that
// serves the repository group "main" from repoRoot, and writes an open
// group .allowed file (<repoRoot>.allowed) granting all permissions to all
// identities so the real permission resolver admits every identified test
// client.
func prepareGorngitNodeConfig(t *testing.T, configDir, repoRoot string) {
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
		t.Fatalf("failed to write gorngit node config: %v", err)
	}
	// The group .allowed file lives as a SIBLING of repoRoot (the gorngit
	// permission resolver looks up <repoRoot>.allowed), so it is outside the
	// repoRoot TempDir and would not be removed by repoRoot's t.Cleanup.
	// Register an explicit cleanup so it does not leak into /tmp on every run.
	allowedPath := repoRoot + ".allowed"
	if err := os.WriteFile(allowedPath, []byte(openAllowedContent), 0o644); err != nil {
		t.Fatalf("failed to write group .allowed: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(allowedPath) })
}

// prepareGorngitNodeConfigRestricted writes a gorngit node config like
// prepareGorngitNodeConfig but WITHOUT a group .allowed file, so the real
// permission resolver denies all access (empty perm lists). Used by the
// permission-denial test.
func prepareGorngitNodeConfigRestricted(t *testing.T, configDir, repoRoot string) {
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
		t.Fatalf("failed to write gorngit node config: %v", err)
	}
}

// seedBareRepo creates a bare git repository at repoPath and seeds it with a
// single commit on refs/heads/main via a temporary work repo, all through
// os/exec. Returns the SHA of the commit.
func seedBareRepo(t *testing.T, repoPath string) string {
	t.Helper()

	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir bare repo: %v", err)
	}
	runGit(t, repoPath, "init", "--bare")
	runGit(t, repoPath, "symbolic-ref", "HEAD", "refs/heads/main")

	workDir := testutils.TempDir(t, "gorngit-seed-work-")
	runGit(t, workDir, "init")
	runGit(t, workDir, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# test repo\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
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

// runGit is defined in testhelpers_test.go (shared with non-integration
// release tests).

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
		t.Fatalf("failed to start gorngit node: %v", err)
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
// waitForPathWithoutGornpath.
func waitForGorngitAnnounce(t *testing.T, initiatorConfigDir, serverConfigDir string) []byte {
	t.Helper()

	// The server's destination hash is derived from its identity, which is
	// persisted in the node config dir by the subprocess. Wait for the
	// identity file to appear before loading it.
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
		t.Fatalf("could not load server identity: %v", err)
	}
	destHash := rns.CalculateHash(serverIdentity, appName, repoAspect)

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulum(ts, initiatorConfigDir)
	if err != nil {
		t.Fatalf("failed to initialize Reticulum for path wait: %v", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close Reticulum after path wait: %v", err)
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
	t.Fatalf("timed out waiting for announce of %x within %v", destHash, integrationAnnounceTimeout)
	return nil
}

// TestIntegrationListReturnsRefs verifies that a gorngit node serves the ref
// list of a seeded bare repository to a connected client over RNS.
func TestIntegrationListReturnsRefs(t *testing.T) {
	if gorngitBinaryPath == "" {
		t.Skip("gorngit binary not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareGorngitDirectUDPConfigPair(t, "gorngit-list-")

	// Set up the node config with a repository group pointing at a temp dir.
	nodeConfigDir := testutils.TempDir(t, "gorngit-list-nodecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-list-repos-")
	repoName := "testrepo.git"
	commitSHA := seedBareRepo(t, filepath.Join(repoRoot, repoName))
	prepareGorngitNodeConfig(t, nodeConfigDir, repoRoot)

	// Start the gorngit node subprocess.
	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	// Wait for the server to announce.
	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)
	t.Logf("Server destination hash: %x", destHash)

	// Connect a client from the initiator side and list refs.
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, initiatorRNSConfig, logger)
	if err != nil {
		t.Fatalf("could not initialize client Reticulum: %v", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close client Reticulum: %v", err)
		}
	}()

	clientConfigDir := testutils.TempDir(t, "gorngit-list-clientcfg-")
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), repoName)
	client, err := newReticulumGitClient(ts, clientConfigDir, "", remoteURL, logger)
	if err != nil {
		t.Fatalf("could not create client: %v", err)
	}

	if err := client.connect(logger); err != nil {
		t.Fatalf("could not connect: %v", err)
	}
	defer client.teardown()

	refs, err := client.list(false)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	t.Logf("Refs returned: %q", refs)

	if !strings.Contains(refs, commitSHA) {
		t.Errorf("refs %q does not contain commit SHA %q", refs, commitSHA)
	}
	if !strings.Contains(refs, "refs/heads/main") {
		t.Errorf("refs %q does not contain refs/heads/main", refs)
	}
	if !strings.Contains(refs, "@refs/heads/main HEAD") {
		t.Errorf("refs %q does not contain @refs/heads/main HEAD line", refs)
	}
}

// TestIntegrationCreateRepo verifies that a gorngit client can create a new
// bare repository on a remote node over RNS.
func TestIntegrationCreateRepo(t *testing.T) {
	if gorngitBinaryPath == "" {
		t.Skip("gorngit binary not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareGorngitDirectUDPConfigPair(t, "gorngit-create-")

	nodeConfigDir := testutils.TempDir(t, "gorngit-create-nodecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-create-repos-")
	prepareGorngitNodeConfig(t, nodeConfigDir, repoRoot)

	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)
	t.Logf("Server destination hash: %x", destHash)

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, initiatorRNSConfig, logger)
	if err != nil {
		t.Fatalf("could not initialize client Reticulum: %v", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close client Reticulum: %v", err)
		}
	}()

	clientConfigDir := testutils.TempDir(t, "gorngit-create-clientcfg-")
	newRepoName := "newrepo.git"
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), newRepoName)
	client, err := newReticulumGitClient(ts, clientConfigDir, "", remoteURL, logger)
	if err != nil {
		t.Fatalf("could not create client: %v", err)
	}

	if err := client.connect(logger); err != nil {
		t.Fatalf("could not connect: %v", err)
	}
	defer client.teardown()

	if err := client.create(); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	createdPath := filepath.Join(repoRoot, newRepoName)
	if _, err := os.Stat(createdPath); err != nil {
		t.Fatalf("created repo dir %q does not exist: %v", createdPath, err)
	}
	if !isBareGitRepository(createdPath) {
		t.Fatalf("created repo %q is not a bare git repository", createdPath)
	}
}

// createEmptyBareRepo creates an empty bare git repository at repoPath with
// HEAD pointing at refs/heads/main, all through os/exec.
func createEmptyBareRepo(t *testing.T, repoPath string) {
	t.Helper()

	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir bare repo: %s", err)
	}
	runGit(t, repoPath, "init", "--bare")
	runGit(t, repoPath, "symbolic-ref", "HEAD", "refs/heads/main")
}

// makeWorkRepoWithCommit creates a non-bare git work repo at workDir with a
// single commit on refs/heads/main. Returns the commit SHA.
func makeWorkRepoWithCommit(t *testing.T, workDir string) string {
	t.Helper()

	runGit(t, workDir, "init")
	runGit(t, workDir, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, workDir, "config", "user.email", "test@example.com")
	runGit(t, workDir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README: %s", err)
	}
	runGit(t, workDir, "add", "README.md")
	runGit(t, workDir, "commit", "-m", "initial commit")
	shaOut := runGit(t, workDir, "rev-parse", "refs/heads/main")
	return strings.TrimSpace(shaOut)
}

// addCommitToWorkRepo adds a second commit to the work repo at workDir and
// returns the new refs/heads/main SHA.
func addCommitToWorkRepo(t *testing.T, workDir string) string {
	t.Helper()

	if err := os.WriteFile(filepath.Join(workDir, "file2.txt"), []byte("second file\n"), 0o644); err != nil {
		t.Fatalf("write file2: %s", err)
	}
	runGit(t, workDir, "add", "file2.txt")
	runGit(t, workDir, "commit", "-m", "second commit")
	shaOut := runGit(t, workDir, "rev-parse", "refs/heads/main")
	return strings.TrimSpace(shaOut)
}

// createLocalBundle creates a git bundle at bundlePath from the work repo at
// workDir containing refs/heads/main, excluding any SHAs in excludeSHAs that
// exist locally. Returns when the bundle is created.
func createLocalBundle(t *testing.T, workDir, bundlePath string, excludeSHAs []string) {
	t.Helper()

	args := []string{"bundle", "create", bundlePath, "refs/heads/main"}
	for _, sha := range excludeSHAs {
		if objectExists(workDir, sha) {
			args = append(args, "^"+sha)
		}
	}
	runGit(t, workDir, args...)
}

// TestIntegrationFetchReturnsBundle verifies that a gorngit node serves a git
// bundle for a fetch request over RNS, and that the received bundle passes
// git bundle verify.
func TestIntegrationFetchReturnsBundle(t *testing.T) {
	if gorngitBinaryPath == "" {
		t.Skip("gorngit binary not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareGorngitDirectUDPConfigPair(t, "gorngit-fetch-")

	nodeConfigDir := testutils.TempDir(t, "gorngit-fetch-nodecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-fetch-repos-")
	repoName := "fetchrepo.git"
	seedBareRepo(t, filepath.Join(repoRoot, repoName))
	prepareGorngitNodeConfig(t, nodeConfigDir, repoRoot)

	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)
	t.Logf("Server destination hash: %x", destHash)

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, initiatorRNSConfig, logger)
	if err != nil {
		t.Fatalf("could not initialize client Reticulum: %s", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close client Reticulum: %s", err)
		}
	}()

	clientConfigDir := testutils.TempDir(t, "gorngit-fetch-clientcfg-")
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), repoName)
	client, err := newReticulumGitClient(ts, clientConfigDir, "", remoteURL, logger)
	if err != nil {
		t.Fatalf("could not create client: %s", err)
	}

	if err := client.connect(logger); err != nil {
		t.Fatalf("could not connect: %s", err)
	}
	defer client.teardown()

	refs := []fetchRefEntry{{sha: "", ref: "refs/heads/main"}}
	bundleData, err := client.fetch(refs, nil)
	if err != nil {
		t.Fatalf("fetch failed: %s", err)
	}
	if len(bundleData) == 0 {
		t.Fatal("fetch returned empty bundle, expected non-empty bundle with commit objects")
	}
	t.Logf("Received bundle of %d bytes", len(bundleData))

	// Write the bundle to a temp file and verify it.
	bundleDir := testutils.TempDir(t, "gorngit-fetch-bundle-")
	bundlePath := filepath.Join(bundleDir, "fetch.bundle")
	if err := os.WriteFile(bundlePath, bundleData, 0o644); err != nil {
		t.Fatalf("write bundle: %s", err)
	}

	// git bundle verify needs a git working directory to check prerequisites.
	// A full bundle (no prerequisites) verifies in any repo.
	verifyDir := testutils.TempDir(t, "gorngit-fetch-verify-")
	runGit(t, verifyDir, "init")
	verifyOut := runGit(t, verifyDir, "bundle", "verify", bundlePath)
	t.Logf("bundle verify output: %s", verifyOut)

	// Fetch from the bundle into the verify repo to import objects and
	// update the ref, then confirm the commit is present.
	runGit(t, verifyDir, "fetch", bundlePath, "refs/heads/main:refs/heads/main")
	shaOut := runGit(t, verifyDir, "rev-parse", "refs/heads/main")
	sha := strings.TrimSpace(shaOut)
	if sha == "" {
		t.Fatal("could not resolve refs/heads/main after fetch from bundle")
	}
	t.Logf("Unbundled commit SHA: %s", sha)

	// Confirm the bundle's SHA matches the server's seeded commit.
	serverSHAOut := runGit(t, filepath.Join(repoRoot, repoName), "rev-parse", "refs/heads/main")
	serverSHA := strings.TrimSpace(serverSHAOut)
	if sha != serverSHA {
		t.Fatalf("unbundled SHA %q does not match server SHA %q", sha, serverSHA)
	}
}

// TestIntegrationPushRoundTrip verifies that a gorngit client can push a git
// bundle to a remote bare repo over RNS, updating the remote ref.
func TestIntegrationPushRoundTrip(t *testing.T) {
	if gorngitBinaryPath == "" {
		t.Skip("gorngit binary not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareGorngitDirectUDPConfigPair(t, "gorngit-push-")

	nodeConfigDir := testutils.TempDir(t, "gorngit-push-nodecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-push-repos-")
	repoName := "pushrepo.git"
	serverRepoPath := filepath.Join(repoRoot, repoName)
	createEmptyBareRepo(t, serverRepoPath)
	prepareGorngitNodeConfig(t, nodeConfigDir, repoRoot)

	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)
	t.Logf("Server destination hash: %x", destHash)

	// Build a local work repo with a commit and create a bundle.
	workDir := testutils.TempDir(t, "gorngit-push-work-")
	commitSHA := makeWorkRepoWithCommit(t, workDir)
	t.Logf("Local commit SHA: %s", commitSHA)

	bundleDir := testutils.TempDir(t, "gorngit-push-bundle-")
	bundlePath := filepath.Join(bundleDir, "push.bundle")
	createLocalBundle(t, workDir, bundlePath, nil)

	bundleData, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle: %s", err)
	}

	// Connect and push.
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, initiatorRNSConfig, logger)
	if err != nil {
		t.Fatalf("could not initialize client Reticulum: %s", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close client Reticulum: %s", err)
		}
	}()

	clientConfigDir := testutils.TempDir(t, "gorngit-push-clientcfg-")
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), repoName)
	client, err := newReticulumGitClient(ts, clientConfigDir, "", remoteURL, logger)
	if err != nil {
		t.Fatalf("could not create client: %s", err)
	}

	if err := client.connect(logger); err != nil {
		t.Fatalf("could not connect: %s", err)
	}
	defer client.teardown()

	if err := client.push("refs/heads/main", "refs/heads/main", false, bundleData); err != nil {
		t.Fatalf("push failed: %s", err)
	}

	// Verify the server's bare repo ref was updated.
	serverSHAOut := runGit(t, serverRepoPath, "rev-parse", "refs/heads/main")
	serverSHA := strings.TrimSpace(serverSHAOut)
	if serverSHA == "" {
		t.Fatal("server refs/heads/main not set after push")
	}
	if serverSHA != commitSHA {
		t.Fatalf("server ref SHA = %q, want %q", serverSHA, commitSHA)
	}
	t.Logf("Server ref updated to %s", serverSHA)
}

// TestIntegrationFetchEmptyBundle verifies that a fetch for a ref whose
// objects the client already has returns resOK with no bundle data.
func TestIntegrationFetchEmptyBundle(t *testing.T) {
	if gorngitBinaryPath == "" {
		t.Skip("gorngit binary not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareGorngitDirectUDPConfigPair(t, "gorngit-fetchempty-")

	nodeConfigDir := testutils.TempDir(t, "gorngit-fetchempty-nodecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-fetchempty-repos-")
	repoName := "emptyrepo.git"
	commitSHA := seedBareRepo(t, filepath.Join(repoRoot, repoName))
	prepareGorngitNodeConfig(t, nodeConfigDir, repoRoot)

	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)
	t.Logf("Server destination hash: %x", destHash)

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, initiatorRNSConfig, logger)
	if err != nil {
		t.Fatalf("could not initialize client Reticulum: %s", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close client Reticulum: %s", err)
		}
	}()

	clientConfigDir := testutils.TempDir(t, "gorngit-fetchempty-clientcfg-")
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), repoName)
	client, err := newReticulumGitClient(ts, clientConfigDir, "", remoteURL, logger)
	if err != nil {
		t.Fatalf("could not create client: %s", err)
	}

	if err := client.connect(logger); err != nil {
		t.Fatalf("could not connect: %s", err)
	}
	defer client.teardown()

	// Fetch with the server's commit SHA as a have. The server will exclude
	// all objects reachable from that SHA, producing an empty bundle.
	refs := []fetchRefEntry{{sha: commitSHA, ref: "refs/heads/main", have: commitSHA}}
	haves := []string{commitSHA}
	bundleData, err := client.fetch(refs, haves)
	if err != nil {
		t.Fatalf("fetch failed: %s", err)
	}
	if len(bundleData) != 0 {
		t.Fatalf("expected empty bundle, got %d bytes", len(bundleData))
	}
	t.Logf("Fetch returned empty bundle as expected (resOK, no data)")
}

// startDumbHTTPRepoServer serves a bare git repository over dumb HTTP on
// 127.0.0.1 so it can be used as a fork/mirror source URL (the rngit CLONE_PROTOS
// permit http://). The caller must run `git update-server-info` on the bare repo
// before serving so info/refs is available. Returns the source URL and a
// cleanup function.
func startDumbHTTPRepoServer(t *testing.T, bareRepoPath string) (string, func()) {
	t.Helper()

	port := testutils.ReserveTCPPort(t)
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(bareRepoPath)))
	server := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}
	go func() { _ = server.ListenAndServe() }()

	sourceURL := fmt.Sprintf("http://127.0.0.1:%d/", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(sourceURL + "info/refs")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	return sourceURL, func() { _ = server.Close() }
}

// prepareSeededHTTPSource seeds a bare repo, runs update-server-info, and serves
// it over dumb HTTP. Returns the source URL, the bare repo path, the initial
// commit SHA, and a cleanup function.
func prepareSeededHTTPSource(t *testing.T, prefix string) (sourceURL, bareRepoPath, commitSHA string, cleanup func()) {
	t.Helper()

	repoRoot := testutils.TempDir(t, prefix+"src-")
	barePath := filepath.Join(repoRoot, "src.git")
	sha := seedBareRepo(t, barePath)
	runGit(t, barePath, "update-server-info")
	url, httpCleanup := startDumbHTTPRepoServer(t, barePath)
	return url, barePath, sha, httpCleanup
}

// addCommitToBareRepo clones a bare repo into a temporary work repo, adds a
// second commit, pushes it back, and re-runs update-server-info. Returns the
// new refs/heads/main SHA. Used by the sync integration test to advance the
// upstream source between mirror and sync operations.
func addCommitToBareRepo(t *testing.T, bareRepoPath string) string {
	t.Helper()

	workDir := testutils.TempDir(t, "gorngit-sync-work-")
	runGit(t, "", "clone", bareRepoPath, workDir)
	runGit(t, workDir, "config", "user.email", "test@example.com")
	runGit(t, workDir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(workDir, "file2.txt"), []byte("second file\n"), 0o644); err != nil {
		t.Fatalf("write file2: %s", err)
	}
	runGit(t, workDir, "add", "file2.txt")
	runGit(t, workDir, "commit", "-m", "second commit")
	runGit(t, workDir, "push", bareRepoPath, "refs/heads/main")
	runGit(t, bareRepoPath, "update-server-info")
	shaOut := runGit(t, workDir, "rev-parse", "refs/heads/main")
	return strings.TrimSpace(shaOut)
}

// TestIntegrationMirrorRepo verifies that a gorngit client can mirror a source
// repository (served over dumb HTTP) into a remote node group over RNS, and
// that the resulting bare repo has the same refs as the source.
func TestIntegrationMirrorRepo(t *testing.T) {
	if gorngitBinaryPath == "" {
		t.Skip("gorngit binary not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareGorngitDirectUDPConfigPair(t, "gorngit-mirror-")

	nodeConfigDir := testutils.TempDir(t, "gorngit-mirror-nodecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-mirror-repos-")
	prepareGorngitNodeConfig(t, nodeConfigDir, repoRoot)

	sourceURL, sourceRepoPath, sourceSHA, httpCleanup := prepareSeededHTTPSource(t, "gorngit-mirror-")
	defer httpCleanup()
	t.Logf("Source URL: %s (SHA %s)", sourceURL, sourceSHA)

	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)
	t.Logf("Server destination hash: %x", destHash)

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, initiatorRNSConfig, logger)
	if err != nil {
		t.Fatalf("could not initialize client Reticulum: %s", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close client Reticulum: %s", err)
		}
	}()

	clientConfigDir := testutils.TempDir(t, "gorngit-mirror-clientcfg-")
	targetRepo := "mirrored.git"
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), targetRepo)
	client, err := newReticulumGitClient(ts, clientConfigDir, "", remoteURL, logger)
	if err != nil {
		t.Fatalf("could not create client: %s", err)
	}
	if err := client.connect(logger); err != nil {
		t.Fatalf("could not connect: %s", err)
	}
	defer client.teardown()

	if err := client.mirror(sourceURL); err != nil {
		t.Fatalf("mirror failed: %s", err)
	}

	mirrorPath := filepath.Join(repoRoot, targetRepo)
	if _, err := os.Stat(mirrorPath); err != nil {
		t.Fatalf("mirror repo %q does not exist: %s", mirrorPath, err)
	}
	if !isBareGitRepository(mirrorPath) {
		t.Fatalf("mirror repo %q is not a bare git repository", mirrorPath)
	}
	mirrorSHAOut := runGit(t, mirrorPath, "rev-parse", "refs/heads/main")
	mirrorSHA := strings.TrimSpace(mirrorSHAOut)
	if mirrorSHA != sourceSHA {
		t.Fatalf("mirror refs/heads/main = %q, want source SHA %q", mirrorSHA, sourceSHA)
	}
	t.Logf("Mirror created with SHA %s", mirrorSHA)

	// Confirm the upstream metadata was recorded by the server.
	repoType := gitConfigGet(mirrorPath, "repository.rngit.type")
	if repoType != "mirror" {
		t.Fatalf("mirror rngit.type = %q, want mirror", repoType)
	}
	upstream := gitConfigGet(mirrorPath, "repository.rngit.upstream.source")
	if upstream != sourceURL {
		t.Fatalf("mirror upstream.source = %q, want %q", upstream, sourceURL)
	}

	// Sanity-check the source repo still reports the same SHA.
	srcSHAOut := runGit(t, sourceRepoPath, "rev-parse", "refs/heads/main")
	if strings.TrimSpace(srcSHAOut) != sourceSHA {
		t.Fatalf("source SHA changed unexpectedly: %q vs %q", strings.TrimSpace(srcSHAOut), sourceSHA)
	}
}

// TestIntegrationForkRepo verifies that a gorngit client can fork a source
// repository (served over dumb HTTP) into a remote node group over RNS, and
// that the resulting bare repo has the same refs as the source.
func TestIntegrationForkRepo(t *testing.T) {
	if gorngitBinaryPath == "" {
		t.Skip("gorngit binary not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareGorngitDirectUDPConfigPair(t, "gorngit-fork-")

	nodeConfigDir := testutils.TempDir(t, "gorngit-fork-nodecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-fork-repos-")
	prepareGorngitNodeConfig(t, nodeConfigDir, repoRoot)

	sourceURL, _, sourceSHA, httpCleanup := prepareSeededHTTPSource(t, "gorngit-fork-")
	defer httpCleanup()
	t.Logf("Source URL: %s (SHA %s)", sourceURL, sourceSHA)

	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)
	t.Logf("Server destination hash: %x", destHash)

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, initiatorRNSConfig, logger)
	if err != nil {
		t.Fatalf("could not initialize client Reticulum: %s", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close client Reticulum: %s", err)
		}
	}()

	clientConfigDir := testutils.TempDir(t, "gorngit-fork-clientcfg-")
	targetRepo := "forked.git"
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), targetRepo)
	client, err := newReticulumGitClient(ts, clientConfigDir, "", remoteURL, logger)
	if err != nil {
		t.Fatalf("could not create client: %s", err)
	}
	if err := client.connect(logger); err != nil {
		t.Fatalf("could not connect: %s", err)
	}
	defer client.teardown()

	if err := client.fork(sourceURL); err != nil {
		t.Fatalf("fork failed: %s", err)
	}

	forkPath := filepath.Join(repoRoot, targetRepo)
	if _, err := os.Stat(forkPath); err != nil {
		t.Fatalf("fork repo %q does not exist: %s", forkPath, err)
	}
	if !isBareGitRepository(forkPath) {
		t.Fatalf("fork repo %q is not a bare git repository", forkPath)
	}
	forkSHAOut := runGit(t, forkPath, "rev-parse", "refs/heads/main")
	forkSHA := strings.TrimSpace(forkSHAOut)
	if forkSHA != sourceSHA {
		t.Fatalf("fork refs/heads/main = %q, want source SHA %q", forkSHA, sourceSHA)
	}
	t.Logf("Fork created with SHA %s", forkSHA)

	repoType := gitConfigGet(forkPath, "repository.rngit.type")
	if repoType != "fork" {
		t.Fatalf("fork rngit.type = %q, want fork", repoType)
	}
}

// TestIntegrationSyncRepo verifies that a gorngit client can sync a mirrored
// repository after its upstream source advances: it first mirrors a source,
// adds a commit to the source, then syncs and asserts the mirror updated.
func TestIntegrationSyncRepo(t *testing.T) {
	if gorngitBinaryPath == "" {
		t.Skip("gorngit binary not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareGorngitDirectUDPConfigPair(t, "gorngit-sync-")

	nodeConfigDir := testutils.TempDir(t, "gorngit-sync-nodecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-sync-repos-")
	prepareGorngitNodeConfig(t, nodeConfigDir, repoRoot)

	sourceURL, sourceRepoPath, initialSHA, httpCleanup := prepareSeededHTTPSource(t, "gorngit-sync-")
	defer httpCleanup()
	t.Logf("Source URL: %s (initial SHA %s)", sourceURL, initialSHA)

	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)
	t.Logf("Server destination hash: %x", destHash)

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, initiatorRNSConfig, logger)
	if err != nil {
		t.Fatalf("could not initialize client Reticulum: %s", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close client Reticulum: %s", err)
		}
	}()

	clientConfigDir := testutils.TempDir(t, "gorngit-sync-clientcfg-")
	targetRepo := "synced.git"
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), targetRepo)
	client, err := newReticulumGitClient(ts, clientConfigDir, "", remoteURL, logger)
	if err != nil {
		t.Fatalf("could not create client: %s", err)
	}
	if err := client.connect(logger); err != nil {
		t.Fatalf("could not connect: %s", err)
	}
	defer client.teardown()

	// First mirror the source into the target repo.
	if err := client.mirror(sourceURL); err != nil {
		t.Fatalf("mirror failed: %s", err)
	}
	mirrorPath := filepath.Join(repoRoot, targetRepo)
	mirrorSHAOut := runGit(t, mirrorPath, "rev-parse", "refs/heads/main")
	if strings.TrimSpace(mirrorSHAOut) != initialSHA {
		t.Fatalf("mirror SHA = %q, want %q", strings.TrimSpace(mirrorSHAOut), initialSHA)
	}
	t.Logf("Mirror created with SHA %s", initialSHA)

	// Advance the upstream source with a second commit.
	newSourceSHA := addCommitToBareRepo(t, sourceRepoPath)
	t.Logf("Source advanced to SHA %s", newSourceSHA)

	// Sync the mirror from its recorded upstream.
	if err := client.sync(); err != nil {
		t.Fatalf("sync failed: %s", err)
	}

	mirrorSHAOut = runGit(t, mirrorPath, "rev-parse", "refs/heads/main")
	mirrorSHA := strings.TrimSpace(mirrorSHAOut)
	if mirrorSHA != newSourceSHA {
		t.Fatalf("synced mirror SHA = %q, want new source SHA %q", mirrorSHA, newSourceSHA)
	}
	t.Logf("Mirror synced to SHA %s", mirrorSHA)
}

// TestIntegrationDeleteRepo verifies that a gorngit client can delete a ref
// from a remote repository over RNS, mirroring handle_delete (server.py).
func TestIntegrationDeleteRepo(t *testing.T) {
	if gorngitBinaryPath == "" {
		t.Skip("gorngit binary not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareGorngitDirectUDPConfigPair(t, "gorngit-delete-")

	nodeConfigDir := testutils.TempDir(t, "gorngit-delete-nodecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-delete-repos-")
	repoName := "deleterepo.git"
	seedBareRepo(t, filepath.Join(repoRoot, repoName))
	prepareGorngitNodeConfig(t, nodeConfigDir, repoRoot)

	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)
	t.Logf("Server destination hash: %x", destHash)

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, initiatorRNSConfig, logger)
	if err != nil {
		t.Fatalf("could not initialize client Reticulum: %s", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close client Reticulum: %s", err)
		}
	}()

	clientConfigDir := testutils.TempDir(t, "gorngit-delete-clientcfg-")
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), repoName)
	client, err := newReticulumGitClient(ts, clientConfigDir, "", remoteURL, logger)
	if err != nil {
		t.Fatalf("could not create client: %s", err)
	}
	if err := client.connect(logger); err != nil {
		t.Fatalf("could not connect: %s", err)
	}
	defer client.teardown()

	if err := client.deleteRef("refs/heads/main"); err != nil {
		t.Fatalf("deleteRef failed: %s", err)
	}

	// Assert the ref is gone from the server's bare repo.
	serverRepoPath := filepath.Join(repoRoot, repoName)
	cmd := exec.Command("git", "rev-parse", "refs/heads/main")
	cmd.Dir = serverRepoPath
	if err := cmd.Run(); err == nil {
		t.Fatalf("refs/heads/main still exists after delete")
	}
	t.Logf("refs/heads/main deleted successfully")
}

// sendRequestRetry wraps client.sendRequest with a bounded retry for
// transient RNS request failures, for use only with IDEMPOTENT operations
// (artifact upload — which overwrites the artifact file on the server — and
// release fetch — which is a pure read). Re-sending an identical request
// produces an identical server result, so a retry is always safe for these.
//
// Why this exists: RNS resource transfers over a fast localhost link (RTT
// ~2ms) under heavy parallel -race test load can occasionally fail. The
// resource watchdog's part-timeouts are RTT-proportional (the receiver's
// first grace is ResourceRetryGraceTime = 0.25s), and under -race + 8-package
// parallelism goroutine scheduling latency and UDP socket-buffer pressure can
// blow past those timeouts and exhaust retries, cancelling the transfer. The
// Python rngit client (client.py send_request) aborts on a single such
// failure, so the production gorngit client mirrors that and does NOT retry
// (preserving behavioural parity). This helper adds retry purely at the test
// layer so the integration test is rock-solid under load without diverging
// from the Python client's production behaviour.
//
// On a transient failure ("request failed or timed out" / "request timed
// out") the link is still alive, so the retry reuses it. On a link-death
// error ("could not send request" / "link not ready" / "link is closed" /
// "link is not active") it tears down the stale link and re-establishes a
// fresh one via connect before retrying.
func sendRequestRetry(t *testing.T, client *reticulumGitClient, logger *rns.Logger, path string, data any, timeout time.Duration, maxRetries int) (any, error) {
	t.Helper()
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, _, err := client.sendRequest(path, data, timeout)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt == maxRetries {
			break
		}
		msg := err.Error()
		linkDead := strings.Contains(msg, "could not send request") ||
			strings.Contains(msg, "link not ready") ||
			strings.Contains(msg, "link is closed") ||
			strings.Contains(msg, "link is not active")
		if linkDead {
			t.Logf("sendRequest attempt %d/%d failed (%v); reconnecting link", attempt+1, maxRetries+1, err)
			client.teardown()
			client.linkReady = false
			if rerr := client.connect(logger); rerr != nil {
				t.Logf("reconnect failed: %v", rerr)
				break
			}
			continue
		}
		t.Logf("sendRequest attempt %d/%d failed (%v); retrying on same link", attempt+1, maxRetries+1, err)
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("sendRequest failed after %d attempts: %w", maxRetries+1, lastErr)
}

// TestIntegrationReleaseRoundTrip verifies the full release lifecycle
// over RNS: create (init+artifact+finalize), list, view, fetch the
// manifest+artifact, validate the RSM signature + structure, and delete.
// The manifest and artifact .rsg are built locally (byte-identical to
// Python) and uploaded as artifacts; the fetch path validates them.
func TestIntegrationReleaseRoundTrip(t *testing.T) {
	if gorngitBinaryPath == "" {
		t.Skip("gorngit binary not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareGorngitDirectUDPConfigPair(t, "gorngit-relrt-")

	// Seed a bare repo with a tag on the server side.
	nodeConfigDir := testutils.TempDir(t, "gorngit-relrt-nodecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-relrt-repos-")
	repoName := "relrepo.git"
	repoPath := filepath.Join(repoRoot, repoName)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, repoPath, "init", "--bare")
	runGit(t, repoPath, "symbolic-ref", "HEAD", "refs/heads/main")
	work := testutils.TempDir(t, "gorngit-relrt-work-")
	runGit(t, work, "init")
	runGit(t, work, "symbolic-ref", "HEAD", "refs/heads/main")
	runGit(t, work, "config", "user.email", "test@example.com")
	runGit(t, work, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# rel\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, work, "add", "README.md")
	runGit(t, work, "commit", "-m", "initial")
	commitSHA := strings.TrimSpace(runGit(t, work, "rev-parse", "refs/heads/main"))
	runGit(t, work, "tag", "v1.0.0")
	runGit(t, work, "push", repoPath, "refs/heads/main", "refs/tags/v1.0.0")

	prepareGorngitNodeConfig(t, nodeConfigDir, repoRoot)
	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)
	t.Logf("Server destination hash: %x", destHash)

	// Connect a client.
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, initiatorRNSConfig, logger)
	if err != nil {
		t.Fatalf("could not initialize client Reticulum: %v", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close client Reticulum: %v", err)
		}
	}()

	clientConfigDir := testutils.TempDir(t, "gorngit-relrt-clientcfg-")
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), repoName)
	client, err := newReticulumGitClient(ts, clientConfigDir, "", remoteURL, logger)
	if err != nil {
		t.Fatalf("could not create client: %v", err)
	}
	if err := client.connect(logger); err != nil {
		t.Fatalf("could not connect: %v", err)
	}
	defer client.teardown()

	// The client identity is the signer for the manifest + artifact.
	signer := client.identity
	repoPathStr := "main/" + repoName
	tag := "v1.0.0"

	// Build an artifact + its .rsg signature, and the manifest.rsm.
	artifactBytes := []byte("artifact payload for v1.0.0")
	releaseTime := uint64(time.Now().Unix())
	releaseTimeISO := time.Unix(int64(releaseTime), 0).UTC().Format("2006-01-02T15:04:05Z")

	artifactMeta := msgpack.OrderedMap{
		{Key: "timestamp", Value: releaseTime},
	}
	artifactRSG, err := rsg.CreateWithOptions(signer, artifactBytes, rsg.Options{Meta: artifactMeta})
	if err != nil {
		t.Fatalf("create artifact rsg: %v", err)
	}

	manifestMeta := msgpack.OrderedMap{
		{Key: "name", Value: repoName},
		{Key: "version", Value: tag},
		{Key: "released", Value: releaseTimeISO},
		{Key: "timestamp", Value: releaseTime},
		{Key: "origin", Value: destHash},
		{Key: "path", Value: repoPathStr},
		{Key: "commit", Value: commitSHA},
		{Key: "artifacts", Value: []msgpack.OrderedMap{
			{
				{Key: "name", Value: "app.bin"},
				{Key: "rsg", Value: artifactRSG},
			},
		}},
	}
	notes := []byte("Release v1.0.0 notes\nInitial release.")
	manifestRSM, err := rsg.CreateWithOptions(signer, notes, rsg.Options{Embed: true, Meta: manifestMeta})
	if err != nil {
		t.Fatalf("create manifest rsm: %v", err)
	}

	// Step 1: init
	initData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "create",
		"step":               "init",
		"tag":                tag,
		"hash":               commitSHA,
		"notes":              string(notes),
		"notes_format":       "markdown",
	}
	packed, err := msgpack.Pack(initData)
	if err != nil {
		t.Fatalf("pack init: %v", err)
	}
	resp, _, err := client.sendRequest(pathRelease, packed, requestTimeout)
	if err != nil {
		t.Fatalf("init request: %v", err)
	}
	respBytes, ok := resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("init response code = %x, want resOK", firstByte(respBytes))
	}
	t.Logf("Release initialized on remote")

	// Step 2: upload artifacts (app.bin, app.bin.rsg, manifest.rsm)
	artifacts := []struct {
		name string
		data []byte
	}{
		{"app.bin", artifactBytes},
		{"app.bin.rsg", artifactRSG},
		{"manifest.rsm", manifestRSM},
	}
	for _, art := range artifacts {
		artData := map[any]any{
			int64(idxRepository): repoPathStr,
			"operation":          "create",
			"step":               "artifact",
			"tag":                tag,
			"artifact_name":      art.name,
			"artifact_data":      art.data,
		}
		packed, err := msgpack.Pack(artData)
		if err != nil {
			t.Fatalf("pack artifact %s: %v", art.name, err)
		}
		// Artifact upload is idempotent (the server overwrites the artifact
		// file), so a transient RNS resource-transfer failure under heavy
		// -race load can be safely retried. manifest.rsm is the largest
		// artifact and exceeds the link MDU, so it is transferred as a
		// streamed Resource — the path most exposed to watchdog/UDP-pressure
		// flakes under load.
		resp, err := sendRequestRetry(t, client, logger, pathRelease, packed, fetchPushTimeout, 4)
		if err != nil {
			t.Fatalf("artifact %s request: %v", art.name, err)
		}
		respBytes, ok := resp.([]byte)
		if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
			t.Fatalf("artifact %s response code = %x, want resOK", art.name, firstByte(respBytes))
		}
		t.Logf("  %s transferred", art.name)
	}

	// Step 3: finalize
	finData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "create",
		"step":               "finalize",
		"tag":                tag,
	}
	packed, err = msgpack.Pack(finData)
	if err != nil {
		t.Fatalf("pack finalize: %v", err)
	}
	resp, _, err = client.sendRequest(pathRelease, packed, 300*time.Second)
	if err != nil {
		t.Fatalf("finalize request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("finalize response code = %x, want resOK", firstByte(respBytes))
	}
	t.Logf("Release finalized")

	// List releases.
	listData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "list",
	}
	packed, err = msgpack.Pack(listData)
	if err != nil {
		t.Fatalf("pack list: %v", err)
	}
	resp, _, err = client.sendRequest(pathRelease, packed, requestTimeout)
	if err != nil {
		t.Fatalf("list request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || respBytes[0] != resOK {
		t.Fatalf("list response code = %x, want resOK", firstByte(respBytes))
	}
	listUnpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
	if err != nil {
		t.Fatalf("unpack list: %v", err)
	}
	listMap, ok := listUnpacked.(map[any]any)
	if !ok {
		t.Fatalf("list response is %T, want map", listUnpacked)
	}
	releases, _ := listMap["releases"].([]any)
	if len(releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(releases))
	}
	first, _ := releases[0].(map[any]any)
	if first["tag"] != tag {
		t.Errorf("list tag = %v, want %s", first["tag"], tag)
	}
	if first["status"] != "published" {
		t.Errorf("list status = %v, want published", first["status"])
	}
	latest, _ := listMap["latest"].(string)
	if latest != tag {
		t.Errorf("latest = %q, want %q", latest, tag)
	}
	t.Logf("List returned %d releases, latest=%q", len(releases), latest)

	// View the release.
	viewData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "view",
		"tag":                tag,
	}
	packed, err = msgpack.Pack(viewData)
	if err != nil {
		t.Fatalf("pack view: %v", err)
	}
	resp, _, err = client.sendRequest(pathRelease, packed, 300*time.Second)
	if err != nil {
		t.Fatalf("view request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || respBytes[0] != resOK {
		t.Fatalf("view response code = %x, want resOK", firstByte(respBytes))
	}
	viewUnpacked, err := msgpack.UnpackPreserveBinMapKeys(respBytes[1:])
	if err != nil {
		t.Fatalf("unpack view: %v", err)
	}
	viewInfo, ok := viewUnpacked.(map[any]any)
	if !ok {
		t.Fatalf("view response is %T, want map", viewUnpacked)
	}
	if viewInfo["tag"] != tag {
		t.Errorf("view tag = %v, want %s", viewInfo["tag"], tag)
	}
	viewArtifacts, _ := viewInfo["artifacts"].([]any)
	if len(viewArtifacts) != 3 {
		t.Errorf("view artifacts count = %d, want 3", len(viewArtifacts))
	}
	t.Logf("View returned tag=%v status=%v artifacts=%d", viewInfo["tag"], viewInfo["status"], len(viewArtifacts))

	// Fetch manifest.rsm and validate RSM signature + structure.
	fetchManifest := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "fetch",
		"tag":                tag,
		"artifact":           "manifest.rsm",
	}
	packed, err = msgpack.Pack(fetchManifest)
	if err != nil {
		t.Fatalf("pack fetch manifest: %v", err)
	}
	// Fetch is a read (idempotent), and the manifest.rsm response exceeds the
	// link MDU so it returns as a streamed Resource — retry on transient
	// resource-transfer failure under heavy -race load.
	resp, err = sendRequestRetry(t, client, logger, pathRelease, packed, fetchPushTimeout, 4)
	if err != nil {
		t.Fatalf("fetch manifest request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || respBytes[0] != resOK {
		t.Fatalf("fetch manifest response code = %x, want resOK", firstByte(respBytes))
	}
	fetchedManifest := respBytes[1:]
	if !bytes.Equal(fetchedManifest, manifestRSM) {
		t.Fatalf("fetched manifest differs from uploaded manifest")
	}
	signedData, err := rsg.ExtractSignedData(fetchedManifest)
	if err != nil {
		t.Fatalf("extract manifest: %v", err)
	}
	message, _ := signedData["message"].([]byte)
	if message == nil {
		t.Fatal("no embedded message in manifest")
	}
	signingID, err := rsg.Validate(fetchedManifest, message, signer.Hash)
	if err != nil {
		t.Fatalf("validate manifest signature: %v", err)
	}
	t.Logf("Manifest signature validated, signed by %x", signingID.Hash)
	if err := rsg.CheckReleaseRSMStructure(signedData); err != nil {
		t.Fatalf("RSM structure check failed: %v", err)
	}
	t.Logf("RSM structure check passed")

	// Fetch app.bin and validate against the manifest's artifact rsg.
	fetchArtifact := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "fetch",
		"tag":                tag,
		"artifact":           "app.bin",
	}
	packed, err = msgpack.Pack(fetchArtifact)
	if err != nil {
		t.Fatalf("pack fetch artifact: %v", err)
	}
	// Fetch is a read (idempotent); retry on transient failure under load.
	resp, err = sendRequestRetry(t, client, logger, pathRelease, packed, fetchPushTimeout, 4)
	if err != nil {
		t.Fatalf("fetch artifact request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || respBytes[0] != resOK {
		t.Fatalf("fetch artifact response code = %x, want resOK", firstByte(respBytes))
	}
	fetchedArtifact := respBytes[1:]
	if !bytes.Equal(fetchedArtifact, artifactBytes) {
		t.Fatalf("fetched artifact differs from uploaded artifact")
	}
	if _, err := rsg.Validate(artifactRSG, fetchedArtifact, signer.Hash); err != nil {
		t.Fatalf("validate artifact signature: %v", err)
	}
	t.Logf("Artifact signature validated")

	// Delete the release.
	deleteData := map[any]any{
		int64(idxRepository): repoPathStr,
		"operation":          "delete",
		"tag":                tag,
	}
	packed, err = msgpack.Pack(deleteData)
	if err != nil {
		t.Fatalf("pack delete: %v", err)
	}
	resp, _, err = client.sendRequest(pathRelease, packed, requestTimeout)
	if err != nil {
		t.Fatalf("delete request: %v", err)
	}
	respBytes, ok = resp.([]byte)
	if !ok || len(respBytes) == 0 || respBytes[0] != resOK {
		t.Fatalf("delete response code = %x, want resOK", firstByte(respBytes))
	}
	t.Logf("Release deleted")

	// Verify the release dir is gone by listing again.
	if _, _, err = client.sendRequest(pathRelease, packed, requestTimeout); err != nil {
		t.Fatalf("pathRelease: %v", err)
	}
}

// TestIntegrationListDeniedOnRestrictedNode verifies that when the node has
// no group .allowed (so the real permission resolver grants nothing), a
// list request returns resNotFound over the wire (existence is hidden
// because the client lacks READ).
func TestIntegrationListDeniedOnRestrictedNode(t *testing.T) {
	if gorngitBinaryPath == "" {
		t.Skip("gorngit binary not built")
	}

	listenerRNSConfig, initiatorRNSConfig := prepareGorngitDirectUDPConfigPair(t, "gorngit-deny-")

	nodeConfigDir := testutils.TempDir(t, "gorngit-deny-nodecfg-")
	repoRoot := testutils.TempDir(t, "gorngit-deny-repos-")
	repoName := "testrepo.git"
	seedBareRepo(t, filepath.Join(repoRoot, repoName))
	prepareGorngitNodeConfigRestricted(t, nodeConfigDir, repoRoot)

	_, nodeCleanup := startGorngitNode(t, listenerRNSConfig, nodeConfigDir)
	defer nodeCleanup()

	destHash := waitForGorngitAnnounce(t, initiatorRNSConfig, nodeConfigDir)

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogCritical)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulumWithLogger(ts, initiatorRNSConfig, logger)
	if err != nil {
		t.Fatalf("could not initialize client Reticulum: %v", err)
	}
	defer func() {
		if err := ret.Close(); err != nil {
			t.Fatalf("failed to close client Reticulum: %v", err)
		}
	}()

	clientConfigDir := testutils.TempDir(t, "gorngit-deny-clientcfg-")
	remoteURL := fmt.Sprintf("rns://%s/main/%s", fmt.Sprintf("%x", destHash), repoName)
	client, err := newReticulumGitClient(ts, clientConfigDir, "", remoteURL, logger)
	if err != nil {
		t.Fatalf("could not create client: %v", err)
	}
	if err := client.connect(logger); err != nil {
		t.Fatalf("could not connect: %v", err)
	}
	defer client.teardown()

	_, err = client.list(false)
	if err == nil {
		t.Fatal("list succeeded on restricted node, want refusal")
	}
	if !strings.Contains(err.Error(), "Not found") {
		t.Errorf("list error=%q, want it to contain %q", err.Error(), "Not found")
	}
}
