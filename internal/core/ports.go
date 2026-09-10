// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/store"
)

// describePortRangeExhausted replaces a bare store.ErrPortRangeExhausted
// with one that names the range that was actually exhausted and where to
// widen it — docs/architecture.md §6.2: "Exhausted range is a clear error
// naming the range and how to widen it, not a cryptic bind failure."
// Rendered here rather than in store.AllocatePort because the range is
// the caller's parameter, not the store's: the store is handed a low/high
// pair and has no idea it came from ports.range in the global config
// (§12.3's three-layer resolution puts that knowledge above the store).
//
// The original error is wrapped, not discarded, so errors.Is still
// identifies it as ErrPortRangeExhausted for any caller matching on the
// sentinel rather than on the message.
func describePortRangeExhausted(err error, rangeLow, rangeHigh int) error {
	if !errors.Is(err, store.ErrPortRangeExhausted) {
		return err
	}
	return fmt.Errorf("%w: every host port in %d-%d is taken — widen ports.range in ~/.claudio/config.yml, or free ports by destroying instances you no longer need (`claudio ls`)", err, rangeLow, rangeHigh)
}

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
// The port is also declared in the instance worktree's .claudio.yml,
// because that file — not the store — is what a restart re-derives its
// ports from (see allocatePorts). Without the declaration the store
// reservation would be dropped by the very restart the user is told to
// run, since stopping releases every reservation.
//
// Nothing is published until that restart: Docker cannot add a binding
// to a running container. The app inside the container has to be started
// again afterwards to listen on the port.
func AddPort(ctx context.Context, st PortsStore, idOrName string, containerPort int, rangeLow, rangeHigh int) (hostPort int, err error) {
	inst, err := st.GetInstance(ctx, idOrName)
	if err != nil {
		return 0, wrapGetInstance(fmt.Sprintf("core: ports --add %d", containerPort), idOrName, err)
	}
	op := fmt.Sprintf("core: ports %s --add %d", inst.ID, containerPort)

	serviceName := fmt.Sprintf("manual-%d", containerPort)

	// Declare it before reserving: a failure to record the port should be
	// a plain error, not a reservation that silently vanishes on restart.
	if err := config.AddPortToRepoConfig(inst.WorktreeDir, serviceName, containerPort); err != nil {
		if errors.Is(err, config.ErrPortAlreadyDeclared) {
			return 0, coreerr.Wrap(coreerr.Conflict, op, err)
		}
		return 0, coreerr.Wrap(coreerr.Internal, op+": declare in .claudio.yml", err)
	}

	hostPort, err = st.AllocatePort(ctx, inst.ID, containerPort, serviceName, store.PortManual, nil, rangeLow, rangeHigh)
	if err != nil {
		return 0, wrapAllocatePort(op, err, rangeLow, rangeHigh)
	}
	return hostPort, nil
}

// RemovePort releases a host port reservation — `claudio ports <id>
// --remove <container>` (ROD-98). Same "takes effect on next restart"
// caveat as AddPort applies here too.
func RemovePort(ctx context.Context, st PortsStore, idOrName string, containerPort int) error {
	inst, err := st.GetInstance(ctx, idOrName)
	if err != nil {
		return wrapGetInstance(fmt.Sprintf("core: ports --remove %d", containerPort), idOrName, err)
	}
	op := fmt.Sprintf("core: ports %s --remove %d", inst.ID, containerPort)
	if err := st.ReleasePort(ctx, inst.ID, containerPort); err != nil {
		code := coreerr.Internal
		if errors.Is(err, store.ErrPortNotMapped) {
			code = coreerr.NotFound
		}
		return coreerr.Wrap(code, op, err)
	}
	return nil
}
