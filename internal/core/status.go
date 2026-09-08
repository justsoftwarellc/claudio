package core

import (
	"context"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/store"
)

// StatusStore is the subset of *store.Store GetInstanceView needs.
type StatusStore interface {
	GetInstance(ctx context.Context, idOrName string) (store.Instance, error)
	PortMappings(ctx context.Context, instanceID string) ([]store.PortMapping, error)
	TransitionDesiredState(ctx context.Context, instanceID string, to store.DesiredState) error
	RecordEvent(ctx context.Context, instanceID string, kind store.EventKind, message string) error
}

// GetInstanceView resolves one instance (by ID, name, or the store's own
// exact-match lookup — see store.GetInstance) merged with live Docker
// state, for `claudio status <id>`. Deliberately not implemented by
// filtering ListInstances' output: that would mean one full container
// listing plus fetching every instance's port mappings just to look at
// one, where this needs only the single instance's own row and ports —
// see docs/architecture.md §10.1, the same "observed state is read live,
// never trusted from the store" contract ListInstances follows.
func GetInstanceView(ctx context.Context, st StatusStore, dockerHost, idOrName string) (InstanceView, error) {
	inst, err := st.GetInstance(ctx, idOrName)
	if err != nil {
		return InstanceView{}, wrapGetInstance("core: status", idOrName, err)
	}
	op := fmt.Sprintf("core: status %s", inst.ID)
	ports, err := st.PortMappings(ctx, inst.ID)
	if err != nil {
		return InstanceView{}, coreerr.Wrap(coreerr.Internal, op, err)
	}

	view := InstanceView{Instance: inst, Ports: ports}
	foundContainer := false

	if inst.ContainerID != nil && *inst.ContainerID != "" {
		containers, err := engine.ListClaudioContainers(ctx, dockerHost, true)
		if err != nil {
			return InstanceView{}, coreerr.Wrap(coreerr.Unavailable, op+": list containers", err)
		}
		for _, cs := range containers {
			if cs.ContainerID != *inst.ContainerID {
				continue
			}
			foundContainer = true
			view.ContainerRunning = cs.Running
			view.ContainerStatus = cs.Status
			if !cs.Running {
				if oom, err := engine.InspectOOMKilled(ctx, dockerHost, cs.ContainerID); err == nil {
					view.OOMKilled = oom
				}
			}
			break
		}
	}

	// Same inline correction ListInstances applies (ROD-99's lazy
	// reconciler), same StepHealthy guard (see ListInstances' comment for
	// why: an instance still mid-provision legitimately has no container
	// yet, which is not this situation): the store still says running,
	// but no container backs this instance any more — persist
	// StateStopped so the next command (e.g. `claudio start`) sees
	// accurate stored intent. A failure here is not fatal to this status
	// view itself.
	if !foundContainer && inst.DesiredState == store.StateRunning && inst.ProvisionStep == store.StepHealthy {
		if err := st.TransitionDesiredState(ctx, inst.ID, store.StateStopped); err == nil {
			view.DesiredState = store.StateStopped
			recordMarkStopped(ctx, st, inst.ID)
		}
	}

	return view, nil
}
