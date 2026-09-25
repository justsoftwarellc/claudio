// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resources(memory string, cpus, pids int) Resources {
	var r Resources
	if memory != "" {
		r.Memory = &memory
	}
	if cpus != 0 {
		r.CPUs = &cpus
	}
	if pids != 0 {
		r.PIDs = &pids
	}
	return r
}

func TestSetResourcesCreatesFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := SetResourcesInRepoConfig(filepath.Join(dir, FileName), resources("12g", 8, 0)); err != nil {
		t.Fatalf("SetResourcesInRepoConfig: %v", err)
	}

	cfg, err := LoadRepoConfig(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if cfg.Resources.Memory == nil || *cfg.Resources.Memory != "12g" {
		t.Errorf("Memory = %v, want 12g", cfg.Resources.Memory)
	}
	if cfg.Resources.CPUs == nil || *cfg.Resources.CPUs != 8 {
		t.Errorf("CPUs = %v, want 8", cfg.Resources.CPUs)
	}
	// Not typed by the user, so not written: see the function's doc on
	// why an unset field must not be frozen to today's global default.
	if cfg.Resources.PIDs != nil {
		t.Errorf("PIDs = %v, want nil (never overridden)", *cfg.Resources.PIDs)
	}
}

// The bug this whole function exists for (ROD-137): a create-time
// override has to be readable back as the repo layer, because that is
// all a later `claudio restart` gets to see.
func TestSetResourcesRoundTripsThroughEffectiveResources(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := SetResourcesInRepoConfig(path, resources("12g", 0, 0)); err != nil {
		t.Fatalf("SetResourcesInRepoConfig: %v", err)
	}

	cfg, err := LoadRepoConfig(path)
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	// Restart's own resolution: global baseline, repo file on top, no
	// local override (restart has no flags to supply one).
	effective, _ := EffectiveResources(Defaults().Resources, cfg.Resources, nil)
	if effective.Memory == nil || *effective.Memory != "12g" {
		t.Errorf("effective memory after restart-style resolution = %v, want 12g", effective.Memory)
	}
}

// A second create against the same folder must update the number, not
// leave two `memory:` keys — which would be confusing to read and is
// rejected outright by LoadRepoConfig's strict decoding.
func TestSetResourcesReplacesRatherThanDuplicates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := SetResourcesInRepoConfig(path, resources("12g", 0, 0)); err != nil {
		t.Fatalf("first SetResourcesInRepoConfig: %v", err)
	}
	if err := SetResourcesInRepoConfig(path, resources("4g", 0, 0)); err != nil {
		t.Fatalf("second SetResourcesInRepoConfig: %v", err)
	}

	cfg, err := LoadRepoConfig(path)
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if cfg.Resources.Memory == nil || *cfg.Resources.Memory != "4g" {
		t.Errorf("Memory = %v, want 4g (the later write)", cfg.Resources.Memory)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "memory:"); n != 1 {
		t.Errorf("file has %d memory: keys, want 1:\n%s", n, data)
	}
}

// Overriding one field must not erase a sibling the repo committed:
// `--cpus 8` against a repo asking for 12g memory keeps the memory.
func TestSetResourcesPreservesUntouchedSiblings(t *testing.T) {
	dir := t.TempDir()
	path := writeYML(t, dir, "resources:\n  memory: 12g\n  pids: 900\n")

	if err := SetResourcesInRepoConfig(path, resources("", 8, 0)); err != nil {
		t.Fatalf("SetResourcesInRepoConfig: %v", err)
	}

	cfg, err := LoadRepoConfig(path)
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if cfg.Resources.Memory == nil || *cfg.Resources.Memory != "12g" {
		t.Errorf("Memory = %v, want 12g preserved", cfg.Resources.Memory)
	}
	if cfg.Resources.PIDs == nil || *cfg.Resources.PIDs != 900 {
		t.Errorf("PIDs = %v, want 900 preserved", cfg.Resources.PIDs)
	}
	if cfg.Resources.CPUs == nil || *cfg.Resources.CPUs != 8 {
		t.Errorf("CPUs = %v, want 8", cfg.Resources.CPUs)
	}
}

// The file belongs to the user — same requirement AddPortToRepoConfig
// carries, and the reason both edit a yaml.Node tree instead of
// round-tripping through RepoConfig.
func TestSetResourcesPreservesUnknownKeysAndComments(t *testing.T) {
	dir := t.TempDir()
	path := writeYML(t, dir, "# keep me\nfuture_key: value\nports:\n  - name: web\n    container: 3000\n")

	if err := SetResourcesInRepoConfig(path, resources("12g", 0, 0)); err != nil {
		t.Fatalf("SetResourcesInRepoConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "# keep me") {
		t.Errorf("comment lost:\n%s", got)
	}
	if !strings.Contains(got, "future_key") {
		t.Errorf("unknown key lost:\n%s", got)
	}
	if !strings.Contains(got, "container: 3000") {
		t.Errorf("existing ports lost:\n%s", got)
	}
}

// `resources:` with nothing under it parses as a null scalar rather than
// an empty mapping — writing into it naively produces a file that no
// longer loads.
func TestSetResourcesHandlesEmptyResourcesKey(t *testing.T) {
	dir := t.TempDir()
	path := writeYML(t, dir, "resources:\n")

	if err := SetResourcesInRepoConfig(path, resources("12g", 0, 0)); err != nil {
		t.Fatalf("SetResourcesInRepoConfig: %v", err)
	}

	cfg, err := LoadRepoConfig(path)
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if cfg.Resources.Memory == nil || *cfg.Resources.Memory != "12g" {
		t.Errorf("Memory = %v, want 12g", cfg.Resources.Memory)
	}
}

// No override supplied is not an error and must not create a file: every
// create without resource flags would otherwise litter the user's folder
// with a .claudio.yml they never asked for.
func TestSetResourcesEmptyOverrideWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	if err := SetResourcesInRepoConfig(path, Resources{}); err != nil {
		t.Fatalf("SetResourcesInRepoConfig: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file created for an empty override (stat err = %v)", err)
	}
}
