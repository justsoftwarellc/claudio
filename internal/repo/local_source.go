// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package repo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveSource turns `claudio create`'s first argument into a source
// git can clone from, so a local directory works everywhere a remote URL
// does (ROD-115). A remote form — SSH, HTTPS, or the "acme/web"
// shorthand — is returned unchanged; a local directory becomes
// "file://<absolute path>", which EnsureRoot and Slug already handle.
//
// A directory that is not yet a git repo is `git init`ed and its
// existing contents committed, so `claudio create .` works on
// not-yet-versioned work without the user having to set up git first.
//
// docs/architecture.md §5.1 originally ruled local-path cloning out
// because it "invites confusion about whether uncommitted work and
// local-only branches come along." Verified empirically rather than
// assumed, `git clone <path>` answers that cleanly, and the answer is
// the one a user would want:
//
//   - Uncommitted and staged work does NOT come along. The instance
//     starts from committed history, so an agent can never be handed a
//     half-finished edit the user has not decided to keep.
//   - Local-only branches DO come along, as remote-tracking refs off the
//     source, so nothing has to be pushed anywhere first.
//   - The clone is self-contained (no .git/objects/info/alternates), so
//     work inside the sandbox cannot corrupt the source repo's objects.
//
// The source directory is otherwise left untouched: this never commits
// on the user's behalf in a repo that already exists, and never writes
// into their working tree.
func ResolveSource(ctx context.Context, input string) (string, error) {
	if !looksLocal(input) {
		return input, nil
	}

	expanded, err := expandHome(input)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("repo: resolve %q: %w", input, err)
	}
	// Resolve symlinks so the slug — and therefore the on-disk root — is
	// the same whether the user typed the symlink or its target.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("repo: %q is not a directory or a recognized repo URL", input)
		}
		return "", fmt.Errorf("repo: stat %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repo: %q is a file, not a directory", input)
	}

	if !isGitRepo(ctx, abs) {
		if err := initSourceRepo(ctx, abs); err != nil {
			return "", err
		}
	}
	return "file://" + abs, nil
}

// looksLocal reports whether input should be treated as a filesystem
// path rather than a remote repo URL.
//
// The check is deliberately conservative in one direction: "acme/web" is
// a documented GitHub shorthand (§5.1), so a bare relative path is only
// treated as local when it is explicitly path-shaped ("." , "..",
// "./x", "~/x") or absolute. That keeps the shorthand working even in
// the unlucky case where a directory named "acme/web" happens to exist
// in the cwd — which would otherwise silently change what an existing
// command means.
func looksLocal(input string) bool {
	switch {
	case input == "":
		return false
	case strings.Contains(input, "://"):
		return false // ssh://, https://, file:// — already a URL
	case strings.HasPrefix(input, "git@"):
		return false
	case input == "." || input == "..":
		return true
	case strings.HasPrefix(input, "/"), strings.HasPrefix(input, "./"), strings.HasPrefix(input, "../"), strings.HasPrefix(input, "~/"):
		return true
	default:
		return false
	}
}

func isGitRepo(ctx context.Context, dir string) bool {
	out, err := runGit(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// initSourceRepo git-inits dir and commits whatever is already there.
//
// The commit is not optional: EnsureRoot clones this directory, and
// cloning a repo with no commits produces a clone with no branch for a
// worktree to check out. Committing the existing contents also keeps the
// promise ResolveSource's doc comment makes — what is in the directory
// when the user runs `claudio create .` is what reaches the instance.
func initSourceRepo(ctx context.Context, dir string) error {
	if _, err := runGit(ctx, dir, "init"); err != nil {
		return fmt.Errorf("repo: git init %s: %w", dir, err)
	}
	if _, err := runGit(ctx, dir, "add", "-A"); err != nil {
		return fmt.Errorf("repo: stage %s: %w", dir, err)
	}
	// --allow-empty so an empty directory still gets the branch-bearing
	// commit the clone needs.
	if _, err := runGit(ctx, dir, "commit", "--allow-empty", "-m", "Initial commit"); err != nil {
		return fmt.Errorf("repo: initial commit in %s: %w", dir, err)
	}
	return nil
}

// expandHome resolves a leading "~" against the user's home directory.
// A "~/path" argument normally arrives already expanded by the shell,
// but a quoted argument or a programmatic caller does not — and without
// this, "~" would be taken as a literal directory name and resolve to
// ./~/path.
func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("repo: resolve home directory: %w", err)
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}
