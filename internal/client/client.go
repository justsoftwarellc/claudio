// Package client defines the interface every CLI command depends on.
// Phase 1 wires local.Client (in-process, calls core directly); phase 2
// wires remote.Client (HTTP+JSON over a Unix socket, once claudiod
// exists — see docs/architecture.md §12.4). Commands never depend on
// core directly, so the daemon lands as an additive change, not a
// rewrite: swap which Client newClient() constructs, and every command
// keeps working unmodified.
package client

import (
	"context"

	"github.com/rodrigomorales/claudio/internal/core"
)

// Client is deliberately small right now — only what phase 1's commands
// need. Grows alongside ROD-97/98/99/100 without touching cmd/.
type Client interface {
	// ListInstances merges store intent with live Docker reality and
	// returns both known instances and any untracked containers found
	// alongside them (docs/architecture.md §10.1, ROD-99) — one call, one
	// Docker round-trip, since `claudio ls` needs both in the same
	// listing.
	ListInstances(ctx context.Context) ([]core.InstanceView, []core.UntrackedContainer, error)

	// RuntimeInfo reports the detected Docker-API-compatible runtime —
	// see docs/architecture.md §12.1.
	RuntimeInfo(ctx context.Context) (core.RuntimeView, error)

	Close() error
}
