package core

import (
	"context"
	"os/exec"
	"testing"

	"github.com/rodrigomorales/claudio/internal/store"
)

// TestListInstancesPersistsStoppedForGoneContainer verifies ROD-99's
// lazy-reconciliation promise ("runs as part of whatever command next
// touches an instance, ls/status") is now actually wired into
// ListInstances, not just implemented in the standalone Reconcile
// function nothing calls (see reconcile_test.go for that function's own
// unit tests, unchanged by this).
func TestListInstancesPersistsStoppedForGoneContainer(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Simulate the container disappearing out of band — e.g. `docker rm`
	// by hand, or an OOM death already cleaned up — without going
	// through StopInstance (which would already update the store).
	if out, err := exec.Command("docker", "rm", "-f", result.ContainerID).CombinedOutput(); err != nil {
		t.Fatalf("docker rm -f: %v: %s", err, out)
	}

	views, _, err := ListInstances(context.Background(), s, "")
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("len(views) = %d, want 1", len(views))
	}
	if views[0].DesiredState != store.StateStopped {
		t.Errorf("returned view DesiredState = %s, want %s", views[0].DesiredState, store.StateStopped)
	}

	// The correction must be persisted, not just reflected in this one
	// call's return value — that's the whole point of wiring it in.
	inst, err := s.GetInstance(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.DesiredState != store.StateStopped {
		t.Errorf("stored DesiredState = %s, want %s (persisted by ListInstances)", inst.DesiredState, store.StateStopped)
	}
}

func TestGetInstanceViewPersistsStoppedForGoneContainer(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if out, err := exec.Command("docker", "rm", "-f", result.ContainerID).CombinedOutput(); err != nil {
		t.Fatalf("docker rm -f: %v: %s", err, out)
	}

	view, err := GetInstanceView(context.Background(), s, "", result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstanceView: %v", err)
	}
	if view.DesiredState != store.StateStopped {
		t.Errorf("returned view DesiredState = %s, want %s", view.DesiredState, store.StateStopped)
	}

	inst, err := s.GetInstance(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.DesiredState != store.StateStopped {
		t.Errorf("stored DesiredState = %s, want %s (persisted by GetInstanceView)", inst.DesiredState, store.StateStopped)
	}
}

// TestListInstancesDoesNotMarkStoppedMidProvision guards against the
// obvious wrong generalization: an instance still mid-provision
// (ProvisionStep != StepHealthy) legitimately has no container yet, and
// must not be mistaken for "container disappeared."
func TestListInstancesDoesNotMarkStoppedMidProvision(t *testing.T) {
	s := openTestStore(t)
	createInstance(t, s, "mid-provision", store.StepPortsReady)

	views, _, err := ListInstances(context.Background(), s, "")
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("len(views) = %d, want 1", len(views))
	}
	if views[0].DesiredState != store.StateRunning {
		t.Errorf("DesiredState = %s, want %s (must not be marked stopped mid-provision)", views[0].DesiredState, store.StateRunning)
	}

	inst, err := s.GetInstance(context.Background(), "mid-provision")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.DesiredState != store.StateRunning {
		t.Errorf("stored DesiredState = %s, want %s (must not be persisted as stopped mid-provision)", inst.DesiredState, store.StateRunning)
	}
}
