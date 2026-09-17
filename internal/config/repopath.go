// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import "path/filepath"

// FileName is the repo config file. Named here rather than spelled as a
// literal at each call site because ROD-133 was, mechanically, eight call
// sites each appending "/.claudio.yml" to a path of their own choosing:
// when the name and the directory are both decided locally, nothing stops
// two commands from reading two different files.
const FileName = ".claudio.yml"

// RepoConfigPath returns the single .claudio.yml an instance uses.
//
// Config is local and machine-local (ROD-133): it lives in the folder the
// user actually stands in, and the store — not a search, not a
// convention — is what knows which folder that is. sourceDir is that
// recorded path, nil for an instance created from a remote URL.
//
// The worktree fallback is not vestigial and cannot be removed: `claudio
// create git@github.com:acme/web.git` never gives the user a folder to
// put config in, so for those instances the worktree copy is the only
// file there is.
//
// An empty (rather than nil) sourceDir is treated as absent. A row
// written by an older caller must not resolve config reads to
// "/.claudio.yml" at the filesystem root, which is both wrong and, for a
// write, potentially harmful.
func RepoConfigPath(sourceDir *string, worktreeDir string) string {
	if sourceDir != nil && *sourceDir != "" {
		return filepath.Join(*sourceDir, FileName)
	}
	if worktreeDir == "" {
		// LoadRepoConfig reads a missing file as "no config", which is the
		// right answer here: nothing is known about where config would be.
		return ""
	}
	return filepath.Join(worktreeDir, FileName)
}
