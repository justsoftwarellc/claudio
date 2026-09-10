package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rodrigomorales/claudio/internal/repo"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
}

func seedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "user.email", "t@example.com")
	gitIn(t, dir, "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "c1")
	return dir
}

// A remote source is never prompted about: the user named the repo, so
// its default branch is what they meant. "" tells core to use
// main-clone's own branch.
func TestResolveBaseBranchRemoteDoesNotPrompt(t *testing.T) {
	got, ok := resolveBaseBranch(context.Background(), "git@github.com:acme/web.git", false)
	if !ok {
		t.Fatal("resolveBaseBranch returned not-ok for a remote source")
	}
	if got != "" {
		t.Errorf("branch = %q, want \"\" (defer to main-clone's branch)", got)
	}
}

// A local directory with no upstream has nothing to fetch from, so it
// must not prompt either — the prototype case.
func TestResolveBaseBranchLocalOnlyDoesNotPrompt(t *testing.T) {
	dir := seedRepo(t)
	got, ok := resolveBaseBranch(context.Background(), "file://"+dir, false)
	if !ok {
		t.Fatal("resolveBaseBranch returned not-ok for a local-only source")
	}
	if got != "" {
		t.Errorf("branch = %q, want \"\"", got)
	}
}

// With --yes (or a non-terminal stdin) the prompt must resolve to a
// sensible default rather than blocking a script forever.
func TestResolveBaseBranchAssumeYesTakesDefault(t *testing.T) {
	up := seedRepo(t)
	work := t.TempDir()
	gitIn(t, "", "clone", "-q", up, work)

	got, ok := resolveBaseBranch(context.Background(), "file://"+work, true)
	if !ok {
		t.Fatal("resolveBaseBranch returned not-ok under --yes")
	}
	if got != "main" {
		t.Errorf("branch = %q, want %q", got, "main")
	}
}

// The offered default is the branch the user is standing on, when the
// upstream also has it — they are most likely to mean the branch in
// front of them.
func TestResolveBaseBranchPrefersCheckedOutBranch(t *testing.T) {
	up := seedRepo(t)
	gitIn(t, up, "branch", "feat/auth")
	work := t.TempDir()
	gitIn(t, "", "clone", "-q", up, work)
	gitIn(t, work, "checkout", "-q", "feat/auth")

	got, ok := resolveBaseBranch(context.Background(), "file://"+work, true)
	if !ok {
		t.Fatal("resolveBaseBranch returned not-ok")
	}
	if got != "feat/auth" {
		t.Errorf("branch = %q, want the checked-out %q", got, "feat/auth")
	}
}

// An unreachable upstream must degrade to "create from the existing
// clone" rather than failing — being offline should not block a create.
func TestResolveBaseBranchUnreachableUpstreamDegrades(t *testing.T) {
	work := t.TempDir()
	gitIn(t, work, "init", "-q", "-b", "main")
	gitIn(t, work, "remote", "add", "origin", filepath.Join(t.TempDir(), "does-not-exist.git"))

	got, ok := resolveBaseBranch(context.Background(), "file://"+work, true)
	if !ok {
		t.Fatal("an unreachable upstream must not fail the create")
	}
	if got != "" {
		t.Errorf("branch = %q, want \"\" (no refresh)", got)
	}
}

func TestDefaultAmongFallsBackToMain(t *testing.T) {
	if got := defaultAmong([]string{"dev", "main"}, ""); got != "main" {
		t.Errorf("defaultAmong = %q, want main", got)
	}
	if got := defaultAmong([]string{"dev", "master"}, ""); got != "master" {
		t.Errorf("defaultAmong = %q, want master", got)
	}
	if got := defaultAmong([]string{"trunk"}, ""); got != "trunk" {
		t.Errorf("defaultAmong = %q, want the only branch", got)
	}
	// A checked-out branch the upstream does not have must not be offered.
	if got := defaultAmong([]string{"main"}, "local-only-branch"); got != "main" {
		t.Errorf("defaultAmong = %q, want main", got)
	}
}

// Guards the classification the end-to-end create test caught: a bare
// file:// repo must be treated as a remote, not as a working copy.
func TestBareFileRepoNeedsNoPrompt(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "--bare")
	if got := repo.ClassifySource(context.Background(), "file://"+dir).Kind; got != repo.SourceRemote {
		t.Errorf("Kind = %v, want SourceRemote", got)
	}
}
