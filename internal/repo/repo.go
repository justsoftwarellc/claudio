// Package repo implements Claudio's repository model: one clone per repo
// ("root"), one git worktree per session. See docs/architecture.md §5.1
// and Appendix B.
//
// All git operations here shell out to the host `git` binary rather than
// using a Go git library — worktree administrative files (relative gitdir
// pointers, the reverse pointer under main-clone/.git/worktrees/<name>)
// are exact-format-sensitive, and shelling out guarantees byte-for-byte
// the same behavior a human running git would see, which is what the
// verified fix in Appendix B depends on.
package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Root is one repository's clone directory: <workspace>/repos/<slug>.
// MainClone holds .git and the object store; Worktrees holds one
// subdirectory per session.
type Root struct {
	Path      string
	MainClone string
	Worktrees string
}

func rootPaths(workspaceRoot, slug string) Root {
	root := filepath.Join(workspaceRoot, "repos", slug)
	return Root{
		Path:      root,
		MainClone: filepath.Join(root, "main-clone"),
		Worktrees: filepath.Join(root, "worktrees"),
	}
}

// RootFromPath reconstructs Root from a root path already known to
// exist — store.Instance.RepoRoot, persisted at create time — without
// going through EnsureRoot/InitRoot's clone-or-init logic. Callers that
// already have a live instance row (e.g. DestroyInstance) should always
// use this instead of re-deriving the root from RepoURL: EnsureRoot
// assumes RepoURL is a real, clonable remote, which is false for a
// greenfield instance's synthetic "local:<name>" RepoURL — verified
// empirically, `claudio destroy` on a --new instance tried to `git
// clone local:market-research` and failed with a DNS resolution error
// for host "local".
func RootFromPath(rootPath string) Root {
	return Root{
		Path:      rootPath,
		MainClone: filepath.Join(rootPath, "main-clone"),
		Worktrees: filepath.Join(rootPath, "worktrees"),
	}
}

// Slug derives the on-disk root directory name from a repo URL, e.g.
// "git@github.com:acme/web.git" -> "github.com-acme-web". Deterministic so
// a second `create` against the same repo finds the same root rather than
// cloning again.
func Slug(repoURL string) (string, error) {
	host, path, err := parseRepoURL(repoURL)
	if err != nil {
		return "", err
	}
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	slug := host + "-" + strings.ReplaceAll(path, "/", "-")
	if slug == "" {
		return "", fmt.Errorf("repo: empty slug derived from %q", repoURL)
	}
	return slug, nil
}

var (
	scpLikeRe = regexp.MustCompile(`^(?:[\w.-]+@)?([\w.-]+):(.+)$`)
)

// parseRepoURL accepts git@host:path, ssh://[user@]host/path, and
// https://host/path, and returns (host, path). Local-path cloning is
// deliberately unsupported (docs/architecture.md §5.1) so this never
// needs to handle a bare filesystem path.
func parseRepoURL(repoURL string) (host, path string, err error) {
	switch {
	case strings.HasPrefix(repoURL, "file://"):
		// Only reachable via a test/CI mirror URL, never via NormalizeSSH
		// (docs/architecture.md §5.1 rules out local-path cloning as user
		// input). Still needs a slug so EnsureRoot can derive a root dir.
		return "local", strings.TrimPrefix(repoURL, "file://"), nil
	case strings.HasPrefix(repoURL, "ssh://"), strings.HasPrefix(repoURL, "https://"), strings.HasPrefix(repoURL, "http://"):
		rest := repoURL
		rest = strings.TrimPrefix(rest, "ssh://")
		rest = strings.TrimPrefix(rest, "https://")
		rest = strings.TrimPrefix(rest, "http://")
		if at := strings.Index(rest, "@"); at != -1 && strings.Index(rest, "/") > at {
			rest = rest[at+1:]
		}
		slash := strings.Index(rest, "/")
		if slash == -1 {
			return "", "", fmt.Errorf("repo: cannot parse URL %q", repoURL)
		}
		return rest[:slash], rest[slash+1:], nil
	default:
		m := scpLikeRe.FindStringSubmatch(repoURL)
		if m == nil {
			return "", "", fmt.Errorf("repo: cannot parse URL %q", repoURL)
		}
		return m[1], m[2], nil
	}
}

