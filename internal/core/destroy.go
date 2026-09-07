package core

import (
	"context"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/repo"
	"github.com/rodrigomorales/claudio/internal/store"
)

// DestroyStore is the subset of *store.Store DestroyInstance needs.
type DestroyStore interface {
	GetInstance(ctx context.Context, idOrName string) (store.Instance, error)
	TransitionDesiredState(ctx context.Context, instanceID string, to store.DesiredState) error
	DeleteInstance(ctx context.Context, instanceID string) error
}

// DestroyParams bundles what DestroyInstance needs beyond the instance
// ID: the repo Root to remove the worktree from (WorkspaceRoot resolves
// it the same way CreateInstance did) and whether to keep the worktree
// on disk.
type DestroyParams struct {
	IDOrName      string
	WorkspaceRoot string
	KeepWorkspace bool // docs/architecture.md: `destroy --keep-workspace` leaves the worktree on disk
	DockerHost    string
}

// DestroyInstance tears down one instance: removes its container (if
// any), removes its git worktree (unless KeepWorkspace), transitions the
// store row to StateDestroyed, then deletes the row entirely — mirroring
// docs/architecture.md §4.1's "DESTROYED is what removes the container;
// the row itself is cleaned up once the caller has finished tearing down
// the worktree and container" (store.DeleteInstance's doc).
//
// Container removal happens before the state transition so a failure
// there (e.g. Docker unreachable) leaves the instance retryable from
// `claudio destroy` again rather than silently orphaning the container
// with a store row that already claims it's gone.
func DestroyInstance(ctx context.Context, st DestroyStore, params DestroyParams) error {
	inst, err := st.GetInstance(ctx, params.IDOrName)
	if err != nil {
		return fmt.Errorf("core: destroy %s: %w", params.IDOrName, err)
	}

	if inst.ContainerID != nil && *inst.ContainerID != "" {
		if err := engine.RemoveContainer(ctx, params.DockerHost, *inst.ContainerID); err != nil {
			return fmt.Errorf("core: destroy %s: remove container: %w", inst.ID, err)
		}
	}

	if !params.KeepWorkspace && inst.RepoRoot != "" {
		root, err := repo.EnsureRoot(ctx, params.WorkspaceRoot, inst.RepoURL)
		if err != nil {
			return fmt.Errorf("core: destroy %s: resolve repo root: %w", inst.ID, err)
		}
		if err := repo.RemoveWorktree(ctx, root, inst.ID); err != nil {
			return fmt.Errorf("core: destroy %s: remove worktree: %w", inst.ID, err)
		}
	}

	if err := st.TransitionDesiredState(ctx, inst.ID, store.StateDestroyed); err != nil {
		return fmt.Errorf("core: destroy %s: %w", inst.ID, err)
	}
	if err := st.DeleteInstance(ctx, inst.ID); err != nil {
		return fmt.Errorf("core: destroy %s: %w", inst.ID, err)
	}
	return nil
}
