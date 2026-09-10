// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import "github.com/rodrigomorales/claudio/internal/store"

// ProgressEvent is one step of CreateInstance/StartInstance's
// provisioning becoming visible to the caller — docs/architecture.md
// §12.4: "progress is a caller-supplied sink rather than a print. create
// is long-running (clone, pull, start). The caller passes a
// writer/callback: phase 1 a terminal writer, phase 2 the HTTP response
// stream, with the same core signature." Step mirrors store.ProvisionStep
// (the state machine already names exactly the stages worth reporting)
// so a caller correlating progress against `claudio status` sees the
// same vocabulary in both places.
type ProgressEvent struct {
	Step    store.ProvisionStep `json:"step"`
	Message string              `json:"message"`
}

// ProgressFunc is the caller-supplied sink itself. Deliberately not a
// field on CreateParams: docs/architecture.md §12.4 says request/response
// types are plain serializable structs with "no func values" — a
// progress callback is neither part of the request nor the response, it
// is a side channel core calls into as it works, the same role an
// io.Writer would play if the interface allowed one. Phase 1's
// cmd/claudio supplies a closure that prints to the terminal; phase 2's
// daemon would supply one that writes an SSE chunk to the HTTP response,
// per the doc's own example — same CreateInstance signature either way.
//
// A nil ProgressFunc is valid and means "no progress reporting" — every
// call site nil-checks before invoking it, so existing callers that
// don't care about progress (most of core's own tests) pass nil rather
// than a no-op closure.
type ProgressFunc func(ProgressEvent)

// reportProgress calls fn if it is non-nil — the one-line guard every
// provisioning call site uses, so the nil-check isn't repeated at each
// of the ~6 stages CreateInstance/StartInstance report.
func reportProgress(fn ProgressFunc, step store.ProvisionStep, message string) {
	if fn == nil {
		return
	}
	fn(ProgressEvent{Step: step, Message: message})
}

// shortContainerID truncates a full 64-char container ID to Docker's own
// short form for a progress message — matching cmd/claudio/ls.go's
// shortID so the two displays agree, without core depending on cmd
// (core stays I/O-free; this is string formatting, not I/O).
func shortContainerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
