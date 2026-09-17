// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import (
	"path/filepath"
	"testing"
)

// The whole point of ROD-133: config lives in the folder the user stands
// in, and the store is what knows which folder that is. A worktree path
// is never the answer when a source directory is recorded.
func TestRepoConfigPathPrefersSourceDir(t *testing.T) {
	source := "/Users/me/web"
	got := RepoConfigPath(&source, "/root/acme/worktrees/brave-otter")
	want := filepath.Join(source, FileName)
	if got != want {
		t.Fatalf("RepoConfigPath = %q, want %q", got, want)
	}
}

// An instance created from a remote URL has no folder the user stands in,
// so the worktree is the only file there is. This fallback is why the
// worktree read cannot simply be deleted.
func TestRepoConfigPathFallsBackToWorktree(t *testing.T) {
	worktree := "/root/acme/worktrees/brave-otter"
	got := RepoConfigPath(nil, worktree)
	want := filepath.Join(worktree, FileName)
	if got != want {
		t.Fatalf("RepoConfigPath = %q, want %q", got, want)
	}
}

// A recorded-but-empty source dir is treated as absent rather than
// resolving to "/.claudio.yml" at the filesystem root — a row written by
// an older or buggy caller must not send config reads somewhere
// catastrophic.
func TestRepoConfigPathIgnoresEmptySourceDir(t *testing.T) {
	empty := ""
	worktree := "/root/acme/worktrees/brave-otter"
	got := RepoConfigPath(&empty, worktree)
	want := filepath.Join(worktree, FileName)
	if got != want {
		t.Fatalf("RepoConfigPath with empty source = %q, want worktree fallback %q", got, want)
	}
}

// Both absent is not a panic: it yields an empty path, which
// LoadRepoConfig treats as a missing file (every key is optional).
func TestRepoConfigPathWithNeither(t *testing.T) {
	if got := RepoConfigPath(nil, ""); got != "" {
		t.Fatalf("RepoConfigPath(nil, \"\") = %q, want empty", got)
	}
}
