// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package repo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Config is local (ROD-133), so a .claudio.yml sitting in the user's own
// folder must not show up as a stray untracked file in their git status.
func TestExcludeConfigInSourceDirHidesUntrackedConfig(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	git(t, "", "init", dir)
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "test")

	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte("ports: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := git(t, dir, "status", "--short"); !strings.Contains(out, ConfigFileName) {
		t.Fatalf("precondition: expected %s to show as untracked, got %q", ConfigFileName, out)
	}

	if err := ExcludeConfigInSourceDir(ctx, dir); err != nil {
		t.Fatalf("ExcludeConfigInSourceDir: %v", err)
	}

	if out := git(t, dir, "status", "--short"); strings.Contains(out, ConfigFileName) {
		t.Fatalf("git status still reports %s after excluding it: %q", ConfigFileName, out)
	}
}

// Idempotent: a create against the same folder runs this every time and
// must not append a duplicate line on each run.
func TestExcludeConfigInSourceDirIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	git(t, "", "init", dir)

	for range 3 {
		if err := ExcludeConfigInSourceDir(ctx, dir); err != nil {
			t.Fatalf("ExcludeConfigInSourceDir: %v", err)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), ConfigFileName); n != 1 {
		t.Fatalf("%s appears %d times in info/exclude, want exactly 1", ConfigFileName, n)
	}
}

// The limit that forces the warning: info/exclude cannot hide a file git
// already tracks, so a repo that committed its .claudio.yml keeps showing
// local edits as dirty no matter what Claudio writes.
func TestConfigIsTrackedDistinguishesCommittedFromLocal(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	git(t, "", "init", dir)
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "test")

	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte("ports: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ConfigIsTracked(ctx, dir) {
		t.Fatal("ConfigIsTracked = true for an untracked file, want false")
	}

	git(t, dir, "add", ConfigFileName)
	git(t, dir, "commit", "-m", "commit config")

	if !ConfigIsTracked(ctx, dir) {
		t.Fatal("ConfigIsTracked = false after committing the file, want true")
	}

	// And the reason it matters: excluding it changes nothing now.
	if err := ExcludeConfigInSourceDir(ctx, dir); err != nil {
		t.Fatalf("ExcludeConfigInSourceDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte("ports: [{name: web, container: 3000}]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := git(t, dir, "status", "--short"); !strings.Contains(out, ConfigFileName) {
		t.Fatalf("a tracked file's edit should still show dirty, got %q", out)
	}
}

// A directory that is not a git repo at all is not a crash: `claudio
// create .` on unversioned work git-inits it first, but the helper must
// not assume that ordering.
func TestExcludeConfigInSourceDirOnNonRepo(t *testing.T) {
	if err := ExcludeConfigInSourceDir(context.Background(), t.TempDir()); err == nil {
		t.Fatal("ExcludeConfigInSourceDir on a non-repo = nil, want an error the caller can degrade on")
	}
}
