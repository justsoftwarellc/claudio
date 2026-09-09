package compose

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Files bundles the compose files one `docker compose` invocation needs:
// the repo's own compose file (empty for a repo with no compose file at
// all — a pure .claudio.yml services: synthesis has no base to layer
// over) plus this package's generated override, always present.
type Files struct {
	Base     string // "" when the repo has no compose file of its own
	Override string
}

func (f Files) args() []string {
	if f.Base == "" {
		return []string{"-f", f.Override}
	}
	return []string{"-f", f.Base, "-f", f.Override}
}

// Up runs `docker compose -p <project> -f ... up -d`, starting every
// sidecar and the agent container together — docs/architecture.md §6.4:
// a single `docker compose up` for the whole project. dockerHost, when
// non-empty, is passed via DOCKER_HOST so this honors the same runtime
// selection engine's SDK client does (docs/architecture.md's
// runtime.docker_host, ROD-113) rather than always targeting whatever
// `docker compose` defaults to.
func Up(ctx context.Context, dockerHost, project string, files Files) (string, error) {
	args := append([]string{"compose", "-p", project}, files.args()...)
	args = append(args, "up", "-d")
	return run(ctx, dockerHost, args...)
}

// Down runs `docker compose -p <project> down`, stopping and removing
// every container in the project. removeVolumes adds -v — `claudio
// destroy` (unless --keep-workspace, though workspace and sidecar
// volumes are orthogonal: keep-workspace is about the git worktree, not
// a database's data volume) removes sidecar volumes; `claudio stop`
// does not, so `start` has something to resume into, matching the
// single-container path's "STOPPED keeps the workspace" contract
// extended to a sidecar's own data.
func Down(ctx context.Context, dockerHost, project string, files Files, removeVolumes bool) (string, error) {
	args := append([]string{"compose", "-p", project}, files.args()...)
	args = append(args, "down")
	if removeVolumes {
		args = append(args, "-v")
	}
	return run(ctx, dockerHost, args...)
}

// ServiceState is one row of `docker compose ps`'s output for a single
// service — enough for `claudio status` to surface sidecar health
// (docs/architecture.md §6.4: "an agent blocked on a database that
// failed to start should be diagnosable without dropping to docker ps").
type ServiceState struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// Ps returns docker compose ps's per-service state for project, parsed
// from its --format json output (one JSON object per line, Compose's own
// convention — not a JSON array).
func Ps(ctx context.Context, dockerHost, project string) ([]ServiceState, error) {
	out, err := run(ctx, dockerHost, "compose", "-p", project, "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	return parsePsOutput(out)
}

// Logs runs `docker compose logs` for project, optionally scoped to one
// service — `claudio logs <id> [--service X]` (docs/architecture.md
// §6.4). This is the non-daemon, non-streaming form: it returns once
// compose's own `logs` invocation exits, following is left to a caller
// passing --follow through to this same command's stdout rather than
// this function buffering an unbounded stream in memory.
func Logs(ctx context.Context, dockerHost, project string, service string, follow bool, stdout, stderr *os.File) error {
	args := []string{"compose", "-p", project, "logs"}
	if service != "" {
		args = append(args, service)
	}
	if follow {
		args = append(args, "-f")
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = envWithDockerHost(dockerHost)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func run(ctx context.Context, dockerHost string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = envWithDockerHost(dockerHost)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("compose: docker %v: %w: %s", args, err, out.String())
	}
	return out.String(), nil
}

func envWithDockerHost(dockerHost string) []string {
	env := os.Environ()
	if dockerHost != "" {
		env = append(env, "DOCKER_HOST="+dockerHost)
	}
	return env
}
