package core

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/idgen"
	"github.com/rodrigomorales/claudio/internal/portdetect"
	"github.com/rodrigomorales/claudio/internal/repo"
	"github.com/rodrigomorales/claudio/internal/store"
)

// CreateStore is the subset of *store.Store CreateInstance needs. Kept
// as an interface for the same reason as InstanceStore/AdoptStore —
// core stays testable without a real SQLite file (docs/architecture.md
// §12.4).
type CreateStore interface {
	CreateInstance(ctx context.Context, p store.NewInstanceParams) error
	GetInstance(ctx context.Context, idOrName string) (store.Instance, error)
	TransitionProvisionStep(ctx context.Context, instanceID string, step store.ProvisionStep) error
	MarkFailed(ctx context.Context, instanceID string) error
	SetContainerID(ctx context.Context, instanceID, containerID string) error
	AllocatePort(ctx context.Context, instanceID string, containerPort int, serviceName string, source store.PortSource, detectedFrom *string, rangeLow, rangeHigh int) (int, error)
	UpsertRepo(ctx context.Context, rootPath, repoURL string) error
}

// CreateParams is everything `claudio create` collects from flags and
// resolved config before any provisioning starts.
type CreateParams struct {
	RepoURL string
	// GreenfieldName, when set, provisions a brand-new initiative with no
	// upstream repo instead of cloning RepoURL — `claudio create --new
	// <name>` (docs/architecture.md §5.1). RepoURL and Branch are ignored
	// when this is set (a fresh git-init has no existing branch for
	// Branch to check out); NewBranch still applies — it names the
	// branch this call creates, defaulting to claudio/<id> like the
	// cloned-repo path does, since the greenfield root's own main clone
	// occupies its default branch and a worktree cannot also check that
	// out (the same "one instance = one line of work" rule ROD-97
	// verified for cloned repos).
	GreenfieldName string
	Branch         string // explicit --branch: check out, must already exist
	NewBranch      string // explicit --new-branch: create from the default branch
	Name           *string
	ManualPorts    []portdetect.Manual
	Env            map[string]string // credential + any extra vars, e.g. CLAUDE_CODE_OAUTH_TOKEN
	EnvFile        string            // --env-file: host path to copy into the worktree at .claudio/env (docs/architecture.md §5.1)
	WorkspaceRoot  string            // global config's workspace_root, already expanded
	Image          string
	Resources      engine.ResourceLimits
	PortRangeLow   int
	PortRangeHigh  int
	DockerHost     string

	// CleanOnFail reverses the default in docs/architecture.md §4.1/
	// ROD-99 ("FAILED preserves the workspace for inspection unless
	// --clean-on-fail"): when true, a failure during provisioning removes
	// the worktree this call already created, instead of leaving it for
	// the user to inspect. Only applies once a worktree actually exists —
	// a failure before that point (branch resolution, worktree creation
	// itself) has nothing to clean up.
	CleanOnFail bool
}

// CreateResult is what a caller (the CLI) needs to report success.
type CreateResult struct {
	InstanceID  string
	Branch      string
	WorktreeDir string
	ContainerID string
	Ports       []store.PortMapping
}

// CreateInstance runs the full provisioning state machine from
// docs/architecture.md §4.1: repo root + worktree, port detection and
// allocation, container creation, in that order — advancing
// provision_step after each stage completes so a crash mid-provision
// resumes rather than restarts (ROD-99). A failure at any stage marks
// the instance StepFailed rather than leaving it silently stuck; the
// worktree and any already-reserved ports are left in place for the
// user to inspect or retry, per docs/architecture.md's stance that
// destroying state on failure risks losing diagnostic information.
func CreateInstance(ctx context.Context, st CreateStore, params CreateParams) (CreateResult, error) {
	return createInstanceWithCmd(ctx, st, params, nil)
}

