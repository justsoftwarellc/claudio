package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
)

// newLocalOriginRepoWithSeed is newLocalOriginRepo, but hands back the
// seed working copy too so a test can push further commits to the shared
// origin — simulating the upstream moving on after the first create.
func newLocalOriginRepoWithSeed(t *testing.T) (repoURL, seed string) {
	t.Helper()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	seed = filepath.Join(dir, "seed")

	runGit(t, "", "init", "--bare", origin)
	runGit(t, "", "init", seed)
	runGit(t, seed, "config", "user.email", "test@example.com")
	runGit(t, seed, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(seed, "VERSION"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "VERSION")
	runGit(t, seed, "commit", "-m", "initial")
	runGit(t, seed, "branch", "-M", "main")
	runGit(t, seed, "remote", "add", "origin", origin)
	runGit(t, seed, "push", "origin", "main")
	return "file://" + origin, seed
}

// TestSecondCreateSeesNewUpstreamCommits is the end-to-end form of the
// staleness gap: main-clone is cloned on the first create and reused by
// every later one, so without a refresh the second instance branches
// from first-clone state and never sees anything committed upstream in
// between. Nothing about the resulting worktree looks wrong, which is
// what made this worth fixing rather than documenting.
func TestSecondCreateSeesNewUpstreamCommits(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL, seed := newLocalOriginRepoWithSeed(t)
	workspaceRoot := t.TempDir()

	params1 := baseCreateParams(t, repoURL)
	params1.WorkspaceRoot = workspaceRoot
	result1, err := createForTest(t, s, params1)
	if err != nil {
		t.Fatalf("CreateInstance (1st): %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", result1.ContainerID) })

	// Upstream moves on between the two creates.
	if err := os.WriteFile(filepath.Join(seed, "VERSION"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "VERSION")
	runGit(t, seed, "commit", "-m", "second")
	runGit(t, seed, "push", "origin", "main")

	params2 := baseCreateParams(t, repoURL)
	params2.WorkspaceRoot = workspaceRoot
	result2, err := createForTest(t, s, params2)
	if err != nil {
		t.Fatalf("CreateInstance (2nd): %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", result2.ContainerID) })

	got, err := os.ReadFile(filepath.Join(result2.WorktreeDir, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2\n" {
		t.Errorf("second instance's VERSION = %q, want %q — it branched from stale main-clone state", got, "v2\n")
	}
}

// --no-refresh (SkipRefresh) must genuinely skip the fetch, so a user
// who wants the existing clone — offline, or deliberately pinned — gets
// exactly the old behavior.
func TestSkipRefreshLeavesMainCloneStale(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL, seed := newLocalOriginRepoWithSeed(t)
	workspaceRoot := t.TempDir()

	params1 := baseCreateParams(t, repoURL)
	params1.WorkspaceRoot = workspaceRoot
	result1, err := createForTest(t, s, params1)
	if err != nil {
		t.Fatalf("CreateInstance (1st): %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", result1.ContainerID) })

	if err := os.WriteFile(filepath.Join(seed, "VERSION"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "VERSION")
	runGit(t, seed, "commit", "-m", "second")
	runGit(t, seed, "push", "origin", "main")

	params2 := baseCreateParams(t, repoURL)
	params2.WorkspaceRoot = workspaceRoot
	params2.SkipRefresh = true
	result2, err := createForTest(t, s, params2)
	if err != nil {
		t.Fatalf("CreateInstance (2nd): %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", result2.ContainerID) })

	got, err := os.ReadFile(filepath.Join(result2.WorktreeDir, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v1\n" {
		t.Errorf("with SkipRefresh, VERSION = %q, want the stale %q", got, "v1\n")
	}
}

// A greenfield root has no upstream at all; the refresh must not try to
// fetch from its synthetic "local:<name>" URL, which is not clonable.
func TestGreenfieldCreateSkipsRefresh(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)

	params := baseCreateParams(t, "")
	params.WorkspaceRoot = t.TempDir()
	params.GreenfieldName = "prototype"
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance --new: %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", result.ContainerID) })
}
