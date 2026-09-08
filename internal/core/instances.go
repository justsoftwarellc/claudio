package core

import (
	"context"
	"fmt"
	"strconv"

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
	TransitionDesiredState(ctx context.Context, instanceID string, to store.DesiredState) error
	RecordEvent(ctx context.Context, instanceID string, kind store.EventKind, message string) error
}

// ListInstances merges the store's intent with Docker's observed reality
// — see docs/architecture.md §10.1. A container's running/exited status,
// and whether it was OOM-killed (ROD-112), are read live and never
// trusted from the store; that is what makes what's *printed* correct by
// construction when a container is killed out-of-band, without waiting
// for a background reconciler to notice.
//
// It also persists the one unambiguous correction from ROD-99's lazy
// reconciler (the "runs as part of whatever command next touches an
// instance" design, never a background loop in phase 1): an instance the
// store still calls StateRunning whose container is simply gone gets
// transitioned to StateStopped here, so the *stored* desired_state
// catches up too — e.g. so a later `claudio start` sees StateStopped
// instead of refusing with "instance is running, not stopped." This does
// not change what this call itself prints (the view already reflects
// live reality regardless), only what the next call sees.
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
		} else if inst.DesiredState == store.StateRunning && inst.ProvisionStep == store.StepHealthy {
			// ActionMarkStopped's case, applied inline (ROD-99): the store
			// still says running, but no container claims this instance —
			// it is gone, whether via `docker rm` out of band or an OOM
			// death that already got cleaned up. Guarded to StepHealthy
			// only: an instance still mid-provision (StepPending through
			// StepContainerUp) legitimately has no container yet — that is
			// not the same situation and must not be mistaken for one
			// (ActionResumeProvisioning is Reconcile's answer for that
			// case, not this correction). A failure here is not fatal to
			// the listing itself (the view already shows accurate live
			// reality via ContainerRunning=false, the default), only the
			// store's own bookkeeping falls further behind.
			if err := st.TransitionDesiredState(ctx, inst.ID, store.StateStopped); err == nil {
				view.DesiredState = store.StateStopped
				recordMarkStopped(ctx, st, inst.ID)
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
			CreatedAt:     parseLabelTimestamp(c.CreatedAt),
			Running:       c.Running,
		})
	}

	return views, untracked, nil
}

// eventRecorder is the sliver of InstanceStore and StatusStore that
// recordMarkStopped needs, so both call sites share one emitter rather
// than one interface widening to cover the other's methods.
type eventRecorder interface {
	RecordEvent(ctx context.Context, instanceID string, kind store.EventKind, message string) error
}

// recordMarkStopped logs the lazy reconciler's mark-stopped correction to
// the event log (docs/architecture.md §10.1), so a user who later asks
// why their instance is stopped can see that Claudio noticed the
// container was gone rather than someone having stopped it. The error is
// deliberately dropped for the same reason the transition's is: this
// runs on read paths (`claudio ls`, `claudio status`), and a lost log
// line must not turn a correct listing into a failed command.
func recordMarkStopped(ctx context.Context, st eventRecorder, instanceID string) {
	_ = st.RecordEvent(ctx, instanceID, store.EventReconciled,
		"container no longer exists; marked stopped")
}

// parseLabelTimestamp parses the claudio.created_at label, which engine
// stores verbatim as whatever string was written when the label was
// stamped (a Unix timestamp — see engine.LabelCreatedAt). An unparseable
// or absent label (a container Claudio didn't label the usual way) yields
// 0 rather than an error, since this only ever feeds a "created Nd ago"
// display, not a correctness-sensitive path.
func parseLabelTimestamp(v string) int64 {
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}
