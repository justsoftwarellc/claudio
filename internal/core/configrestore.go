// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/store"
)

// RestoreConfigStore is the subset of *store.Store RestoreConfig needs.
type RestoreConfigStore interface {
	GetInstance(ctx context.Context, idOrName string) (store.Instance, error)
}

// RestoreConfigResult reports what RestoreConfig actually did, so the
// CLI can print it without re-deriving any of it — core stays I/O-free
// (see this package's doc), and every line the user sees comes from a
// field here.
type RestoreConfigResult struct {
	InstanceID string `json:"instance_id"`

	// ConfigPath is the host path to the rewritten file. The container
	// sees the same bytes at /home/agent/.claude.json via the home/ bind
	// mount, which is why this operation needs no running container.
	ConfigPath string `json:"config_path"`

	// BackupPath is where the previous file was moved, empty when there
	// was no file to preserve.
	BackupPath string `json:"backup_path,omitempty"`

	// Workdir is the container-side cwd the trust entry was keyed to —
	// /repo/worktrees/<id>, matching what engine.CreateAndStart set as
	// the container's WorkingDir and what the entrypoint substitutes into
	// its own template.
	Workdir string `json:"workdir"`

	// Existed is false when there was no ~/.claude.json at all (the
	// instance never started, or home/ was wiped). The command still
	// writes a good one, but nothing was broken and nothing was salvaged.
	Existed bool `json:"existed"`

	// Parsed reports whether the *live* config file was valid JSON — not
	// whether content was salvaged, which RecoveredFrom answers. False is
	// the case this command exists for: a file Claude Code refuses to
	// start against.
	Parsed bool `json:"parsed"`

	// ParseError is the JSON syntax error from the broken file, kept so
	// the user learns what was actually wrong rather than only that
	// something was. Empty when Parsed.
	ParseError string `json:"parse_error,omitempty"`

	// SalvagedKeys are the top-level keys carried over verbatim from
	// whichever source supplied them (the previous file, or RecoveredFrom),
	// sorted.
	SalvagedKeys []string `json:"salvaged_keys,omitempty"`

	// RecoveredFrom names the file the salvaged content actually came
	// from, when it was not the live config but one of Claude Code's own
	// backups under ~/.claude/backups/ — see recoverFromClaudeBackup. Empty
	// when the live file was parseable, or when no usable backup existed.
	RecoveredFrom string `json:"recovered_from,omitempty"`
}

// configBaseline is the minimum Claude Code needs to start
// non-interactively in a Claudio container, and is deliberately the same
// shape image/Dockerfile bakes into /opt/claudio/claude.json.template:
// "theme" and "hasCompletedOnboarding" clear the first-run onboarding
// prompt, and projects["<cwd>"].hasTrustDialogAccepted clears the "is
// this a project you trust?" dialog for the one path the session
// actually starts in.
//
// It is duplicated here rather than read out of the image because this
// command's whole point is to work when the container will not start —
// reading the template would mean running something inside the very
// container that is broken. The two must stay in step; the test in
// configrestore_test.go asserts they do by parsing the Dockerfile.
func configBaseline(workdir string) map[string]any {
	return map[string]any{
		"theme":                  "dark",
		"hasCompletedOnboarding": true,
		"projects": map[string]any{
			workdir: map[string]any{
				"hasTrustDialogAccepted": true,
			},
		},
	}
}

// baselineKeys are the top-level keys configBaseline owns. A salvaged
// file's own values for these are dropped rather than kept: they are
// exactly the fields whose wrong values cause the failure this command
// repairs (an onboarding flag reset to false, a projects map keyed to a
// path from another machine).
var baselineKeys = map[string]bool{
	"theme":                  true,
	"hasCompletedOnboarding": true,
	"projects":               true,
}

