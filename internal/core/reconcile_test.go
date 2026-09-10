// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"os/exec"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/store"
)

// dockerAvailable mirrors the skip condition used elsewhere in the repo
// (internal/repo/repo_test.go) for tests that need a real daemon.
func dockerAvailable(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping docker-backed test in -short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not reachable")
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), t.TempDir()+"/state.db")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func createInstance(t *testing.T, s *store.Store, id string, step store.ProvisionStep) {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateInstance(ctx, store.NewInstanceParams{
		ID: id, RepoURL: "git@github.com:acme/web.git", RepoRoot: "/r", WorktreeDir: "/r/w/" + id,
		Branch: "main", Image: "claudio/base", RuntimeProfile: "orbstack",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	stepOrder := []store.ProvisionStep{store.StepRepoReady, store.StepPortsReady, store.StepConfigReady, store.StepContainerUp, store.StepHealthy}
	for _, s2 := range stepOrder {
		if err := s.TransitionProvisionStep(ctx, id, s2); err != nil {
			t.Fatalf("advance to %s: %v", s2, err)
		}
		if s2 == step {
			return
		}
	}
}

func TestReconcileFlagsInstanceStillProvisioningAsNeedingResume(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.CreateInstance(ctx, store.NewInstanceParams{
		ID: "inst-1", RepoURL: "x", RepoRoot: "x", WorktreeDir: "x", Branch: "x",
		Image: "x", RuntimeProfile: "x",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	// Left at StepPending — simulates a process that crashed mid-provision.

	dockerAvailable(t) // Reconcile calls engine.ListClaudioContainers, needs a live daemon even with zero containers.
	actions, err := Reconcile(ctx, s, "")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	mine := actionsFor(actions, "inst-1")
	if len(mine) != 1 || mine[0].Kind != ActionResumeProvisioning {
		t.Fatalf("actions for inst-1 = %+v, want single ResumeProvisioning (all: %+v)", mine, actions)
	}
}

func TestReconcileHealthyInstanceWithNoContainerIsMarkedStopped(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	createInstance(t, s, "inst-1", store.StepHealthy)
	// No real container exists for inst-1 in Docker, so this must surface
	// as MarkStopped rather than silently doing nothing.

	actions, err := Reconcile(context.Background(), s, "")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	mine := actionsFor(actions, "inst-1")
	if len(mine) != 1 || mine[0].Kind != ActionMarkStopped {
		t.Fatalf("actions for inst-1 = %+v, want single MarkStopped (all: %+v)", mine, actions)
	}

	if err := ApplyMarkStopped(context.Background(), s, "inst-1"); err != nil {
		t.Fatalf("ApplyMarkStopped: %v", err)
	}
	inst, err := s.GetInstance(context.Background(), "inst-1")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.DesiredState != store.StateStopped {
		t.Errorf("DesiredState = %s, want %s", inst.DesiredState, store.StateStopped)
	}
}

func TestReconcileIgnoresDestroyedInstances(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	createInstance(t, s, "inst-1", store.StepHealthy)
	if err := s.TransitionDesiredState(context.Background(), "inst-1", store.StateDestroyed); err != nil {
		t.Fatalf("transition to destroyed: %v", err)
	}

	actions, err := Reconcile(context.Background(), s, "")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if mine := actionsFor(actions, "inst-1"); len(mine) != 0 {
		t.Fatalf("actions for inst-1 = %+v, want none for a destroyed instance", mine)
	}
}

// actionsFor narrows Reconcile's output to one instance.
//
// Reconcile queries the live Docker daemon, which is shared with whatever
// Claudio instances the developer running the tests happens to have going
// — each of those legitimately reconciles to a flag_untracked action,
// since this test's store knows nothing about them. Asserting on the
// whole list therefore only passed on a machine with no instances
// running, and the failures it produced elsewhere masked real ones
// (ROD-119 hid behind exactly this noise). What each test here actually
// means to pin is the action taken for the instance it created.
func actionsFor(actions []ReconcileAction, instanceID string) []ReconcileAction {
	var out []ReconcileAction
	for _, a := range actions {
		if a.InstanceID == instanceID {
			out = append(out, a)
		}
	}
	return out
}

func TestReconcileFlagsUntrackedContainer(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)

	name := "claudio-test-untracked-" + t.Name()
	out, err := exec.Command("docker", "run", "-d", "--rm",
		"--label", engine.LabelInstanceID+"=ghost-1",
		"--label", engine.LabelRepo+"=git@github.com:acme/ghost.git",
		"--label", engine.LabelCreatedAt+"=12345",
		"--name", name,
		"alpine", "sleep", "60").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	containerID := trimNewline(string(out))
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", containerID).Run() })

	actions, err := Reconcile(context.Background(), s, "")
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var found *ReconcileAction
	for i := range actions {
		if actions[i].Kind == ActionFlagUntracked && actions[i].Untracked.ContainerID == containerID {
			found = &actions[i]
		}
	}
	if found == nil {
		t.Fatalf("actions = %+v, want a FlagUntracked entry for %s", actions, containerID)
	}
	if found.Untracked.RepoURL != "git@github.com:acme/ghost.git" {
		t.Errorf("Untracked.RepoURL = %q, want the ghost repo URL", found.Untracked.RepoURL)
	}
	if found.Untracked.CreatedAt != 12345 {
		t.Errorf("Untracked.CreatedAt = %d, want 12345", found.Untracked.CreatedAt)
	}
}

// TestApplyMarkStoppedEmitsEvent pins ApplyMarkStopped to the same event
// the inline corrections in ListInstances/GetInstanceView emit, so the
// event log doesn't depend on which mark-stopped path a caller took.
func TestApplyMarkStoppedEmitsEvent(t *testing.T) {
	s := openTestStore(t)
	createInstance(t, s, "inst-1", store.StepHealthy)

	if err := ApplyMarkStopped(context.Background(), s, "inst-1"); err != nil {
		t.Fatalf("ApplyMarkStopped: %v", err)
	}

	events, err := s.ListEvents(context.Background(), "inst-1")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Kind != store.EventReconciled {
		t.Errorf("Kind = %q, want %q", events[0].Kind, store.EventReconciled)
	}
}

// TestApplyMarkStoppedEmitsNoEventOnFailedTransition pairs with the
// above: the event records that a correction happened, so a rejected
// transition (destroyed is terminal) must leave the log untouched
// rather than claiming a stop that never occurred.
func TestApplyMarkStoppedEmitsNoEventOnFailedTransition(t *testing.T) {
	s := openTestStore(t)
	createInstance(t, s, "inst-1", store.StepHealthy)
	if err := s.TransitionDesiredState(context.Background(), "inst-1", store.StateDestroyed); err != nil {
		t.Fatalf("TransitionDesiredState to destroyed: %v", err)
	}

	if err := ApplyMarkStopped(context.Background(), s, "inst-1"); err == nil {
		t.Fatal("expected an error marking a destroyed instance stopped (destroyed is terminal)")
	}

	events, err := s.ListEvents(context.Background(), "inst-1")
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none when the transition was rejected", events)
	}
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
