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
