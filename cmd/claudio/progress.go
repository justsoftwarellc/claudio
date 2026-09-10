// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"fmt"
	"os"

	"github.com/rodrigomorales/claudio/internal/core"
)

// terminalProgress is phase 1's caller-supplied progress sink
// (docs/architecture.md §12.4: "phase 1 passes a terminal writer") for
// create/start/restart — the long-running commands whose provisioning
// would otherwise sit silent until it either finishes or fails several
// seconds later. Each event prints one line to stderr (stdout is
// reserved for the command's final structured result, e.g. once --json
// exists), so redirecting stdout doesn't capture progress noise.
func terminalProgress() core.ProgressFunc {
	return func(event core.ProgressEvent) {
		fmt.Fprintf(os.Stderr, "  [%s] %s\n", event.Step, event.Message)
	}
}
