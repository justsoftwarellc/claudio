// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

// Package coreerr defines the typed error codes core's exported
// operations return, per docs/architecture.md §12.4's interface
// discipline: "errors carry a typed code, not just a string." This is
// scoped to the Client boundary — the operations client.Client calls —
// not to every internal helper failure in store/repo/engine, since those
// never cross the phase-2 daemon's wire format on their own; core wraps
// them into a coreerr.Error once, at the point it returns to its caller.
//
// Phase 1 uses Code only to let a caller (cmd/claudio) render an
// actionable message and pick an exit code without string-matching.
// Phase 2's HTTP layer maps Code to a status (e.g. NotFound -> 404,
// InvalidInput -> 400, Conflict -> 409) — that mapping does not exist
// yet and is out of this package's scope; it only needs Code to exist
// and be stable.
package coreerr

import (
	"errors"
	"fmt"
)

// Code names a class of failure a core operation can produce. Kept
// small and closed to the failure classes core's operations actually
// produce today — see each constant's doc for the operations that use
// it. Add a new one only when an operation needs a caller to react
// differently to it than to any existing code; do not add a code "for
// completeness."
type Code string

const (
	// NotFound: the instance, container, or repo an operation named does
	// not exist. Wraps store.GetInstance's sql.ErrNoRows and
	// core.AdoptContainer's "no such labelled container" case.
	NotFound Code = "not_found"

	// AmbiguousID: an ID prefix matched more than one instance — see
	// store.ErrAmbiguousID. The caller should show the candidates rather
	// than retry.
	AmbiguousID Code = "ambiguous_id"

	// InvalidInput: the caller supplied something core rejects outright
	// before touching any external system — an unparseable branch name,
	// an unsupported --ports form, mutually exclusive flags. Phase 2 maps
	// this to 400: retrying with the same input will never succeed.
	InvalidInput Code = "invalid_input"

	// Conflict: the operation collides with existing state that isn't
	// simply "not found" — a branch already checked out elsewhere
	// (repo.BranchCollisionError), a port range exhausted
	// (store.ErrPortRangeExhausted), an instance in the wrong
	// desired_state for the requested transition
	// (store.ErrInvalidTransition, or StartInstance's "not stopped"
	// check).
	Conflict Code = "conflict"

	// CloneFailed: repo.EnsureRoot/InitRoot could not clone or initialize
	// a repo root — a network failure, an invalid remote, a permissions
	// problem on the workspace root. Distinct from NotFound: the repo URL
	// was well-formed, git itself failed.
	CloneFailed Code = "clone_failed"

	// ProvisionFailed: something failed after the repo/branch/worktree
	// stage — port allocation, container creation, post_create — the
	// broad "provisioning didn't reach StepHealthy" bucket. Phase 1 does
	// not yet subdivide this further; see CreateInstance's provisioning
	// stages in docs/architecture.md §4.1 for what can fail here.
	ProvisionFailed Code = "provision_failed"

	// Unavailable: the runtime itself couldn't be reached or used —
	// Docker daemon unreachable, no credential to start Claude Code. Not
	// the caller's input being wrong; the environment isn't ready.
	Unavailable Code = "unavailable"

	// Internal: anything that doesn't fit the above — a store write that
	// should never fail (e.g. after a value the code itself just
	// validated), a bug rather than an expected failure mode. Phase 2
	// maps this to 500.
	Internal Code = "internal"
)

// Error is what every core.* operation that client.Client calls returns
// on failure. Op names the operation for a log/error message
// (docs/architecture.md's `core: <op> <id>: ...` convention, kept
// verbatim so error text doesn't change shape); Cause is the underlying
// error, always preserved for errors.Is/As and for %+v-style debugging —
// Code exists to let a caller branch without inspecting Cause's type or
// text.
type Error struct {
	Code  Code
	Op    string
	Cause error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: %s", e.Op, e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Op, e.Cause)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

// Wrap builds a coreerr.Error, or returns nil unchanged so call sites
// can write `return coreerr.Wrap(...)` unconditionally after an `if err
// != nil` without an extra branch for the nil case.
func Wrap(code Code, op string, cause error) error {
	if cause == nil {
		return nil
	}
	return &Error{Code: code, Op: op, Cause: cause}
}

// Is reports whether err is a *coreerr.Error carrying code — the
// intended way for a caller (cmd/claudio, or a future HTTP handler) to
// branch on failure class without depending on message text. Walks the
// error chain via errors.As, so it still matches through any %w wrapping
// added above this package's own Wrap call.
func Is(err error, code Code) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == code
}

// CodeOf returns the code of the first *coreerr.Error in err's chain,
// and false if none is found — for a caller that wants the code itself
// rather than a yes/no answer against one specific value.
func CodeOf(err error) (Code, bool) {
	var e *Error
	if !errors.As(err, &e) {
		return "", false
	}
	return e.Code, true
}
