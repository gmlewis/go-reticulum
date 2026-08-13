// Copyright 2026 Glenn Lewis. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

// Command publish-github-release-artifacts builds standalone executables for
// the major supported platforms/targets and publishes them as assets of a
// new GitHub Release (via the gh CLI) tagged with the current version string
// read from rns/version.go.
//
// If a release for that version already exists, the command fails unless
// --force is supplied, in which case the previous release (and its assets) is
// deleted and replaced with freshly built ones. With --force the version's git
// tag is also deleted and recreated on the latest commit, so the tag always
// points at the commit the artifacts were built from. The release notes embed a
// sha256 checksum table for every uploaded artifact.
//
// Usage:
//
//	publish-github-release-artifacts [--force] [-n]
//
// This program is normally driven by scripts/publish-github-release-artifacts.sh.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// versionFile is parsed (not imported) so the publisher always reads whatever
// version string currently sits in the working tree, with no build cache that
// could serve a stale value.
const versionFile = "rns/version.go"

// target is a single GOOS/GOARCH build target.
type target struct {
	goos, goarch string
}

// majorTargets lists the major platforms we ship release artifacts for, each
// built for both amd64 and arm64. Windows gets the .exe suffix; everything
// else is a bare executable.
var majorTargets = []target{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"windows", "amd64"},
	{"windows", "arm64"},
	{"freebsd", "amd64"},
	{"freebsd", "arm64"},
}

// platformBlacklist names programs that compile cleanly for a GOOS but whose
// core functionality is non-functional there at runtime, so we refuse to build
// or publish them for that platform. An entry here means "this binary would
// misbehave for users on that OS"; it is not for mere compile failures (those
// are caught by the build itself). Keyed by GOOS because every known issue is
// whole-OS, not architecture-specific.
//
// windows:
//   - gornodeconf: RNode serial config/flashing/EEPROM/signing. Its
//     serial-other.go / flash-other.go / unsupported-live-other.go stubs
//     return "not supported on platform windows" for every live hardware
//     operation, which is the tool's entire purpose.
//   - gornsh: remote shell. pty-other.go returns "PTY execution is not
//     supported on this platform" for the interactive PTY shell (its core
//     feature), and session.go defaults the shell to /bin/sh (absent on
//     Windows). The non-interactive exec path works, but the primary
//     interactive-shell purpose is broken.
var platformBlacklist = map[string]map[string]bool{
	"windows": {
		"gornodeconf": true,
		"gornsh":      true,
	},
}

// blacklisted reports whether binaryName must not be built for the given GOOS.
func blacklisted(goos, binaryName string) bool {
	return platformBlacklist[goos][binaryName]
}

// discoverBinaryNames scans the cmd/ directory for every program whose name
// starts with "go" and returns them sorted, instead of relying on a hard-coded
// list. A directory qualifies when it starts with "go" and contains at least
// one .go file declaring package main, so non-program directories (and this
// publisher's own directory) are skipped.
func discoverBinaryNames() ([]string, error) {
	entries, err := os.ReadDir("cmd")
	if err != nil {
		return nil, fmt.Errorf("read cmd/ directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "go") {
			continue
		}
		isMain, err := dirIsMainPackage(filepath.Join("cmd", name))
		if err != nil {
			return nil, fmt.Errorf("inspect cmd/%v: %w", name, err)
		}
		if isMain {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no programs starting with \"go\" found under cmd/")
	}
	sort.Strings(names)
	return names, nil
}

// dirIsMainPackage reports whether the directory at dir contains at least one
// .go file declaring "package main". Build-constraint-ignored files are not a
// concern here: every shipped program has an unconditional package declaration.
func dirIsMainPackage(dir string) (bool, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	mainRe := regexp.MustCompile(`(?m)^package main\b`)
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return false, err
		}
		if mainRe.Match(data) {
			return true, nil
		}
	}
	return false, nil
}

