// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
)

// captureOutput redirects os.Stdout/os.Stderr for the duration of fn and
// returns everything written to either — cmd/claudio's commands print
// directly rather than returning a string (this package owns all
// terminal I/O per its own doc), so this is the only way to assert on
// exact message text from outside.
func captureOutput(t *testing.T, fn func()) string {
	t.Helper()
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = wOut, wErr
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()

	fn()

	wOut.Close()
	wErr.Close()
	outBytes, _ := io.ReadAll(rOut)
	errBytes, _ := io.ReadAll(rErr)
	return string(outBytes) + string(errBytes)
}

// TestImageBuildProducesBaseImage is this issue's first empirical
// verification requirement, exercised through the real CLI dispatcher:
// `claudio image build` actually produces claudio/base:latest, visible
// to `docker image inspect`. Builds under the real, shared tag (unlike
// internal/imagebuild's own live tests, which use throwaway tags to
// avoid clobbering a developer's own image) since this is specifically
// testing the command that's supposed to produce that exact tag — Docker's
// own layer cache means a machine that already has it built pays almost
// nothing to rebuild it (see internal/engine and internal/imagebuild's
// own cache tests).
func TestImageBuildProducesBaseImage(t *testing.T) {
	ctx := context.Background()
	if _, err := engine.DetectRuntime(ctx, ""); err != nil {
		t.Skipf("no reachable Docker-API-compatible daemon: %v", err)
	}
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	var code int
	out := captureOutput(t, func() {
		code = run([]string{"image", "build"})
	})
	if code != 0 {
		t.Errorf("claudio image build exited %d, want 0 (output: %s)", code, out)
	}
	if !strings.Contains(out, "Built claudio/base:latest") {
		t.Errorf("output = %q, want it to report building claudio/base:latest", out)
	}

	if err := exec.Command("docker", "image", "inspect", "claudio/base:latest").Run(); err != nil {
		t.Errorf("docker image inspect claudio/base:latest failed after `claudio image build`: %v", err)
	}
}

// TestImageBuildWithRepoBuildsRepoLayer covers --repo: a local checkout
// with a .claudio.yml declaring apt packages gets a second, tagged layer
// built on top of the base — verified against docker image inspect for
// the exact tag imagebuild.RepoImageTag derives, the same way
// TestImageBuildProducesBaseImage checks the base tag.
func TestImageBuildWithRepoBuildsRepoLayer(t *testing.T) {
	ctx := context.Background()
	if _, err := engine.DetectRuntime(ctx, ""); err != nil {
		t.Skipf("no reachable Docker-API-compatible daemon: %v", err)
	}
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	repoPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoPath, ".claudio.yml"), []byte("image:\n  apt: [jq]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No git remote configured for this repoPath — cmdImageBuild's
	// repoIdentity falls back to a synthetic file:// URL, exercising that
	// path specifically (a bare local directory with no "origin" is the
	// common case for a quick `--repo .` against a repo not yet pushed
	// anywhere, or one of claudio's own --new greenfield roots).

	var code int
	out := captureOutput(t, func() {
		code = run([]string{"image", "build", "--repo", repoPath})
	})
	if code != 0 {
		t.Errorf("claudio image build --repo exited %d, want 0 (output: %s)", code, out)
	}
	if !strings.Contains(out, "Built claudio/base:latest") {
		t.Errorf("output = %q, want it to report building the base image too", out)
	}
	if !strings.Contains(out, "claudio/repo-") {
		t.Errorf("output = %q, want it to report building a claudio/repo-* tag", out)
	}

	// Extract the repo tag from the output and confirm Docker actually
	// has it, and that jq (declared in .claudio.yml) is present.
	var repoTag string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// Matches only cmdImageBuild's own "Built <tag>" summary line, not
		// the raw build log's "Successfully tagged ..." message that also
		// contains "claudio/repo-" (both go to the same captured stream —
		// see captureOutput's doc) — a looser substring match on the
		// build log line was tried first and picked up "Successfully
		// tagged claudio/repo-...:latest failed:" as if it were a tag.
		if tag, ok := strings.CutPrefix(line, "Built "); ok && strings.Contains(tag, "claudio/repo-") {
			repoTag = tag
		}
	}
	if repoTag == "" {
		t.Fatalf("could not find the repo image tag in output: %q", out)
	}
	t.Cleanup(func() { exec.Command("docker", "rmi", "-f", repoTag).Run() })

	if err := exec.Command("docker", "image", "inspect", repoTag).Run(); err != nil {
		t.Fatalf("docker image inspect %s failed: %v", repoTag, err)
	}
	if runOut, err := exec.Command("docker", "run", "--rm", "--entrypoint", "jq", repoTag, "--version").CombinedOutput(); err != nil {
		t.Errorf("jq not present in built repo image: %v: %s", err, runOut)
	}
}

