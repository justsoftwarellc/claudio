// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"fmt"
	"os"

	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/store"
)

// StartStore is the subset of *store.Store StartInstance needs. A
// superset of CreateStore (start re-provisions a container the same way
// create does) plus the desired_state transition create never needs.
type StartStore interface {
	CreateStore
	TransitionDesiredState(ctx context.Context, instanceID string, to store.DesiredState) error
}

// StartInstance brings a StateStopped instance back up: resets its
// provision_step to StepPending, re-runs port detection/allocation and
// container creation against the instance's existing worktree and
// branch (StopInstance never touched either), and transitions back to
// StateRunning. There is no cheaper path than a full re-provision —
// StopInstance already removed the old container and released its
// ports, so a new container needs new port bindings exactly like create
// does.
//
// params carries the same resolved-config fields CreateParams does
// (image, resources, port range, docker host, env) — the caller
// (client.Local.Start) fills these in from global config the same way
// it does for Create, so StartInstance itself needs no separate params
// type. RepoURL/Branch/Name/NewBranch are ignored if set; only the
// resolved-config fields are read.
//
// fresh, when true, wipes the instance's home/ directory before
// recreating the container — docs/architecture.md §4.1/ROD-99's
// `--fresh` flag: the default behavior instead resumes the existing
// Claude Code session, since transcripts and settings persist in home/
// across a container rebuild (verified working in an earlier session).
//
// progress is CreateInstance's same caller-supplied sink
// (docs/architecture.md §12.4) — a start/restart re-runs the same
// clone-through-container-start work a create does, so it deserves the
// same visibility. May be nil.
func StartInstance(ctx context.Context, st StartStore, params CreateParams, idOrName string, fresh bool, progress ProgressFunc) (CreateResult, error) {
	return startInstanceWithCmd(ctx, st, params, idOrName, fresh, nil, progress)
}

// startInstanceWithCmd is StartInstance's real implementation, taking the
// same test-only engine.CreateSpec.Cmd override as createInstanceWithCmd
// — see that function's doc for why. Production callers always go
// through StartInstance.
func startInstanceWithCmd(ctx context.Context, st StartStore, params CreateParams, idOrName string, fresh bool, cmd []string, progress ProgressFunc) (CreateResult, error) {
	inst, err := st.GetInstance(ctx, idOrName)
	if err != nil {
		return CreateResult{}, wrapGetInstance("core: start", idOrName, err)
	}
	op := fmt.Sprintf("core: start %s", inst.ID)
	if inst.DesiredState != store.StateStopped {
		return CreateResult{}, coreerr.Wrap(coreerr.Conflict, op,
			fmt.Errorf("instance is %s, not %s — nothing to start", inst.DesiredState, store.StateStopped))
	}

	if fresh {
		homeDir := inst.WorktreeDir + ".home"
		if err := os.RemoveAll(homeDir); err != nil {
			return CreateResult{}, coreerr.Wrap(coreerr.Internal, op+": --fresh: remove home dir", err)
		}
		// provisionContainer below recreates homeDir via os.MkdirAll before
		// calling engine.CreateAndStart, same as a brand new instance.
	}

	if err := st.TransitionProvisionStep(ctx, inst.ID, store.StepPending); err != nil {
		return CreateResult{}, coreerr.Wrap(coreerr.Internal, op, err)
	}

	containerID, ports, err := provisionContainer(ctx, st, inst.ID, inst.RepoURL, inst.RepoRoot, inst.WorktreeDir, inst.CreatedAt, params, cmd, false, progress)
	if err != nil {
		return CreateResult{}, err // provisionContainer already marks StepFailed; already a *coreerr.Error
	}

	if err := st.TransitionDesiredState(ctx, inst.ID, store.StateRunning); err != nil {
		return CreateResult{}, coreerr.Wrap(coreerr.Internal, op, err)
	}

	mappings := make([]store.PortMapping, 0, len(ports))
	for _, p := range ports {
		mappings = append(mappings, store.PortMapping{
			InstanceID:    inst.ID,
			ContainerPort: p.Container,
			HostPort:      p.HostPort,
			ServiceName:   p.ServiceName,
			Source:        p.Source,
			DetectedFrom:  p.DetectedFrom,
		})
	}

	return CreateResult{
		InstanceID:  inst.ID,
		Branch:      inst.Branch,
		WorktreeDir: inst.WorktreeDir,
		ContainerID: containerID,
		Ports:       mappings,
	}, nil
}

// RestartInstance is StopInstance followed by StartInstance — `claudio
// restart <id> [--fresh]`. Kept as a single call so the CLI command
// doesn't need to sequence the two itself and so a failure between stop
// and start is reported as one coherent "restart failed", not a
// half-finished stop the caller has to notice on its own.
func RestartInstance(ctx context.Context, st interface {
	StopStore
	StartStore
}, dockerHost string, params CreateParams, idOrName string, fresh bool, progress ProgressFunc) (CreateResult, error) {
	if err := StopInstance(ctx, st, dockerHost, idOrName); err != nil {
		return CreateResult{}, fmt.Errorf("core: restart: %w", err)
	}
	result, err := StartInstance(ctx, st, params, idOrName, fresh, progress)
	if err != nil {
		return CreateResult{}, fmt.Errorf("core: restart: %w", err)
	}
	return result, nil
}
