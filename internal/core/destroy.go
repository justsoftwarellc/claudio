package core

import (
	"context"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/coreerr"
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
// ID: whether to keep the worktree on disk, and which Docker daemon to
// remove the container on. The repo Root itself is reconstructed from
// the stored instance row's RepoRoot (repo.RootFromPath), not derived
// from a workspace root here — see RootFromPath's doc for why that
// matters for a greenfield instance.
type DestroyParams struct {
	IDOrName      string
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
		return wrapGetInstance("core: destroy", params.IDOrName, err)
	}
	op := fmt.Sprintf("core: destroy %s", inst.ID)

	if inst.ContainerID != nil && *inst.ContainerID != "" {
		if err := engine.RemoveContainer(ctx, params.DockerHost, *inst.ContainerID); err != nil {
			return coreerr.Wrap(coreerr.Unavailable, op+": remove container", err)
		}
	}

	if !params.KeepWorkspace && inst.RepoRoot != "" {
		// RootFromPath, not EnsureRoot: the root already exists (this
		// instance was created from it) and its path is already known
		// from the stored row, so there's nothing to clone-or-init.
		// EnsureRoot would instead try to treat inst.RepoURL as a
		// clonable remote, which breaks for a greenfield instance's
		// synthetic "local:<name>" RepoURL (see RootFromPath's doc).
		root := repo.RootFromPath(inst.RepoRoot)
		if err := repo.RemoveWorktree(ctx, root, inst.ID); err != nil {
			return coreerr.Wrap(coreerr.Internal, op+": remove worktree", err)
		}
	}

	if err := st.TransitionDesiredState(ctx, inst.ID, store.StateDestroyed); err != nil {
		return coreerr.Wrap(coreerr.Conflict, op, err)
	}
	if err := st.DeleteInstance(ctx, inst.ID); err != nil {
		return coreerr.Wrap(coreerr.Internal, op, err)
	}
	return nil
}
