package client

import (
	"context"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/core"
	"github.com/rodrigomorales/claudio/internal/store"
)

// Local is the phase-1 Client: a thin pass-through to core, in the same
// process as the CLI command that invoked it. See docs/architecture.md
// §12.4 — this is deliberately the *only* thing that changes when the
// daemon arrives in phase 2; command code in cmd/claudio never changes.
type Local struct {
	store  *store.Store
	global config.GlobalConfig
}

func NewLocal(st *store.Store, global config.GlobalConfig) *Local {
	return &Local{store: st, global: global}
}

func (l *Local) ListInstances(ctx context.Context) ([]core.InstanceView, []core.UntrackedContainer, error) {
	return core.ListInstances(ctx, l.store, l.global.Runtime.DockerHost)
}

func (l *Local) RuntimeInfo(ctx context.Context) (core.RuntimeView, error) {
	return core.DetectRuntime(ctx, l.global.Runtime.DockerHost)
}

func (l *Local) Close() error {
	return l.store.Close()
}
