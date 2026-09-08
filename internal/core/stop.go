package core

import (
	"context"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/store"
)

// StopStore is the subset of *store.Store StopInstance needs.
type StopStore interface {
	GetInstance(ctx context.Context, idOrName string) (store.Instance, error)
	TransitionDesiredState(ctx context.Context, instanceID string, to store.DesiredState) error
}

// StopInstance removes an instance's container and transitions it to
// StateStopped, which releases its port reservations (see
// TransitionDesiredState's doc). The worktree and home/ directory are
// left untouched — docs/architecture.md §4.1: "STOPPED keeps the
// workspace on disk" — so `claudio start` on the same instance has
// something to resume into.
//
// Unlike DestroyInstance, a missing container is not an error here
// either: engine.RemoveContainer already treats "already gone" as
// success (see its doc), which matters just as much for stop as for
// destroy — an instance already stopped out of band should still
// transition cleanly rather than getting stuck.
func StopInstance(ctx context.Context, st StopStore, dockerHost, idOrName string) error {
	inst, err := st.GetInstance(ctx, idOrName)
	if err != nil {
		return wrapGetInstance("core: stop", idOrName, err)
	}
	op := fmt.Sprintf("core: stop %s", inst.ID)

	if inst.ContainerID != nil && *inst.ContainerID != "" {
		if err := engine.RemoveContainer(ctx, dockerHost, *inst.ContainerID); err != nil {
			return coreerr.Wrap(coreerr.Unavailable, op+": remove container", err)
		}
	}

	if err := st.TransitionDesiredState(ctx, inst.ID, store.StateStopped); err != nil {
		return coreerr.Wrap(coreerr.Conflict, op, err)
	}
	return nil
}
