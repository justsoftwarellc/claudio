package core

import (
	"context"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/store"
)

// AdoptStore is the subset of *store.Store that AdoptContainer needs,
// mirroring InstanceStore's pattern of depending on behavior rather than
// the concrete store type (docs/architecture.md §12.4).
type AdoptStore interface {
	CreateInstance(ctx context.Context, p store.NewInstanceParams) error
	AllocatePort(ctx context.Context, instanceID string, containerPort int, serviceName string, source store.PortSource, detectedFrom *string, rangeLow, rangeHigh int) (int, error)
}

// AdoptContainer reconstructs a store row for a container flagged
// ActionFlagUntracked, from Docker's own labels and published ports —
// never from a guess. See docs/architecture.md §10.1 and ROD-99: adopt is
// a deliberate user decision, never automatic, because reconstructing the
// wrong thing risks resurrecting an instance the user meant to discard.
//
// containerID's ports are re-reserved via AllocatePort at each port's
// *current* host binding rather than picking a fresh one from the range —
// re-allocating would change the URL a user may already have bookmarked
// or have running in a browser tab, for a container that was working
// fine before its row went missing. The probe inside AllocatePort will
// simply confirm the already-bound port is reachable rather than finding
// it "free", since the container itself is holding it.
//
// dockerHost identifies which daemon to inspect the container on;
// instanceID is derived from the container's own claudio.instance.id
// label (the adoption's whole point is trusting the label, not
// generating a new identity).
func AdoptContainer(ctx context.Context, st AdoptStore, dockerHost, containerID string, createdAt int64) (instanceID string, err error) {
	containers, err := engine.ListClaudioContainers(ctx, dockerHost, true)
	if err != nil {
		return "", fmt.Errorf("core: adopt: list containers: %w", err)
	}
	var target *engine.ContainerState
	for i := range containers {
		if containers[i].ContainerID == containerID {
			target = &containers[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("core: adopt: no Claudio-labelled container %s found", containerID)
	}
	if target.InstanceID == "" {
		return "", fmt.Errorf("core: adopt: container %s has no claudio.instance.id label", containerID)
	}

	if err := st.CreateInstance(ctx, store.NewInstanceParams{
		ID:             target.InstanceID,
		RepoURL:        target.RepoURL,
		RepoRoot:       "", // unknown from labels alone; left for the user to fix via `claudio repair` if needed
		WorktreeDir:    "",
		Branch:         "",
		Image:          "",
		RuntimeProfile: "",
		CreatedAt:      createdAt,
	}); err != nil {
		return "", fmt.Errorf("core: adopt: create instance row: %w", err)
	}

	ports, err := engine.InspectPublishedPorts(ctx, dockerHost, containerID)
	if err != nil {
		return "", fmt.Errorf("core: adopt: inspect published ports: %w", err)
	}
	for _, p := range ports {
		if _, err := st.AllocatePort(ctx, target.InstanceID, p.ContainerPort,
			fmt.Sprintf("port-%d", p.ContainerPort), store.PortManual, nil, p.HostPort, p.HostPort); err != nil {
			return "", fmt.Errorf("core: adopt: re-reserve port %d: %w", p.HostPort, err)
		}
	}

	return target.InstanceID, nil
}

// ForgetContainer removes an untracked container permanently — the other
// half of ROD-99's adopt/forget pair, for when the user decides an
// orphaned container is not worth reconstructing a row for.
func ForgetContainer(ctx context.Context, dockerHost, containerID string) error {
	return engine.RemoveContainer(ctx, dockerHost, containerID)
}
