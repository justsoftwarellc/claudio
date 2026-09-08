package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
)

// RunInContainer runs one shell command string inside containerID via
// `docker exec sh -c <shellCmd>`, blocking until it exits, and returns
// its combined stdout+stderr and exit code. Used for
// docs/architecture.md §5.1's `post_create` hook: "run once, inside the
// container, after mounting" — each .claudio.yml post_create entry is
// already a whole shell command line (e.g. "npm ci"), so this takes one
// string per call rather than an argv-style slice; the caller loops over
// the list.
//
// workingDir sets the exec's cwd (post_create commands run relative to
// the worktree, not the container's default WORKDIR, which ROD-114 left
// unset — see engine.CreateSpec's doc).
func RunInContainer(ctx context.Context, host, containerID, workingDir, shellCmd string) (output string, exitCode int, err error) {
	cli, err := newClient(ctx, host)
	if err != nil {
		return "", 0, err
	}
	defer cli.Close()

	created, err := cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		Cmd:          []string{"sh", "-c", shellCmd},
		WorkingDir:   workingDir,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", 0, fmt.Errorf("engine: exec create in %s: %w", containerID, err)
	}

	attached, err := cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("engine: exec attach in %s: %w", containerID, err)
	}
	defer attached.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, attached.Reader); err != nil {
		return "", 0, fmt.Errorf("engine: exec read output in %s: %w", containerID, err)
	}

	inspect, err := cli.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return "", 0, fmt.Errorf("engine: exec inspect in %s: %w", containerID, err)
	}

	return buf.String(), inspect.ExitCode, nil
}
