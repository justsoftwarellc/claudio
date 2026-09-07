package store

import (
	"context"
	"fmt"
)

// ListInstances returns every instance row, regardless of desired_state.
// Callers merge this with live Docker state (docs/architecture.md
// §10.1) — this function only ever returns the store's *intent*.
func (s *Store) ListInstances(ctx context.Context) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, repo_url, repo_root, worktree_dir, branch, commit_sha,
		       image, container_id, runtime_profile, desired_state, provision_step,
		       created_at, last_active
		FROM instances ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("store: list instances: %w", err)
	}
	defer rows.Close()

	var out []Instance
	for rows.Next() {
		var inst Instance
		if err := rows.Scan(&inst.ID, &inst.Name, &inst.RepoURL, &inst.RepoRoot, &inst.WorktreeDir,
			&inst.Branch, &inst.CommitSHA, &inst.Image, &inst.ContainerID, &inst.RuntimeProfile,
			&inst.DesiredState, &inst.ProvisionStep, &inst.CreatedAt, &inst.LastActive); err != nil {
			return nil, fmt.Errorf("store: scan instance: %w", err)
		}
		out = append(out, inst)
	}
	return out, rows.Err()
}

// GetInstance looks up one instance by ID or name.
func (s *Store) GetInstance(ctx context.Context, idOrName string) (Instance, error) {
	var inst Instance
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, repo_url, repo_root, worktree_dir, branch, commit_sha,
		       image, container_id, runtime_profile, desired_state, provision_step,
		       created_at, last_active
		FROM instances WHERE id = ? OR name = ?`, idOrName, idOrName).
		Scan(&inst.ID, &inst.Name, &inst.RepoURL, &inst.RepoRoot, &inst.WorktreeDir,
			&inst.Branch, &inst.CommitSHA, &inst.Image, &inst.ContainerID, &inst.RuntimeProfile,
			&inst.DesiredState, &inst.ProvisionStep, &inst.CreatedAt, &inst.LastActive)
	if err != nil {
		return Instance{}, fmt.Errorf("store: get instance %q: %w", idOrName, err)
	}
	return inst, nil
}
