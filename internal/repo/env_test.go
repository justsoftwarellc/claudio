// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package repo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExcludeClaudioDirAppendsOnce(t *testing.T) {
	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}

	if err := ExcludeClaudioDir(root); err != nil {
		t.Fatalf("ExcludeClaudioDir (1st): %v", err)
	}
	if err := ExcludeClaudioDir(root); err != nil {
		t.Fatalf("ExcludeClaudioDir (2nd, should be idempotent): %v", err)
	}

	excludePath := filepath.Join(root.MainClone, ".git", "info", "exclude")
	data, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read %s: %v", excludePath, err)
	}
	count := strings.Count(string(data), ".claudio/")
	if count != 1 {
		t.Errorf(".claudio/ appears %d times in %s, want exactly 1 (idempotent)", count, excludePath)
	}
}

func TestCopyEnvFileWritesToClaudioEnv(t *testing.T) {
	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	worktreeDir, err := AddWorktree(ctx, root, "brave-otter", "claudio/brave-otter", true)
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	hostEnvFile := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(hostEnvFile, []byte("API_KEY=abc123\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := CopyEnvFile(hostEnvFile, worktreeDir); err != nil {
		t.Fatalf("CopyEnvFile: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(worktreeDir, ".claudio", "env"))
	if err != nil {
		t.Fatalf("read copied env file: %v", err)
	}
	if string(got) != "API_KEY=abc123\n" {
		t.Errorf("copied content = %q, want %q", got, "API_KEY=abc123\n")
	}
}

func TestCopyEnvFileMissingSourceErrors(t *testing.T) {
	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	worktreeDir, err := AddWorktree(ctx, root, "brave-otter", "claudio/brave-otter", true)
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	err = CopyEnvFile(filepath.Join(t.TempDir(), "does-not-exist.env"), worktreeDir)
	if err == nil {
		t.Fatal("expected an error copying a nonexistent env file")
	}
}
