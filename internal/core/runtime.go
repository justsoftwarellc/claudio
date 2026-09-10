// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"

	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/engine"
)

// DetectRuntime wraps engine.DetectRuntime with core's I/O-free contract:
// takes a context, returns a typed view and error, never prints. host
// comes from resolved global config (runtime.docker_host — ROD-113);
// empty means "resolve automatically" (docker context, then SDK default —
// see engine.DetectRuntime's doc for why that order matters).
func DetectRuntime(ctx context.Context, host string) (RuntimeView, error) {
	info, err := engine.DetectRuntime(ctx, host)
	if err != nil {
		// A failure here means no Docker-API-compatible daemon was
		// reachable at all — the runtime environment isn't ready, not a
		// bad argument the caller passed.
		return RuntimeView{}, coreerr.Wrap(coreerr.Unavailable, "core: detect runtime", err)
	}
	return RuntimeView{
		Profile:         string(info.Profile),
		OperatingSystem: info.OperatingSystem,
		ServerVersion:   info.ServerVersion,
		MemTotalBytes:   info.MemTotal,
		NCPU:            info.NCPU,
		DockerHost:      info.DockerHost,
	}, nil
}
