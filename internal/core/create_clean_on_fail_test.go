// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"os"
	"testing"
)

// TestCreateInstanceCleanOnFailRemovesWorktree verifies ROD-99's
// --clean-on-fail: docs/architecture.md §4.1 says a failed create
// preserves the workspace for inspection *unless* this flag is set, in
// which case the worktree this call created should be removed instead.
// Forces a deterministic provisioning failure with an image name Docker
// cannot resolve (a bogus registry host, never pulled over the network
// in CI-like environments) — no real Docker daemon reachability is
// needed for the image resolution to fail this way, but the daemon
// itself still must be reachable to attempt it, hence dockerAvailable.
func TestCreateInstanceCleanOnFailRemovesWorktree(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	params := baseCreateParams(t, repoURL)
	params.Image = "claudio-test-nonexistent-registry.invalid/does-not-exist:latest"
	params.CleanOnFail = true

	_, err := CreateInstance(t.Context(), s, params, nil)
	if err == nil {
		t.Fatal("expected CreateInstance to fail with an unresolvable image")
	}

	// The instance ID is embedded in the error message ("core: create
	// <id>: ..."); rather than parse it out, list instances directly —
	// there is exactly one, since this is a fresh store.
	instances, listErr := s.ListInstances(t.Context())
	if listErr != nil {
		t.Fatalf("ListInstances: %v", listErr)
	}
	if len(instances) != 1 {
		t.Fatalf("len(instances) = %d, want 1", len(instances))
	}
	worktreeDir := instances[0].WorktreeDir

	if _, statErr := os.Stat(worktreeDir); !os.IsNotExist(statErr) {
		t.Errorf("worktree %s should be removed after a --clean-on-fail failure, stat err = %v", worktreeDir, statErr)
	}
}

// TestCreateInstanceWithoutCleanOnFailKeepsWorktree is the control case:
// the default behavior (CleanOnFail unset) must still preserve the
// worktree for inspection, unchanged from before this flag existed.
func TestCreateInstanceWithoutCleanOnFailKeepsWorktree(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	params := baseCreateParams(t, repoURL)
	params.Image = "claudio-test-nonexistent-registry.invalid/does-not-exist:latest"

	_, err := CreateInstance(t.Context(), s, params, nil)
	if err == nil {
		t.Fatal("expected CreateInstance to fail with an unresolvable image")
	}

	instances, listErr := s.ListInstances(t.Context())
	if listErr != nil {
		t.Fatalf("ListInstances: %v", listErr)
	}
	if len(instances) != 1 {
		t.Fatalf("len(instances) = %d, want 1", len(instances))
	}
	worktreeDir := instances[0].WorktreeDir

	if _, statErr := os.Stat(worktreeDir); statErr != nil {
		t.Errorf("worktree %s should survive a failure by default, stat err = %v", worktreeDir, statErr)
	}
}
