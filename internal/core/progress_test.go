package core

import (
	"os/exec"
	"testing"

	"github.com/rodrigomorales/claudio/internal/store"
)

func TestCreateInstanceReportsProgressAtEachStage(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	var events []ProgressEvent
	result, err := createInstanceWithCmd(t.Context(), s, baseCreateParams(t, repoURL), []string{"sleep", "60"}, func(e ProgressEvent) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	// Two StepPending reports: the generated instance id, then the
	// main-clone refresh (a fetch can be slow, and progress exists so a
	// long create does not sit silent — see refreshBase).
	wantSteps := []store.ProvisionStep{
		store.StepPending,
		store.StepPending,
		store.StepRepoReady,
		store.StepPortsReady,
		store.StepConfigReady,
		store.StepContainerUp,
		store.StepHealthy,
	}
	if len(events) != len(wantSteps) {
		t.Fatalf("got %d progress events, want %d: %+v", len(events), len(wantSteps), events)
	}
	for i, want := range wantSteps {
		if events[i].Step != want {
			t.Errorf("event[%d].Step = %q, want %q", i, events[i].Step, want)
		}
		if events[i].Message == "" {
			t.Errorf("event[%d] (step %s) has an empty Message", i, events[i].Step)
		}
	}
}

func TestCreateInstanceNilProgressIsSafe(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	// The whole point of "may be nil" — every production call site that
	// doesn't care about progress (most of this package's own tests, and
	// any future caller with nothing to show) must not have to pass a
	// no-op closure.
	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance with nil progress: %v", err)
	}
	exec.Command("docker", "rm", "-f", result.ContainerID).Run()
}

func TestStartInstanceReportsProgress(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := StopInstance(t.Context(), s, "", result.InstanceID); err != nil {
		t.Fatalf("StopInstance: %v", err)
	}

	var steps []store.ProvisionStep
	startResult, err := startInstanceWithCmd(t.Context(), s, baseCreateParams(t, repoURL), result.InstanceID, false, []string{"sleep", "60"}, func(e ProgressEvent) {
		steps = append(steps, e.Step)
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", startResult.ContainerID).Run() })

	// Start skips StepPending's report (CreateInstance's is emitted right
	// after ID generation, which Start has no equivalent of — it already
	// has an ID) but must still report the provisioning stages
	// provisionContainer itself drives.
	wantSteps := []store.ProvisionStep{
		store.StepPortsReady,
		store.StepConfigReady,
		store.StepContainerUp,
		store.StepHealthy,
	}
	if len(steps) != len(wantSteps) {
		t.Fatalf("got steps %v, want %v", steps, wantSteps)
	}
	for i, want := range wantSteps {
		if steps[i] != want {
			t.Errorf("steps[%d] = %q, want %q", i, steps[i], want)
		}
	}
}

func TestFailedCreateStopsReportingAtTheFailurePoint(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	params := baseCreateParams(t, repoURL)
	params.Image = "claudio-test-nonexistent-registry.invalid/does-not-exist:latest"

	var steps []store.ProvisionStep
	_, err := createInstanceWithCmd(t.Context(), s, params, nil, func(e ProgressEvent) {
		steps = append(steps, e.Step)
	})
	if err == nil {
		t.Fatal("expected CreateInstance to fail with an unresolvable image")
	}

	// Reaches StepConfigReady's "starting container" report (emitted
	// before engine.CreateAndStart is even called) but never
	// StepContainerUp or StepHealthy, since the image can't be pulled.
	wantSteps := []store.ProvisionStep{
		store.StepPending,
		store.StepPending, // id generated, then the refresh (refreshBase)
		store.StepRepoReady,
		store.StepPortsReady,
		store.StepConfigReady,
	}
	if len(steps) != len(wantSteps) {
		t.Fatalf("got steps %v, want %v (failure should stop progress at the point of failure)", steps, wantSteps)
	}
}
