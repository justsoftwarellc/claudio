package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrAmbiguousID is returned by GetInstance when idOrName matches more
// than one instance ID as a prefix and isn't an exact ID or name match —
// docs/architecture.md §9: "accept unambiguous prefixes," which implies
// an ambiguous one must be a distinct, legible error rather than an
// arbitrary pick.
var ErrAmbiguousID = errors.New("store: ambiguous instance id prefix")

// ListInstances returns every instance row, regardless of desired_state.
// Callers merge this with live Docker state (docs/architecture.md
// §10.1) — this function only ever returns the store's *intent*.
func (s *Store) ListInstances(ctx context.Context) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, repo_url, repo_root, worktree_dir, branch, commit_sha,
		       image, container_id, runtime_profile, desired_state, provision_step,
		       created_at, last_active, compose_project
		FROM instances ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("store: list instances: %w", err)
	}
	defer rows.Close()

	var out []Instance
	for rows.Next() {
		var inst Instance
		if err := rows.Scan(&inst.ID, &inst.Name, &inst.RepoURL, &inst.RepoRoot, &inst.WorktreeDir,
			&inst.Branch, &inst.CommitSHA, &inst.Image, &inst.ContainerID, &inst.RuntimeProfile,
			&inst.DesiredState, &inst.ProvisionStep, &inst.CreatedAt, &inst.LastActive, &inst.ComposeProject); err != nil {
			return nil, fmt.Errorf("store: scan instance: %w", err)
		}
		out = append(out, inst)
	}
	return out, rows.Err()
}

// GetInstance looks up one instance by exact ID, exact name, or —
// failing both — an unambiguous prefix of an instance ID
// (docs/architecture.md §9: "IDs are short and human-typeable ...
// Accept unambiguous prefixes"). An exact ID or name match always wins
// outright, even if it also happens to prefix-match some other row's
// ID, since it is unambiguous by definition; prefix matching against
// name is deliberately not offered — names are user-chosen aliases with
// no fixed shape, so "prefix of a name" has no natural meaning the way
// "prefix of a generated ID" does.
func (s *Store) GetInstance(ctx context.Context, idOrName string) (Instance, error) {
	inst, err := s.scanInstance(ctx, `WHERE id = ? OR name = ?`, idOrName, idOrName)
	if err == nil {
		return inst, nil
	}
	if idOrName == "" {
		// Every ID prefixes with "" — refuse rather than let an empty
		// argument silently resolve to "whichever instance happens to be
		// the only one" (or a spurious ambiguity error) as a side effect
		// of the general prefix-matching loop below.
		return Instance{}, fmt.Errorf("store: get instance %q: %w", idOrName, err)
	}

	// Fall back to ID-prefix matching in Go rather than a SQL LIKE — the
	// idgen alphabet never contains '%'/'_', but idOrName is still user
	// input, and matching in Go sidesteps having to think about it at
	// all. Instance counts are small (this is a per-developer-machine
	// orchestrator, not a fleet), so an ids-only full scan costs nothing.
	ids, idsErr := s.db.QueryContext(ctx, `SELECT id FROM instances`)
	if idsErr != nil {
		return Instance{}, fmt.Errorf("store: get instance %q: %w", idOrName, err)
	}
	defer ids.Close()
	var matches []string
	for ids.Next() {
		var id string
		if scanErr := ids.Scan(&id); scanErr != nil {
			return Instance{}, fmt.Errorf("store: get instance %q: %w", idOrName, scanErr)
		}
		if strings.HasPrefix(id, idOrName) {
			matches = append(matches, id)
		}
	}
	if rowsErr := ids.Err(); rowsErr != nil {
		return Instance{}, fmt.Errorf("store: get instance %q: %w", idOrName, rowsErr)
	}

	switch len(matches) {
	case 0:
		return Instance{}, fmt.Errorf("store: get instance %q: %w", idOrName, err)
	case 1:
		return s.scanInstance(ctx, `WHERE id = ?`, matches[0])
	default:
		return Instance{}, fmt.Errorf("%w: %q matches %v", ErrAmbiguousID, idOrName, matches)
	}
}

func (s *Store) scanInstance(ctx context.Context, where string, args ...interface{}) (Instance, error) {
	var inst Instance
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, repo_url, repo_root, worktree_dir, branch, commit_sha,
		       image, container_id, runtime_profile, desired_state, provision_step,
		       created_at, last_active, compose_project
		FROM instances `+where, args...).
		Scan(&inst.ID, &inst.Name, &inst.RepoURL, &inst.RepoRoot, &inst.WorktreeDir,
			&inst.Branch, &inst.CommitSHA, &inst.Image, &inst.ContainerID, &inst.RuntimeProfile,
			&inst.DesiredState, &inst.ProvisionStep, &inst.CreatedAt, &inst.LastActive, &inst.ComposeProject)
	if err != nil {
		return Instance{}, err
	}
	return inst, nil
}

// SetComposeProject records that instance id is backed by the named
// Docker Compose project — set once, right after CreateInstance's row is
// inserted, when provisionContainer determines the repo needs the
// compose path rather than a single container (ROD-106).
func (s *Store) SetComposeProject(ctx context.Context, instanceID, project string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET compose_project = ? WHERE id = ?`, project, instanceID)
	if err != nil {
		return fmt.Errorf("store: set compose project for %s: %w", instanceID, err)
	}
	return nil
}
