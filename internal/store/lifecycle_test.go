// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package store

import (
	"context"
	"errors"
	"testing"
)

func createTestInstance(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.CreateInstance(context.Background(), NewInstanceParams{
		ID:             id,
		RepoURL:        "git@github.com:acme/web.git",
		RepoRoot:       "/repos/acme",
		WorktreeDir:    "/repos/acme/worktrees/" + id,
		Branch:         "main",
		Image:          "claudio/base",
		RuntimeProfile: "orbstack",
		CreatedAt:      0,
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
}

func TestCreateInstanceStartsAtPendingRunning(t *testing.T) {
	s := openTest(t)
	createTestInstance(t, s, "inst-1")

	inst, err := s.GetInstance(context.Background(), "inst-1")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.DesiredState != StateRunning {
		t.Errorf("DesiredState = %s, want %s", inst.DesiredState, StateRunning)
	}
	if inst.ProvisionStep != StepPending {
		t.Errorf("ProvisionStep = %s, want %s", inst.ProvisionStep, StepPending)
	}
}

func TestCreateInstanceDuplicateIDErrors(t *testing.T) {
	s := openTest(t)
	createTestInstance(t, s, "inst-1")

	err := s.CreateInstance(context.Background(), NewInstanceParams{
		ID: "inst-1", RepoURL: "x", RepoRoot: "x", WorktreeDir: "x", Branch: "x",
		Image: "x", RuntimeProfile: "x",
	})
	if err == nil {
		t.Fatal("expected error creating duplicate instance id")
	}
}

func TestProvisionStepHappyPath(t *testing.T) {
	s := openTest(t)
	createTestInstance(t, s, "inst-1")
	ctx := context.Background()

	steps := []ProvisionStep{StepRepoReady, StepPortsReady, StepConfigReady, StepContainerUp, StepHealthy}
	for _, step := range steps {
		if err := s.TransitionProvisionStep(ctx, "inst-1", step); err != nil {
			t.Fatalf("transition to %s: %v", step, err)
		}
	}

	inst, err := s.GetInstance(ctx, "inst-1")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.ProvisionStep != StepHealthy {
		t.Errorf("ProvisionStep = %s, want %s", inst.ProvisionStep, StepHealthy)
	}
}

func TestProvisionStepRejectsSkippingAhead(t *testing.T) {
	s := openTest(t)
	createTestInstance(t, s, "inst-1")
	ctx := context.Background()

	err := s.TransitionProvisionStep(ctx, "inst-1", StepContainerUp)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition skipping ahead, got %v", err)
	}
}

func TestProvisionStepRejectsBackwardMove(t *testing.T) {
	s := openTest(t)
	createTestInstance(t, s, "inst-1")
	ctx := context.Background()

	if err := s.TransitionProvisionStep(ctx, "inst-1", StepRepoReady); err != nil {
		t.Fatalf("advance to repo_ready: %v", err)
	}
	if err := s.TransitionProvisionStep(ctx, "inst-1", StepPortsReady); err != nil {
		t.Fatalf("advance to ports_ready: %v", err)
	}

	err := s.TransitionProvisionStep(ctx, "inst-1", StepRepoReady)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition moving backward, got %v", err)
	}
}

func TestProvisionStepReenteringSameStepIsIdempotent(t *testing.T) {
	s := openTest(t)
	createTestInstance(t, s, "inst-1")
	ctx := context.Background()

	if err := s.TransitionProvisionStep(ctx, "inst-1", StepRepoReady); err != nil {
		t.Fatalf("first transition: %v", err)
	}
	// A crashed process resuming re-enters the same step; must not error.
	if err := s.TransitionProvisionStep(ctx, "inst-1", StepRepoReady); err != nil {
		t.Fatalf("re-entering same step: %v", err)
	}
}