// RestoreConfig rewrites an instance's ~/.claude.json so Claude Code can
// start against it again — the repair for a container whose `claudio
// attach` fails with a JSON syntax error out of Claude Code itself
// rather than out of Claudio.
//
// The file is written on the host, at <worktreeDir>.home/.claude.json,
// which is the same inode the container sees at /home/agent/.claude.json
// through the home/ bind mount (see CreateInstance's homeDir and
// engine.CreateSpec.HomeDir). Doing it host-side rather than via `docker
// exec` is what makes the command useful in the case that produces the
// bug report: the config is broken, so the session will not come up, so
// there is nothing to exec into. A stopped instance repairs the same way
// a running one does.
//
// Salvage rather than overwrite: a parseable file's other top-level keys
// (accumulated MCP servers, session history, per-project state for other
// paths) are carried over verbatim, and only the fields that gate
// startup are reset from the baseline. An unparseable file has nothing
// readable to carry over, so it yields the baseline alone — still the
// right outcome, since a file Claude Code cannot parse is one it treats
// as absent anyway.
//
// The previous file is never deleted, only renamed to
// .claude.json.broken-<RFC3339 timestamp>, so a user who wanted
// something out of it still has it. Timestamped rather than a fixed
// .bak so a second run cannot destroy the first run's evidence.
func RestoreConfig(ctx context.Context, st RestoreConfigStore, idOrName string) (RestoreConfigResult, error) {
	inst, err := st.GetInstance(ctx, idOrName)
	if err != nil {
		return RestoreConfigResult{}, wrapGetInstance("core: config restore", idOrName, err)
	}
	op := fmt.Sprintf("core: config restore %s", inst.ID)

	// The trust entry is keyed by the exact cwd string Claude Code is
	// started in, so it must be the container-side path, not
	// inst.WorktreeDir's host path. An adopted instance whose worktree
	// isn't under its repo root can't produce one — and unlike attach,
	// which can fall back to tmux's default cwd, there is no sensible
	// key to write here, so this is a real failure with an actionable
	// message rather than a silently wrong file.
	workdir, err := engine.ContainerWorkdir(inst.RepoRoot, inst.WorktreeDir)
	if err != nil {
		return RestoreConfigResult{}, coreerr.Wrap(coreerr.InvalidInput, op,
			fmt.Errorf("cannot derive the container's working directory for this instance: %w", err))
	}

	homeDir := inst.WorktreeDir + ".home"
	configPath := filepath.Join(homeDir, ".claude.json")
	result := RestoreConfigResult{
		InstanceID: inst.ID,
		ConfigPath: configPath,
		Workdir:    workdir,
	}

	// MkdirAll, not a bare stat: `stop` keeps home/ but nothing
	// guarantees it, and a --fresh start removes it outright. Recreating
	// it means the repair works even then, matching provisionContainer's
	// own os.MkdirAll of the same path.
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return RestoreConfigResult{}, coreerr.Wrap(coreerr.Internal, op+": create home dir", err)
	}

	config := configBaseline(workdir)

	existing, readErr := os.ReadFile(configPath)
	switch {
	case readErr == nil:
		result.Existed = true
		var prev map[string]any
		if unmarshalErr := json.Unmarshal(existing, &prev); unmarshalErr != nil {
			// The reported failure mode. Record why the live file is
			// unusable, then look for something better than the baseline to
			// rebuild from: Claude Code writes its own timestamped copies
			// under ~/.claude/backups/ before it rewrites the config, and
			// the newest parseable one is a far richer starting point than
			// a bare template — it is the user's real config from minutes
			// earlier, not a fresh one.
			result.ParseError = unmarshalErr.Error()
			if recovered, from, ok := recoverFromClaudeBackup(homeDir); ok {
				prev = recovered
				result.RecoveredFrom = from
			}
		} else {
			result.Parsed = true
		}
		if prev != nil {
			for k, v := range prev {
				if baselineKeys[k] {
					continue
				}
				config[k] = v
				result.SalvagedKeys = append(result.SalvagedKeys, k)
			}
			sort.Strings(result.SalvagedKeys)

			// Salvage the trust flags for any *other* project paths in the
			// old file. Dropping "projects" wholesale would silently
			// re-prompt the trust dialog for a worktree the user had
			// already accepted (possible on an instance whose config
			// outlived a path change), which is a regression the baseline
			// reset is not meant to cause.
			config["projects"] = mergeProjects(prev["projects"], workdir)
			if _, ok := prev["projects"]; ok {
				result.SalvagedKeys = append(result.SalvagedKeys, "projects")
				sort.Strings(result.SalvagedKeys)
			}
		}
	case os.IsNotExist(readErr):
		// Nothing to back up or salvage; writing the baseline is still
		// the right outcome, and pre-seeds a home/ that the entrypoint
		// would otherwise have to seed on next start.
	default:
		return RestoreConfigResult{}, coreerr.Wrap(coreerr.Internal, op+": read "+configPath, readErr)
	}

	// Back up before writing, so a failure to write cannot lose the
	// original: at every point either the original is in place or the
	// backup is.
	if result.Existed {
		backupPath, err := backupBrokenConfig(configPath, time.Now().UTC())
		if err != nil {
			return RestoreConfigResult{}, coreerr.Wrap(coreerr.Internal, op+": back up "+configPath, err)
		}
		result.BackupPath = backupPath
	}

	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return RestoreConfigResult{}, coreerr.Wrap(coreerr.Internal, op+": encode config", err)
	}
	encoded = append(encoded, '\n')

	// 0o644, matching what the entrypoint's own `sed > $HOME/.claude.json`
	// produces under the container user's umask. The container runs as a
	// non-root user whose uid the host side does not model here; the
	// bind mount carries host ownership through, and the image's agent
	// user is the same uid the worktree itself is written as, so a
	// world-readable, owner-writable file is what the container needs.
	if err := os.WriteFile(configPath, encoded, 0o644); err != nil {
		return RestoreConfigResult{}, coreerr.Wrap(coreerr.Internal, op+": write "+configPath, err)
	}

	return result, nil
}

