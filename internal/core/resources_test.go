// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/engine"
)

func writeClaudioYML(t *testing.T, worktreeDir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(worktreeDir, ".claudio.yml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestResolveResourcesNoRepoConfigKeepsGlobalBaseline mirrors
// TestResolveImageDefaultsToBaseWithNoImageConfig: a repo with no
// .claudio.yml (or one with no resources: section) must not change what
// resolveCreateParams already resolved from global config.
func TestResolveResourcesNoRepoConfigKeepsGlobalBaseline(t *testing.T) {
	worktreeDir := t.TempDir()
	global := engine.ResourceLimits{MemoryBytes: 6 << 30, NanoCPUs: 4_000_000_000, PIDs: 512}

	resolved, note, err := resolveResources(worktreeDir, global, nil)
	if err != nil {
		t.Fatalf("resolveResources: %v", err)
	}
	if resolved != global {
		t.Errorf("resolved = %+v, want the global baseline unchanged: %+v", resolved, global)
	}
	if note != "" {
		t.Errorf("note = %q, want empty when there is nothing to override", note)
	}
}

// TestResolveResourcesRepoOverridesGlobal is ROD-112's middle layer:
// "global config < repo .claudio.yml" — a repo asking for more memory
// than the machine default gets it, with no local override present.
func TestResolveResourcesRepoOverridesGlobal(t *testing.T) {
	worktreeDir := t.TempDir()
	writeClaudioYML(t, worktreeDir, "resources:\n  memory: 10g\n")
	global := engine.ResourceLimits{MemoryBytes: 6 << 30, NanoCPUs: 4_000_000_000, PIDs: 512}

	resolved, note, err := resolveResources(worktreeDir, global, nil)
	if err != nil {
		t.Fatalf("resolveResources: %v", err)
	}
	wantMemory := int64(10) << 30
	if resolved.MemoryBytes != wantMemory {
		t.Errorf("MemoryBytes = %d, want %d (10g from the repo's .claudio.yml)", resolved.MemoryBytes, wantMemory)
	}
	if resolved.NanoCPUs != global.NanoCPUs || resolved.PIDs != global.PIDs {
		t.Errorf("CPUs/PIDs changed to %+v, want the global values unchanged since the repo only requested memory", resolved)
	}
	if note != "" {
		t.Errorf("note = %q, want empty — no local override exists to conflict with the repo's request", note)
	}
}

// TestResolveResourcesLocalOverrideWinsAndReportsChange is ROD-112's top
// layer: "repo .claudio.yml < local per-instance override", and "create
// says when it is overriding a repo request rather than doing it
// silently."
func TestResolveResourcesLocalOverrideWinsAndReportsChange(t *testing.T) {
	worktreeDir := t.TempDir()
	writeClaudioYML(t, worktreeDir, "resources:\n  memory: 10g\n")
	global := engine.ResourceLimits{MemoryBytes: 6 << 30, NanoCPUs: 4_000_000_000, PIDs: 512}
	overrideMem := "2g"
	override := &config.Resources{Memory: &overrideMem}

	resolved, note, err := resolveResources(worktreeDir, global, override)
	if err != nil {
		t.Fatalf("resolveResources: %v", err)
	}
	wantMemory := int64(2) << 30
	if resolved.MemoryBytes != wantMemory {
		t.Errorf("MemoryBytes = %d, want %d (the local override, not the repo's 10g request)", resolved.MemoryBytes, wantMemory)
	}
	if note == "" {
		t.Error("note is empty, want a message reporting that the override changed the repo's requested memory")
	}
	if !strings.Contains(note, "10g") || !strings.Contains(note, "2g") {
		t.Errorf("note = %q, want it to name both the repo's request (10g) and the override (2g)", note)
	}
}

// TestResolveResourcesOverrideMatchingRepoRequestProducesNoNote pins that
// the note is about an actual conflict, not "an override exists" —
// nothing changed here, so nothing should be reported.
func TestResolveResourcesOverrideMatchingRepoRequestProducesNoNote(t *testing.T) {
	worktreeDir := t.TempDir()
	writeClaudioYML(t, worktreeDir, "resources:\n  memory: 3g\n")
	global := engine.ResourceLimits{MemoryBytes: 6 << 30}
	overrideMem := "3g"
	override := &config.Resources{Memory: &overrideMem}

	resolved, note, err := resolveResources(worktreeDir, global, override)
	if err != nil {
		t.Fatalf("resolveResources: %v", err)
	}
	if resolved.MemoryBytes != int64(3)<<30 {
		t.Errorf("MemoryBytes = %d, want %d", resolved.MemoryBytes, int64(3)<<30)
	}
	if note != "" {
		t.Errorf("note = %q, want empty — the override matches what the repo already requested, nothing actually changed", note)
	}
}

// TestResolveResourcesLocalOverrideWithNoRepoRequestProducesNoNote covers
// the common case: a repo with no resources: section at all, and a local
// override. There is no repo request to conflict with, so no note.
func TestResolveResourcesLocalOverrideWithNoRepoRequestProducesNoNote(t *testing.T) {
	worktreeDir := t.TempDir()
	global := engine.ResourceLimits{MemoryBytes: 6 << 30}
	overrideMem := "2g"
	override := &config.Resources{Memory: &overrideMem}

	resolved, note, err := resolveResources(worktreeDir, global, override)
	if err != nil {
		t.Fatalf("resolveResources: %v", err)
	}
	if resolved.MemoryBytes != int64(2)<<30 {
		t.Errorf("MemoryBytes = %d, want %d", resolved.MemoryBytes, int64(2)<<30)
	}
	if note != "" {
		t.Errorf("note = %q, want empty — no repo request existed to override", note)
	}
}

// TestResolveResourcesOverrideCPUsAndPIDs pins that CPUs/PIDs overrides
// apply independently of memory, mirroring config.Resources.Merge's
// per-field semantics.
func TestResolveResourcesOverrideCPUsAndPIDs(t *testing.T) {
	worktreeDir := t.TempDir()
	global := engine.ResourceLimits{MemoryBytes: 6 << 30, NanoCPUs: 4_000_000_000, PIDs: 512}
	cpus, pids := 2, 128
	override := &config.Resources{CPUs: &cpus, PIDs: &pids}

	resolved, _, err := resolveResources(worktreeDir, global, override)
	if err != nil {
		t.Fatalf("resolveResources: %v", err)
	}
	if resolved.NanoCPUs != engine.NanoCPUs(2) {
		t.Errorf("NanoCPUs = %d, want %d", resolved.NanoCPUs, engine.NanoCPUs(2))
	}
	if resolved.PIDs != 128 {
		t.Errorf("PIDs = %d, want 128", resolved.PIDs)
	}
	if resolved.MemoryBytes != global.MemoryBytes {
		t.Errorf("MemoryBytes = %d, want the global value unchanged since neither layer touched memory", resolved.MemoryBytes)
	}
}

// TestResolveResourcesInvalidRepoMemoryIsInvalidInput pins the error
// classification a malformed .claudio.yml resources.memory produces —
// cmd/claudio's describeErr passes InvalidInput messages straight
// through, so this must already be an actionable sentence.
func TestResolveResourcesInvalidRepoMemoryIsInvalidInput(t *testing.T) {
	worktreeDir := t.TempDir()
	writeClaudioYML(t, worktreeDir, "resources:\n  memory: not-a-size\n")

	_, _, err := resolveResources(worktreeDir, engine.ResourceLimits{}, nil)
	if err == nil {
		t.Fatal("expected an error for an unparseable memory string")
	}
	if !coreerr.Is(err, coreerr.InvalidInput) {
		code, _ := coreerr.CodeOf(err)
		t.Errorf("code = %q, want %q", code, coreerr.InvalidInput)
	}
}

// TestResolveResourcesInvalidOverrideMemoryIsInvalidInput is the same
// check for the local-override path, which parses independently of the
// repo path.
func TestResolveResourcesInvalidOverrideMemoryIsInvalidInput(t *testing.T) {
	worktreeDir := t.TempDir()
	bad := "not-a-size"
	override := &config.Resources{Memory: &bad}

	_, _, err := resolveResources(worktreeDir, engine.ResourceLimits{}, override)
	if err == nil {
		t.Fatal("expected an error for an unparseable memory string")
	}
	if !coreerr.Is(err, coreerr.InvalidInput) {
		code, _ := coreerr.CodeOf(err)
		t.Errorf("code = %q, want %q", code, coreerr.InvalidInput)
	}
}
