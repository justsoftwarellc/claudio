package core

import (
	"errors"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/store"
)

// wrapGetInstance classifies a store.GetInstance failure for the six
// core operations that resolve an <id> argument before doing anything
// else (destroy, status, stop, start, ports add/remove). GetInstance's
// only failure mode is "no such instance" — sql.ErrNoRows for an exact
// miss, or store.ErrAmbiguousID for a prefix matching more than one row
// — so this is the one call site in each of those files that needs to
// distinguish the two rather than flattening both to NotFound.
func wrapGetInstance(op, idOrName string, err error) error {
	if errors.Is(err, store.ErrAmbiguousID) {
		return coreerr.Wrap(coreerr.AmbiguousID, fmt.Sprintf("%s %s", op, idOrName), err)
	}
	return coreerr.Wrap(coreerr.NotFound, fmt.Sprintf("%s %s", op, idOrName), err)
}

// wrapAllocatePort classifies a store.AllocatePort failure, shared by
// core.AddPort and CreateInstance's allocatePorts: a full range is
// Conflict (the caller's own ports.range config is the fix — see
// describePortRangeExhausted), anything else is an unexpected store
// failure.
func wrapAllocatePort(op string, err error, rangeLow, rangeHigh int) error {
	described := describePortRangeExhausted(err, rangeLow, rangeHigh)
	code := coreerr.Internal
	if errors.Is(err, store.ErrPortRangeExhausted) {
		code = coreerr.Conflict
	}
	return coreerr.Wrap(code, op, described)
}
