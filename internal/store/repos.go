// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Repo is a row in the repos table: one per cloned root
// (docs/architecture.md §5.1), independent of how many instances/worktrees
// currently exist under it. Tracked separately from instances so a root
// with zero live worktrees is still known to `claudio gc` (ROD-107)
// rather than being invisible once its last instance is destroyed.
type Repo struct {
	RootPath string
	RepoURL  string
	// SourceDir is the user's own folder for a repo created with
	// `claudio create .` — the directory they stand in, and the one place
	// this repo's local .claudio.yml lives (ROD-133). nil for a repo
	// cloned from a remote URL, which has no such folder; that is what
	// tells config resolution to fall back to the worktree rather than
	// guessing at a path.
	SourceDir   *string
	LastFetchAt *int64
}

// UpsertRepo records that rootPath holds a clone of repoURL. Called after
// repo.EnsureRoot/InitRoot succeeds, so the store's view of known roots
// stays in sync with what actually exists on disk without duplicating any
// git state itself (docs/architecture.md §10.1: the store holds intent,
// not observable reality — here "intent" is just "this root exists and
// maps to this repo").
func (s *Store) UpsertRepo(ctx context.Context, rootPath, repoURL string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO repos (root_path, repo_url) VALUES (?, ?)
		ON CONFLICT(root_path) DO UPDATE SET repo_url = excluded.repo_url`,
		rootPath, repoURL)
	if err != nil {
		return fmt.Errorf("store: upsert repo %s: %w", rootPath, err)
	}
	return nil
}

// UpsertRepoWithSource records a root that was created from a local
// directory (`claudio create .`), remembering that directory as the home
// of this repo's local .claudio.yml (ROD-133).
//
// Separate from UpsertRepo rather than a nullable parameter on it so the
// plain call can never blank a known source directory by omission: a
// later `claudio create git@github.com:acme/web.git` against a root that
// was first created from a folder still has config living in that folder,
// and silently forgetting it there would resurrect exactly the
// "which file is this reading?" ambiguity this column exists to end.
// Passing a new sourceDir does replace the old one — a moved folder is a
// real event, and the user's config moved with it.
func (s *Store) UpsertRepoWithSource(ctx context.Context, rootPath, repoURL, sourceDir string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO repos (root_path, repo_url, source_dir) VALUES (?, ?, ?)
		ON CONFLICT(root_path) DO UPDATE SET
			repo_url = excluded.repo_url,
			source_dir = excluded.source_dir`,
		rootPath, repoURL, sourceDir)
	if err != nil {
		return fmt.Errorf("store: upsert repo %s: %w", rootPath, err)
	}
	return nil
}

// GetRepo returns the tracking row for rootPath. A root with no row is an
// error rather than a zero value: callers use this to find where config
// lives, and "I don't know this root" must not be indistinguishable from
// "this root has no source directory" (which is a nil SourceDir on a row
// that does exist).
func (s *Store) GetRepo(ctx context.Context, rootPath string) (Repo, error) {
	var r Repo
	err := s.db.QueryRowContext(ctx,
		`SELECT root_path, repo_url, source_dir, last_fetch_at FROM repos WHERE root_path = ?`,
		rootPath).Scan(&r.RootPath, &r.RepoURL, &r.SourceDir, &r.LastFetchAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Repo{}, fmt.Errorf("store: no repo tracked at %s", rootPath)
	}
	if err != nil {
		return Repo{}, fmt.Errorf("store: get repo %s: %w", rootPath, err)
	}
	return r, nil
}

// TouchRepoFetch records that rootPath's main clone was just fetched.
func (s *Store) TouchRepoFetch(ctx context.Context, rootPath string, at int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE repos SET last_fetch_at = ? WHERE root_path = ?`, at, rootPath)
	if err != nil {
		return fmt.Errorf("store: touch repo fetch %s: %w", rootPath, err)
	}
	return nil
}

// ListRepos returns every known root, regardless of how many worktrees
// currently exist under it — used by `claudio gc` (ROD-107) to find roots
// with zero live instances.
func (s *Store) ListRepos(ctx context.Context) ([]Repo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT root_path, repo_url, source_dir, last_fetch_at FROM repos ORDER BY root_path`)
	if err != nil {
		return nil, fmt.Errorf("store: list repos: %w", err)
	}
	defer rows.Close()

	var out []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.RootPath, &r.RepoURL, &r.SourceDir, &r.LastFetchAt); err != nil {
			return nil, fmt.Errorf("store: scan repo: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRepo removes a root's tracking row (after `claudio gc` deletes it
// from disk). Instances referencing this root_path are expected to already
// be gone — destroy always precedes gc (docs/architecture.md §5.1: "the
// root and its objects stay" after a single instance's destroy).
func (s *Store) DeleteRepo(ctx context.Context, rootPath string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM repos WHERE root_path = ?`, rootPath)
	if err != nil {
		return fmt.Errorf("store: delete repo %s: %w", rootPath, err)
	}
	return nil
}
