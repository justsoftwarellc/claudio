package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// ContainerState is what Docker reports about one of Claudio's
// containers, keyed by the claudio.instance.id label. Read live and
// merged with store state at query time — see docs/architecture.md
// §10.1.
type ContainerState struct {
	ContainerID string
	Name        string
	Running     bool
	Status      string
	OOMKilled   bool

	// Labels, present only on containers Claudio created.
	InstanceID string
	RepoURL    string
	CreatedAt  string
}

// ListClaudioContainers returns every container carrying the
// claudio.instance.id label (running-only, or including stopped ones
// when includeStopped is set). "Untracked" (a container with a label
// but no matching store row) is not something this package can decide —
// the label only defines "this is Claudio's container"; whether a store
// row exists for it is the caller's (core's) concern, since only core
// has both this list and the store.
func ListClaudioContainers(ctx context.Context, host string, includeStopped bool) ([]ContainerState, error) {
	cli, err := newClient(ctx, host)
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	f := filters.NewArgs(filters.Arg("label", LabelInstanceID))
	containers, err := cli.ContainerList(ctx, container.ListOptions{All: includeStopped, Filters: f})
	if err != nil {
		return nil, fmt.Errorf("engine: list containers: %w", err)
	}

	out := make([]ContainerState, 0, len(containers))
	for _, c := range containers {
		cs := ContainerState{
			ContainerID: c.ID,
			Running:     strings.HasPrefix(strings.ToLower(c.State), "running"),
			Status:      c.Status,
			InstanceID:  c.Labels[LabelInstanceID],
			RepoURL:     c.Labels[LabelRepo],
			CreatedAt:   c.Labels[LabelCreatedAt],
		}
		if len(c.Names) > 0 {
			cs.Name = strings.TrimPrefix(c.Names[0], "/")
		}
		out = append(out, cs)
	}
	return out, nil
}

// PublishedPort is one host<->container TCP/UDP binding Docker reports
// for a running container, read live via inspect — used by `claudio
// adopt` (ROD-99) to re-reserve an untracked container's ports in the
// store without trusting anything but Docker's own view of them.
type PublishedPort struct {
	ContainerPort int
	HostPort      int
	Protocol      string
}

// InspectPublishedPorts returns every host port binding an untracked
// container currently has, so adopt can recreate port_mappings rows that
// match what is actually bound rather than re-running detection (which
// might disagree with what the orphaned container was actually given).
func InspectPublishedPorts(ctx context.Context, host, containerID string) ([]PublishedPort, error) {
	cli, err := newClient(ctx, host)
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	inspect, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("engine: inspect %s: %w", containerID, err)
	}

	var out []PublishedPort
	for portProto, bindings := range inspect.NetworkSettings.Ports {
		for _, b := range bindings {
			hostPort, err := strconv.Atoi(b.HostPort)
			if err != nil {
				continue
			}
			out = append(out, PublishedPort{
				ContainerPort: portProto.Int(),
				HostPort:      hostPort,
				Protocol:      portProto.Proto(),
			})
		}
	}
	return out, nil
}

// RemoveContainer force-removes a container by ID — the mechanism behind
// `claudio forget <container>` (ROD-99) and `claudio destroy` (ROD-100).
// A container that is already gone (removed out of band, e.g. by hand
// with `docker rm`) is treated as success rather than an error: both
// callers only care that no container with this ID exists afterward,
// which is already true. Without this, `destroy` could never be retried
// after a container disappeared between two failed attempts — verified
// empirically, Docker's remove API 404s rather than no-op'ing on an
// unknown ID.
func RemoveContainer(ctx context.Context, host, containerID string) error {
	cli, err := newClient(ctx, host)
	if err != nil {
		return err
	}
	defer cli.Close()

	if err := cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("engine: remove container %s: %w", containerID, err)
	}
	return nil
}

// InspectContainerIDByName resolves a container's ID from its exact
// name (e.g. "claudio-<id>", the same claudio-<instance-id> convention
// every path uses — docs/architecture.md §9.1) — needed by the compose
// path (ROD-106), where `docker compose up` starts the agent container
// but never hands this package its ID the way engine.CreateAndStart's
// own ContainerCreate call does.
func InspectContainerIDByName(ctx context.Context, host, name string) (string, error) {
	cli, err := newClient(ctx, host)
	if err != nil {
		return "", err
	}
	defer cli.Close()

	inspect, err := cli.ContainerInspect(ctx, name)
	if err != nil {
		return "", fmt.Errorf("engine: inspect %s: %w", name, err)
	}
	return inspect.ID, nil
}

// InspectOOMKilled reports whether a container's last exit was an OOM
// kill, so a resource ceiling produces a legible failure rather than a
// bare "stopped" — see ROD-112: an inexplicable stop is a worse failure
// mode than an explained one.
func InspectOOMKilled(ctx context.Context, host, containerID string) (bool, error) {
	cli, err := newClient(ctx, host)
	if err != nil {
		return false, err
	}
	defer cli.Close()

	inspect, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, fmt.Errorf("engine: inspect %s: %w", containerID, err)
	}
	return inspect.State != nil && inspect.State.OOMKilled, nil
}

// InspectMemoryLimit returns a running container's configured memory
// ceiling in bytes (its HostConfig.Memory), or 0 if it was created with
// no limit — used to sum configured limits across running instances
// before provisioning another one (ROD-112: "warn ... when the sum of
// configured limits for running instances would exceed the VM's
// memory").
func InspectMemoryLimit(ctx context.Context, host, containerID string) (int64, error) {
	cli, err := newClient(ctx, host)
	if err != nil {
		return 0, err
	}
	defer cli.Close()

	inspect, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return 0, fmt.Errorf("engine: inspect %s: %w", containerID, err)
	}
	if inspect.HostConfig == nil {
		return 0, nil
	}
	return inspect.HostConfig.Memory, nil
}

func newClient(ctx context.Context, host string) (*client.Client, error) {
	resolvedHost := host
	if resolvedHost == "" {
		resolvedHost = currentContextHost(ctx)
	}
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if resolvedHost != "" {
		opts = append(opts, client.WithHost(resolvedHost))
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("engine: create docker client: %w", err)
	}
	return cli, nil
}
