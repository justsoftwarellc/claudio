// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/store"
)

// restoreFixture registers an instance whose repo root and worktree are
// real directories under t.TempDir(), so RestoreConfig's host-side file
// work has somewhere to happen. No Docker: the operation deliberately
// never touches a container, which is what makes it usable on the
// broken instance it exists to repair.
func restoreFixture(t *testing.T) (s *store.Store, id, homeDir, configPath string) {
	t.Helper()
	s = openTestStore(t)

	root := t.TempDir()
	id = "cfgrestore1"
	worktreeDir := filepath.Join(root, "worktrees", id)
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	if err := s.CreateInstance(context.Background(), store.NewInstanceParams{
		ID: id, RepoURL: "git@github.com:acme/web.git", RepoRoot: root,
		WorktreeDir: worktreeDir, Branch: "main", Image: "claudio/base",
		RuntimeProfile: "orbstack",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	homeDir = worktreeDir + ".home"
	return s, id, homeDir, filepath.Join(homeDir, ".claude.json")
}

func writeConfig(t *testing.T, homeDir, configPath, content string) {
	t.Helper()
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func readConfig(t *testing.T, configPath string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read restored config: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("restored config is not valid JSON: %v\n%s", err, raw)
	}
	return parsed
}

// The reported bug: a new machine, `claudio attach`, and Claude Code
// refuses to start because ~/.claude.json has invalid syntax. After the
// restore the file must parse and carry the flags that let Claude Code
// start unattended.
func TestRestoreConfigRepairsInvalidJSON(t *testing.T) {
	s, id, homeDir, configPath := restoreFixture(t)
	writeConfig(t, homeDir, configPath, `{"theme": "dark",,, "hasCompletedOnboarding": tru`)

	result, err := RestoreConfig(context.Background(), s, id)
	if err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}

	if result.Parsed {
		t.Error("Parsed = true, want false — the fixture is deliberately invalid JSON")
	}
	if result.ParseError == "" {
		t.Error("ParseError is empty; the user needs to be told what was wrong with the old file")
	}

	restored := readConfig(t, configPath)
	if restored["hasCompletedOnboarding"] != true {
		t.Errorf("hasCompletedOnboarding = %v, want true — onboarding would re-prompt", restored["hasCompletedOnboarding"])
	}
	if restored["theme"] == nil {
		t.Error("theme is absent; Claude Code prompts for one on first run")
	}

	projects, ok := restored["projects"].(map[string]any)
	if !ok {
		t.Fatalf("projects = %T, want an object", restored["projects"])
	}
	entry, ok := projects[result.Workdir].(map[string]any)
	if !ok {
		t.Fatalf("projects[%q] missing; the trust dialog would block startup", result.Workdir)
	}
	if entry["hasTrustDialogAccepted"] != true {
		t.Errorf("hasTrustDialogAccepted = %v, want true", entry["hasTrustDialogAccepted"])
	}
}

// The trust entry is keyed by the exact cwd Claude Code starts in, which
// is the container path /repo/worktrees/<id> — not the host worktree
// path. Keying it to the host path silently reproduces the trust dialog.
func TestRestoreConfigKeysTrustToTheContainerPath(t *testing.T) {
	s, id, homeDir, configPath := restoreFixture(t)
	writeConfig(t, homeDir, configPath, `not json at all`)

	result, err := RestoreConfig(context.Background(), s, id)
	if err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}

	want := "/repo/worktrees/" + id
	if result.Workdir != want {
		t.Errorf("Workdir = %q, want %q", result.Workdir, want)
	}

	projects := readConfig(t, configPath)["projects"].(map[string]any)
	if _, ok := projects[want]; !ok {
		t.Errorf("projects has no %q key; got keys %v", want, keysOf(projects))
	}
}

// The broken file is evidence. It is renamed, never deleted, so a user
// who wanted something out of it still can get it.
func TestRestoreConfigBacksUpTheBrokenFile(t *testing.T) {
	s, id, homeDir, configPath := restoreFixture(t)
	const broken = `{"theme": "dark"` // truncated
	writeConfig(t, homeDir, configPath, broken)

	result, err := RestoreConfig(context.Background(), s, id)
	if err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}

	if result.BackupPath == "" {
		t.Fatal("BackupPath is empty, want the broken file preserved")
	}
	saved, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(saved) != broken {
		t.Errorf("backup = %q, want the original bytes %q", saved, broken)
	}
}

