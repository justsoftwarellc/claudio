// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/engine"
)

// The workflow --memory exists for: a repo needs more memory than this
// machine's default, the user says so at create time, and the instance
// keeps that limit for its whole life. Before create recorded the
// override in .claudio.yml this failed at the first restart — `restart`
// takes no resource flags and re-derives everything from that file, so
// the 12g instance silently became a 6g one with nothing to show an
// override had ever applied (ROD-137).
//
// Asserted against the container's real HostConfig.Memory rather than
// against resolveResources' return value: the mechanism agreeing with
// itself is what already passed while the workflow was broken.
func TestResourceOverrideSurvivesRestart(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	ctx := context.Background()
	repoURL := newLocalOriginRepo(t)

	params := baseCreateParams(t, repoURL)
	twelveGig := "12g"
	params.ResourceOverride = &config.Resources{Memory: &twelveGig}

	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", result.ContainerID) })

	const want = int64(12) << 30
	got, err := engine.InspectMemoryLimit(ctx, params.DockerHost, result.ContainerID)
	if err != nil {
		t.Fatalf("InspectMemoryLimit after create: %v", err)
	}
	if got != want {
		t.Fatalf("memory limit after create = %d, want %d (--memory 12g)", got, want)
	}

	// Restart the way the CLI does: no resource flags, since `claudio
	// restart` has none to pass.
	restartParams := baseCreateParams(t, repoURL)
	restartParams.WorkspaceRoot = params.WorkspaceRoot
	restarted, err := RestartInstance(ctx, s, params.DockerHost, restartParams, result.InstanceID, false, nil)
	if err != nil {
		t.Fatalf("RestartInstance: %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", restarted.ContainerID) })

	got, err = engine.InspectMemoryLimit(ctx, params.DockerHost, restarted.ContainerID)
	if err != nil {
		t.Fatalf("InspectMemoryLimit after restart: %v", err)
	}
	if got != want {
		t.Errorf("memory limit after restart = %d, want %d — the override vanished (ROD-137)", got, want)
	}
}

// The override is recorded in the file the *store* says is this repo's
// config, which for a `claudio create .` is the user's own folder and
// not the worktree (ROD-133). Writing it anywhere else would leave
// restart re-deriving from a file that never mentions the override.
func TestResourceOverrideRecordedInResolvedConfigPath(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	ctx := context.Background()
	repoURL := newLocalOriginRepo(t)

	params := baseCreateParams(t, repoURL)
	eightCPUs := 8
	params.ResourceOverride = &config.Resources{CPUs: &eightCPUs}

	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", result.ContainerID) })

	inst, err := s.GetInstance(ctx, result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	path := resolveConfigPath(ctx, s, inst.RepoRoot, inst.WorktreeDir)
	if path == "" {
		t.Fatal("resolveConfigPath returned empty; nowhere for the override to live")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file %s not written: %v", path, err)
	}

	cfg, err := config.LoadRepoConfig(path)
	if err != nil {
		t.Fatalf("LoadRepoConfig(%s): %v", path, err)
	}
	if cfg.Resources.CPUs == nil || *cfg.Resources.CPUs != 8 {
		t.Errorf("CPUs in %s = %v, want 8", path, cfg.Resources.CPUs)
	}
}

// A create with no resource flags must leave the repo's own committed
// numbers exactly as they are — the override layer writing a resolved
// value would freeze today's global default into the user's file.
func TestNoResourceOverrideLeavesRepoConfigUntouched(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepoWithClaudioYML(t, "resources:\n  memory: 2g\n")

	params := baseCreateParams(t, repoURL)
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { engine.RemoveContainer(context.Background(), "", result.ContainerID) })

	inst, err := s.GetInstance(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	cfg, err := config.LoadRepoConfig(filepath.Join(inst.WorktreeDir, config.FileName))
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if cfg.Resources.Memory == nil || *cfg.Resources.Memory != "2g" {
		t.Errorf("Memory = %v, want the repo's own 2g unchanged", cfg.Resources.Memory)
	}
	if cfg.Resources.CPUs != nil || cfg.Resources.PIDs != nil {
		t.Errorf("cpus/pids written into a repo that never asked for them: %+v", cfg.Resources)
	}
}
