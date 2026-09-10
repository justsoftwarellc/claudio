// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package store

import (
	"context"
	"fmt"
)

// NewInstanceParams is everything the caller (core.CreateInstance, once
// ROD-100 lands) knows before any provisioning has happened: the repo
// root and worktree path already exist on disk (internal/repo), but no
// ports are reserved yet and no container exists. desired_state starts at
// StateRunning and provision_step at StepPending — see
// TransitionDesiredState's doc for why PENDING/PROVISIONING aren't
// desired_state values.
type NewInstanceParams struct {
	ID             string
	Name           *string
	RepoURL        string
	RepoRoot       string
	WorktreeDir    string
	Branch         string
	Image          string
	RuntimeProfile string
	CreatedAt      int64
}

// CreateInstance inserts a new instance row in StepPending. Returns the
// underlying SQLite constraint error unchanged (wrapped) on a duplicate
// id or name, rather than pre-checking — the UNIQUE indexes are the
// source of truth and a pre-check would just race against a concurrent
// `create` the same way port allocation would without BEGIN IMMEDIATE.
func (s *Store) CreateInstance(ctx context.Context, p NewInstanceParams) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO instances (id, name, repo_url, repo_root, worktree_dir, branch,
		                        image, runtime_profile, desired_state, provision_step,
		                        created_at, last_active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.RepoURL, p.RepoRoot, p.WorktreeDir, p.Branch,
		p.Image, p.RuntimeProfile, StateRunning, StepPending, p.CreatedAt, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("store: create instance %s: %w", p.ID, err)
	}
	return nil
}

// SetContainerID records the container Docker created for this instance,
// once provisioning reaches StepContainerUp.
func (s *Store) SetContainerID(ctx context.Context, instanceID, containerID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET container_id = ? WHERE id = ?`, containerID, instanceID)
	if err != nil {
		return fmt.Errorf("store: set container id for %s: %w", instanceID, err)
	}
	return nil
}

// DeleteInstance removes an instance row entirely. Only valid once
// desired_state is StateDestroyed (docs/architecture.md §4.1: DESTROYED
// is what removes the container; the row itself is cleaned up once the
// caller has finished tearing down the worktree and container). Deleting
// cascades to port_mappings and events via their FK ON DELETE CASCADE.
func (s *Store) DeleteInstance(ctx context.Context, instanceID string) error {
	inst, err := s.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	if inst.DesiredState != StateDestroyed {
		return fmt.Errorf("%w: cannot delete instance %s in desired_state %s (must be %s)",
			ErrInvalidTransition, instanceID, inst.DesiredState, StateDestroyed)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM instances WHERE id = ?`, instanceID); err != nil {
		return fmt.Errorf("store: delete instance %s: %w", instanceID, err)
	}
	return nil
}
