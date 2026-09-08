// Package core's reconciler settles disagreements between SQLite's
// intent and Docker's observed reality — docs/architecture.md §10.1 and
// ROD-99. Phase 1 has no daemon watching the Docker event stream, so
// reconciliation is lazy: it runs as part of whatever command next
// touches an instance (`ls`, `status`), not continuously. The event-
// stream subscriber lands with the daemon in ROD-101.
package core

import (
	"context"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/store"
)

// ReconcileActionKind names what Reconcile decided needed to happen. Every
// action is either applied immediately (MarkStopped, ResumeProvisioning)
// or surfaced for a human decision (FlagUntracked) — docs/architecture.md
// §10.1 and ROD-99 are explicit that an orphaned container is never
// auto-adopted or auto-removed.
type ReconcileActionKind string

const (
	// ActionMarkStopped: the store said an instance should be running but
	// Docker has no matching container anymore. Applied automatically —
	// there is nothing to ask the user, the container is simply gone.
	ActionMarkStopped ReconcileActionKind = "mark_stopped"

	// ActionFlagUntracked: a container carries Claudio's labels but no
	// store row claims it. Surfaced only; resolved by adopt or forget.
	ActionFlagUntracked ReconcileActionKind = "flag_untracked"

	// ActionResumeProvisioning: the store says an instance is mid
	// provisioning (a non-terminal ProvisionStep) at the start of a
	// command run — most likely a previous process crashed or was killed
	// mid-provision. Surfaced so the caller can resume the state machine
	// from provision_step rather than silently leaving it stuck.
	ActionResumeProvisioning ReconcileActionKind = "resume_provisioning"
)

type ReconcileAction struct {
	Kind       ReconcileActionKind
	InstanceID string             // set for MarkStopped, ResumeProvisioning
	Untracked  UntrackedContainer // set for FlagUntracked
}

// Reconcile compares every store instance against live Docker state and
// returns the actions needed to converge them, applying the ones that
// have an unambiguous right answer (MarkStopped) and merely reporting the
// ones that need a human (FlagUntracked, ResumeProvisioning).
//
// This intentionally does not call ListInstances itself even though the
// logic overlaps: ListInstances optimizes for one Docker round-trip to
// serve `ls`, while Reconcile needs to also inspect instances that ls
// wouldn't otherwise care about (e.g. STOPPED/DESTROYED rows) and to
// decide provisioning resume — different enough call shapes that sharing
// the Docker listing directly would tangle two call sites together for a
// small amount of duplication.
func Reconcile(ctx context.Context, st InstanceStore, dockerHost string) ([]ReconcileAction, error) {
	instances, err := st.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	containers, err := engine.ListClaudioContainers(ctx, dockerHost, true)
	if err != nil {
		return nil, fmt.Errorf("core: reconcile: list containers: %w", err)
	}

	byInstanceID := make(map[string]engine.ContainerState, len(containers))
	claimed := make(map[string]bool, len(containers))
	for _, c := range containers {
		if c.InstanceID != "" {
			byInstanceID[c.InstanceID] = c
		}
	}

	var actions []ReconcileAction
	for _, inst := range instances {
		if inst.DesiredState == store.StateDestroyed {
			continue // terminal; nothing to reconcile
		}

		cs, hasContainer := byInstanceID[inst.ID]
		if hasContainer {
			claimed[cs.ContainerID] = true
		}

		switch {
		case inst.DesiredState == store.StateRunning && !hasContainer && inst.ProvisionStep == store.StepHealthy:
			// Was fully up, container is gone now: Docker's reality has
			// moved past what the store still believes.
			actions = append(actions, ReconcileAction{Kind: ActionMarkStopped, InstanceID: inst.ID})

		case inst.ProvisionStep != store.StepHealthy && inst.ProvisionStep != store.StepFailed:
			// PENDING or any of the intermediate steps at the start of a
			// command run means the previous process never reached
			// StepHealthy or StepFailed — most likely killed mid-provision.
			actions = append(actions, ReconcileAction{Kind: ActionResumeProvisioning, InstanceID: inst.ID})
		}
	}

	for _, c := range containers {
		if claimed[c.ContainerID] {
			continue
		}
		actions = append(actions, ReconcileAction{
			Kind: ActionFlagUntracked,
			Untracked: UntrackedContainer{
				ContainerID:   c.ContainerID,
				ContainerName: c.Name,
				RepoURL:       c.RepoURL,
				CreatedAt:     parseLabelTimestamp(c.CreatedAt),
				Running:       c.Running,
			},
		})
	}

	return actions, nil
}

// ApplyMarkStopped is the one Reconcile action with an unambiguous
// automatic resolution — see ActionMarkStopped's doc. Kept as a separate
// call rather than folded into Reconcile itself so a caller can inspect
// every action (e.g. for a `claudio status` printout) before anything
// mutates the store.
//
// Emits the same event as the inline correction in ListInstances and
// GetInstanceView: this is the path a future caller walking Reconcile's
// actions would use (ROD-101's daemon, most likely), and a mark-stopped
// that leaves no trace in one path but not the others would make the
// event log's completeness depend on which entry point happened to run.
func ApplyMarkStopped(ctx context.Context, st *store.Store, instanceID string) error {
	if err := st.TransitionDesiredState(ctx, instanceID, store.StateStopped); err != nil {
		return err
	}
	recordMarkStopped(ctx, st, instanceID)
	return nil
}
