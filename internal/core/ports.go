package core

import (
	"context"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/store"
)

// PortsStore is the subset of *store.Store AddPort/RemovePort need.
type PortsStore interface {
	GetInstance(ctx context.Context, idOrName string) (store.Instance, error)
	AllocatePort(ctx context.Context, instanceID string, containerPort int, serviceName string, source store.PortSource, detectedFrom *string, rangeLow, rangeHigh int) (int, error)
	ReleasePort(ctx context.Context, instanceID string, containerPort int) error
}

// AddPort reserves a new host port for an already-existing instance's
// container port — `claudio ports <id> --add <container>` (ROD-98).
// Recorded with store.PortManual, matching AdoptContainer's convention
// for a mapping the user asserted rather than one detection found.
//
// The reservation only ever touches the store: Docker cannot add a
// published port to a running container, so the caller (cmd/claudio)
// must warn that this takes effect on the instance's next `claudio
// restart`, not immediately — ROD-98's own doc calls this out as a
// known phase-1 limitation, not a bug to work around here.
func AddPort(ctx context.Context, st PortsStore, idOrName string, containerPort int, rangeLow, rangeHigh int) (hostPort int, err error) {
	inst, err := st.GetInstance(ctx, idOrName)
	if err != nil {
		return 0, fmt.Errorf("core: ports %s --add %d: %w", idOrName, containerPort, err)
	}
	hostPort, err = st.AllocatePort(ctx, inst.ID, containerPort, fmt.Sprintf("manual-%d", containerPort), store.PortManual, nil, rangeLow, rangeHigh)
	if err != nil {
		return 0, fmt.Errorf("core: ports %s --add %d: %w", inst.ID, containerPort, err)
	}
	return hostPort, nil
}

// RemovePort releases a host port reservation — `claudio ports <id>
// --remove <container>` (ROD-98). Same "takes effect on next restart"
// caveat as AddPort applies here too.
func RemovePort(ctx context.Context, st PortsStore, idOrName string, containerPort int) error {
	inst, err := st.GetInstance(ctx, idOrName)
	if err != nil {
		return fmt.Errorf("core: ports %s --remove %d: %w", idOrName, containerPort, err)
	}
	if err := st.ReleasePort(ctx, inst.ID, containerPort); err != nil {
		return fmt.Errorf("core: ports %s --remove %d: %w", inst.ID, containerPort, err)
	}
	return nil
}
