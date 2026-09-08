package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/core"
	"github.com/rodrigomorales/claudio/internal/engine"
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

// Create fills in the fields CreateParams needs from resolved global
// config (workspace root, docker host, port range, default resources and
// image — docs/architecture.md §12.3's three-layer resolution) so the
// CLI layer only ever supplies what the user actually typed.
func (l *Local) Create(ctx context.Context, params core.CreateParams, progress core.ProgressFunc) (core.CreateResult, error) {
	params, err := l.resolveCreateParams(params)
	if err != nil {
		return core.CreateResult{}, err
	}
	return core.CreateInstance(ctx, l.store, params, progress)
}

// resolveCreateParams fills in the resolved-global-config fields shared
// by Create, Start, and Restart — all three provision a container the
// same way (Start/Restart re-run it for an existing instance rather than
// a new one), so they must not drift on where workspace root, docker
// host, port range, image, and resources come from.
func (l *Local) resolveCreateParams(params core.CreateParams) (core.CreateParams, error) {
	workspaceRoot, err := expandHome(l.global.WorkspaceRoot)
	if err != nil {
		return core.CreateParams{}, err
	}
	params.WorkspaceRoot = workspaceRoot
	params.DockerHost = l.global.Runtime.DockerHost
	params.PortRangeLow = l.global.Ports.Range[0]
	params.PortRangeHigh = l.global.Ports.Range[1]
	if params.Image == "" {
		params.Image = "claudio/base:latest"
	}
	if params.Resources == (engine.ResourceLimits{}) {
		mem, err := engine.ParseMemory(derefStr(l.global.Resources.Memory))
		if err != nil {
			return core.CreateParams{}, err
		}
		params.Resources = engine.ResourceLimits{
			MemoryBytes: mem,
			NanoCPUs:    engine.NanoCPUs(derefInt(l.global.Resources.CPUs)),
			PIDs:        int64(derefInt(l.global.Resources.PIDs)),
		}
	}
	return params, nil
}

func (l *Local) GetInstance(ctx context.Context, idOrName string) (store.Instance, error) {
	return l.store.GetInstance(ctx, idOrName)
}

func (l *Local) Destroy(ctx context.Context, params core.DestroyParams) error {
	params.DockerHost = l.global.Runtime.DockerHost
	return core.DestroyInstance(ctx, l.store, params)
}

func (l *Local) Adopt(ctx context.Context, containerID string, createdAt int64) (string, error) {
	return core.AdoptContainer(ctx, l.store, l.global.Runtime.DockerHost, containerID, createdAt)
}

func (l *Local) Forget(ctx context.Context, containerID string) error {
	return core.ForgetContainer(ctx, l.global.Runtime.DockerHost, containerID)
}

func (l *Local) Status(ctx context.Context, idOrName string) (core.InstanceView, error) {
	return core.GetInstanceView(ctx, l.store, l.global.Runtime.DockerHost, idOrName)
}

func (l *Local) Stop(ctx context.Context, idOrName string) error {
	return core.StopInstance(ctx, l.store, l.global.Runtime.DockerHost, idOrName)
}

func (l *Local) Start(ctx context.Context, idOrName string, fresh bool, env map[string]string, progress core.ProgressFunc) (core.CreateResult, error) {
	params, err := l.resolveCreateParams(core.CreateParams{Env: env})
	if err != nil {
		return core.CreateResult{}, err
	}
	return core.StartInstance(ctx, l.store, params, idOrName, fresh, progress)
}

func (l *Local) Restart(ctx context.Context, idOrName string, fresh bool, env map[string]string, progress core.ProgressFunc) (core.CreateResult, error) {
	params, err := l.resolveCreateParams(core.CreateParams{Env: env})
	if err != nil {
		return core.CreateResult{}, err
	}
	return core.RestartInstance(ctx, l.store, l.global.Runtime.DockerHost, params, idOrName, fresh, progress)
}

func (l *Local) AddPort(ctx context.Context, idOrName string, containerPort int) (int, error) {
	return core.AddPort(ctx, l.store, idOrName, containerPort, l.global.Ports.Range[0], l.global.Ports.Range[1])
}

func (l *Local) RemovePort(ctx context.Context, idOrName string, containerPort int) error {
	return core.RemovePort(ctx, l.store, idOrName, containerPort)
}

func (l *Local) DockerHost() string {
	return l.global.Runtime.DockerHost
}

func (l *Local) Close() error {
	return l.store.Close()
}

func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}
