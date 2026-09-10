// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// The launch itself, which nothing else in the suite covers: every other
// test in this package stops at a failure *before* the editor would run,
// so without this the one thing `claudio open` exists to do would be
// unverified.
//
// A shell script standing in for the editor is what makes the two
// load-bearing properties observable — that the binary is handed the
// worktree as its single argument, and that the call returns instead of
// waiting for the editor to exit. A GUI editor's launcher process
// outliving the shell is exactly the case cmdOpen's detached start is
// for, and a blocking launch would hold the user's terminal for as long
// as their editor stayed open.
func TestLaunchEditorStartsTheBinaryDetached(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in editor is a shell script")
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "launched.txt")
	fakeEditor := filepath.Join(dir, "fake-editor")

	// Records the argument it was given, then exits. `sleep` first so a
	// launchEditor that waited would visibly block.
	script := "#!/bin/sh\nsleep 1\nprintf '%s\\n' \"$1\" > " + marker + "\n"
	if err := os.WriteFile(fakeEditor, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake editor: %v", err)
	}

	worktree := t.TempDir()

	start := time.Now()
	if err := launchEditor(fakeEditor, worktree); err != nil {
		t.Fatalf("launchEditor: %v", err)
	}
	// The stand-in sleeps for a second before writing; returning faster
	// than that is what proves the call did not wait on it.
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("launchEditor blocked for %v; it must start the editor and return", elapsed)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(marker)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if got, want := string(b), worktree+"\n"; got != want {
			t.Fatalf("editor received %q, want the worktree path %q", got, want)
		}
		return
	}
	t.Fatal("the editor never ran: its marker file was never written")
}