// A second run must not overwrite the first run's backup — the
// timestamped name is what keeps the original evidence reachable after a
// user runs the command twice.
func TestRestoreConfigTwiceKeepsBothBackups(t *testing.T) {
	s, id, homeDir, configPath := restoreFixture(t)
	writeConfig(t, homeDir, configPath, `{{{`)

	first, err := RestoreConfig(context.Background(), s, id)
	if err != nil {
		t.Fatalf("first RestoreConfig: %v", err)
	}
	second, err := RestoreConfig(context.Background(), s, id)
	if err != nil {
		t.Fatalf("second RestoreConfig: %v", err)
	}

	if second.BackupPath == first.BackupPath {
		t.Fatalf("both runs backed up to %s; the first run's evidence was destroyed", first.BackupPath)
	}
	if _, err := os.Stat(first.BackupPath); err != nil {
		t.Errorf("first backup no longer readable: %v", err)
	}
}

// Salvage: a parseable config's unrelated state (MCP servers, history)
// survives, because the user has accumulated it and the restore is only
// meant to reset what gates startup.
func TestRestoreConfigSalvagesUnrelatedKeys(t *testing.T) {
	s, id, homeDir, configPath := restoreFixture(t)
	writeConfig(t, homeDir, configPath, `{
	  "hasCompletedOnboarding": false,
	  "mcpServers": {"linear": {"command": "linear-mcp"}},
	  "numStartups": 7
	}`)

	result, err := RestoreConfig(context.Background(), s, id)
	if err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}
	if !result.Parsed {
		t.Fatalf("Parsed = false, want true — the fixture is valid JSON (%s)", result.ParseError)
	}

	restored := readConfig(t, configPath)
	mcp, ok := restored["mcpServers"].(map[string]any)
	if !ok || mcp["linear"] == nil {
		t.Errorf("mcpServers was dropped: %v", restored["mcpServers"])
	}
	if restored["numStartups"] != float64(7) {
		t.Errorf("numStartups = %v, want 7 preserved", restored["numStartups"])
	}

	// The startup gate is reset even though the old file explicitly set
	// it false — that field is precisely what this command overrides.
	if restored["hasCompletedOnboarding"] != true {
		t.Errorf("hasCompletedOnboarding = %v, want true (reset from the old false)", restored["hasCompletedOnboarding"])
	}
	if !contains(result.SalvagedKeys, "mcpServers") {
		t.Errorf("SalvagedKeys = %v, want it to report mcpServers", result.SalvagedKeys)
	}
}

// Trust already granted for other paths must survive, or the restore
// trades one dialog for another on every other project in the file.
func TestRestoreConfigKeepsTrustForOtherProjects(t *testing.T) {
	s, id, homeDir, configPath := restoreFixture(t)
	writeConfig(t, homeDir, configPath, `{
	  "projects": {
	    "/repo/worktrees/other": {"hasTrustDialogAccepted": true, "history": ["a"]}
	  }
	}`)

	if _, err := RestoreConfig(context.Background(), s, id); err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}

	projects := readConfig(t, configPath)["projects"].(map[string]any)
	other, ok := projects["/repo/worktrees/other"].(map[string]any)
	if !ok {
		t.Fatalf("the other project's entry was dropped; keys = %v", keysOf(projects))
	}
	if other["hasTrustDialogAccepted"] != true {
		t.Error("the other project's trust flag was lost")
	}
	if other["history"] == nil {
		t.Error("the other project's history was lost")
	}
}

// This instance's own per-project history survives the trust flag being
// forced — the entry is merged, not replaced.
func TestRestoreConfigKeepsOwnProjectHistory(t *testing.T) {
	s, id, homeDir, configPath := restoreFixture(t)
	workdir := "/repo/worktrees/" + id
	writeConfig(t, homeDir, configPath, `{
	  "projects": {
	    "`+workdir+`": {"hasTrustDialogAccepted": false, "history": ["earlier prompt"]}
	  }
	}`)

	if _, err := RestoreConfig(context.Background(), s, id); err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}

	entry := readConfig(t, configPath)["projects"].(map[string]any)[workdir].(map[string]any)
	if entry["hasTrustDialogAccepted"] != true {
		t.Error("hasTrustDialogAccepted was not forced true for this instance's own path")
	}
	if entry["history"] == nil {
		t.Error("this project's history was dropped; only the trust flag should be overridden")
	}
}

// A parseable file whose "projects" is the wrong type is discarded
// rather than merged — carrying it through would write a config Claude
// Code rejects for a different reason than the one just repaired.
func TestRestoreConfigReplacesWrongTypedProjects(t *testing.T) {
	s, id, homeDir, configPath := restoreFixture(t)
	writeConfig(t, homeDir, configPath, `{"projects": "not-an-object"}`)

	result, err := RestoreConfig(context.Background(), s, id)
	if err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}

	projects, ok := readConfig(t, configPath)["projects"].(map[string]any)
	if !ok {
		t.Fatalf("projects is still not an object: %T", readConfig(t, configPath)["projects"])
	}
	if projects[result.Workdir] == nil {
		t.Error("the instance's own trust entry is missing after replacing a wrong-typed projects value")
	}
}