func TestProvisionStepCanFailFromAnyStep(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	for _, from := range []ProvisionStep{StepPending, StepRepoReady, StepPortsReady, StepConfigReady, StepContainerUp} {
		id := "inst-" + string(from)
		createTestInstance(t, s, id)
		// Walk to `from` by re-creating and stepping forward each time.
		for _, step := range []ProvisionStep{StepRepoReady, StepPortsReady, StepConfigReady, StepContainerUp} {
			if step == from {
				break
			}
			if err := s.TransitionProvisionStep(ctx, id, step); err != nil {
				t.Fatalf("walking to %s via %s: %v", from, step, err)
			}
		}
		if err := s.TransitionProvisionStep(ctx, id, StepFailed); err != nil {
			t.Errorf("transition to failed from %s: %v", from, err)
		}
	}
}

func TestMarkFailedThenRetryFromPending(t *testing.T) {
	s := openTest(t)
	createTestInstance(t, s, "inst-1")
	ctx := context.Background()

	if err := s.MarkFailed(ctx, "inst-1"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if err := s.TransitionProvisionStep(ctx, "inst-1", StepPending); err != nil {
		t.Fatalf("retry from pending after failure: %v", err)
	}
}

func TestDesiredStateRunningStoppedCycle(t *testing.T) {
	s := openTest(t)
	createTestInstance(t, s, "inst-1")
	ctx := context.Background()

	if _, err := s.AllocatePort(ctx, "inst-1", 3000, "web", PortDetected, nil, 43000, 43010); err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}

	if err := s.TransitionDesiredState(ctx, "inst-1", StateStopped); err != nil {
		t.Fatalf("transition to stopped: %v", err)
	}
	mappings, err := s.PortMappings(ctx, "inst-1")
	if err != nil {
		t.Fatalf("PortMappings: %v", err)
	}
	if len(mappings) != 0 {
		t.Errorf("expected ports released on stop, got %+v", mappings)
	}

	if err := s.TransitionDesiredState(ctx, "inst-1", StateRunning); err != nil {
		t.Fatalf("transition back to running: %v", err)
	}
}

func TestDesiredStateDestroyedIsTerminal(t *testing.T) {
	s := openTest(t)
	createTestInstance(t, s, "inst-1")
	ctx := context.Background()

	if err := s.TransitionDesiredState(ctx, "inst-1", StateDestroyed); err != nil {
		t.Fatalf("transition to destroyed: %v", err)
	}
	err := s.TransitionDesiredState(ctx, "inst-1", StateRunning)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition reviving a destroyed instance, got %v", err)
	}
}

func TestDeleteInstanceRequiresDestroyedState(t *testing.T) {
	s := openTest(t)
	createTestInstance(t, s, "inst-1")
	ctx := context.Background()

	err := s.DeleteInstance(ctx, "inst-1")
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected ErrInvalidTransition deleting a non-destroyed instance, got %v", err)
	}

	if err := s.TransitionDesiredState(ctx, "inst-1", StateDestroyed); err != nil {
		t.Fatalf("transition to destroyed: %v", err)
	}
	if err := s.DeleteInstance(ctx, "inst-1"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	if _, err := s.GetInstance(ctx, "inst-1"); err == nil {
		t.Fatal("expected GetInstance to fail after delete")
	}
}

func TestDeleteInstanceCascadesPortMappings(t *testing.T) {
	s := openTest(t)
	createTestInstance(t, s, "inst-1")
	ctx := context.Background()

	if _, err := s.AllocatePort(ctx, "inst-1", 3000, "web", PortDetected, nil, 43000, 43010); err != nil {
		t.Fatalf("AllocatePort: %v", err)
	}
	if err := s.TransitionDesiredState(ctx, "inst-1", StateDestroyed); err != nil {
		t.Fatalf("transition to destroyed: %v", err)
	}
	if err := s.DeleteInstance(ctx, "inst-1"); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	mappings, err := s.PortMappings(ctx, "inst-1")
	if err != nil {
		t.Fatalf("PortMappings: %v", err)
	}
	if len(mappings) != 0 {
		t.Errorf("expected port_mappings cascaded away, got %+v", mappings)
	}
}
