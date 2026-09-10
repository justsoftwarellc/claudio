// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package repo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SourceKind classifies where a root's commits ultimately come from,
// which decides whether `claudio create` refreshes main-clone before
// cutting a worktree and whether it may do so without asking.
//
// The distinction exists because main-clone is cloned exactly once and
// then reused for every subsequent create against the same source (see
// EnsureRoot). Without a refresh, every instance after the first
// branches from whatever the source held at first-clone time, and grows
// staler for the life of the repo — silently, since nothing about the
// worktree looks wrong.
type SourceKind int

const (
	// SourceRemote is a real remote URL the user named directly
	// (git@github.com:acme/web.git and the HTTPS/shorthand forms
	// NormalizeSSH rewrites into one). The remote is authoritative and
	// the user asked for it by name, so a create refreshes from it
	// without prompting.
	SourceRemote SourceKind = iota

	// SourceLocalWithUpstream is a local directory (`claudio create .`)
	// that is itself a clone — it has an origin. The upstream is
	// authoritative, but which branch to base an instance on is a real
	// question here in a way it is not for SourceRemote: the user is
	// standing in a working copy that may be on any branch, so this
	// prompts rather than assuming.
	SourceLocalWithUpstream

	// SourceLocalOnly is a local directory with no origin, or a
	// greenfield `--new` root. There is nothing to fetch from, so a
	// create skips the refresh entirely.
	//
	// Classification is redone on every create rather than recorded at
	// create time, so a prototype that later gains an origin (the user
	// pushed it to GitHub) starts being refreshed on its next create
	// with no migration and nothing for the user to re-run.
	SourceLocalOnly
)

func (k SourceKind) String() string {
	switch k {
	case SourceRemote:
		return "remote"
	case SourceLocalWithUpstream:
		return "local-with-upstream"
	default:
		return "local-only"
	}
}

// Source describes a root's origin for refresh purposes.
type Source struct {
	Kind SourceKind

	// FetchURL is what to fetch from: the remote URL itself for
	// SourceRemote, and the *source directory's own origin* for
	// SourceLocalWithUpstream — not the source directory, which is only
	// main-clone's clone parent. Empty for SourceLocalOnly.
	FetchURL string

	// SourceDir is the local directory behind a file:// source, used to
	// report what was inspected. Empty for SourceRemote.
	SourceDir string
}

// ClassifySource inspects repoURL — the resolved form stored on the
// instance, so file://<path> for a local source — and reports how a
// create should refresh it.
//
// A local source's upstream is read live from the directory rather than
// from main-clone: cloning does not copy remotes (verified — main-clone's
// own origin points at the source directory, never at the source's
// GitHub), so main-clone has no record of the real upstream at all.
// Reading it live is also what lets a repo that gains an origin later be
// picked up without any migration.
func ClassifySource(ctx context.Context, repoURL string) Source {
	if repoURL == "" || strings.HasPrefix(repoURL, "local:") {
		// Greenfield `claudio create --new`: a synthetic URL, never
		// clonable, with no upstream to consult.
		return Source{Kind: SourceLocalOnly}
	}
	if !strings.HasPrefix(repoURL, "file://") {
		return Source{Kind: SourceRemote, FetchURL: repoURL}
	}

	dir := strings.TrimPrefix(repoURL, "file://")

	// A source directory that has been moved or deleted since the
	// instance was created has nothing to fetch from. That is a skip,
	// not a failure: the clone on disk is still perfectly usable, and a
	// vanished source should not block creating an instance.
	if _, err := os.Stat(dir); err != nil {
		return Source{Kind: SourceLocalOnly, SourceDir: dir}
	}

	// A file:// source that is not a working copy — a bare repo, or one
	// with no checkout — is a remote in every way that matters: nobody
	// edits it in place, so it is authoritative and there is no "which
	// branch are you standing on" question to ask. Only a real working
	// copy (`claudio create .`) is the prompting case.
	if !isWorkingCopy(ctx, dir) {
		return Source{Kind: SourceRemote, FetchURL: repoURL, SourceDir: dir}
	}

	upstream := originURL(ctx, dir)
	if upstream == "" {
		return Source{Kind: SourceLocalOnly, SourceDir: dir}
	}
	return Source{Kind: SourceLocalWithUpstream, FetchURL: upstream, SourceDir: dir}
}