// No config at all (never started, or home/ wiped by `start --fresh`)
// is not an error: writing a good one is still the right outcome, and
// pre-seeds what the entrypoint would otherwise seed on next start.
func TestRestoreConfigWithNoExistingFile(t *testing.T) {
	s, id, _, configPath := restoreFixture(t)

	result, err := RestoreConfig(context.Background(), s, id)
	if err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}
	if result.Existed {
		t.Error("Existed = true, want false — no file was written by the fixture")
	}
	if result.BackupPath != "" {
		t.Errorf("BackupPath = %q, want empty when there was nothing to back up", result.BackupPath)
	}
	if readConfig(t, configPath)["hasCompletedOnboarding"] != true {
		t.Error("a fresh config was not written")
	}
}

// home/ itself missing must not fail the repair: `start --fresh` removes
// the directory outright, and the user may run the restore before the
// next start recreates it.
func TestRestoreConfigRecreatesMissingHomeDir(t *testing.T) {
	s, id, homeDir, configPath := restoreFixture(t)
	if err := os.RemoveAll(homeDir); err != nil {
		t.Fatalf("remove home: %v", err)
	}

	if _, err := RestoreConfig(context.Background(), s, id); err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}
	if readConfig(t, configPath)["hasCompletedOnboarding"] != true {
		t.Error("config was not written into a recreated home dir")
	}
}

func TestRestoreConfigUnknownInstanceIsNotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := RestoreConfig(context.Background(), s, "does-not-exist")
	if err == nil {
		t.Fatal("expected an error for a nonexistent instance")
	}
	if !coreerr.Is(err, coreerr.NotFound) {
		t.Errorf("code = %v, want NotFound", err)
	}
}