func main() {
	force := flag.Bool("force", false,
		"replace an existing release for this version (deletes previous assets)")
	dryRun := flag.Bool("n", false,
		"print the full Markdown release description that would be written to "+
			"stdout and exit, without publishing (artifacts are still built so "+
			"the sha256 checksums are real)")
	flag.Usage = func() {
		log.Printf("Usage: %v [--force] [-n]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if err := run(*force, *dryRun); err != nil {
		log.Printf("publish-github-release-artifacts: %v\n", err)
		os.Exit(1)
	}
}

// run builds the release artifacts and either publishes them as a new GitHub
// release (when dryRun is false) or prints the Markdown release description
// that would be written to stdout and returns (when dryRun is true). In dry-run
// mode all progress output is routed to stderr so stdout contains only the
// Markdown description.
func run(force, dryRun bool) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh CLI not found in PATH: %w", err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go toolchain not found in PATH: %w", err)
	}

	// progress writes build/publish chatter; in dry-run mode it goes to stderr
	// so stdout stays a clean Markdown document.
	progress := os.Stdout
	if dryRun {
		progress = os.Stderr
	}

	version, err := readVersion()
	if err != nil {
		return err
	}
	tag := "v" + version
	mustFprintf(progress, "Publishing release for version %v (tag %v)\n", version, tag)

	repo, err := ghRepoSlug()
	if err != nil {
		return err
	}
	mustFprintf(progress, "Repository: %v\n", repo)

	binaryNames, err := discoverBinaryNames()
	if err != nil {
		return err
	}
	mustFprintf(progress, "Publishing %v program(s): %v\n",
		len(binaryNames), strings.Join(binaryNames, ", "))

	exists := false
	if !dryRun {
		exists, err = releaseExists(tag)
		if err != nil {
			return err
		}
		if exists && !force {
			return fmt.Errorf(
				"release %v already exists; run scripts/bump-minor-version.sh to "+
					"bump the minor version in %v, then retry "+
					"(or re-run with --force to replace the existing release)",
				tag, versionFile)
		}
	}

	// Build into a scratch dir under the system temp location so we never
	// pollute the working tree and can clean up wholesale.
	outDir, err := os.MkdirTemp("/tmp", "gorns-release-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(outDir) }()

	assets, err := buildAll(outDir, version, binaryNames, progress)
	if err != nil {
		return err
	}

	notes := buildReleaseNotes(version, repo, assets)

	if dryRun {
		fmt.Print(notes)
		return nil
	}

	if force {
		// With --force we replace anything already published for this version
		// and then re-tag the latest commit. Each step tolerates there being
		// nothing to replace, so an unnecessary --force (no release, no tag)
		// does not fail — it just prints a note and falls through to publish.
		if exists {
			mustFprintf(progress, "--force: deleting existing release %v and its assets\n", tag)
			// Delete the release without --cleanup-tag: that flag makes gh also
			// delete the git tag ref, but it hard-fails (HTTP 422 "Reference
			// does not exist") when the tag is absent even though the release
			// exists. We handle the tag separately below, where absence is fine.
			if err := gh("release", "delete", tag, "--yes"); err != nil {
				return fmt.Errorf("delete existing release: %w", err)
			}
		}
		// Whether or not a release existed, a git tag may be present (left over
		// from a manual push, or a prior release deleted without --cleanup-tag).
		// Drop it so the create below re-tags the latest commit; tolerate
		// absence.
		hasTag, err := tagExists(tag)
		if err != nil {
			return err
		}
		if hasTag {
			mustFprintf(progress, "--force: deleting existing tag %v so it moves to the latest commit\n", tag)
			if err := deleteTagRef(tag); err != nil {
				return fmt.Errorf("delete existing tag: %w", err)
			}
		}
		if !exists && !hasTag {
			mustFprintf(progress,
				"--force: no existing release or tag %v found; --force was unnecessary, continuing to publish\n",
				tag)
		}
	}

	args := []string{"release", "create", tag, "--title", tag, "--notes", notes}
	args = append(args, assets...)
	if err := gh(args...); err != nil {
		return fmt.Errorf("create release: %w", err)
	}

	mustFprintf(progress, "\nPublished release %v with %v asset(s):\n", tag, len(assets))
	for _, a := range assets {
		mustFprintf(progress, "  %v\n", filepath.Base(a))
	}
	return nil
}

// readVersion parses the VERSION constant out of rns/version.go.
func readVersion() (string, error) {
	data, err := os.ReadFile(versionFile)
	if err != nil {
		return "", fmt.Errorf("read %v: %w", versionFile, err)
	}
	re := regexp.MustCompile(`VERSION\s*=\s*"([^"]+)"`)
	m := re.FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("no VERSION string constant found in %v", versionFile)
	}
	v := string(m[1])
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(v) {
		return "", fmt.Errorf("version %q in %v is not a clean MAJOR.MINOR.PATCH semver", v, versionFile)
	}
	return v, nil
}

