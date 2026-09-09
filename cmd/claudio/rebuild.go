package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/rodrigomorales/claudio/internal/core"
)

// cmdRebuild implements `claudio rebuild <id> [--fresh]`: rebuild the
// image, then re-provision the instance's container from it.
//
// This exists because a change to image/entrypoint.sh — the pane command
// in ROD-116, say — reaches a running instance through neither of the
// obvious routes. Rebuilding the CLI doesn't touch it (the entrypoint
// lives in the image, not the binary), and `claudio restart` re-provisions
// from whatever image is tagged *now* without rebuilding it, so the fix
// silently isn't there. Discovering that costs a confusing debugging
// session: `claudio ls` reports a healthy instance, the CLI is current,
// and the container is still running last week's entrypoint.
//
// The two halves are deliberately one command rather than a documented
// two-step: `claudio image build` followed by `claudio restart` is the
// same work, but only if you know that's the pairing — and the failure
// mode of not knowing is invisible rather than loud.
//
// home/ is preserved by default (the Claude Code session resumes across
// the container swap, ROD-99); --fresh discards it, matching `restart`
// and `start`.
func cmdRebuild(ctx context.Context, args []string) int {
	rest, fresh, ok := parseIDAndFresh(args, "claudio rebuild")
	if !ok {
		return 1
	}

	env, ok := credentialEnv("claudio rebuild")
	if !ok {
		return 1
	}

	c, err := newClient(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio rebuild:", describeErr(err))
		return 1
	}
	defer c.Close()

	idOrName, ok := resolveIDWithClient(ctx, c, rest, "claudio rebuild")
	if !ok {
		return 1
	}

	inst, err := c.GetInstance(ctx, idOrName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio rebuild:", describeErr(err))
		return 1
	}

	// Read the image the container is on now, before anything replaces
	// it, so the summary can report the actual drift this command closed.
	// Best-effort: a container that's already gone is exactly a case
	// rebuild should still handle, not refuse.
	before := containerImageID(ctx, c.DockerHost(), "claudio-"+inst.ID)

	// The repo-specific layer (if this repo's .claudio.yml asks for one)
	// is built from the instance's own worktree, so a rebuild refreshes
	// the same image the instance actually runs — not just the base.
	fmt.Fprintln(os.Stderr, "Rebuilding image...")
	buildResult, err := c.BuildImage(ctx, core.BuildImageParams{
		RepoPath: inst.WorktreeDir,
		RepoURL:  inst.RepoURL,
		UserUID:  os.Getuid(),
		UserGID:  os.Getgid(),
	}, func(line string) {
		fmt.Fprint(os.Stderr, line)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio rebuild:", describeErr(err))
		return 1
	}

	// Restart rather than a bare Start: the instance is normally running,
	// and Start alone rejects anything that isn't already stopped.
	// Restart re-provisions the container, which is what picks up the
	// image just built.
	result, err := c.Restart(ctx, idOrName, fresh, env, terminalProgress())
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudio rebuild:", describeErr(err))
		return 1
	}

	after := containerImageID(ctx, c.DockerHost(), "claudio-"+result.InstanceID)

	image := buildResult.BaseImage
	if buildResult.RepoImage != "" {
		image = buildResult.RepoImage
	}
	fmt.Printf("Rebuilt %s from %s\n", result.InstanceID, image)
	// Report the drift concretely. Saying "the image didn't change" out
	// loud matters as much as reporting that it did: it's the difference
	// between "my fix isn't in the image" and "my fix is in, look
	// elsewhere" — the exact ambiguity that made this command necessary.
	switch {
	case before == "" || after == "":
	case before != after:
		fmt.Printf("Image %s -> %s\n", shortImageID(before), shortImageID(after))
	default:
		fmt.Printf("Image unchanged (%s) — it was already up to date\n", shortImageID(before))
	}
	if fresh {
		fmt.Println("Session history discarded (--fresh)")
	}
	fmt.Printf("Attach with: claudio attach %s\n", result.InstanceID)
	return 0
}

// containerImageID reports the image a container is currently built
// from, or "" if that can't be determined. Shelling out to `docker
// inspect` keeps this to a read-only lookup for a cosmetic summary line
// rather than widening the Client interface for it; every failure mode
// (no docker, no such container, malformed output) collapses to "" and
// simply omits the line.
func containerImageID(ctx context.Context, dockerHost, containerName string) string {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return ""
	}
	cmd := exec.CommandContext(ctx, dockerPath, "inspect", "--format", "{{.Image}}", containerName)
	if dockerHost != "" {
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+dockerHost)
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// shortImageID trims a sha256:-prefixed digest to the 12 hex characters
// Docker itself displays, so the before/after line reads like `docker
// images` output rather than two 71-character strings.
func shortImageID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