// An instance whose worktree isn't under its repo root (an adopted
// container) has no derivable container cwd, so there is no correct key
// to write. Failing loudly beats writing a config keyed to the wrong
// path, which would look repaired and still show the trust dialog.
func TestRestoreConfigRejectsUnderivableWorkdir(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateInstance(context.Background(), store.NewInstanceParams{
		ID: "adopted1", RepoURL: "git@github.com:acme/web.git",
		RepoRoot: t.TempDir(), WorktreeDir: t.TempDir(), // unrelated trees
		Branch: "main", Image: "claudio/base", RuntimeProfile: "orbstack",
	}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	_, err := RestoreConfig(context.Background(), s, "adopted1")
	if err == nil {
		t.Fatal("expected an error when the container workdir cannot be derived")
	}
	if !coreerr.Is(err, coreerr.InvalidInput) {
		t.Errorf("code = %v, want InvalidInput", err)
	}
}

// configBaseline duplicates what image/Dockerfile bakes into
// /opt/claudio/claude.json.template, because the repair must work when
// the container will not start and so cannot be read from. Duplication
// is only safe while the two agree — this asserts they do, and fails
// the moment the template changes without this file following.
func TestConfigBaselineMatchesTheImageTemplate(t *testing.T) {
	dockerfile, err := os.ReadFile("../../image/Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}

	const placeholder = "__CLAUDIO_WORKDIR__"
	body := string(dockerfile)
	start := strings.Index(body, "cat > /opt/claudio/claude.json.template <<'JSON'")
	if start == -1 {
		t.Skip("the Dockerfile no longer writes claude.json.template with a heredoc; update this test alongside that change")
	}
	body = body[start:]
	body = body[strings.Index(body, "\n")+1:]
	end := strings.Index(body, "\nJSON")
	if end == -1 {
		t.Fatal("unterminated claude.json.template heredoc in image/Dockerfile")
	}

	var template map[string]any
	if err := json.Unmarshal([]byte(body[:end]), &template); err != nil {
		t.Fatalf("the Dockerfile's claude.json.template is not valid JSON: %v", err)
	}

	baseline := configBaseline(placeholder)
	wantJSON, _ := json.Marshal(template)
	gotJSON, _ := json.Marshal(baseline)
	if string(wantJSON) != string(gotJSON) {
		t.Errorf("configBaseline has drifted from image/Dockerfile's template.\n template: %s\n baseline: %s", wantJSON, gotJSON)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// backupBrokenConfig's uniqueness must not depend on the clock advancing
// — two runs within the same second is the ordinary case when a user
// re-runs the command immediately, and an overwritten backup is a silent
// data loss.
func TestBackupBrokenConfigWithAFrozenClock(t *testing.T) {
	dir := t.TempDir()
	frozen := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		configPath := filepath.Join(dir, ".claude.json")
		if err := os.WriteFile(configPath, []byte(fmt.Sprintf("broken-%d", i)), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		got, err := backupBrokenConfig(configPath, frozen)
		if err != nil {
			t.Fatalf("backupBrokenConfig: %v", err)
		}
		if seen[got] {
			t.Fatalf("backup path %s reused on run %d", got, i)
		}
		seen[got] = true

		content, err := os.ReadFile(got)
		if err != nil {
			t.Fatalf("read backup: %v", err)
		}
		if want := fmt.Sprintf("broken-%d", i); string(content) != want {
			t.Errorf("backup %s = %q, want %q", got, content, want)
		}
	}
}

// Claude Code writes timestamped copies of the config under
// ~/.claude/backups/ before rewriting it, and home/ is bind-mounted, so
// those copies are readable from the host. Recovering the newest
// parseable one restores the user's real accumulated state instead of a
// bare template — the difference between un-breaking a session and
// actually restoring it. Verified against Claude Code 2.1.266.
func TestRestoreConfigRecoversFromClaudeOwnBackup(t *testing.T) {
	s, id, homeDir, configPath := restoreFixture(t)
	writeConfig(t, homeDir, configPath, `{"numStartups": 6, "truncated`)

	backups := filepath.Join(homeDir, ".claude", "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	good := filepath.Join(backups, ".claude.json.backup.1788980325511")
	if err := os.WriteFile(good, []byte(`{"numStartups": 6, "tipsHistory": {"warmup": 1}}`), 0o644); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	result, err := RestoreConfig(context.Background(), s, id)
	if err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}

	if result.RecoveredFrom != good {
		t.Errorf("RecoveredFrom = %q, want %q", result.RecoveredFrom, good)
	}
	if result.Parsed {
		t.Error("Parsed = true; it must keep meaning the *live* file parsed, which it did not")
	}

	restored := readConfig(t, configPath)
	if restored["numStartups"] != float64(6) {
		t.Errorf("numStartups = %v, want 6 recovered from the backup", restored["numStartups"])
	}
	if restored["tipsHistory"] == nil {
		t.Error("tipsHistory was not recovered from the backup")
	}
	if restored["hasCompletedOnboarding"] != true {
		t.Error("the startup gate was not applied on top of the recovered content")
	}
}

// The backup directory also holds the copies Claude Code took *from* the
// already-broken file. Those parse no better than the live one, so the
// newest parseable candidate — not simply the newest — is the one to
// use.
func TestRestoreConfigSkipsUnparseableBackups(t *testing.T) {
	s, id, homeDir, configPath := restoreFixture(t)
	writeConfig(t, homeDir, configPath, `{"broken`)

	backups := filepath.Join(homeDir, ".claude", "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	older := filepath.Join(backups, ".claude.json.backup.1788980000000")
	newerBroken := filepath.Join(backups, ".claude.json.backup.1789018664431")
	if err := os.WriteFile(older, []byte(`{"numStartups": 4}`), 0o644); err != nil {
		t.Fatalf("write older backup: %v", err)
	}
	if err := os.WriteFile(newerBroken, []byte(`{"broken`), 0o644); err != nil {
		t.Fatalf("write newer backup: %v", err)
	}
	// Make the broken one unambiguously newest by mtime, which is what
	// the ordering actually keys on.
	now := time.Now()
	if err := os.Chtimes(older, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	result, err := RestoreConfig(context.Background(), s, id)
	if err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}

	if result.RecoveredFrom != older {
		t.Errorf("RecoveredFrom = %q, want the newest *parseable* backup %q", result.RecoveredFrom, older)
	}
	if readConfig(t, configPath)["numStartups"] != float64(4) {
		t.Error("content from the parseable backup was not used")
	}
}

// No backups directory at all (a fresh instance, or an older Claude Code)
// must still repair — recovery is a best-effort improvement, never a
// precondition.
func TestRestoreConfigWithoutBackupsFallsBackToBaseline(t *testing.T) {
	s, id, homeDir, configPath := restoreFixture(t)
	writeConfig(t, homeDir, configPath, `{"broken`)

	result, err := RestoreConfig(context.Background(), s, id)
	if err != nil {
		t.Fatalf("RestoreConfig: %v", err)
	}
	if result.RecoveredFrom != "" {
		t.Errorf("RecoveredFrom = %q, want empty with no backups directory", result.RecoveredFrom)
	}
	if readConfig(t, configPath)["hasCompletedOnboarding"] != true {
		t.Error("the baseline config was not written")
	}
}
