package store

import (
	"context"
	"errors"
	"fmt"
)

// ErrInvalidTransition is returned by TransitionProvisionStep and
// TransitionDesiredState when the requested change is not reachable from
// the instance's current state, per the lifecycle in
// docs/architecture.md §4.1.
var ErrInvalidTransition = errors.New("store: invalid lifecycle transition")

// provisionStepEdges enumerates the sub-state machine a PENDING instance
// walks through to become healthy (docs/architecture.md §4.1's numbered
// steps 1-8, collapsed into the ProvisionStep enum). Each step is
// idempotent by design, so re-entering the same step (a crashed process
// resuming) is allowed and is a no-op here rather than an error — only a
// genuinely backward or skipped-forward move is rejected.
var provisionStepEdges = map[ProvisionStep][]ProvisionStep{
	StepPending:     {StepPending, StepRepoReady, StepFailed},
	StepRepoReady:   {StepRepoReady, StepPortsReady, StepFailed},
	StepPortsReady:  {StepPortsReady, StepConfigReady, StepFailed},
	StepConfigReady: {StepConfigReady, StepContainerUp, StepFailed},
	StepContainerUp: {StepContainerUp, StepHealthy, StepFailed},
	// StepHealthy -> StepPending: not a dead end after all — `claudio
	// stop` followed by `claudio start` (or `restart`, ROD-99/ROD-100)
	// re-provisions a fresh container for the same instance row, which
	// walks the sub-state machine from the top again. "Terminal" only
	// meant "nothing left to do while this container is up", not "this
	// row can never provision again."
	StepHealthy: {StepHealthy, StepPending},
	StepFailed:  {StepFailed, StepPending},
}

// desiredStateEdges is the top-level lifecycle from docs/architecture.md
// §4.1: RUNNING and STOPPED cycle against each other; DESTROYED is
// terminal. There is no explicit PROVISIONING/DESTROYING row here because
// those are represented by (DesiredState=StateRunning,
// ProvisionStep=<in progress>) and a direct transition to StateDestroyed
// respectively — see TransitionDesiredState's doc.
var desiredStateEdges = map[DesiredState][]DesiredState{
	StateRunning:   {StateRunning, StateStopped, StateDestroyed},
	StateStopped:   {StateStopped, StateRunning, StateDestroyed},
	StateDestroyed: {StateDestroyed}, // terminal
}

// TransitionProvisionStep validates and applies a provision_step change.
// Returns ErrInvalidTransition (wrapped with the attempted edge) if step
// is not reachable from the instance's current step — e.g. jumping
// straight from StepPending to StepContainerUp, which would skip
// port/config materialization that later steps depend on.
func (s *Store) TransitionProvisionStep(ctx context.Context, instanceID string, step ProvisionStep) error {
	inst, err := s.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	if !stepAllowed(inst.ProvisionStep, step) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, inst.ProvisionStep, step)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE instances SET provision_step = ? WHERE id = ?`, step, instanceID)
	if err != nil {
		return fmt.Errorf("store: transition %s provision_step to %s: %w", instanceID, step, err)
	}
	return nil
}

func stepAllowed(from, to ProvisionStep) bool {
	for _, allowed := range provisionStepEdges[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// TransitionDesiredState validates and applies a desired_state change —
// the RUNNING/STOPPED/DESTROYED half of docs/architecture.md §4.1.
// PENDING and PROVISIONING are not desired_state values at all: an
// instance's desired_state is StateRunning from creation onward, and
// ProvisionStep (above) tracks how far it has gotten toward that; only
// stop/destroy actually change desired_state. Stopping releases port
// reservations (docs/architecture.md §4.1: "STOPPED ... releases the port
// reservations"); destroying does not touch ports here since destroy
// also removes the row's ports via the port_mappings FK ON DELETE CASCADE
// once the instance itself is deleted by the caller.
func (s *Store) TransitionDesiredState(ctx context.Context, instanceID string, to DesiredState) error {
	inst, err := s.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	if !stateAllowed(inst.DesiredState, to) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, inst.DesiredState, to)
	}

	if to == StateStopped {
		if err := s.ReleasePorts(ctx, instanceID); err != nil {
			return err
		}
	}

	_, err = s.db.ExecContext(ctx, `UPDATE instances SET desired_state = ? WHERE id = ?`, to, instanceID)
	if err != nil {
		return fmt.Errorf("store: transition %s desired_state to %s: %w", instanceID, to, err)
	}
	return nil
}

func stateAllowed(from, to DesiredState) bool {
	for _, allowed := range desiredStateEdges[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// MarkFailed transitions an instance directly to StepFailed regardless of
// its current provision_step — provisioning can fail at any of the eight
// steps in docs/architecture.md §4.1, so every step allows this edge (see
// provisionStepEdges), but this helper skips the lookup-then-validate
// dance for the common "something in the middle of provisioning threw"
// call site.
func (s *Store) MarkFailed(ctx context.Context, instanceID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET provision_step = ? WHERE id = ?`, StepFailed, instanceID)
	if err != nil {
		return fmt.Errorf("store: mark %s failed: %w", instanceID, err)
	}
	return nil
}