// createInstanceWithCmd is CreateInstance's real implementation, taking
// an extra engine.CreateSpec.Cmd override that only tests use — to keep
// a non-Claudio image (e.g. "alpine", used so tests don't need the real
// claudio/base image built) alive long enough to assert against, the
// same way internal/engine's own tests do. Production callers always go
// through CreateInstance, which passes nil (use the image's own
// entrypoint).
func createInstanceWithCmd(ctx context.Context, st CreateStore, params CreateParams, cmd []string) (CreateResult, error) {
	id, err := uniqueID(ctx, st)
	if err != nil {
		return CreateResult{}, err
	}

	var root repo.Root
	var branch string
	var newBranch bool
	repoURL := params.RepoURL

	if params.GreenfieldName != "" {
		// docs/architecture.md §5.1: no upstream repo, git-init a fresh
		// root instead.
		repoURL = "local:" + params.GreenfieldName // synthetic, for the repo_url column and claudio.repo label — never a real clone URL
		root, err = repo.InitRoot(ctx, params.WorkspaceRoot, params.GreenfieldName)
		if err != nil {
			return CreateResult{}, fmt.Errorf("core: create %s: %w", id, err)
		}
		// InitRoot's main clone is itself a normal (non-bare) checkout of
		// its default branch — verified empirically: a worktree cannot
		// check out that same branch too, git refuses with "already
		// checked out," the exact rule that makes "one instance = one
		// line of work" structural for the cloned-repo path (ROD-97).
		// So, same as that path's own default, this always creates a new
		// branch off the current one (claudio/<id> unless the caller
		// asked for something else) rather than reusing it directly.
		branch = params.NewBranch
		if branch == "" {
			branch = "claudio/" + id
		}
		newBranch = true
	} else {
		root, err = repo.EnsureRoot(ctx, params.WorkspaceRoot, params.RepoURL)
		if err != nil {
			return CreateResult{}, fmt.Errorf("core: create %s: %w", id, err)
		}
		branch, newBranch, err = resolveBranch(ctx, root, id, params)
		if err != nil {
			return CreateResult{}, fmt.Errorf("core: create %s: %w", id, err)
		}
	}
	if err := st.UpsertRepo(ctx, root.Path, repoURL); err != nil {
		return CreateResult{}, fmt.Errorf("core: create %s: %w", id, err)
	}

	worktreeDir, err := repo.AddWorktree(ctx, root, id, branch, newBranch)
	if err != nil {
		return CreateResult{}, fmt.Errorf("core: create %s: %w", id, err)
	}

	if err := repo.ExcludeClaudioDir(root); err != nil {
		return CreateResult{}, fmt.Errorf("core: create %s: %w", id, err)
	}
	if params.EnvFile != "" {
		if err := repo.CopyEnvFile(params.EnvFile, worktreeDir); err != nil {
			return CreateResult{}, fmt.Errorf("core: create %s: %w", id, err)
		}
	}

	createdAt := time.Now().Unix()
	if err := st.CreateInstance(ctx, store.NewInstanceParams{
		ID:          id,
		Name:        params.Name,
		RepoURL:     repoURL,
		RepoRoot:    root.Path,
		WorktreeDir: worktreeDir,
		Branch:      branch,
		Image:       params.Image,
		CreatedAt:   createdAt,
	}); err != nil {
		return CreateResult{}, fmt.Errorf("core: create %s: %w", id, err)
	}

	containerID, ports, err := provisionContainer(ctx, st, id, repoURL, root.Path, worktreeDir, createdAt, params, cmd)
	if err != nil {
		// provisionContainer already marks StepFailed; CleanOnFail is the
		// one additional thing left to the caller (ROD-99's
		// --clean-on-fail), since provisionContainer has no params.
		// CleanOnFail field of its own to act on (StartInstance shares it
		// and must never delete a worktree on a *re*-provisioning
		// failure — the instance already existed before that call).
		if params.CleanOnFail {
			if rmErr := repo.RemoveWorktree(ctx, root, id); rmErr != nil {
				return CreateResult{}, fmt.Errorf("%w (also failed to clean up worktree: %v)", err, rmErr)
			}
		}
		return CreateResult{}, err
	}

	mappings := make([]store.PortMapping, 0, len(ports))
	for _, p := range ports {
		mappings = append(mappings, store.PortMapping{
			InstanceID:    id,
			ContainerPort: p.Container,
			HostPort:      p.HostPort,
			ServiceName:   p.ServiceName,
			Source:        p.Source,
			DetectedFrom:  p.DetectedFrom,
		})
	}

	return CreateResult{
		InstanceID:  id,
		Branch:      branch,
		WorktreeDir: worktreeDir,
		ContainerID: containerID,
		Ports:       mappings,
	}, nil
}

