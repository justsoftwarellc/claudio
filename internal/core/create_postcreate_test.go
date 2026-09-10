// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCreateInstanceRunsPostCreate(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	params := baseCreateParams(t, repoURL)
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	// .claudio.yml wasn't present at create time (it can't be — the
	// worktree didn't exist until AddWorktree ran inside CreateInstance
	// itself), so this test writes the file after the fact and calls
	// runPostCreate directly against the real container CreateInstance
	// already produced, rather than trying to seed .claudio.yml into the
	// origin repo before cloning.
	claudioYML := filepath.Join(result.WorktreeDir, ".claudio.yml")
	if err := os.WriteFile(claudioYML, []byte("post_create:\n  - echo hello > post-create-marker.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inst, err := s.GetInstance(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if err := runPostCreate(t.Context(), "", result.ContainerID, inst.RepoRoot, result.WorktreeDir); err != nil {
		t.Fatalf("runPostCreate: %v", err)
	}

	marker := filepath.Join(result.WorktreeDir, "post-create-marker.txt")
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read marker file (post_create should have created it): %v", err)
	}
	if string(data) != "hello\n" {
		t.Errorf("marker content = %q, want %q", data, "hello\n")
	}
}

func TestCreateInstanceNoPostCreateIsNoop(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	// No .claudio.yml at all — runPostCreate must succeed as a no-op,
	// not error, since most repos declare no post_create.
	params := baseCreateParams(t, repoURL)
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })
	// CreateInstance itself already called runPostCreate internally with
	// no .claudio.yml present — reaching this point without error is the
	// assertion.
}

// TestStartInstanceDoesNotRerunPostCreate verifies the "run once" gate:
// post_create must not re-execute on a plain restart, even though
// StartInstance/RestartInstance share provisionContainer with
// CreateInstance — see provisionContainer's runPostCreateHook parameter.
func TestStartInstanceDoesNotRerunPostCreate(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)
	workspaceRoot := t.TempDir()

	createParams := baseCreateParams(t, repoURL)
	createParams.WorkspaceRoot = workspaceRoot
	result, err := createForTest(t, s, createParams)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// A post_create that appends, so a second run would be detectable —
	// written after create (the worktree didn't exist before it), same
	// as the other tests in this file.
	claudioYML := filepath.Join(result.WorktreeDir, ".claudio.yml")
	if err := os.WriteFile(claudioYML, []byte("post_create:\n  - echo run >> post-create-log.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Run it once manually to simulate what create would have done had
	// .claudio.yml existed at create time (it didn't, so create's own
	// internal call was a no-op) — this establishes the "already ran
	// once" baseline this test checks doesn't grow.
	inst, err := s.GetInstance(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if err := runPostCreate(t.Context(), "", result.ContainerID, inst.RepoRoot, result.WorktreeDir); err != nil {
		t.Fatalf("runPostCreate (baseline): %v", err)
	}

	if err := StopInstance(t.Context(), s, "", result.InstanceID); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	startParams := baseCreateParams(t, repoURL)
	startParams.WorkspaceRoot = workspaceRoot
	startResult, err := startInstanceForTest(t, s, startParams, result.InstanceID, false)
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", startResult.ContainerID).Run() })

	log, err := os.ReadFile(filepath.Join(result.WorktreeDir, "post-create-log.txt"))
	if err != nil {
		t.Fatalf("read post-create-log.txt: %v", err)
	}
	lines := 0
	for _, b := range log {
		if b == '\n' {
			lines++
		}
	}
	if lines != 1 {
		t.Errorf("post-create-log.txt has %d line(s) after start, want exactly 1 (post_create must not re-run on start)", lines)
	}
}

func TestCreateInstancePostCreateFailureFailsCreate(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	// Same trick as TestCreateInstanceRunsPostCreate: seed .claudio.yml
	// after the worktree exists, then call runPostCreate directly to
	// verify a nonzero exit is surfaced as an error (the path
	// CreateInstance's own call site treats as a provisioning failure).
	params := baseCreateParams(t, repoURL)
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	claudioYML := filepath.Join(result.WorktreeDir, ".claudio.yml")
	if err := os.WriteFile(claudioYML, []byte("post_create:\n  - exit 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inst, err := s.GetInstance(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	err = runPostCreate(t.Context(), "", result.ContainerID, inst.RepoRoot, result.WorktreeDir)
	if err == nil {
		t.Fatal("expected an error from a post_create command that exits nonzero")
	}
}
