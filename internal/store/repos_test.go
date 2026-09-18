// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package store

import (
	"context"
	"testing"
)

func TestUpsertRepoInsertsThenUpdates(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if err := s.UpsertRepo(ctx, "/root/acme", "git@github.com:acme/web.git"); err != nil {
		t.Fatalf("UpsertRepo (insert): %v", err)
	}
	repos, err := s.ListRepos(ctx)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 1 || repos[0].RepoURL != "git@github.com:acme/web.git" {
		t.Fatalf("ListRepos = %+v, want one repo with acme/web URL", repos)
	}

	// Re-upserting the same root_path with a different URL updates in place
	// rather than erroring or duplicating.
	if err := s.UpsertRepo(ctx, "/root/acme", "git@github.com:acme/web2.git"); err != nil {
		t.Fatalf("UpsertRepo (update): %v", err)
	}
	repos, err = s.ListRepos(ctx)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 1 || repos[0].RepoURL != "git@github.com:acme/web2.git" {
		t.Fatalf("ListRepos after update = %+v, want updated URL", repos)
	}
}

func TestTouchRepoFetchAndDelete(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if err := s.UpsertRepo(ctx, "/root/acme", "git@github.com:acme/web.git"); err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}
	if err := s.TouchRepoFetch(ctx, "/root/acme", 1234); err != nil {
		t.Fatalf("TouchRepoFetch: %v", err)
	}
	repos, err := s.ListRepos(ctx)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 1 || repos[0].LastFetchAt == nil || *repos[0].LastFetchAt != 1234 {
		t.Fatalf("ListRepos = %+v, want last_fetch_at=1234", repos)
	}

	if err := s.DeleteRepo(ctx, "/root/acme"); err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	repos, err = s.ListRepos(ctx)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("ListRepos after delete = %+v, want empty", repos)
	}
}

// TestUpsertRepoRecordsSourceDir covers ROD-133's core premise: the store
// is what knows where a repo's local .claudio.yml lives. Without a
// persisted source_dir the path can only be re-derived by string-parsing
// repo_url, which is exactly the ambiguity the issue is about.
func TestUpsertRepoRecordsSourceDir(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if err := s.UpsertRepoWithSource(ctx, "/root/acme", "file:///Users/me/web", "/Users/me/web"); err != nil {
		t.Fatalf("UpsertRepoWithSource: %v", err)
	}

	got, err := s.GetRepo(ctx, "/root/acme")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if got.SourceDir == nil {
		t.Fatalf("GetRepo().SourceDir = nil, want /Users/me/web")
	}
	if *got.SourceDir != "/Users/me/web" {
		t.Fatalf("GetRepo().SourceDir = %q, want /Users/me/web", *got.SourceDir)
	}
}

// A remote-sourced repo has no source directory at all, and that must be
// distinguishable from "a source dir that happens to be empty" — the
// resolver branches on exactly this to decide whether to fall back to the
// worktree.
func TestUpsertRepoLeavesSourceDirNullForRemote(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if err := s.UpsertRepo(ctx, "/root/acme", "git@github.com:acme/web.git"); err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}

	got, err := s.GetRepo(ctx, "/root/acme")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if got.SourceDir != nil {
		t.Fatalf("GetRepo().SourceDir = %q, want nil for a remote source", *got.SourceDir)
	}
}

// Re-running `claudio create .` against the same folder must not blank a
// recorded source dir, and a repo that later gains one must pick it up.
func TestUpsertRepoSourceDirIsStickyThenUpdatable(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if err := s.UpsertRepoWithSource(ctx, "/root/acme", "file:///Users/me/web", "/Users/me/web"); err != nil {
		t.Fatalf("UpsertRepoWithSource: %v", err)
	}
	// A plain UpsertRepo (e.g. a later create from the remote URL) must not
	// erase what is already known about where config lives.
	if err := s.UpsertRepo(ctx, "/root/acme", "git@github.com:acme/web.git"); err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}
	got, err := s.GetRepo(ctx, "/root/acme")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if got.SourceDir == nil || *got.SourceDir != "/Users/me/web" {
		t.Fatalf("SourceDir = %v, want it preserved as /Users/me/web", got.SourceDir)
	}

	// Moving the folder is a real event: a new source dir replaces the old.
	if err := s.UpsertRepoWithSource(ctx, "/root/acme", "file:///Users/me/web2", "/Users/me/web2"); err != nil {
		t.Fatalf("UpsertRepoWithSource (move): %v", err)
	}
	got, err = s.GetRepo(ctx, "/root/acme")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if got.SourceDir == nil || *got.SourceDir != "/Users/me/web2" {
		t.Fatalf("SourceDir = %v, want updated to /Users/me/web2", got.SourceDir)
	}
}

// GetRepo on an unknown root is a normal "not found", not an error: an
// instance created before this migration has no repos row to find.
func TestGetRepoUnknownRoot(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	_, err := s.GetRepo(ctx, "/root/nope")
	if err == nil {
		t.Fatal("GetRepo on unknown root = nil error, want a not-found error")
	}
}
