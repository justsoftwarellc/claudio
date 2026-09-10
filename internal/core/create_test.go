// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/store"
)

// newLocalOriginRepo creates a bare repo on disk to act as a clone
// source, mirroring internal/repo's own test helper (unexported there,
// so duplicated here rather than exported purely for tests) — tests
// never touch the network, matching this project's empirical-but-hermetic
// testing convention (docs/architecture.md's verification discipline).
func newLocalOriginRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	seed := filepath.Join(dir, "seed")

	runGit(t, "", "init", "--bare", origin)
	runGit(t, "", "init", seed)
	runGit(t, seed, "config", "user.email", "test@example.com")
	runGit(t, seed, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "README.md")
	runGit(t, seed, "commit", "-m", "initial")
	runGit(t, seed, "branch", "-M", "main")
	runGit(t, seed, "remote", "add", "origin", origin)
	runGit(t, seed, "push", "origin", "main")
	return "file://" + origin
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func baseCreateParams(t *testing.T, repoURL string) CreateParams {
	t.Helper()
	return CreateParams{
		RepoURL:       repoURL,
		WorkspaceRoot: t.TempDir(),
		Image:         "alpine", // no real claudio/base needed: Cmd below keeps it alive
		Env:           map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "test-token"},
		PortRangeLow:  43000,
		PortRangeHigh: 43999,
	}
}

func TestCreateInstanceGeneratedBranchDefault(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", result.ContainerID) })

	if result.Branch != "claudio/"+result.InstanceID {
		t.Errorf("Branch = %q, want claudio/%s (the generated default)", result.Branch, result.InstanceID)
	}

	inst, err := s.GetInstance(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.ProvisionStep != store.StepHealthy {
		t.Errorf("ProvisionStep = %s, want %s", inst.ProvisionStep, store.StepHealthy)
	}
	if inst.DesiredState != store.StateRunning {
		t.Errorf("DesiredState = %s, want %s", inst.DesiredState, store.StateRunning)
	}
	if inst.ContainerID == nil || *inst.ContainerID != result.ContainerID {
		t.Errorf("ContainerID = %v, want %s", inst.ContainerID, result.ContainerID)
	}
}

func TestCreateInstanceNewBranch(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	params := baseCreateParams(t, repoURL)
	params.NewBranch = "feat/thing"
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", result.ContainerID) })

	if result.Branch != "feat/thing" {
		t.Errorf("Branch = %q, want feat/thing", result.Branch)
	}
}

func TestCreateInstanceExplicitBranchMustExist(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	params := baseCreateParams(t, repoURL)
	params.Branch = "does-not-exist"
	_, err := createForTest(t, s, params)
	if err == nil {
		t.Fatal("expected error checking out a branch that does not exist")
	}
}

func TestCreateInstanceNewBranchCollisionErrors(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	params := baseCreateParams(t, repoURL)
	params.NewBranch = "main" // already exists from the seed commit
	_, err := createForTest(t, s, params)
	if err == nil {
		t.Fatal("expected error creating a branch that already exists")
	}
}

func TestCreateInstanceTwiceReusesRepoRoot(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)
	workspaceRoot := t.TempDir()

	params1 := baseCreateParams(t, repoURL)
	params1.WorkspaceRoot = workspaceRoot
	result1, err := createForTest(t, s, params1)
	if err != nil {
		t.Fatalf("CreateInstance (1st): %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", result1.ContainerID) })

	params2 := baseCreateParams(t, repoURL)
	params2.WorkspaceRoot = workspaceRoot
	result2, err := createForTest(t, s, params2)
	if err != nil {
		t.Fatalf("CreateInstance (2nd): %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", result2.ContainerID) })

	inst1, _ := s.GetInstance(context.Background(), result1.InstanceID)
	inst2, _ := s.GetInstance(context.Background(), result2.InstanceID)
	if inst1.RepoRoot != inst2.RepoRoot {
		t.Errorf("RepoRoot differs across two creates against the same repo: %q vs %q, want the same root (one clone per repo)", inst1.RepoRoot, inst2.RepoRoot)
	}
	if result1.InstanceID == result2.InstanceID {
		t.Fatal("expected two distinct generated instance IDs")
	}
}

// createForTest calls CreateInstance with the alpine "sleep 60" long-
// running command engine.create_test.go's own tests use, so a real,
// removable container gets created without needing the real claudio/base
// image built (that requires image/Dockerfile, out of scope for core's
// own tests — see internal/engine/create_test.go's package comment).
func createForTest(t *testing.T, s *store.Store, params CreateParams) (CreateResult, error) {
	t.Helper()
	return createInstanceWithCmd(context.Background(), s, params, []string{"sleep", "60"}, nil)
}