// NormalizeSSH turns an HTTPS GitHub URL or an "owner/repo" shorthand into
// an SSH clone URL, per docs/architecture.md §5.1: "always a remote GitHub
// URL, cloned over SSH." A URL that is already SSH-shaped passes through
// unchanged.
func NormalizeSSH(input string) (string, error) {
	switch {
	case strings.HasPrefix(input, "git@"):
		return input, nil
	case strings.HasPrefix(input, "https://github.com/"):
		rest := strings.TrimPrefix(input, "https://github.com/")
		rest = strings.TrimSuffix(rest, ".git")
		if rest == "" {
			return "", fmt.Errorf("repo: cannot parse URL %q", input)
		}
		return fmt.Sprintf("git@github.com:%s.git", rest), nil
	case strings.Contains(input, "/") && !strings.Contains(input, ":") && !strings.Contains(input, "@"):
		// "acme/web" shorthand.
		if strings.Count(input, "/") != 1 {
			return "", fmt.Errorf("repo: cannot parse shorthand %q", input)
		}
		return fmt.Sprintf("git@github.com:%s.git", input), nil
	default:
		return "", fmt.Errorf("repo: cannot parse URL %q", input)
	}
}

// EnsureRoot returns the Root for repoURL, cloning it on the host over SSH
// if it does not already exist on disk. The clone is bare-ish in spirit
// but a normal (non-bare) clone: main-clone is a real checkout of the
// default branch, which the first worktree is added alongside.
//
// Cloning happens at most once per repo (docs/architecture.md §5.1):
// subsequent calls for the same repoURL are a no-op fast path.
func EnsureRoot(ctx context.Context, workspaceRoot, repoURL string) (Root, error) {
	slug, err := Slug(repoURL)
	if err != nil {
		return Root{}, err
	}
	root := rootPaths(workspaceRoot, slug)

	if _, err := os.Stat(filepath.Join(root.MainClone, ".git")); err == nil {
		return root, nil // already cloned
	} else if !errors.Is(err, os.ErrNotExist) {
		return Root{}, fmt.Errorf("repo: stat %s: %w", root.MainClone, err)
	}

	if err := os.MkdirAll(root.Worktrees, 0o755); err != nil {
		return Root{}, fmt.Errorf("repo: create root %s: %w", root.Path, err)
	}
	if _, err := runGit(ctx, "", "clone", repoURL, root.MainClone); err != nil {
		return Root{}, fmt.Errorf("repo: clone %s: %w", repoURL, err)
	}
	return root, nil
}

// InitRoot creates a Root for a greenfield initiative with no upstream
// repo (docs/architecture.md §5.1: `claudio create --new <name>`).
// main-clone is `git init`ed instead of cloned.
func InitRoot(ctx context.Context, workspaceRoot, name string) (Root, error) {
	root := rootPaths(workspaceRoot, name)
	if _, err := os.Stat(filepath.Join(root.MainClone, ".git")); err == nil {
		return Root{}, fmt.Errorf("repo: root %q already exists", name)
	}
	if err := os.MkdirAll(root.MainClone, 0o755); err != nil {
		return Root{}, fmt.Errorf("repo: create root %s: %w", root.Path, err)
	}
	if err := os.MkdirAll(root.Worktrees, 0o755); err != nil {
		return Root{}, fmt.Errorf("repo: create worktrees dir: %w", err)
	}
	if _, err := runGit(ctx, root.MainClone, "init"); err != nil {
		return Root{}, fmt.Errorf("repo: init %s: %w", name, err)
	}
	if _, err := runGit(ctx, root.MainClone, "commit", "--allow-empty", "-m", "Initial commit"); err != nil {
		return Root{}, fmt.Errorf("repo: initial commit for %s: %w", name, err)
	}
	return root, nil
}

// BranchCollisionError reports that a branch is already checked out in
// another worktree, naming which one — docs/architecture.md §5.1 requires
// surfacing the holder and a way forward, never just a bare error.
type BranchCollisionError struct {
	Branch      string
	WorktreeDir string // the other worktree's path, as git reports it
}

func (e *BranchCollisionError) Error() string {
	return fmt.Sprintf("branch %q is already checked out at %s", e.Branch, e.WorktreeDir)
}

