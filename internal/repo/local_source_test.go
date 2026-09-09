package repo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveSourceRemoteURLsPassThrough guards the pre-existing
// contract: adding local-path support must not reinterpret any of the
// remote forms docs/architecture.md §5.1 already accepts. In
// particular "acme/web" is a GitHub shorthand, not a relative directory.
func TestResolveSourceRemoteURLsPassThrough(t *testing.T) {
	cases := []string{
		"git@github.com:acme/web.git",
		"https://github.com/acme/web.git",
		"ssh://git@github.com/acme/web.git",
		"acme/web",
	}
	for _, in := range cases {
		got, err := ResolveSource(context.Background(), in)
		if err != nil {
			t.Fatalf("ResolveSource(%q): %v", in, err)
		}
		if got != in {
			t.Errorf("ResolveSource(%q) = %q, want it unchanged", in, got)
		}
	}
}

// TestResolveSourceExistingRepo covers the first acceptance criterion:
// `claudio create .` inside an existing git repo. The returned source
// must be a file:// URL naming the repo's absolute path, so Slug and
// EnsureRoot's existing clone path handle it with no special casing.
func TestResolveSourceExistingRepo(t *testing.T) {
	dir := t.TempDir()
	// t.TempDir can hand back a symlinked path (/var -> /private/var on
	// macOS); resolve it so the comparison below tests ResolveSource's
	// behavior rather than the platform's symlink layout.
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	run(t, "", "init", dir)

	got, err := ResolveSource(context.Background(), dir)
	if err != nil {
		t.Fatalf("ResolveSource(%q): %v", dir, err)
	}
	if got != "file://"+dir {
		t.Errorf("ResolveSource(%q) = %q, want %q", dir, got, "file://"+dir)
	}
	// It must not have re-inited or otherwise disturbed an existing repo.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf("existing repo lost its .git: %v", err)
	}
}

// TestResolveSourceRelativePathResolvesToAbsolute covers the literal
// `claudio create .` form: a relative path must become an absolute
// file:// URL, because the slug (and therefore the on-disk root) has to
// be stable no matter which directory the command was invoked from.
func TestResolveSourceRelativePathResolvesToAbsolute(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	run(t, "", "init", dir)
	t.Chdir(dir)

	got, err := ResolveSource(context.Background(), ".")
	if err != nil {
		t.Fatalf("ResolveSource(\".\"): %v", err)
	}
	if got != "file://"+dir {
		t.Errorf("ResolveSource(\".\") = %q, want %q", got, "file://"+dir)
	}
}

// TestResolveSourceInitsNonRepo covers the second acceptance criterion:
// a directory that is not a git repo is `git init`ed first. The initial
// commit matters — EnsureRoot clones this directory, and cloning a repo
// with no commits yields a clone with no branch for a worktree to check
// out.
func TestResolveSourceInitsNonRepo(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("work in progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveSource(context.Background(), dir)
	if err != nil {
		t.Fatalf("ResolveSource(%q): %v", dir, err)
	}
	if got != "file://"+dir {
		t.Errorf("ResolveSource(%q) = %q, want %q", dir, got, "file://"+dir)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("directory was not git-initialized: %v", err)
	}

	// The pre-existing file must be committed, not just left untracked:
	// EnsureRoot clones committed history only, so an uncommitted file
	// would silently not reach the instance — the exact "does my work
	// come along?" confusion §5.1 warned about.
	out := output(t, dir, "log", "--oneline")
	if strings.TrimSpace(out) == "" {
		t.Fatal("git-initialized directory has no commit; a clone of it would have no branch to check out")
	}
	files := output(t, dir, "ls-files")
	if !strings.Contains(files, "notes.md") {
		t.Errorf("pre-existing file not committed by ResolveSource; tracked files = %q", files)
	}
}

// TestResolveSourceInitIsIdempotent verifies a second `claudio create .`
// against a directory ResolveSource already initialized does not create
// a second empty commit or otherwise churn history.
func TestResolveSourceInitIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := ResolveSource(ctx, dir); err != nil {
		t.Fatalf("first ResolveSource: %v", err)
	}
	first := output(t, dir, "rev-parse", "HEAD")

	if _, err := ResolveSource(ctx, dir); err != nil {
		t.Fatalf("second ResolveSource: %v", err)
	}
	if second := output(t, dir, "rev-parse", "HEAD"); second != first {
		t.Errorf("second ResolveSource moved HEAD: %q -> %q", first, second)
	}
}

