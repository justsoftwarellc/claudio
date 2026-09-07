package core

import (
	"context"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/store"
)

// StatusStore is the subset of *store.Store GetInstanceView needs.
type StatusStore interface {
	GetInstance(ctx context.Context, idOrName string) (store.Instance, error)
	PortMappings(ctx context.Context, instanceID string) ([]store.PortMapping, error)
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
		return InstanceView{}, fmt.Errorf("core: status %s: %w", idOrName, err)
	}
	ports, err := st.PortMappings(ctx, inst.ID)
	if err != nil {
		return InstanceView{}, fmt.Errorf("core: status %s: %w", inst.ID, err)
	}

	view := InstanceView{Instance: inst, Ports: ports}

	if inst.ContainerID != nil && *inst.ContainerID != "" {
		containers, err := engine.ListClaudioContainers(ctx, dockerHost, true)
		if err != nil {
			return InstanceView{}, fmt.Errorf("core: status %s: list containers: %w", inst.ID, err)
		}
		for _, cs := range containers {
			if cs.ContainerID != *inst.ContainerID {
				continue
			}
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

	return view, nil
}