// releaseExists reports whether a GitHub release with the given tag exists.
func releaseExists(tag string) (bool, error) {
	cmd := exec.Command("gh", "release", "view", tag, "--json", "tagName")
	if err := cmd.Run(); err != nil {
		// gh returns a non-zero exit (and a message like "release not found")
		// when the tag does not exist; treat that as "does not exist".
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			if strings.Contains(string(ee.Stderr), "not found") {
				return false, nil
			}
		}
		// Fall back to parsing combined output for the not-found signal.
		out, outErr := exec.Command("gh", "release", "view", tag).CombinedOutput()
		if outErr != nil && strings.Contains(string(out), "not found") {
			return false, nil
		}
		if outErr == nil {
			return true, nil
		}
		return false, fmt.Errorf("check existing release %v: %w", tag, err)
	}
	return true, nil
}

// tagExists reports whether a git tag named tag exists on the remote.
func tagExists(tag string) (bool, error) {
	cmd := exec.Command("gh", "api", "--method", "GET",
		"repos/:owner/:repo/git/refs/tags/"+tag, "--silent")
	if err := cmd.Run(); err != nil {
		// gh exits non-zero with a "not found" / 404 message when the ref
		// does not exist; treat that as "no such tag".
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			if strings.Contains(string(ee.Stderr), "not found") ||
				strings.Contains(string(ee.Stderr), "404") {
				return false, nil
			}
		}
		return false, fmt.Errorf("check existing tag %v: %w", tag, err)
	}
	return true, nil
}

// deleteTagRef deletes the git tag ref named tag from the remote so that a
// subsequent release create can re-create it on the latest commit.
func deleteTagRef(tag string) error {
	return gh("api", "--method", "DELETE", "repos/:owner/:repo/git/refs/tags/"+tag)
}

