// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/rodrigomorales/claudio/internal/store"
)

// startInstanceForTest and restartInstanceForTest mirror createForTest:
// they use the alpine "sleep 60" long-running command so tests don't
// need the real claudio/base image built (see createForTest's doc).
func startInstanceForTest(t *testing.T, s *store.Store, params CreateParams, idOrName string, fresh bool) (CreateResult, error) {
	t.Helper()
	return startInstanceWithCmd(context.Background(), s, params, idOrName, fresh, []string{"sleep", "60"}, nil)
}

func restartInstanceForTest(t *testing.T, s *store.Store, params CreateParams, idOrName string, fresh bool) (CreateResult, error) {
	t.Helper()
	if err := StopInstance(context.Background(), s, "", idOrName); err != nil {
		return CreateResult{}, err
	}
	return startInstanceWithCmd(context.Background(), s, params, idOrName, fresh, []string{"sleep", "60"}, nil)
}

func TestStopInstanceRemovesContainerReleasesPortsKeepsWorktree(t *testing.T) {
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

	if err := StopInstance(context.Background(), s, "", result.InstanceID); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	inst, err := s.GetInstance(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.DesiredState != store.StateStopped {
		t.Errorf("DesiredState = %s, want %s", inst.DesiredState, store.StateStopped)
	}

	mappings, err := s.PortMappings(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("PortMappings: %v", err)
	}
	if len(mappings) != 0 {
		t.Errorf("PortMappings = %+v, want none after stop (ports released)", mappings)
	}

	if _, err := os.Stat(result.WorktreeDir); err != nil {
		t.Errorf("worktree %s should survive stop: %v", result.WorktreeDir, err)
	}

	out, err := exec.Command("docker", "inspect", result.ContainerID).CombinedOutput()
	if err == nil {
		t.Errorf("container %s should be removed after stop, but docker inspect succeeded: %s", result.ContainerID, out)
	}
}

func TestStartInstanceRecreatesContainer(t *testing.T) {
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

	if err := StopInstance(context.Background(), s, "", result.InstanceID); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	startParams := baseCreateParams(t, repoURL)
	startParams.WorkspaceRoot = workspaceRoot
	startResult, err := startInstanceForTest(t, s, startParams, result.InstanceID, false)
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", startResult.ContainerID).Run() })

	if startResult.InstanceID != result.InstanceID {
		t.Errorf("StartInstance produced a different instance ID: %q vs %q", startResult.InstanceID, result.InstanceID)
	}
	if startResult.ContainerID == result.ContainerID {
		t.Error("StartInstance should create a new container, not reuse the removed one")
	}

	inst, err := s.GetInstance(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.DesiredState != store.StateRunning {
		t.Errorf("DesiredState = %s, want %s", inst.DesiredState, store.StateRunning)
	}
	if inst.ProvisionStep != store.StepHealthy {
		t.Errorf("ProvisionStep = %s, want %s", inst.ProvisionStep, store.StepHealthy)
	}
	if inst.ContainerID == nil || *inst.ContainerID != startResult.ContainerID {
		t.Errorf("ContainerID = %v, want %s", inst.ContainerID, startResult.ContainerID)
	}
}

func TestStartInstanceRequiresStopped(t *testing.T) {
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
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	_, err = startInstanceForTest(t, s, params, result.InstanceID, false)
	if err == nil {
		t.Fatal("expected error starting an instance that is already running")
	}
}

func TestStartInstanceFreshWipesHomeDir(t *testing.T) {
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
	homeDir := result.WorktreeDir + ".home"
	marker := homeDir + "/marker.txt"
	if err := os.WriteFile(marker, []byte("session state\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	if err := StopInstance(context.Background(), s, "", result.InstanceID); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	startParams := baseCreateParams(t, repoURL)
	startParams.WorkspaceRoot = workspaceRoot
	startResult, err := startInstanceForTest(t, s, startParams, result.InstanceID, true)
	if err != nil {
		t.Fatalf("StartInstance (fresh): %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", startResult.ContainerID).Run() })

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("marker file should be gone after --fresh, stat err = %v", err)
	}
}

func TestRestartInstanceStopsThenStarts(t *testing.T) {
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

	restartParams := baseCreateParams(t, repoURL)
	restartParams.WorkspaceRoot = workspaceRoot
	restartResult, err := restartInstanceForTest(t, s, restartParams, result.InstanceID, false)
	if err != nil {
		t.Fatalf("RestartInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", restartResult.ContainerID).Run() })

	if restartResult.ContainerID == result.ContainerID {
		t.Error("RestartInstance should produce a new container ID")
	}

	inst, err := s.GetInstance(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.DesiredState != store.StateRunning {
		t.Errorf("DesiredState = %s, want %s", inst.DesiredState, store.StateRunning)
	}
}
