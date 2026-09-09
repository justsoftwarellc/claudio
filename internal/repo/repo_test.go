package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/web.git":     "github.com-acme-web",
		"https://github.com/acme/web.git": "github.com-acme-web",
		"https://github.com/acme/web":     "github.com-acme-web",
	}
	for in, want := range cases {
		got, err := Slug(in)
		if err != nil {
			t.Fatalf("Slug(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeSSH(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/web.git":     "git@github.com:acme/web.git",
		"https://github.com/acme/web":     "git@github.com:acme/web.git",
		"https://github.com/acme/web.git": "git@github.com:acme/web.git",
		"acme/web":                        "git@github.com:acme/web.git",
	}
	for in, want := range cases {
		got, err := NormalizeSSH(in)
		if err != nil {
			t.Fatalf("NormalizeSSH(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("NormalizeSSH(%q) = %q, want %q", in, got, want)
		}
	}
}

// newLocalOriginRepo creates a bare repo on disk to act as a clone source
// so tests never touch the network, then returns its file:// path.
func newLocalOriginRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.git")
	seed := filepath.Join(dir, "seed")

	run(t, "", "init", "--bare", origin)
	run(t, "", "init", seed)
	run(t, seed, "config", "user.email", "test@example.com")
	run(t, seed, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, seed, "add", "README.md")
	run(t, seed, "commit", "-m", "initial")
	run(t, seed, "branch", "-M", "main")
	run(t, seed, "remote", "add", "origin", origin)
	run(t, seed, "push", "origin", "main")
	return "file://" + origin
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func TestEnsureRootClonesOnceThenReuses(t *testing.T) {
	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root.MainClone, ".git")); err != nil {
		t.Fatalf("main-clone not created: %v", err)
	}

	// Second call must be a no-op fast path, not a re-clone: prove it by
	// writing a marker into main-clone and confirming it survives.
	marker := filepath.Join(root.MainClone, ".claudio-test-marker")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root2, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot (second call): %v", err)
	}
	if root2.Path != root.Path {
		t.Fatalf("root path changed between calls: %s vs %s", root.Path, root2.Path)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("second EnsureRoot re-cloned instead of reusing: marker gone: %v", err)
	}
}

func TestAddWorktreeRewritesRelativeGitdirPointers(t *testing.T) {
	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}

	worktreeDir, err := AddWorktree(ctx, root, "brave-otter", "claudio/brave-otter", true)
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	gitFile, err := os.ReadFile(filepath.Join(worktreeDir, ".git"))
	if err != nil {
		t.Fatalf("read worktree .git: %v", err)
	}
	content := strings.TrimSpace(string(gitFile))
	if strings.HasPrefix(strings.TrimPrefix(content, "gitdir: "), "/") {
		t.Fatalf(".git pointer is absolute, want relative: %q", content)
	}
	wantSuffix := filepath.Join("main-clone", ".git", "worktrees", "brave-otter")
	if !strings.HasSuffix(content, wantSuffix) {
		t.Fatalf(".git pointer = %q, want suffix %q", content, wantSuffix)
	}

	reverse, err := os.ReadFile(filepath.Join(root.MainClone, ".git", "worktrees", "brave-otter", "gitdir"))
	if err != nil {
		t.Fatalf("read reverse gitdir: %v", err)
	}
	reverseContent := strings.TrimSpace(string(reverse))
	if strings.HasPrefix(reverseContent, "/") {
		t.Fatalf("reverse gitdir is absolute, want relative: %q", reverseContent)
	}

	// Host git must still work against the worktree after rewriting.
	run(t, worktreeDir, "status")
	out := runOut(t, worktreeDir, "rev-parse", "--abbrev-ref", "HEAD")
	if strings.TrimSpace(out) != "claudio/brave-otter" {
		t.Fatalf("host branch = %q, want claudio/brave-otter", out)
	}
}

func runOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestAddWorktreeBranchCollision(t *testing.T) {
	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	if _, err := AddWorktree(ctx, root, "brave-otter", "claudio/shared", true); err != nil {
		t.Fatalf("first AddWorktree: %v", err)
	}

	_, err = AddWorktree(ctx, root, "calm-finch", "claudio/shared", false)
	if err == nil {
		t.Fatal("expected branch collision error, got nil")
	}
	var collision *BranchCollisionError
	if !as(err, &collision) {
		t.Fatalf("expected *BranchCollisionError, got %T: %v", err, err)
	}
	if collision.Branch != "claudio/shared" {
		t.Errorf("collision.Branch = %q, want claudio/shared", collision.Branch)
	}
	if !strings.Contains(collision.WorktreeDir, "brave-otter") {
		t.Errorf("collision.WorktreeDir = %q, want it to name brave-otter", collision.WorktreeDir)
	}
}

func as(err error, target **BranchCollisionError) bool {
	for err != nil {
		if be, ok := err.(*BranchCollisionError); ok {
			*target = be
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestSuggestBranchName(t *testing.T) {
	taken := map[string]bool{"feat/auth": true, "feat/auth-2": true}
	got := SuggestBranchName("feat/auth", func(c string) bool { return taken[c] })
	if got != "feat/auth-3" {
		t.Errorf("SuggestBranchName = %q, want feat/auth-3", got)
	}

	got = SuggestBranchName("feat/new", func(c string) bool { return taken[c] })
	if got != "feat/new" {
		t.Errorf("SuggestBranchName = %q, want feat/new (no collision)", got)
	}
}

func TestRemoveWorktree(t *testing.T) {
	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	worktreeDir, err := AddWorktree(ctx, root, "brave-otter", "claudio/brave-otter", true)
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	if err := RemoveWorktree(ctx, root, "brave-otter"); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(worktreeDir); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still exists after remove: %v", err)
	}
	if _, err := os.Stat(root.MainClone); err != nil {
		t.Fatalf("root/main-clone was removed, should survive: %v", err)
	}
}

// RemoveWorktree must be idempotent: destroy removes the container
// before the worktree, so a failure here strands an instance with no
// container and no way to clear it — every subsequent destroy fails the
// same way (ROD-121).

// git prunes main-clone/.git/worktrees/<id> on its own (removing a
// repo's last worktree takes the whole directory with it), which used to
// make RemoveWorktree fail writing the reverse pointer into a parent
// that no longer existed. git also disowns the worktree at that point,
// so the leftover directory has to be removed directly.
func TestRemoveWorktreeWithMissingAdminDir(t *testing.T) {
	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	worktreeDir, err := AddWorktree(ctx, root, "brave-otter", "claudio/brave-otter", true)
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	adminDir := filepath.Join(root.MainClone, ".git", "worktrees", "brave-otter")
	if err := os.RemoveAll(adminDir); err != nil {
		t.Fatal(err)
	}

	if err := RemoveWorktree(ctx, root, "brave-otter"); err != nil {
		t.Fatalf("RemoveWorktree with a pruned admin dir: %v", err)
	}
	if _, err := os.Stat(worktreeDir); !os.IsNotExist(err) {
		t.Errorf("orphaned worktree dir survived removal: %v", err)
	}
	if _, err := os.Stat(root.MainClone); err != nil {
		t.Errorf("main-clone was removed, should survive: %v", err)
	}
}

// The mirror case: administrative state intact, working tree gone from
// disk (a user deleted it, or a previous run got half way).
func TestRemoveWorktreeWithMissingWorktreeDir(t *testing.T) {
	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	worktreeDir, err := AddWorktree(ctx, root, "brave-otter", "claudio/brave-otter", true)
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if err := os.RemoveAll(worktreeDir); err != nil {
		t.Fatal(err)
	}

	if err := RemoveWorktree(ctx, root, "brave-otter"); err != nil {
		t.Fatalf("RemoveWorktree with a missing worktree dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root.MainClone, ".git", "worktrees", "brave-otter")); !os.IsNotExist(err) {
		t.Errorf("admin dir survived removal: %v", err)
	}
}

// Re-running destroy after a partial failure must succeed rather than
// report an error for work already done.
func TestRemoveWorktreeIsIdempotent(t *testing.T) {
	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	if _, err := AddWorktree(ctx, root, "brave-otter", "claudio/brave-otter", true); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	if err := RemoveWorktree(ctx, root, "brave-otter"); err != nil {
		t.Fatalf("first RemoveWorktree: %v", err)
	}
	if err := RemoveWorktree(ctx, root, "brave-otter"); err != nil {
		t.Fatalf("second RemoveWorktree (nothing left to remove): %v", err)
	}
}

// The originally reported sequence: destroying a repo's last worktree
// takes .git/worktrees with it, which used to break the next destroy of
// any other instance in that same root.
func TestRemoveWorktreeAfterLastWorktreeRemoved(t *testing.T) {
	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	for _, id := range []string{"first-otter", "second-otter"} {
		if _, err := AddWorktree(ctx, root, id, "claudio/"+id, true); err != nil {
			t.Fatalf("AddWorktree(%s): %v", id, err)
		}
	}

	if err := RemoveWorktree(ctx, root, "first-otter"); err != nil {
		t.Fatalf("RemoveWorktree(first-otter): %v", err)
	}
	if err := RemoveWorktree(ctx, root, "second-otter"); err != nil {
		t.Fatalf("RemoveWorktree(second-otter) after the first: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root.Worktrees, "second-otter")); !os.IsNotExist(err) {
		t.Errorf("second worktree dir survived removal: %v", err)
	}
}

func TestInitRootGreenfield(t *testing.T) {
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := InitRoot(ctx, workspace, "market-research")
	if err != nil {
		t.Fatalf("InitRoot: %v", err)
	}
	worktreeDir, err := AddWorktree(ctx, root, "brave-otter", "claudio/brave-otter", true)
	if err != nil {
		t.Fatalf("AddWorktree on greenfield root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreeDir, ".git")); err != nil {
		t.Fatalf("worktree .git missing: %v", err)
	}
}

// TestWorktreeAcrossContainerBoundary reproduces docs/architecture.md
// Appendix B end to end: mounting only the worktree breaks git inside a
// container, but mounting the whole root with the worktree as working
// directory works after the gitdir pointers are relativized — and a
// commit made inside the container is visible from the host immediately.
// Skips if Docker is unavailable.
func TestWorktreeAcrossContainerBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping container test in -short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not reachable")
	}

	origin := newLocalOriginRepo(t)
	workspace := t.TempDir()
	ctx := context.Background()

	root, err := EnsureRoot(ctx, workspace, origin)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	worktreeDir, err := AddWorktree(ctx, root, "brave-otter", "claudio/brave-otter", true)
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	// Mounting only the worktree must fail, matching the documented
	// failure mode, before we prove the fix works.
	worktreeOnlyOut, err := exec.Command("docker", "run", "--rm",
		"-v", worktreeDir+":/workspace",
		"alpine/git", "-C", "/workspace", "status").CombinedOutput()
	if err == nil {
		t.Fatalf("expected mounting worktree alone to fail, but git succeeded: %s", worktreeOnlyOut)
	}

	// Mounting the whole root with the worktree as working dir must work.
	rootMountOut, err := exec.Command("docker", "run", "--rm",
		"-v", root.Path+":/repo",
		"-w", "/repo/worktrees/brave-otter",
		"alpine/git", "status").CombinedOutput()
	if err != nil {
		t.Fatalf("git status inside container with root mounted: %v: %s", err, rootMountOut)
	}

	// A commit made inside the container must be visible on the host
	// immediately, with no explicit sync step.
	commitOut, err := exec.Command("docker", "run", "--rm",
		"-v", root.Path+":/repo",
		"-w", "/repo/worktrees/brave-otter",
		"alpine/git",
		"-c", "user.email=test@example.com",
		"-c", "user.name=test",
		"commit", "--allow-empty", "-m", "from container").CombinedOutput()
	if err != nil {
		t.Fatalf("commit inside container: %v: %s", err, commitOut)
	}

	hostLog := runOut(t, worktreeDir, "log", "-1", "--pretty=%s")
	if strings.TrimSpace(hostLog) != "from container" {
		t.Fatalf("host does not see container's commit: log = %q", hostLog)
	}
}
