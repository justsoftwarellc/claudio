// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"strings"
	"testing"
)

// TestCreateRejectsInvalidMemoryFlag pins that a malformed --memory value
// is rejected before any provisioning starts (no repo clone, no Docker
// call needed to observe the rejection) — ROD-112's local-override flag.
func TestCreateRejectsInvalidMemoryFlag(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	var code int
	out := captureOutput(t, func() {
		code = run([]string{"create", "git@github.com:acme/web.git", "--memory", "not-a-size"})
	})
	if code == 0 {
		t.Error("claudio create --memory not-a-size exited 0, want nonzero")
	}
	if !strings.Contains(out, "--memory") {
		t.Errorf("output = %q, want it to name --memory", out)
	}
}

func TestCreateRejectsNonPositiveCPUsFlag(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	var code int
	out := captureOutput(t, func() {
		code = run([]string{"create", "git@github.com:acme/web.git", "--cpus", "0"})
	})
	if code == 0 {
		t.Error("claudio create --cpus 0 exited 0, want nonzero")
	}
	if !strings.Contains(out, "--cpus") {
		t.Errorf("output = %q, want it to name --cpus", out)
	}
}

func TestCreateRejectsNonIntegerCPUsFlag(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	var code int
	out := captureOutput(t, func() {
		code = run([]string{"create", "git@github.com:acme/web.git", "--cpus", "two"})
	})
	if code == 0 {
		t.Error(`claudio create --cpus two exited 0, want nonzero`)
	}
	if !strings.Contains(out, "--cpus") {
		t.Errorf("output = %q, want it to name --cpus", out)
	}
}

func TestCreateRejectsNonPositivePIDsFlag(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	var code int
	out := captureOutput(t, func() {
		code = run([]string{"create", "git@github.com:acme/web.git", "--pids", "-5"})
	})
	if code == 0 {
		t.Error("claudio create --pids -5 exited 0, want nonzero")
	}
	if !strings.Contains(out, "--pids") {
		t.Errorf("output = %q, want it to name --pids", out)
	}
}

func TestCreateMemoryFlagRequiresValue(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	var code int
	captureOutput(t, func() {
		code = run([]string{"create", "git@github.com:acme/web.git", "--memory"})
	})
	if code == 0 {
		t.Error("claudio create --memory (no value) exited 0, want nonzero")
	}
}
