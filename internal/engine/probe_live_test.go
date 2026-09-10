// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package engine

import (
	"context"
	"testing"
	"time"
)

// TestDetectRuntimeAgainstLiveDaemon is an integration smoke test, not a
// unit test: it hits whatever Docker-API-compatible daemon is actually
// running on this machine. Skips cleanly if none is reachable, so `go
// test ./...` stays hermetic in CI; run explicitly with `-run Live` to
// exercise it.
func TestDetectRuntimeAgainstLiveDaemon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := DetectRuntime(ctx, "")
	if err != nil {
		t.Skipf("no reachable Docker-API-compatible daemon: %v", err)
	}

	t.Logf("profile=%s os=%q server=%s mem=%dGB cpus=%d",
		info.Profile, info.OperatingSystem, info.ServerVersion, info.MemTotal/1073741824, info.NCPU)

	if info.Profile == "" {
		t.Fatalf("expected a non-empty classified profile")
	}
}