// provisionContainer runs the port-detection-through-container-creation
// half of the provisioning state machine for instance id, which already
// has a store row in StepPending (either just created by CreateInstance,
// or an existing STOPPED instance being brought back up by StartInstance
// — see start.go). Shared so `create` and `start` cannot drift on what
// "provisioning a container" actually does.
//
// repoURL is taken as an explicit parameter rather than read from
// params.RepoURL: CreateInstance resolves the *actual* repo URL to label
// the container with itself (params.RepoURL is empty for the --new
// greenfield path, which uses a synthetic "local:<name>" instead), and
// StartInstance never has an original RepoURL in its params at all — it
// only knows the instance ID until it loads the stored row. Passing it
// explicitly is what avoids both callers needing to duplicate that
// resolution or risk drifting on it.
func provisionContainer(ctx context.Context, st CreateStore, id, repoURL, repoRoot, worktreeDir string, createdAt int64, params CreateParams, cmd []string) (containerID string, ports []resolvedPort, err error) {
	if err := st.TransitionProvisionStep(ctx, id, store.StepRepoReady); err != nil {
		return "", nil, failAndReturn(ctx, st, id, err)
	}

	ports, err = allocatePorts(ctx, st, id, worktreeDir, params)
	if err != nil {
		return "", nil, failAndReturn(ctx, st, id, err)
	}
	if err := st.TransitionProvisionStep(ctx, id, store.StepPortsReady); err != nil {
		return "", nil, failAndReturn(ctx, st, id, err)
	}

	// StepConfigReady: phase 1 has no resolved.yml materialization step of
	// its own yet (that's the artifact docs/architecture.md §12.3
	// describes for `claudio status`/debugging) — resolution already
	// happened above, in memory, to build CreateParams. Advancing past
	// this step here keeps the state machine's shape intact for when that
	// artifact is added, without inventing a no-op file today.
	if err := st.TransitionProvisionStep(ctx, id, store.StepConfigReady); err != nil {
		return "", nil, failAndReturn(ctx, st, id, err)
	}

	// sibling of the worktree, not inside it — never checked in, never
	// touched by git. Created here, not by Docker: unlike a named volume,
	// a bind mount's source directory must already exist on the host —
	// verified empirically, Docker returns "bind source path does not
	// exist" rather than creating it — and this is the one place in the
	// create flow that first needs it to.
	homeDir := worktreeDir + ".home"
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return "", nil, failAndReturn(ctx, st, id, fmt.Errorf("create home dir: %w", err))
	}

	containerID, err = engine.CreateAndStart(ctx, params.DockerHost, engine.CreateSpec{
		InstanceID:  id,
		RepoURL:     repoURL,
		CreatedAt:   createdAt,
		Image:       params.Image,
		Cmd:         cmd,
		RepoRoot:    repoRoot,
		WorktreeDir: worktreeDir,
		HomeDir:     homeDir,
		Ports:       toBindings(ports),
		Resources:   params.Resources,
		Env:         params.Env,
	})
	if err != nil {
		return "", nil, failAndReturn(ctx, st, id, err)
	}
	if err := st.SetContainerID(ctx, id, containerID); err != nil {
		return "", nil, failAndReturn(ctx, st, id, err)
	}
	if err := st.TransitionProvisionStep(ctx, id, store.StepContainerUp); err != nil {
		return "", nil, failAndReturn(ctx, st, id, err)
	}

	// StepHealthy: phase 1 has no health probe yet (that needs the
	// container to expose something to probe, e.g. an HTTP endpoint or a
	// tmux-session check) — a started container is treated as healthy
	// immediately. A real probe is future work, not this issue's scope.
	if err := st.TransitionProvisionStep(ctx, id, store.StepHealthy); err != nil {
		return "", nil, failAndReturn(ctx, st, id, err)
	}

	return containerID, ports, nil
}

