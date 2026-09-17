// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/store"
)

// RepoLookup is the slice of the store config resolution needs. Narrow on
// purpose: resolving a path must not be able to mutate anything, and a
// one-method interface is what lets provisionContainer be handed either a
// CreateStore or a StartStore without either growing a reason to care.
type RepoLookup interface {
	GetRepo(ctx context.Context, rootPath string) (store.Repo, error)
}

// resolveConfigPath answers "where is this repo's .claudio.yml?" by
// asking the store, which is the only component that knows (ROD-133).
//
// Config is local: it lives in the folder the user stands in, recorded at
// create time. Falling back to the worktree covers two real cases, not
// one — an instance created from a remote URL (no folder to stand in),
// and an instance created before migration003 (no repos row yet). Both
// want the same answer, which is why a lookup failure degrades silently
// instead of failing the create: for a pre-migration instance the
// worktree copy genuinely *is* its config, and erroring here would break
// every existing instance on upgrade.
func resolveConfigPath(ctx context.Context, st RepoLookup, repoRoot, worktreeDir string) string {
	if st == nil || repoRoot == "" {
		return config.RepoConfigPath(nil, worktreeDir)
	}
	r, err := st.GetRepo(ctx, repoRoot)
	if err != nil {
		return config.RepoConfigPath(nil, worktreeDir)
	}
	return config.RepoConfigPath(r.SourceDir, worktreeDir)
}