// TestResolveSourceRejectsMissingPath keeps a typo'd remote-ish argument
// from being silently treated as a directory to create.
func TestResolveSourceRejectsMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := ResolveSource(context.Background(), missing); err == nil {
		t.Fatalf("ResolveSource(%q) succeeded, want an error for a nonexistent path", missing)
	}
}

// TestResolveSourceRejectsFile guards against pointing create at a file
// rather than a directory.
func TestResolveSourceRejectsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveSource(context.Background(), file); err == nil {
		t.Fatalf("ResolveSource(%q) succeeded, want an error for a non-directory", file)
	}
}

// TestLocalSourceEndToEnd is the integration guarantee behind both
// acceptance criteria: a local directory resolves, clones into a root,
// and yields a usable worktree — the same shape the remote-URL path
// produces. It also pins the semantics verified empirically for this
// issue: committed history and local-only branches come along;
// uncommitted work does not.
func TestLocalSourceEndToEnd(t *testing.T) {
	src := t.TempDir()
	src, err := filepath.EvalSymlinks(src)
	if err != nil {
		t.Fatal(err)
	}
	run(t, "", "init", src)
	run(t, src, "config", "user.email", "test@example.com")
	run(t, src, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(src, "committed.txt"), []byte("in history\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, src, "add", "committed.txt")
	run(t, src, "commit", "-m", "initial")

	// A branch that exists only here, with no remote anywhere.
	run(t, src, "branch", "local-only")

	// Uncommitted work that must NOT reach the instance.
	if err := os.WriteFile(filepath.Join(src, "scratch.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	source, err := ResolveSource(ctx, src)
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}

	root, err := EnsureRoot(ctx, t.TempDir(), source)
	if err != nil {
		t.Fatalf("EnsureRoot from a local source: %v", err)
	}
	worktreeDir, err := AddWorktree(ctx, root, "brave-otter", "claudio/brave-otter", true)
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(worktreeDir, "committed.txt")); err != nil {
		t.Errorf("committed file did not reach the worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreeDir, "scratch.txt")); !os.IsNotExist(err) {
		t.Errorf("uncommitted file reached the worktree (err=%v); clone must carry committed history only", err)
	}
	// The local-only branch is reachable in the clone as a remote-tracking
	// ref, which is what lets a later --branch check it out.
	refs := output(t, root.MainClone, "branch", "-a")
	if !strings.Contains(refs, "local-only") {
		t.Errorf("local-only branch did not reach the clone; refs = %q", refs)
	}
}

// TestSlugLocalPathsAreDistinctPerDirectory pins that two repos with the
// same basename in different directories get different roots — the slug
// is derived from the full path, so `create .` in ~/a/web and ~/b/web
// never share a clone.
func TestSlugLocalPathsAreDistinctPerDirectory(t *testing.T) {
	a, err := Slug("file:///Users/rod/a/web")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Slug("file:///Users/rod/b/web")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("slugs collide for different directories with the same basename: both %q", a)
	}
}

func output(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// TestResolveSourceExpandsTilde covers a "~/path" argument reaching
// ResolveSource unexpanded — a shell normally expands it, but a quoted
// argument or a programmatic caller does not, and treating "~" as a
// literal directory name would silently resolve to ./~/... instead of
// the user's home.
func TestResolveSourceExpandsTilde(t *testing.T) {
	home := t.TempDir()
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	sub := filepath.Join(home, "proj")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, "", "init", sub)

	got, err := ResolveSource(context.Background(), "~/proj")
	if err != nil {
		t.Fatalf("ResolveSource(\"~/proj\"): %v", err)
	}
	if got != "file://"+sub {
		t.Errorf("ResolveSource(\"~/proj\") = %q, want %q", got, "file://"+sub)
	}
}
