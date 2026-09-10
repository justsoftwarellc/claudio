package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
	return string(out)
}

// newUpstream creates a repo with one commit, standing in for GitHub.
func newUpstream(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "c1")
	return dir
}

func commitTo(t *testing.T, dir, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", msg)
}

func TestClassifySourceRemote(t *testing.T) {
	src := ClassifySource(context.Background(), "git@github.com:acme/web.git")
	if src.Kind != SourceRemote {
		t.Errorf("Kind = %v, want SourceRemote", src.Kind)
	}
	if src.FetchURL != "git@github.com:acme/web.git" {
		t.Errorf("FetchURL = %q, want the URL itself", src.FetchURL)
	}
}

func TestClassifySourceGreenfieldIsLocalOnly(t *testing.T) {
	// `claudio create --new` stores a synthetic "local:<name>" URL that
	// is not clonable — it must never be treated as something to fetch.
	if got := ClassifySource(context.Background(), "local:prototype").Kind; got != SourceLocalOnly {
		t.Errorf("Kind = %v, want SourceLocalOnly", got)
	}
	if got := ClassifySource(context.Background(), "").Kind; got != SourceLocalOnly {
		t.Errorf("empty URL Kind = %v, want SourceLocalOnly", got)
	}
}

func TestClassifySourceLocalWithoutOriginIsLocalOnly(t *testing.T) {
	dir := newUpstream(t) // a repo, but with no remote configured
	src := ClassifySource(context.Background(), "file://"+dir)
	if src.Kind != SourceLocalOnly {
		t.Errorf("Kind = %v, want SourceLocalOnly for a repo with no origin", src.Kind)
	}
}

// The prototype-then-push case from the requirements: a folder created
// with no upstream gains one later, and must start being refreshed on
// its next create with no migration and nothing to re-run.
func TestClassifySourcePicksUpAnOriginAddedLater(t *testing.T) {
	up := newUpstream(t)
	dir := newUpstream(t)
	ctx := context.Background()

	if got := ClassifySource(ctx, "file://"+dir).Kind; got != SourceLocalOnly {
		t.Fatalf("before adding an origin: Kind = %v, want SourceLocalOnly", got)
	}

	git(t, dir, "remote", "add", "origin", up)

	src := ClassifySource(ctx, "file://"+dir)
	if src.Kind != SourceLocalWithUpstream {
		t.Errorf("after adding an origin: Kind = %v, want SourceLocalWithUpstream", src.Kind)
	}
	if src.FetchURL != up {
		t.Errorf("FetchURL = %q, want the newly added origin %q", src.FetchURL, up)
	}
}

// A source directory that has been moved or deleted since the instance
// was created must not break a create — it degrades to no-refresh.
func TestClassifySourceMissingDirIsLocalOnly(t *testing.T) {
	if got := ClassifySource(context.Background(), "file:///nonexistent/gone").Kind; got != SourceLocalOnly {
		t.Errorf("Kind = %v, want SourceLocalOnly for a missing source dir", got)
	}
}

