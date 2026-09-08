package core

import (
	"context"
	"os/exec"
	"testing"

	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/store"
)

// This file pins the typed-error classification at core's exported
// operations (docs/architecture.md §12.4's "errors carry a typed code,
// not just a string") — the seam client.Client crosses, and the one
// phase 2's HTTP layer will map to status codes. Each test asserts a
// specific coreerr.Code rather than just "an error occurred," since the
// whole point of a typed code is that a caller can branch on it.

func TestDestroyInstanceUnknownIDIsNotFound(t *testing.T) {
	s := openTestStore(t)

	err := DestroyInstance(context.Background(), s, DestroyParams{IDOrName: "does-not-exist"})
	if !coreerr.Is(err, coreerr.NotFound) {
		t.Fatalf("DestroyInstance(unknown id) code = %v, want NotFound; err = %v", codeOrNone(err), err)
	}
}

func TestStatusUnknownIDIsNotFound(t *testing.T) {
	s := openTestStore(t)

	_, err := GetInstanceView(context.Background(), s, "", "does-not-exist")
	if !coreerr.Is(err, coreerr.NotFound) {
		t.Fatalf("GetInstanceView(unknown id) code = %v, want NotFound; err = %v", codeOrNone(err), err)
	}
}

func TestStopUnknownIDIsNotFound(t *testing.T) {
	s := openTestStore(t)

	err := StopInstance(context.Background(), s, "", "does-not-exist")
	if !coreerr.Is(err, coreerr.NotFound) {
		t.Fatalf("StopInstance(unknown id) code = %v, want NotFound; err = %v", codeOrNone(err), err)
	}
}

func TestAddPortUnknownIDIsNotFound(t *testing.T) {
	s := openTestStore(t)

	_, err := AddPort(context.Background(), s, "does-not-exist", 3000, 43000, 43999)
	if !coreerr.Is(err, coreerr.NotFound) {
		t.Fatalf("AddPort(unknown id) code = %v, want NotFound; err = %v", codeOrNone(err), err)
	}
}

func TestGetInstanceAmbiguousPrefixIsAmbiguousID(t *testing.T) {
	// Deterministic, no Docker needed: createInstance (reconcile_test.go)
	// inserts a store row directly with a caller-chosen ID, so the
	// shared-prefix case doesn't depend on idgen's random word pairing
	// happening to collide.
	s := openTestStore(t)
	createInstance(t, s, "brave-otter", store.StepHealthy)
	createInstance(t, s, "brave-falcon", store.StepHealthy)

	err := StopInstance(context.Background(), s, "", "brave")
	if !coreerr.Is(err, coreerr.AmbiguousID) {
		t.Fatalf("StopInstance(ambiguous prefix) code = %v, want AmbiguousID; err = %v", codeOrNone(err), err)
	}
}

func TestStartInstanceWrongStateIsConflict(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	// Instance is StateRunning, not StateStopped — Start must refuse.
	_, err = startInstanceForTest(t, s, baseCreateParams(t, repoURL), result.InstanceID, false)
	if !coreerr.Is(err, coreerr.Conflict) {
		t.Fatalf("StartInstance(already running) code = %v, want Conflict; err = %v", codeOrNone(err), err)
	}
}

func TestCreateInstanceBranchDoesNotExistIsNotFound(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	params := baseCreateParams(t, repoURL)
	params.Branch = "does-not-exist"
	_, err := createForTest(t, s, params)
	if !coreerr.Is(err, coreerr.NotFound) {
		t.Fatalf("CreateInstance(--branch nonexistent) code = %v, want NotFound; err = %v", codeOrNone(err), err)
	}
}

func TestCreateInstanceBranchCollisionIsConflict(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)
	workspaceRoot := t.TempDir()

	params1 := baseCreateParams(t, repoURL)
	params1.WorkspaceRoot = workspaceRoot
	params1.NewBranch = "feat/auth"
	r1, err := createForTest(t, s, params1)
	if err != nil {
		t.Fatalf("CreateInstance (1st): %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", r1.ContainerID).Run() })

	params2 := baseCreateParams(t, repoURL)
	params2.WorkspaceRoot = workspaceRoot
	params2.NewBranch = "feat/auth"
	_, err = createForTest(t, s, params2)
	if !coreerr.Is(err, coreerr.Conflict) {
		t.Fatalf("CreateInstance(colliding branch) code = %v, want Conflict; err = %v", codeOrNone(err), err)
	}
}

func TestAddPortRangeExhaustedIsConflict(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	// A single-port range: the first AddPort takes the only slot, the
	// second must exhaust it.
	if _, err := AddPort(context.Background(), s, result.InstanceID, 9229, 43500, 43500); err != nil {
		t.Fatalf("AddPort (occupy the only port in range): %v", err)
	}
	_, err = AddPort(context.Background(), s, result.InstanceID, 9230, 43500, 43500)
	if !coreerr.Is(err, coreerr.Conflict) {
		t.Fatalf("AddPort(range exhausted) code = %v, want Conflict; err = %v", codeOrNone(err), err)
	}
}

func TestRemovePortNotMappedIsNotFound(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	err = RemovePort(context.Background(), s, result.InstanceID, 59999)
	if !coreerr.Is(err, coreerr.NotFound) {
		t.Fatalf("RemovePort(unmapped port) code = %v, want NotFound; err = %v", codeOrNone(err), err)
	}
}

func TestAdoptContainerUnknownIDIsNotFound(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)

	_, err := AdoptContainer(context.Background(), s, "", "does-not-exist", 0)
	if !coreerr.Is(err, coreerr.NotFound) {
		t.Fatalf("AdoptContainer(unknown container) code = %v, want NotFound; err = %v", codeOrNone(err), err)
	}
}

func TestRestartInstancePreservesInnerCode(t *testing.T) {
	// RestartInstance wraps Stop/Start's errors with fmt.Errorf("%w"),
	// not coreerr.Wrap — pins that this still preserves the inner code,
	// since coreerr.Is/CodeOf walk the chain via errors.As.
	s := openTestStore(t)

	_, err := RestartInstance(context.Background(), s, "", CreateParams{}, "does-not-exist", false, nil)
	if !coreerr.Is(err, coreerr.NotFound) {
		t.Fatalf("RestartInstance(unknown id) code = %v, want NotFound (preserved through restart's own wrap); err = %v", codeOrNone(err), err)
	}
}

func codeOrNone(err error) coreerr.Code {
	code, ok := coreerr.CodeOf(err)
	if !ok {
		return "none"
	}
	return code
}
