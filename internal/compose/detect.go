// Package compose implements ROD-106's compose-sidecar path:
// docs/architecture.md §6.4. A repo that ships a docker-compose.yml (or
// declares services: in .claudio.yml without one) gets a per-instance
// Compose project instead of a single container — sidecars (Postgres,
// Redis) run as their own containers on a per-instance network, and the
// agent container joins that same project as an extra service so it can
// reach them by service name.
//
// Phase 1 has no daemon (ROD-95), so everything this package does runs
// synchronously inside `claudio create`/`stop`/`start`/`restart`/
// `destroy`, and it drives Docker Compose itself rather than the SDK
// (internal/engine's usual approach) — orchestration semantics
// (dependency order, restart policies, volume lifecycle) are exactly
// what `docker compose` already implements correctly; reimplementing
// them against the raw SDK would be substantial scope for no behavior
// difference a user would notice.
package compose

import (
	"os"
	"path/filepath"
)

// composeFileNames are tried in order at the repo root — the same set
// Docker Compose itself recognizes, per docs/architecture.md §6.4:
// "does docker-compose.yml / compose.yaml exist at the repo root?"
var composeFileNames = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

// FindComposeFile returns the path to the repo's own compose file at
// worktreeDir's root, and "" if none of the recognized names exist —
// the detection this issue calls "single-container path stays exactly
// as specified... no compose project, no override generation" hinges
// on. Only the standard locations are checked; a repo-relative path from
// devcontainer.json's dockerComposeFile is ROD-110's concern, not this
// one's (docs/architecture.md's phasing note: the two are separable).
func FindComposeFile(worktreeDir string) string {
	for _, name := range composeFileNames {
		p := filepath.Join(worktreeDir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}
