// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestDestroyInstanceRemovesContainerWorktreeAndRow(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)
	workspaceRoot := t.TempDir()

	params := baseCreateParams(t, repoURL)
	params.WorkspaceRoot = workspaceRoot
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := os.Stat(result.WorktreeDir); err != nil {
		t.Fatalf("worktree should exist after create: %v", err)
	}

	err = DestroyInstance(context.Background(), s, DestroyParams{
		IDOrName: result.InstanceID,
	})
	if err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}

	if _, err := os.Stat(result.WorktreeDir); !os.IsNotExist(err) {
		t.Errorf("worktree dir %s should be gone after destroy, stat err = %v", result.WorktreeDir, err)
	}

	if _, err := s.GetInstance(context.Background(), result.InstanceID); err == nil {
		t.Error("expected GetInstance to fail after destroy (row should be deleted)")
	}
}

func TestDestroyInstanceKeepWorkspace(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)
	workspaceRoot := t.TempDir()

	params := baseCreateParams(t, repoURL)
	params.WorkspaceRoot = workspaceRoot
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	err = DestroyInstance(context.Background(), s, DestroyParams{
		IDOrName:      result.InstanceID,
		KeepWorkspace: true,
	})
	if err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}

	if _, err := os.Stat(result.WorktreeDir); err != nil {
		t.Errorf("worktree dir %s should survive --keep-workspace, stat err = %v", result.WorktreeDir, err)
	}
}

func TestDestroyInstanceUnknownIDErrors(t *testing.T) {
	s := openTestStore(t)

	err := DestroyInstance(context.Background(), s, DestroyParams{IDOrName: "does-not-exist"})
	if err == nil {
		t.Fatal("expected error destroying a nonexistent instance")
	}
}

func TestDestroyInstanceIsRetryableAfterContainerAlreadyGone(t *testing.T) {
	// Simulates the container having already been removed out of band
	// (e.g. `docker rm` by hand, or a prior destroy attempt that failed
	// after removing the container but before deleting the row) before
	// `claudio destroy` runs again: the store row still names a
	// container_id that no longer exists. engine.RemoveContainer must
	// treat "already gone" as success, not fail destroy outright.
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)
	workspaceRoot := t.TempDir()

	params := baseCreateParams(t, repoURL)
	params.WorkspaceRoot = workspaceRoot
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if out, err := exec.Command("docker", "rm", "-f", result.ContainerID).CombinedOutput(); err != nil {
		t.Fatalf("docker rm -f (simulating out-of-band removal): %v: %s", err, out)
	}

	err = DestroyInstance(context.Background(), s, DestroyParams{
		IDOrName: result.InstanceID,
	})
	if err != nil {
		t.Fatalf("DestroyInstance after out-of-band removal: %v", err)
	}
}
