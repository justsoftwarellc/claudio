// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The unset case must be reported before any store or Docker work: it is
// the command's own misconfiguration, and a user with no editor set but
// no reachable runtime should hear about the editor, not about Docker.
//
// CLAUDIO_HOME points at an empty dir, so there is no config.yml at all —
// the same state a fresh install is in.
func TestOpenWithoutConfiguredEditorFails(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())

	code, stderr := captureStderrCode(t, func() int {
		return cmdOpen(context.Background(), []string{"whatever"})
	})
	if code == 0 {
		t.Fatal("cmdOpen with no editor configured = 0, want nonzero")
	}
	if !strings.Contains(stderr, "no editor configured") {
		t.Errorf("stderr should say no editor is configured, got: %q", stderr)
	}
	// The whole point of the at-first-use requirement is that this message
	// carries the fix; without it the user has nowhere to go.
	if !strings.Contains(stderr, "claudio config set editor") {
		t.Errorf("stderr should name the command that fixes it, got: %q", stderr)
	}
}

// A configured-but-missing binary is a different failure from an unset
// one, with a different fix, so it must not collapse into the same
// message — that ambiguity is what the two separate checks buy.
func TestOpenWithMissingEditorBinaryFailsDistinctly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDIO_HOME", home)
	writeConfig(t, home, "editor: definitely-not-a-real-binary-xyz\n")

	code, stderr := captureStderrCode(t, func() int {
		return cmdOpen(context.Background(), []string{"whatever"})
	})
	if code == 0 {
		t.Fatal("cmdOpen with a missing editor binary = 0, want nonzero")
	}
	if !strings.Contains(stderr, "not found on PATH") {
		t.Errorf("stderr should say the binary is missing, got: %q", stderr)
	}
	if strings.Contains(stderr, "no editor configured") {
		t.Errorf("a missing binary must not report as an unset editor, got: %q", stderr)
	}
}

// An unknown flag must be refused rather than resolved as an instance id
// — the same near-miss configrestore_test.go guards against.
func TestOpenRejectsUnknownFlags(t *testing.T) {
	t.Setenv("CLAUDIO_HOME", t.TempDir())
	if code := cmdOpen(context.Background(), []string{"--nope"}); code == 0 {
		t.Error("cmdOpen(--nope) = 0, want nonzero")
	}
}

// A command wired into the dispatcher but absent from help is invisible.
func TestUsageDocumentsOpen(t *testing.T) {
	out := captureStdout(t, printUsage)
	if !strings.Contains(out, "claudio open") {
		t.Errorf("usage should document `claudio open`:\n%s", out)
	}
	if !strings.Contains(out, "claudio config set editor") {
		t.Errorf("usage should document `claudio config set editor`:\n%s", out)
	}
}

func writeConfig(t *testing.T, home, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "config.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config.yml: %v", err)
	}
}

// captureStderrCode runs fn with stderr redirected, returning its exit
// code and whatever it wrote. Every failure path in this file reports to
// stderr, and the *wording* is the behavior under test — an exit code
// alone would not catch two distinct failures collapsing into one
// message.
func captureStderrCode(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	code := fn()

	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read captured stderr: %v", err)
	}
	return code, string(out)
}