// AddWorktree creates a new worktree named id, checking out branch
// (creating it from the default branch first if newBranch is true), then
// rewrites the gitdir pointers to relative paths per Appendix B so the
// worktree remains valid both on the host and when only the repo root is
// mounted into a container.
//
// Returns *BranchCollisionError if branch is already checked out
// elsewhere; callers are expected to offer the caller a suggested
// alternative name (docs/architecture.md §5.1) rather than just
// propagating the error.
func AddWorktree(ctx context.Context, root Root, id, branch string, newBranch bool) (worktreeDir string, err error) {
	worktreeDir = filepath.Join(root.Worktrees, id)

	args := []string{"worktree", "add"}
	if newBranch {
		args = append(args, "-b", branch, worktreeDir)
	} else {
		args = append(args, worktreeDir, branch)
	}

	out, err := runGit(ctx, root.MainClone, args...)
	if err != nil {
		if held, ok := parseWorktreeCollision(out); ok {
			return "", &BranchCollisionError{Branch: branch, WorktreeDir: held}
		}
		return "", fmt.Errorf("repo: add worktree %s: %w", id, err)
	}

	if err := relativizeGitdir(root, id); err != nil {
		return "", err
	}
	return worktreeDir, nil
}

var worktreeCollisionRe = regexp.MustCompile(`already (?:used|checked out) (?:by|at) worktree ['"]?([^'"\s]+)['"]?|already checked out at '([^']+)'`)

func parseWorktreeCollision(gitOutput string) (heldAt string, ok bool) {
	if !strings.Contains(gitOutput, "already") {
		return "", false
	}
	m := worktreeCollisionRe.FindStringSubmatch(gitOutput)
	if m == nil {
		return "", false
	}
	if m[1] != "" {
		return m[1], true
	}
	return m[2], true
}

// relativizeGitdir rewrites the two gitdir pointers created by `git
// worktree add` from absolute host paths to paths relative to each
// other, per the verified fix in docs/architecture.md Appendix B. Without
// this, a container that mounts only the repo root (not the host's
// absolute path) cannot resolve either pointer.
func relativizeGitdir(root Root, id string) error {
	worktreeGitFile := filepath.Join(root.Worktrees, id, ".git")
	adminDir := filepath.Join(root.MainClone, ".git", "worktrees", id)
	reverseGitdirFile := filepath.Join(adminDir, "gitdir")

	if _, err := os.Stat(adminDir); err != nil {
		return fmt.Errorf("repo: worktree admin dir missing after add: %w", err)
	}

	// worktrees/<id>/.git -> "gitdir: ../../main-clone/.git/worktrees/<id>"
	relGitdir := filepath.Join("..", "..", "main-clone", ".git", "worktrees", id)
	if err := os.WriteFile(worktreeGitFile, []byte("gitdir: "+relGitdir+"\n"), 0o644); err != nil {
		return fmt.Errorf("repo: rewrite %s: %w", worktreeGitFile, err)
	}

	// main-clone/.git/worktrees/<id>/gitdir -> "../../worktrees/<id>/.git"
	relReverse := filepath.Join("..", "..", "worktrees", id, ".git")
	if err := os.WriteFile(reverseGitdirFile, []byte(relReverse+"\n"), 0o644); err != nil {
		return fmt.Errorf("repo: rewrite %s: %w", reverseGitdirFile, err)
	}
	return nil
}