// mergeProjects rebuilds the "projects" map with this instance's own
// workdir freshly trusted, preserving every other project entry the old
// file carried. A "projects" value that isn't an object (the file was
// parseable but structurally wrong — null, a string, an array) is
// discarded rather than merged: there is nothing in it to keep, and
// carrying a wrong-typed value through would reproduce a config Claude
// Code rejects for a different reason than the one just fixed.
func mergeProjects(prev any, workdir string) map[string]any {
	projects := map[string]any{}
	if prevMap, ok := prev.(map[string]any); ok {
		for path, entry := range prevMap {
			if path == workdir {
				continue // replaced below with a known-good entry
			}
			projects[path] = entry
		}
	}

	// Preserve the rest of this workdir's own entry (Claude Code keeps
	// per-project history and settings alongside the trust flag) while
	// forcing the one field that gates startup.
	entry := map[string]any{}
	if prevMap, ok := prev.(map[string]any); ok {
		if own, ok := prevMap[workdir].(map[string]any); ok {
			for k, v := range own {
				entry[k] = v
			}
		}
	}
	entry["hasTrustDialogAccepted"] = true
	projects[workdir] = entry

	return projects
}

// backupBrokenConfig renames configPath aside to a timestamped sibling
// and returns where it went.
//
// The timestamp alone is not a unique name: two runs inside the same
// second produce the same path, and os.Rename would silently overwrite
// the first run's backup — destroying exactly the evidence the backup
// exists to keep. So a taken name gets a -2, -3, ... suffix, found by
// O_EXCL create: the check and the claim are one atomic operation, which
// a stat-then-rename cannot be.
func backupBrokenConfig(configPath string, now time.Time) (string, error) {
	base := fmt.Sprintf("%s.broken-%s", configPath, now.Format("20060102T150405Z"))
	for attempt := 1; ; attempt++ {
		candidate := base
		if attempt > 1 {
			candidate = fmt.Sprintf("%s-%d", base, attempt)
		}
		f, err := os.OpenFile(candidate, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if os.IsExist(err) {
			continue // taken by an earlier run this second
		}
		if err != nil {
			return "", err
		}
		f.Close()
		// The placeholder is replaced by the real file; rename over it is
		// atomic, so the backup is never observable as empty.
		if err := os.Rename(configPath, candidate); err != nil {
			os.Remove(candidate)
			return "", err
		}
		return candidate, nil
	}
}

// claudeBackupDir is where Claude Code keeps its own timestamped copies
// of ~/.claude.json. It writes one before rewriting the config, and one
// more (plus a .corrupted.<ms> copy) when it finds the live file
// unparseable — so after the failure this command repairs, the directory
// generally holds a complete, valid config from minutes earlier.
//
// Verified against Claude Code 2.1.266 in a real Claudio container: a
// truncated ~/.claude.json produced exactly the message the bug report
// describes, alongside .claude.json.backup.<ms> files of the full
// pre-corruption config. Because home/ is bind-mounted, those files are
// readable from the host without entering the container.
const claudeBackupDir = ".claude/backups"

// recoverFromClaudeBackup returns the newest parseable config from
// Claude Code's own backup directory, and the path it came from.
//
// Preferring a real backup over the baseline is what makes this command
// restore a session rather than merely un-break it: the backup carries
// the user's accumulated state (history, per-project settings, cached
// flags) that a template cannot reconstruct. Newest-first, and each
// candidate must parse — the directory also holds the
// .corrupted.<ms>/.backup.<ms> pair written *from* the broken file,
// which parse no better than the live one did and are skipped for it.
//
// A missing directory, an unreadable one, or no parseable candidate all
// return ok=false, leaving the caller with the baseline: this is a
// best-effort improvement on the repair, never a precondition for it.
func recoverFromClaudeBackup(homeDir string) (config map[string]any, from string, ok bool) {
	dir := filepath.Join(homeDir, claudeBackupDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", false
	}

	type candidate struct {
		path    string
		modTime time.Time
	}
	var candidates []candidate
	for _, e := range entries {
		if e.IsDir() || !strings.Contains(e.Name(), ".claude.json.backup.") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})

	for _, c := range candidates {
		raw, err := os.ReadFile(c.path)
		if err != nil {
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			continue // a backup taken from the already-broken file
		}
		return parsed, c.path, true
	}
	return nil, "", false
}