// resolvedPort is portdetect.Resolved plus the host port AllocatePort
// assigned it — kept private to this file since nothing outside
// CreateInstance needs the intermediate shape.
type resolvedPort struct {
	portdetect.Resolved
	HostPort int
}

func allocatePorts(ctx context.Context, st CreateStore, id, worktreeDir string, params CreateParams) ([]resolvedPort, error) {
	detected, err := portdetect.Detect(worktreeDir)
	if err != nil {
		return nil, fmt.Errorf("detect ports: %w", err)
	}
	repoCfg, err := config.LoadRepoConfig(worktreeDir + "/.claudio.yml")
	if err != nil {
		return nil, fmt.Errorf("load repo config: %w", err)
	}
	resolved, err := portdetect.Merge(detected, repoCfg.Ports, params.ManualPorts)
	if err != nil {
		return nil, fmt.Errorf("merge ports: %w", err)
	}

	out := make([]resolvedPort, 0, len(resolved))
	for _, r := range resolved {
		if !r.Expose {
			continue // container-internal port: no host binding, nothing to allocate
		}
		hostPort, err := st.AllocatePort(ctx, id, r.Container, r.ServiceName, r.Source, r.DetectedFrom, params.PortRangeLow, params.PortRangeHigh)
		if err != nil {
			return nil, fmt.Errorf("allocate port for %s (container %d): %w", r.ServiceName, r.Container, err)
		}
		out = append(out, resolvedPort{Resolved: r, HostPort: hostPort})
	}
	return out, nil
}

func toBindings(ports []resolvedPort) []engine.PortBinding {
	out := make([]engine.PortBinding, 0, len(ports))
	for _, p := range ports {
		out = append(out, engine.PortBinding{ContainerPort: p.Container, HostPort: p.HostPort})
	}
	return out
}

// resolveBranch applies docs/architecture.md §5.1/§4.1's branch rules:
// --branch checks out an existing branch (must already exist), --new-
// branch creates one, and passing neither generates claudio/<id> — the
// default that keeps "one instance = one line of work" true.
func resolveBranch(ctx context.Context, root repo.Root, id string, params CreateParams) (branch string, isNew bool, err error) {
	switch {
	case params.Branch != "":
		if !repo.BranchExists(ctx, root, params.Branch) {
			return "", false, fmt.Errorf("branch %q does not exist in %s", params.Branch, params.RepoURL)
		}
		return params.Branch, false, nil
	case params.NewBranch != "":
		// A branch by this name already existing is not necessarily an
		// error here — it's exactly docs/architecture.md §5.1's collision
		// scenario ("claudio create acme/web --branch feat/auth" example,
		// which applies just as much to --new-branch naming an existing
		// branch by mistake or on purpose to join it). Check it out like
		// --branch would (isNew=false) and let AddWorktree's own
		// worktree-add report the real *repo.BranchCollisionError if it's
		// actually held elsewhere, rather than pre-empting that with a
		// plain "already exists" error that a caller can't distinguish
		// from any other failure and can't retry against.
		return params.NewBranch, !repo.BranchExists(ctx, root, params.NewBranch), nil
	default:
		return "claudio/" + id, true, nil
	}
}

// uniqueID generates an idgen ID and retries on a collision against the
// store — see docs/architecture.md §9: IDs are short and human-typeable,
// never hashes, so collisions are possible (if rare) and must be
// detected against the actual store rather than assumed away.
func uniqueID(ctx context.Context, st CreateStore) (string, error) {
	const maxAttempts = 20
	for i := 0; i < maxAttempts; i++ {
		candidate, err := idgen.New()
		if err != nil {
			return "", fmt.Errorf("core: generate instance id: %w", err)
		}
		if _, err := st.GetInstance(ctx, candidate); err != nil {
			return candidate, nil // not found: this ID is free
		}
	}
	return "", fmt.Errorf("core: could not generate a unique instance id after %d attempts", maxAttempts)
}

func failAndReturn(ctx context.Context, st CreateStore, id string, cause error) error {
	if err := st.MarkFailed(ctx, id); err != nil {
		return fmt.Errorf("core: create %s: %w (also failed to mark failed: %v)", id, cause, err)
	}
	return fmt.Errorf("core: create %s: %w", id, cause)
}