// The core regression test: a second create must see commits made to the
// upstream after main-clone was first cloned. Without RefreshMainClone
// this fails — main-clone is cloned once and never fetched again.
func TestRefreshMainClonePicksUpNewUpstreamCommits(t *testing.T) {
	ctx := context.Background()
	up := newUpstream(t)
	workspace := t.TempDir()

	root, err := EnsureRoot(ctx, workspace, "file://"+up)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}

	// Upstream moves on after the clone — the staleness window.
	commitTo(t, up, "v2\n", "c2")

	src := Source{Kind: SourceRemote, FetchURL: up}
	if err := RefreshMainClone(ctx, root, src, "main"); err != nil {
		t.Fatalf("RefreshMainClone: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root.MainClone, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2\n" {
		t.Errorf("main-clone f.txt = %q, want %q — the refresh did not take", got, "v2\n")
	}

	// And the worktree cut afterwards inherits it, which is the whole point.
	wt, err := AddWorktree(ctx, root, "inst1", "claudio/inst1", true)
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	got, err = os.ReadFile(filepath.Join(wt, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2\n" {
		t.Errorf("worktree f.txt = %q, want %q", got, "v2\n")
	}
}

// A refresh must leave main-clone coherent, not just move the ref.
// `git update-ref` alone passes the content check above only because
// main-clone's own working tree is never read — but it leaves phantom
// staged deletions in the index that would surface later.
func TestRefreshLeavesMainCloneClean(t *testing.T) {
	ctx := context.Background()
	up := newUpstream(t)
	workspace := t.TempDir()

	root, err := EnsureRoot(ctx, workspace, "file://"+up)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}

	// A commit that both modifies and adds files — an added file is what
	// shows up as a phantom deletion under a ref-only update.
	if err := os.WriteFile(filepath.Join(up, "added.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitTo(t, up, "v2\n", "c2")

	if err := RefreshMainClone(ctx, root, Source{Kind: SourceRemote, FetchURL: up}, "main"); err != nil {
		t.Fatalf("RefreshMainClone: %v", err)
	}

	if out := git(t, root.MainClone, "status", "--porcelain"); out != "" {
		t.Errorf("main-clone is dirty after refresh:\n%s", out)
	}
}

// Local-only sources have nothing to fetch from; a refresh must be a
// silent no-op rather than an error or a bogus git call.
func TestRefreshLocalOnlyIsNoOp(t *testing.T) {
	ctx := context.Background()
	up := newUpstream(t)
	workspace := t.TempDir()
	root, err := EnsureRoot(ctx, workspace, "file://"+up)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	if err := RefreshMainClone(ctx, root, Source{Kind: SourceLocalOnly}, "main"); err != nil {
		t.Errorf("RefreshMainClone on a local-only source = %v, want nil", err)
	}
}

func TestDefaultBranchReportsMainClonesBranch(t *testing.T) {
	ctx := context.Background()
	up := newUpstream(t)
	root, err := EnsureRoot(ctx, t.TempDir(), "file://"+up)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	branch, err := DefaultBranch(ctx, root)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("DefaultBranch = %q, want %q", branch, "main")
	}
}

func TestRemoteBranchesListsHeads(t *testing.T) {
	up := newUpstream(t)
	git(t, up, "branch", "feat/auth")

	branches, err := RemoteBranches(context.Background(), up)
	if err != nil {
		t.Fatalf("RemoteBranches: %v", err)
	}
	want := map[string]bool{"main": false, "feat/auth": false}
	for _, b := range branches {
		if _, ok := want[b]; ok {
			want[b] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("branch %q missing from %v", name, branches)
		}
	}
}

// A bare file:// repo is what tests and mirrors use as a stand-in for a
// remote, and it is a remote in every way that matters: nobody edits it
// in place, so it refreshes silently with no branch prompt. Classifying
// it as a local working copy instead would skip the refresh entirely
// (a bare repo has no origin of its own) — which is exactly how this
// was first written, and what the end-to-end create test caught.
func TestClassifySourceBareFileRepoIsRemote(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "--bare")

	src := ClassifySource(context.Background(), "file://"+dir)
	if src.Kind != SourceRemote {
		t.Errorf("Kind = %v, want SourceRemote for a bare file:// repo", src.Kind)
	}
	if src.FetchURL != "file://"+dir {
		t.Errorf("FetchURL = %q, want the file:// URL itself", src.FetchURL)
	}
}

// A local working copy with an upstream is the prompting case, and must
// fetch from the *upstream*, never from the working copy itself — a
// clone does not copy remotes, so main-clone's own origin points at the
// working copy and knows nothing about the real remote.
func TestClassifySourceWorkingCopyFetchesFromItsUpstream(t *testing.T) {
	up := newUpstream(t)
	work := t.TempDir()
	git(t, "", "clone", "-q", up, work)

	src := ClassifySource(context.Background(), "file://"+work)
	if src.Kind != SourceLocalWithUpstream {
		t.Fatalf("Kind = %v, want SourceLocalWithUpstream", src.Kind)
	}
	if src.FetchURL != up {
		t.Errorf("FetchURL = %q, want the upstream %q, not the working copy", src.FetchURL, up)
	}
}
