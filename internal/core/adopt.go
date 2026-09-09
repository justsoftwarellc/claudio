package core

import (
	"context"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/store"
)

// AdoptStore is the subset of *store.Store that AdoptContainer needs,
// mirroring InstanceStore's pattern of depending on behavior rather than
// the concrete store type (docs/architecture.md §12.4).
type AdoptStore interface {
	CreateInstance(ctx context.Context, p store.NewInstanceParams) error
	ReservePort(ctx context.Context, instanceID string, containerPort, hostPort int, serviceName string, source store.PortSource, detectedFrom *string) error
}

// AdoptContainer reconstructs a store row for a container flagged
// ActionFlagUntracked, from Docker's own labels and published ports —
// never from a guess. See docs/architecture.md §10.1 and ROD-99: adopt is
// a deliberate user decision, never automatic, because reconstructing the
// wrong thing risks resurrecting an instance the user meant to discard.
//
// containerID's ports are re-reserved via ReservePort at each port's
// *current* host binding rather than picking a fresh one from the range —
// re-allocating would change the URL a user may already have bookmarked
// or have running in a browser tab, for a container that was working
// fine before its row went missing.
//
// This deliberately does not go through AllocatePort, whose bind() probe
// asks "is this port free?" — the wrong question here, and one that
// always answers no: the container being adopted is itself listening on
// the port. That mistake made every adopt of a port-publishing container
// fail with "port range exhausted" (ROD-119). ReservePort records the
// binding as the fact it is, while still refusing to overwrite another
// instance's claim on the same host port.
//
// dockerHost identifies which daemon to inspect the container on;
// instanceID is derived from the container's own claudio.instance.id
// label (the adoption's whole point is trusting the label, not
// generating a new identity).
func AdoptContainer(ctx context.Context, st AdoptStore, dockerHost, containerID string, createdAt int64) (instanceID string, err error) {
	op := fmt.Sprintf("core: adopt %s", containerID)
	containers, err := engine.ListClaudioContainers(ctx, dockerHost, true)
	if err != nil {
		return "", coreerr.Wrap(coreerr.Unavailable, op+": list containers", err)
	}
	var target *engine.ContainerState
	for i := range containers {
		if containers[i].ContainerID == containerID {
			target = &containers[i]
			break
		}
	}
	if target == nil {
		return "", coreerr.Wrap(coreerr.NotFound, op, fmt.Errorf("no Claudio-labelled container %s found", containerID))
	}
	if target.InstanceID == "" {
		return "", coreerr.Wrap(coreerr.InvalidInput, op, fmt.Errorf("container %s has no claudio.instance.id label", containerID))
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
		return "", coreerr.Wrap(coreerr.Internal, op+": create instance row", err)
	}

	ports, err := engine.InspectPublishedPorts(ctx, dockerHost, containerID)
	if err != nil {
		return "", coreerr.Wrap(coreerr.Unavailable, op+": inspect published ports", err)
	}
	for _, p := range ports {
		// ReservePort, not AllocatePort: this records a binding the
		// container already holds rather than claiming a free one, so a
		// bind() probe would necessarily fail — the container being adopted
		// is what is listening on the port (ROD-119).
		if err := st.ReservePort(ctx, target.InstanceID, p.ContainerPort, p.HostPort,
			fmt.Sprintf("port-%d", p.ContainerPort), store.PortManual, nil); err != nil {
			return "", coreerr.Wrap(coreerr.Conflict, fmt.Sprintf("%s: re-reserve port %d", op, p.HostPort), err)
		}
	}

	return target.InstanceID, nil
}

// ForgetContainer removes an untracked container permanently — the other
// half of ROD-99's adopt/forget pair, for when the user decides an
// orphaned container is not worth reconstructing a row for.
func ForgetContainer(ctx context.Context, dockerHost, containerID string) error {
	if err := engine.RemoveContainer(ctx, dockerHost, containerID); err != nil {
		return coreerr.Wrap(coreerr.Unavailable, fmt.Sprintf("core: forget %s", containerID), err)
	}
	return nil
}