// isWorkingCopy reports whether dir is a git checkout someone could be
// working in, as opposed to a bare repository (or a path that is not a
// repo at all). Bare repos answer "true" to --is-bare-repository and
// have no working tree to be standing on.
func isWorkingCopy(ctx context.Context, dir string) bool {
	if _, err := os.Stat(dir); err != nil {
		return false
	}
	out, err := runGit(ctx, dir, "rev-parse", "--is-bare-repository")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "false"
}

// originURL returns dir's origin remote URL, or "" if it has none (or
// is not a readable git repo — a source directory that has been moved
// or deleted since the instance was created is a skip, not a failure:
// nothing about a stale origin should block creating an instance).
func originURL(ctx context.Context, dir string) string {
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	out, err := runGit(ctx, dir, "config", "--get", "remote.origin.url")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// DefaultBranch reports the branch main-clone currently has checked
// out, which is the branch a worktree is cut from and therefore the one
// a refresh has to move. main-clone always holds this branch: it is a
// normal (non-bare) checkout, and git structurally forbids any worktree
// from checking out the same branch — verified, `git worktree add` on it
// fails with "already checked out". That is what makes resetting it
// safe: it is the one branch no instance can be working on.
func DefaultBranch(ctx context.Context, root Root) (string, error) {
	out, err := runGit(ctx, root.MainClone, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("repo: read default branch of %s: %w", root.MainClone, err)
	}
	branch := strings.TrimSpace(out)
	if branch == "" || branch == "HEAD" {
		return "", fmt.Errorf("repo: %s is not on a named branch", root.MainClone)
	}
	return branch, nil
}

// RemoteBranches lists the branch names available at fetchURL, in git's
// own order. Used to offer the user a choice for
// SourceLocalWithUpstream rather than making them recall what exists
// upstream.
//
// A failure to reach the remote returns an error the caller can degrade
// on (fall back to asking for a name, or skip the refresh) rather than
// aborting the create.
func RemoteBranches(ctx context.Context, fetchURL string) ([]string, error) {
	out, err := runGit(ctx, "", "ls-remote", "--heads", fetchURL)
	if err != nil {
		return nil, fmt.Errorf("repo: list branches at %s: %w", fetchURL, err)
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		_, ref, ok := strings.Cut(strings.TrimSpace(line), "refs/heads/")
		if !ok || ref == "" {
			continue
		}
		branches = append(branches, ref)
	}
	return branches, nil
}

// RefreshMainClone fetches branch from src and makes it main-clone's
// checked-out state, so a worktree cut afterwards starts from the
// current upstream instead of from whatever was there at first clone.
//
// The reset is deliberate and is why this takes the whole Root rather
// than a bare path: fetching alone moves nothing a worktree would see,
// and moving the ref alone (git update-ref) leaves main-clone's working
// tree and index stale — verified, that produces phantom staged
// deletions in `git status` for every file the new commits added.
// `reset --hard` is the only form that leaves main-clone coherent.
//
// It cannot destroy user work: main-clone is Claudio-managed, never
// handed to the user or a container, and holds the one branch no
// instance worktree is permitted to check out (see DefaultBranch).
func RefreshMainClone(ctx context.Context, root Root, src Source, branch string) error {
	if src.Kind == SourceLocalOnly || src.FetchURL == "" {
		return nil
	}
	if branch == "" {
		return fmt.Errorf("repo: refresh %s: no branch given", root.MainClone)
	}

	if _, err := runGit(ctx, root.MainClone, "fetch", src.FetchURL, branch); err != nil {
		return fmt.Errorf("repo: fetch %s from %s: %w", branch, src.FetchURL, err)
	}
	if _, err := runGit(ctx, root.MainClone, "reset", "--hard", "FETCH_HEAD"); err != nil {
		return fmt.Errorf("repo: reset %s to fetched %s: %w", root.MainClone, branch, err)
	}
	return nil
}

// SourceDirFor returns the local directory behind a file:// repo URL,
// or "" for a remote or greenfield one.
func SourceDirFor(repoURL string) string {
	if !strings.HasPrefix(repoURL, "file://") {
		return ""
	}
	return filepath.Clean(strings.TrimPrefix(repoURL, "file://"))
}
