package core

import (
	"context"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/store"
)

// InstanceStore is the subset of *store.Store that core.ListInstances
// needs. Defined as an interface so core stays testable without a real
// SQLite file — see the daemon-readiness discipline in
// docs/architecture.md §12.4: core depends on behavior, not concrete
// infrastructure types.
type InstanceStore interface {
	ListInstances(ctx context.Context) ([]store.Instance, error)
	PortMappings(ctx context.Context, instanceID string) ([]store.PortMapping, error)
}

// ListInstances merges the store's intent with Docker's observed reality
// — see docs/architecture.md §10.1. A container's running/exited status,
// and whether it was OOM-killed (ROD-112), are read live and never
// trusted from the store; that is what makes this call correct by
// construction when a container is killed out-of-band, rather than
// needing a background reconciler to notice.
//
// dockerHost is the resolved runtime.docker_host from global config
// (empty = auto-resolve, see engine.DetectRuntime).
func ListInstances(ctx context.Context, st InstanceStore, dockerHost string) ([]InstanceView, []UntrackedContainer, error) {
	instances, err := st.ListInstances(ctx)
	if err != nil {
		return nil, nil, err
	}

	containers, err := engine.ListClaudioContainers(ctx, dockerHost, true)
	if err != nil {
		return nil, nil, fmt.Errorf("core: list containers: %w", err)
	}
	byInstanceID := make(map[string]engine.ContainerState, len(containers))
	claimed := make(map[string]bool, len(containers))
	for _, c := range containers {
		if c.InstanceID != "" {
			byInstanceID[c.InstanceID] = c
		}
	}

	views := make([]InstanceView, 0, len(instances))
	for _, inst := range instances {
		ports, err := st.PortMappings(ctx, inst.ID)
		if err != nil {
			return nil, nil, err
		}

		view := InstanceView{Instance: inst, Ports: ports}
		if cs, ok := byInstanceID[inst.ID]; ok {
			claimed[cs.ContainerID] = true
			view.ContainerRunning = cs.Running
			view.ContainerStatus = cs.Status
			if !cs.Running {
				oom, err := engine.InspectOOMKilled(ctx, dockerHost, cs.ContainerID)
				if err == nil {
					view.OOMKilled = oom
				}
				// Inspect failures (container removed between list and
				// inspect) are not fatal to ListInstances as a whole — the
				// view simply reports OOMKilled=false, same as "unknown".
			}
		}
		views = append(views, view)
	}

	// A container carrying Claudio's labels that no store row claimed is
	// untracked: DB lost, deleted out of band, or restored from backup.
	// Per ROD-99 this is surfaced for the user to `adopt` or `forget` —
	// never auto-resolved, because reconstructing risks resurrecting a
	// deliberately-discarded instance and removing risks destroying work.
	var untracked []UntrackedContainer
	for _, c := range containers {
		if claimed[c.ContainerID] {
			continue
		}
		untracked = append(untracked, UntrackedContainer{
			ContainerID:   c.ContainerID,
			ContainerName: c.Name,
			RepoURL:       c.RepoURL,
			Running:       c.Running,
		})
	}

	return views, untracked, nil
}
