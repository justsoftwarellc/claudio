// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/store"
)

// runContainerWithMemoryLimit starts a real, throwaway container with a
// Docker memory limit and returns its ID — the minimal fixture
// checkMemoryBudget's InspectMemoryLimit call needs, mirroring
// TestAdoptContainerReconstructsRowAndReReservesPorts's use of a real
// `docker run` rather than a store-only fake, since the whole point of
// this check is reading Docker's own HostConfig.Memory back.
func runContainerWithMemoryLimit(t *testing.T, memory string) string {
	t.Helper()
	out, err := exec.Command("docker", "run", "-d", "--rm", "-m", memory, "alpine", "sleep", "60").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	containerID := strings.TrimSpace(string(out))
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", containerID).Run() })
	return containerID
}

// TestCheckMemoryBudgetNoWarningWhenWellUnderBudget pins the common
// case: no running siblings, a normal-sized request. Must not warn.
func TestCheckMemoryBudgetNoWarningWhenWellUnderBudget(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)

	note := checkMemoryBudget(context.Background(), s, "", "new-instance", 1<<20) // 1 MiB — trivially small
	if note != "" {
		t.Errorf("note = %q, want empty — nothing close to a real VM's memory total", note)
	}
}

// TestCheckMemoryBudgetZeroMemoryNeverWarns pins that an instance with no
// configured limit at all (MemoryBytes == 0, "unset" per
// engine.ResourceLimits' own doc) is never itself the trigger for a
// warning — there's nothing to sum.
func TestCheckMemoryBudgetZeroMemoryNeverWarns(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	note := checkMemoryBudget(context.Background(), s, "", "new-instance", 0)
	if note != "" {
		t.Errorf("note = %q, want empty for an unlimited (0) request", note)
	}
}

// TestCheckMemoryBudgetIgnoresStoppedAndSelf pins two exclusions: an
// instance whose desired_state isn't running must not count toward the
// sum (it holds no container reservation), and the instance being
// provisioned right now must not double-count itself even if a row for
// it already exists (CreateInstance inserts the row before
// provisionContainer runs).
func TestCheckMemoryBudgetIgnoresStoppedAndSelf(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	ctx := context.Background()

	createInstance(t, s, "stopped-sibling", store.StepHealthy)
	if err := s.TransitionDesiredState(ctx, "stopped-sibling", store.StateStopped); err != nil {
		t.Fatalf("TransitionDesiredState: %v", err)
	}

	createInstance(t, s, "new-instance", store.StepHealthy)

	// Neither row has a container_id at all, so even if the exclusions
	// above were missing, InspectMemoryLimit would never be reached for
	// them — this test only pins that checkMemoryBudget doesn't error or
	// warn spuriously against instances that must be excluded before
	// getting that far.
	note := checkMemoryBudget(ctx, s, "", "new-instance", 1<<30)
	if note != "" {
		t.Errorf("note = %q, want empty — the only other row is stopped, and self must not be counted", note)
	}
}

// TestCheckMemoryBudgetWarnsWhenSumExceedsRuntimeMemory is this check's
// core empirical case: a real running sibling holding most of the
// runtime's memory, plus a new request that pushes the sum over the top,
// must produce a warning naming both figures.
func TestCheckMemoryBudgetWarnsWhenSumExceedsRuntimeMemory(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	ctx := context.Background()

	rt, err := DetectRuntime(ctx, "")
	if err != nil {
		t.Fatalf("DetectRuntime: %v", err)
	}
	if rt.MemTotalBytes <= 0 {
		t.Skip("runtime reports no total memory, cannot construct an over-budget scenario")
	}

	// A sibling holding just over half the runtime's memory...
	siblingMemory := rt.MemTotalBytes/2 + (1 << 20)
	containerID := runContainerWithMemoryLimit(t, fmt.Sprintf("%db", siblingMemory))

	createInstance(t, s, "running-sibling", store.StepHealthy)
	if err := s.SetContainerID(ctx, "running-sibling", containerID); err != nil {
		t.Fatalf("SetContainerID: %v", err)
	}

	createInstance(t, s, "new-instance", store.StepHealthy)

	// ...plus a new request for another half-or-more pushes the sum over
	// the runtime's total.
	note := checkMemoryBudget(ctx, s, "", "new-instance", siblingMemory)
	if note == "" {
		t.Fatal("note is empty, want a warning: the sum of both instances' limits exceeds the runtime's total memory")
	}
	if !strings.Contains(note, "claudio ls") {
		t.Errorf("note = %q, want it to point at `claudio ls` to see what's running", note)
	}
}