// TestCreateMissingImageGivesActionableError is this issue's third
// empirical verification requirement through the real CLI: `claudio
// create` against a repo needing an image that doesn't exist yet must
// print a clear, actionable error naming `claudio image build --repo`,
// not Docker's raw "no such image" text — checked against the exact
// stderr text a user would see.
func TestCreateMissingImageGivesActionableError(t *testing.T) {
	ctx := context.Background()
	if _, err := engine.DetectRuntime(ctx, ""); err != nil {
		t.Skipf("no reachable Docker-API-compatible daemon: %v", err)
	}
	t.Setenv("CLAUDIO_HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "test-token")

	repoURL := newLocalOriginRepoForCLIWithClaudioYML(t, "image:\n  apt: [jq]\n")

	out := captureOutput(t, func() {
		if code := run([]string{"create", repoURL}); code == 0 {
			t.Error("claudio create exited 0, want nonzero — the repo-specific image was never built")
		}
	})
	if !strings.Contains(out, "claudio image build --repo") {
		t.Errorf("output = %q, want it to mention `claudio image build --repo`", out)
	}
	if strings.Contains(out, "No such image") {
		t.Errorf("output = %q, still leaks Docker's own raw error text", out)
	}
}

// TestRepoIdentityFallsBackWhenRemoteURLIsUnparseable is a regression
// test for a real bug found while smoke-testing this feature by hand:
// `git remote add origin ../origin.git` (a relative path — git accepts
// it without complaint, and it's exactly what a local test/clone setup
// produces, including this project's own newLocalOriginRepoForCLI-style
// helpers in earlier form) makes `git remote get-url origin` return that
// same relative string verbatim. repo.Slug's parser has no rule for a
// bare relative path (no "://" prefix, no ":" for the scp-like form), so
// trusting the remote unconditionally made `claudio image build --repo`
// fail outright with "cannot parse URL" instead of falling back to the
// file:// form it already has a rule for.
func TestRepoIdentityFallsBackWhenRemoteURLIsUnparseable(t *testing.T) {
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	seed := filepath.Join(dir, "seed")

	runGitCLI(t, "", "init", "--bare", origin)
	runGitCLI(t, "", "init", seed)
	runGitCLI(t, seed, "config", "user.email", "t@e.com")
	runGitCLI(t, seed, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, seed, "add", "README.md")
	runGitCLI(t, seed, "commit", "-m", "initial")
	runGitCLI(t, seed, "branch", "-M", "main")
	// The bug-triggering step: a relative remote URL, not an absolute
	// path or a real scheme.
	runGitCLI(t, seed, "remote", "add", "origin", "../origin.git")

	got := repoIdentity(seed)
	want := "file://" + seed
	if got != want {
		t.Errorf("repoIdentity(%q) = %q, want fallback %q (relative remote URL is unparseable by repo.Slug)", seed, got, want)
	}
}

func TestRepoIdentityUsesParseableRemote(t *testing.T) {
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	seed := filepath.Join(dir, "seed")

	runGitCLI(t, "", "init", "--bare", origin)
	runGitCLI(t, "", "init", seed)
	runGitCLI(t, seed, "config", "user.email", "t@e.com")
	runGitCLI(t, seed, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, seed, "add", "README.md")
	runGitCLI(t, seed, "commit", "-m", "initial")
	runGitCLI(t, seed, "branch", "-M", "main")
	// An absolute file:// URL IS parseable by repo.Slug, so it must be
	// used verbatim rather than falling back — this is the case that
	// makes `claudio image build --repo <clone>` and a later `claudio
	// create <same-url>` land on the same tag, repoIdentity's whole point.
	absoluteURL := "file://" + origin
	runGitCLI(t, seed, "remote", "add", "origin", absoluteURL)

	got := repoIdentity(seed)
	if got != absoluteURL {
		t.Errorf("repoIdentity(%q) = %q, want the parseable remote %q used verbatim", seed, got, absoluteURL)
	}
}

func newLocalOriginRepoForCLIWithClaudioYML(t *testing.T, claudioYML string) string {
	t.Helper()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	seed := filepath.Join(dir, "seed")

	runGitCLI(t, "", "init", "--bare", origin)
	runGitCLI(t, "", "init", seed)
	runGitCLI(t, seed, "config", "user.email", "test@example.com")
	runGitCLI(t, seed, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, ".claudio.yml"), []byte(claudioYML), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCLI(t, seed, "add", "README.md", ".claudio.yml")
	runGitCLI(t, seed, "commit", "-m", "initial")
	runGitCLI(t, seed, "branch", "-M", "main")
	runGitCLI(t, seed, "remote", "add", "origin", origin)
	runGitCLI(t, seed, "push", "origin", "main")
	return "file://" + origin
}
