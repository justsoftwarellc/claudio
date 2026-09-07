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
	"github.com/rodrigomorales/claudio/internal/store"
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

	// Create provisions a new instance end to end: repo root + worktree,
	// port detection/allocation, container creation (ROD-97/98/99/114).
	Create(ctx context.Context, params core.CreateParams) (core.CreateResult, error)

	// GetInstance resolves an ID, alias, or unambiguous ID prefix to the
	// full instance row — used by attach/destroy/cd, all of which accept
	// the same identifier forms (docs/architecture.md §9).
	GetInstance(ctx context.Context, idOrName string) (store.Instance, error)

	// Destroy tears down one instance: container, worktree (unless
	// keepWorkspace), and the store row (ROD-100).
	Destroy(ctx context.Context, params core.DestroyParams) error

	// Adopt reconstructs a store row for an untracked container from its
	// Docker labels (ROD-99).
	Adopt(ctx context.Context, containerID string, createdAt int64) (instanceID string, err error)

	// Forget permanently removes an untracked container without adopting
	// it (ROD-99).
	Forget(ctx context.Context, containerID string) error

	// Status resolves one instance merged with live Docker state — the
	// single-instance counterpart to ListInstances, for `claudio status
	// <id>` (ROD-100).
	Status(ctx context.Context, idOrName string) (core.InstanceView, error)

	// Stop removes an instance's container and releases its ports,
	// keeping the worktree and home/ on disk (ROD-99/ROD-100).
	Stop(ctx context.Context, idOrName string) error

	// Start re-provisions a container for a StateStopped instance —
	// fresh wipes home/ first instead of resuming the existing Claude
	// Code session (ROD-99/ROD-100). env carries the credential to inject
	// into the new container, same as Create's params.Env — a stopped
	// instance's old container held no reference to it, so it must be
	// supplied again.
	Start(ctx context.Context, idOrName string, fresh bool, env map[string]string) (core.CreateResult, error)

	// Restart is Stop followed by Start as one operation (ROD-100).
	Restart(ctx context.Context, idOrName string, fresh bool, env map[string]string) (core.CreateResult, error)

	// AddPort reserves a new host port for an existing instance —
	// effective on the instance's next Restart, not immediately, since
	// Docker cannot add a binding to a running container (ROD-98).
	AddPort(ctx context.Context, idOrName string, containerPort int) (hostPort int, err error)

	// RemovePort releases a host port reservation — same
	// takes-effect-on-restart caveat as AddPort (ROD-98).
	RemovePort(ctx context.Context, idOrName string, containerPort int) error

	// DockerHost is the resolved runtime.docker_host used for this
	// client's Docker calls — attach needs it directly since it execs
	// into `docker`/the SDK itself rather than going through Client
	// (docs/architecture.md's ROD-100 "attach bypasses every abstraction"
	// exception).
	DockerHost() string

	Close() error
}
