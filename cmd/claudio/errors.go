// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"database/sql"
	"errors"

	"github.com/rodrigomorales/claudio/internal/coreerr"
)

// describeErr renders an error for CLI output. Every coreerr code except
// NotFound already carries a Cause written to be read directly by a user
// (see each call site: Conflict's port-range-exhausted text, Unavailable's
// "image ... run `claudio image build`", InvalidInput's validation
// messages, AmbiguousID's "%q matches %v") — those pass through with just
// the "core: <op>:" prefix stripped.
//
// NotFound is the one code shared between a hand-written message
// (adopt's "no Claudio-labelled container found", image's "does not
// exist — run...") and store.GetInstance's bare sql.ErrNoRows, which
// leaks straight through Error() as "sql: no rows in result set" — an
// internal driver detail, not something a user can act on
// (docs/architecture.md §12.4 / ROD-100: "errors carry typed codes ...
// and render as actionable sentences: what failed, and what to do").
// Only that specific underlying cause gets rewritten; every other
// NotFound message passes through unchanged.
//
// Any non-coreerr error (flag parsing, os errors) is returned as-is.
func describeErr(err error) string {
	if err == nil {
		return ""
	}
	var ce *coreerr.Error
	if !errors.As(err, &ce) {
		return err.Error()
	}
	if ce.Code == coreerr.NotFound && errors.Is(ce.Cause, sql.ErrNoRows) {
		return "no such instance"
	}
	return ce.Cause.Error()
}
