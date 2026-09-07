package main

import (
	"context"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
)

// TestLsAgainstLiveDaemon is an integration smoke test: it exercises the
// full CLI path — newClient, the store, live Docker calls — against
// whatever daemon is reachable on this machine. Skips cleanly when none
// is reachable.
//
// It deliberately does NOT override $HOME: doing so breaks `docker
// context inspect`'s own config resolution (it falls back to
// /var/run/docker.sock, which was found on the development machine to be
// symlinked to a *different* runtime than the active `docker` context —
// see engine.DetectRuntime's doc). CLAUDIO_HOME isolates Claudio's own
// state without touching Docker's.
func TestLsAgainstLiveDaemon(t *testing.T) {
	ctx := context.Background()
	if _, err := engine.DetectRuntime(ctx, ""); err != nil {
		t.Skipf("no reachable Docker-API-compatible daemon: %v", err)
	}

	t.Setenv("CLAUDIO_HOME", t.TempDir())

	c, err := newClient(ctx)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	defer c.Close()

	instances, untracked, err := c.ListInstances(ctx)
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	// Fresh CLAUDIO_HOME means the store is empty; any untracked entries
	// come from real containers on this machine carrying Claudio labels
	// (there may be some if the smoke tests above left one), which is
	// fine — this just proves the call succeeds end to end.
	t.Logf("instances=%d untracked=%d", len(instances), len(untracked))
}
