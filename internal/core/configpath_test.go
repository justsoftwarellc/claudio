// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/store"
)

// resolveConfigPath is what ends ROD-133: every command asks the store
// where this repo's config lives instead of each deciding for itself.
// A source directory recorded at create time wins over the worktree.
func TestResolveConfigPathUsesRecordedSourceDir(t *testing.T) {
	source := t.TempDir()
	worktree := t.TempDir()

	st := &fakeRepoStore{repo: store.Repo{RootPath: "/root/acme", SourceDir: &source}}
	got := resolveConfigPath(context.Background(), st, "/root/acme", worktree)

	if want := filepath.Join(source, config.FileName); got != want {
		t.Fatalf("resolveConfigPath = %q, want %q", got, want)
	}
}

// An instance created from a remote URL has no source directory, so the
// worktree copy is the only config there is.
func TestResolveConfigPathFallsBackToWorktreeForRemote(t *testing.T) {
	worktree := t.TempDir()

	st := &fakeRepoStore{repo: store.Repo{RootPath: "/root/acme"}} // SourceDir nil
	got := resolveConfigPath(context.Background(), st, "/root/acme", worktree)

	if want := filepath.Join(worktree, config.FileName); got != want {
		t.Fatalf("resolveConfigPath = %q, want %q", got, want)
	}
}

// An instance predating migration003 has no repos row at all. That is not
// a failure worth aborting a create over: it degrades to the worktree,
// which is exactly where such an instance's config already is.
func TestResolveConfigPathToleratesMissingRepoRow(t *testing.T) {
	worktree := t.TempDir()

	st := &fakeRepoStore{err: os.ErrNotExist}
	got := resolveConfigPath(context.Background(), st, "/root/acme", worktree)

	if want := filepath.Join(worktree, config.FileName); got != want {
		t.Fatalf("resolveConfigPath with no repo row = %q, want worktree fallback %q", got, want)
	}
}

// The end-to-end property this whole change exists for: config written in
// the user's own folder is what provisioning reads, with no commit and no
// copy into the worktree. Before ROD-133 this file was invisible.
func TestConfigInSourceDirIsReadWithoutCommitting(t *testing.T) {
	source := t.TempDir()
	worktree := t.TempDir()

	// The user's folder declares a port; the worktree declares a different
	// one. Only the user's folder may win.
	if err := os.WriteFile(filepath.Join(source, config.FileName),
		[]byte("ports:\n  - {name: web, container: 3000}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, config.FileName),
		[]byte("ports:\n  - {name: stale, container: 9999}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := &fakeRepoStore{repo: store.Repo{RootPath: "/root/acme", SourceDir: &source}}
	cfg, err := config.LoadRepoConfig(resolveConfigPath(context.Background(), st, "/root/acme", worktree))
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}

	if len(cfg.Ports) != 1 || cfg.Ports[0].Name != "web" {
		t.Fatalf("loaded ports = %+v, want the source folder's 'web' entry", cfg.Ports)
	}
}

type fakeRepoStore struct {
	repo store.Repo
	err  error
}

func (f *fakeRepoStore) GetRepo(context.Context, string) (store.Repo, error) {
	if f.err != nil {
		return store.Repo{}, f.err
	}
	return f.repo, nil
}

// End-to-end through a real create: `claudio create .` from a working
// copy must record that folder and read config from it, with nothing
// committed. This is ROD-134/135/136 as one behavior — before this, the
// uncommitted file below was invisible and a port declared in it never
// reached the instance.
func TestCreateFromWorkingCopyReadsConfigFromSourceFolder(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)

	// A working copy the user stands in, with UNCOMMITTED config.
	source := t.TempDir()
	runGit(t, "", "init", source)
	runGit(t, source, "config", "user.email", "test@example.com")
	runGit(t, source, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "README.md")
	runGit(t, source, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(source, config.FileName),
		[]byte("ports:\n  - {name: web, container: 3000}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	params := baseCreateParams(t, "file://"+source)
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// The instance reports the user's own file, not the worktree copy.
	wantPath := filepath.Join(source, config.FileName)
	if result.ConfigPath != wantPath {
		t.Errorf("ConfigPath = %q, want %q", result.ConfigPath, wantPath)
	}
	if result.ConfigIsTracked {
		t.Error("ConfigIsTracked = true, want false — the file was never committed")
	}

	// The uncommitted declaration actually took effect.
	var found bool
	for _, p := range result.Ports {
		if p.ServiceName == "web" && p.ContainerPort == 3000 {
			found = true
		}
	}
	if !found {
		t.Errorf("ports = %+v, want the uncommitted 'web' declaration to have been applied", result.Ports)
	}

	// And the store knows where config lives, for every later command.
	repoRow, err := s.GetRepo(t.Context(), filepath.Dir(filepath.Dir(result.WorktreeDir)))
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if repoRow.SourceDir == nil || *repoRow.SourceDir != source {
		t.Errorf("stored SourceDir = %v, want %q", repoRow.SourceDir, source)
	}

	// Claudio's own bookkeeping must not dirty the user's repo.
	statusCmd := exec.Command("git", "status", "--short")
	statusCmd.Dir = source
	status, err := statusCmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.Contains(string(status), config.FileName) {
		t.Errorf("source repo shows %s as dirty/untracked: %q", config.FileName, status)
	}
}

// A repo that already commits .claudio.yml is reported, not rewritten:
// info/exclude cannot untrack it, and Claudio never touches the user's
// git index.
func TestCreateReportsAnAlreadyTrackedConfig(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)

	source := t.TempDir()
	runGit(t, "", "init", source)
	runGit(t, source, "config", "user.email", "test@example.com")
	runGit(t, source, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(source, config.FileName), []byte("ports: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", config.FileName)
	runGit(t, source, "commit", "-m", "commit config")

	result, err := createForTest(t, s, baseCreateParams(t, "file://"+source))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if !result.ConfigIsTracked {
		t.Error("ConfigIsTracked = false, want true for a committed .claudio.yml")
	}
	// Still read from the user's folder — tracked or not, it is their file.
	if want := filepath.Join(source, config.FileName); result.ConfigPath != want {
		t.Errorf("ConfigPath = %q, want %q", result.ConfigPath, want)
	}
}

// A remote-sourced instance has no folder the user stands in, so it keeps
// using the worktree. This is the fallback that cannot be deleted.
func TestCreateFromBareSourceUsesWorktreeConfig(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t) // a bare origin.git

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if want := filepath.Join(result.WorktreeDir, config.FileName); result.ConfigPath != want {
		t.Errorf("ConfigPath = %q, want the worktree fallback %q", result.ConfigPath, want)
	}
}
