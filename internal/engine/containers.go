package engine

import (
	"context"
	"fmt"
	"strings"

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