// RemoveWorktree removes worktree id via `git worktree remove` and prunes
// stale administrative metadata. The root and its object store are left
// untouched (docs/architecture.md §5.1: "the root and its objects stay");
// orphaned roots are reclaimed separately by `claudio gc` (ROD-107).
//
// Removal restores the reverse gitdir pointer to an absolute host path
// first: git's own `worktree remove`/`worktree list` machinery validates
// that pointer and refuses to operate ("does not contain absolute path to
// the working tree location") once it has been relativized for the
// container boundary (relativizeGitdir). That is safe to do here because
// removal always runs on the host, where the absolute path is valid.
//
// This is idempotent, and deliberately so (ROD-121): DestroyInstance
// removes the container before the worktree, so an error here strands an
// instance with no container that no subsequent destroy can clear. Each
// half of a worktree — git's administrative directory and the working
// tree itself — may already be gone, and neither absence is a failure:
//
//   - The admin dir is pruned by git on its own; removing a repo's last
//     worktree takes the whole .git/worktrees directory with it. Writing
//     the reverse pointer into a parent that no longer exists was the
//     original bug.
//   - Once the admin dir is gone git disowns the working tree entirely
//     ("is not a working tree"), so `worktree remove` cannot clean up the
//     leftover directory and this has to remove it directly.
func RemoveWorktree(ctx context.Context, root Root, id string) error {
	adminDir := filepath.Join(root.MainClone, ".git", "worktrees", id)
	worktreeDir := filepath.Join(root.Worktrees, id)

	_, adminErr := os.Stat(adminDir)
	adminExists := adminErr == nil
	if adminErr != nil && !os.IsNotExist(adminErr) {
		return fmt.Errorf("repo: stat worktree admin dir for %s: %w", id, adminErr)
	}

	if adminExists {
		absPointer := filepath.Join(worktreeDir, ".git") + "\n"
		if err := os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(absPointer), 0o644); err != nil {
			return fmt.Errorf("repo: restore absolute gitdir for %s: %w", id, err)
		}

		if _, err := runGit(ctx, root.MainClone, "worktree", "remove", "--force", worktreeDir); err != nil {
			return fmt.Errorf("repo: remove worktree %s: %w", id, err)
		}
	} else if err := os.RemoveAll(worktreeDir); err != nil {
		// git no longer knows about this path, so nothing else will ever
		// clean it up — and it occupies the directory a future create for
		// the same id would want.
		return fmt.Errorf("repo: remove orphaned worktree dir %s: %w", worktreeDir, err)
	}

	// Unconditional: this is what clears whatever administrative state is
	// left, including after the orphan path above, and it is a no-op on a
	// repo with nothing to prune.
	if _, err := runGit(ctx, root.MainClone, "worktree", "prune"); err != nil {
		return fmt.Errorf("repo: prune worktrees: %w", err)
	}
	return nil
}

// SuggestBranchName returns branch with a numeric suffix incremented past
// any name for which exists returns true, e.g. "feat/auth" ->
// "feat/auth-2" -> "feat/auth-3". docs/architecture.md §5.1.
func SuggestBranchName(branch string, exists func(candidate string) bool) string {
	if !exists(branch) {
		return branch
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", branch, n)
		if !exists(candidate) {
			return candidate
		}
	}
}

// BranchExists reports whether branch is a known local branch in the
// repo's main clone.
func BranchExists(ctx context.Context, root Root, branch string) bool {
	_, err := runGit(ctx, root.MainClone, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

const envFileRelPath = ".claudio/env"

// ExcludeClaudioDir adds .claudio/ to the main clone's
// .git/info/exclude, so CopyEnvFile's target directory is never
// accidentally committed or shown as untracked by `git status` inside
// any worktree — docs/architecture.md §5.1: "--env-file copies a host
// env file into the worktree at provision time. Add .claudio/ to
// .git/info/exclude." info/exclude (not .gitignore) is used because
// this is host-local provisioning behavior, not a rule the repo itself
// should carry in its own committed history.
//
// Idempotent and safe to call on every create against the same repo
// root: appends the line only if it isn't already present.
func ExcludeClaudioDir(root Root) error {
	excludePath := filepath.Join(root.MainClone, ".git", "info", "exclude")
	existing, err := os.ReadFile(excludePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("repo: read %s: %w", excludePath, err)
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == ".claudio/" {
			return nil // already present
		}
	}

	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return fmt.Errorf("repo: create %s: %w", filepath.Dir(excludePath), err)
	}
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("repo: open %s: %w", excludePath, err)
	}
	defer f.Close()
	if _, err := f.WriteString(".claudio/\n"); err != nil {
		return fmt.Errorf("repo: append to %s: %w", excludePath, err)
	}
	return nil
}

// CopyEnvFile copies hostPath's contents into worktreeDir/.claudio/env —
// the `--env-file` mechanism from docs/architecture.md §5.1. Copies
// verbatim; this package does not parse or validate the file's
// contents, matching the doc's "copies a host env file into the
// worktree" (not "injects it as container environment," which is a
// separate, unrequested scope — the repo's own tooling, e.g. a dotenv
// loader the agent runs, is what reads this file back).
func CopyEnvFile(hostPath, worktreeDir string) error {
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return fmt.Errorf("repo: read env file %s: %w", hostPath, err)
	}
	dest := filepath.Join(worktreeDir, envFileRelPath)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("repo: create %s: %w", filepath.Dir(dest), err)
	}
	if err := os.WriteFile(dest, data, 0o600); err != nil {
		return fmt.Errorf("repo: write %s: %w", dest, err)
	}
	return nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