// ghRepoSlug returns the "owner/repo" slug gh is authenticated against.
func ghRepoSlug() (string, error) {
	out, err := exec.Command("gh", "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner").Output()
	if err != nil {
		return "", fmt.Errorf("determine repo slug: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gh runs a gh command, streaming stdio to the terminal.
func gh(args ...string) error {
	cmd := exec.Command("gh", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// buildAll builds one executable per (binary, target) into outDir and returns
// the absolute paths of the produced artifacts, in job order. progress receives
// build chatter.
//
// The builds run concurrently, bounded by a semaphore sized to
// runtime.NumCPU(). Each individual `go build` is invoked with -p 1 (a single
// compiler process at a time), so at most NumCPU compiler processes are alive
// at once — keeping CPU-bound work balanced across the available cores instead
// of oversubscribing them (running NumCPU builds each at the default -p would
// spawn NumCPU*NumCPU compiler jobs thrashing the scheduler). This is a real
// win because the 90 builds are launched as separate `go build` processes that
// the compiler cannot co-schedule on its own; a single small CLI binary's
// package DAG is too narrow to saturate all cores by itself.
//
// On the first build failure the context is cancelled, in-flight builds are
// killed, and not-yet-started jobs are skipped, so we don't keep burning cores
// after an error.
func buildAll(outDir, version string, binaryNames []string, progress *os.File) ([]string, error) {
	type job struct {
		binaryName string
		t          target
		name       string
		outPath    string
	}
	var jobs []job
	var skipped []string
	for _, binaryName := range binaryNames {
		for _, t := range majorTargets {
			if blacklisted(t.goos, binaryName) {
				skipped = append(skipped, fmt.Sprintf("%v-%v-%v", binaryName, t.goos, t.goarch))
				continue
			}
			name := fmt.Sprintf("%v-%v-%v-%v", binaryName, version, t.goos, t.goarch)
			if t.goos == "windows" {
				name += ".exe"
			}
			jobs = append(jobs, job{
				binaryName: binaryName,
				t:          t,
				name:       name,
				outPath:    filepath.Join(outDir, name),
			})
		}
	}
	if len(skipped) > 0 {
		sort.Strings(skipped)
		mustFprintf(progress,
			"Skipping %v blacklisted build(s) (non-functional on their target platform): %v\n",
			len(skipped), strings.Join(skipped, ", "))
	}

	// Run the builds concurrently, bounded by a semaphore sized to
	// runtime.NumCPU(). Each `go build` is passed -p 1 (a single compiler
	// process at a time), so at most NumCPU compiler processes are alive at
	// once — keeping CPU-bound work balanced across the available cores
	// instead of oversubscribing them (running NumCPU builds each at the
	// default -p would spawn NumCPU*NumCPU compiler jobs thrashing the
	// scheduler). On a 12-core laptop this cuts a cold-cache build of the
	// full matrix roughly in half versus the previous sequential loop.
	//
	// This is a real win because the builds are launched as separate
	// `go build` processes that the Go compiler cannot co-schedule on its
	// own: a single small CLI binary's package DAG is too narrow to saturate
	// all cores by itself, so the sequential loop left cores idle.
	concurrency := max(1, min(runtime.NumCPU(), len(jobs)))
	sema := make(chan struct{}, concurrency)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// progressMu serializes our own "Building ..." log lines so concurrent
	// goroutines don't interleave them. (go build itself is silent on success,
	// so there is nothing else contending for progress in the common case.)
	var progressMu sync.Mutex
	var mu sync.Mutex
	var firstErr error
	built := make([]bool, len(jobs))
	paths := make([]string, len(jobs))

	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sema <- struct{}{}:
			}
			defer func() { <-sema }()

			progressMu.Lock()
			mustFprintf(progress, "Building %v/%v -> %v\n", j.t.goos, j.t.goarch, j.name)
			progressMu.Unlock()

			cmd := exec.CommandContext(ctx, "go", "build",
				"-trimpath", "-p", "1", "-o", j.outPath, "./cmd/"+j.binaryName)
			cmd.Env = append(os.Environ(),
				"GOOS="+j.t.goos,
				"GOARCH="+j.t.goarch,
				"CGO_ENABLED=0",
			)
			cmd.Stdout = progress
			cmd.Stderr = progress
			// Kill the go driver promptly when another build fails and cancels
			// the context, so we don't leave compilers running.
			cmd.Cancel = func() error {
				if cmd.Process == nil {
					return nil
				}
				return cmd.Process.Kill()
			}
			if err := cmd.Run(); err != nil {
				cancel()
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("build %v/%v: %w", j.t.goos, j.t.goarch, err)
				}
				mu.Unlock()
				return
			}
			paths[i] = j.outPath
			built[i] = true
		}(i, j)
	}
	wg.Wait()

	var assets []string
	for i, ok := range built {
		if ok {
			assets = append(assets, paths[i])
		}
	}
	if firstErr != nil {
		return assets, firstErr
	}
	return assets, nil
}

// buildReleaseNotes assembles the Markdown body for the release, including a
// sha256 checksum table for every artifact.
func buildReleaseNotes(version, repo string, assets []string) string {
	var b strings.Builder
	mustFprintf(&b, "# Go Reticulum Network Stack v%v\n\n", version)
	mustFprintf(&b, "Standalone executables built from [github.com/%v](https://github.com/%v) at tag v%v.\n\n", repo, repo, version)
	mustFprintf(&b, "Built with Go on %v/%v with `CGO_ENABLED=0`.\n\n", runtime.GOOS, runtime.GOARCH)
	mustFprintf(&b, "## Artifacts\n\n")
	mustFprintf(&b, "| File | sha256 |\n")
	mustFprintf(&b, "| --- | --- |\n")
	// Sort the artifact rows by filename so the published table is stable and
	// easy to scan regardless of the build order above.
	sorted := make([]string, len(assets))
	copy(sorted, assets)
	sort.Slice(sorted, func(i, j int) bool {
		return filepath.Base(sorted[i]) < filepath.Base(sorted[j])
	})
	for _, a := range sorted {
		sum, err := sha256sum(a)
		if err != nil {
			// Keep going; record the error in the table rather than aborting.
			mustFprintf(&b, "| %v | <error: %v> |\n", filepath.Base(a), err)
			continue
		}
		mustFprintf(&b, "| %v | `%v` |\n", filepath.Base(a), sum)
	}
	mustFprintf(&b, "\nVerify a download with `shasum -a 256 <file>`.\n")
	mustFprintf(&b, "\n## Post-download setup\n\n")
	mustFprintf(&b, "Make the downloaded executable runnable:\n\n")
	mustFprintf(&b, "```\nchmod a+x <binaryname>-<version>-<os>-<arch>\n```\n\n")
	mustFprintf(&b, "On macOS, executables downloaded from the internet carry a\n")
	mustFprintf(&b, "quarantine attribute that blocks them from running until you approve\n")
	mustFprintf(&b, "them. Clear it with:\n\n")
	mustFprintf(&b, "```\nxattr -d com.apple.quarantine <binaryname>-<version>-<os>-<arch>\n```\n")
	return b.String()
}

// sha256sum returns the SHA-256 hex digest of the file at path.
func sha256sum(path string) (string, error) {
	out, err := exec.Command("shasum", "-a", "256", path).Output()
	if err != nil {
		return "", err
	}
	// shasum output: "<hash>  <path>"
	fields := strings.Fields(string(out))
	if len(fields) < 1 {
		return "", fmt.Errorf("unexpected shasum output: %q", string(out))
	}
	return fields[0], nil
}

func mustFprintf(w io.Writer, fmtStr string, args ...any) {
	if _, err := fmt.Fprintf(w, fmtStr, args...); err != nil {
		log.Fatalf("Fprintf failed: %v", err)
	}
}
