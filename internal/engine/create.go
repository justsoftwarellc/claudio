// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-units"
)

// PortBinding is one container<->host TCP mapping to publish at creation
// time. Docker cannot add a binding to a running container (ROD-98), so
// every port an instance needs must be known and reserved (via
// store.AllocatePort) before CreateAndStart is called.
type PortBinding struct {
	ContainerPort int
	HostPort      int
}

// HostGatewayAlias is the hostname Docker's own `host-gateway` magic
// value resolves to — the address of the host from inside the container.
//
// It is added unconditionally rather than only when a host service is
// declared, and that is deliberate: OrbStack and Docker Desktop already
// resolve this name via their embedded DNS (verified on the development
// machine — it is *not* in the container's /etc/hosts there), while plain
// Linux Docker does not resolve it at all without this mapping. Adding it
// everywhere is what makes the name mean the same thing on every runtime
// instead of working by accident on two of them.
//
// Note this alone grants no *access*: it is a name for an address the
// container's network could already route to. What is opt-in is the
// per-service alias below, which is the part a repo actually declares.
const HostGatewayAlias = "host.docker.internal"

// dockerHostGateway is Docker's reserved value for "resolve this to the
// host's gateway address", substituted by the daemon at container create.
const dockerHostGateway = "host-gateway"

// HostService is one host-side service the container should be able to
// reach by name (ROD-128) — the inbound counterpart to PortBinding.
//
// Unlike PortBinding nothing is allocated or published here: the service
// is already listening on the host, and this only teaches the container's
// resolver a name for it. HostPort is therefore the user's to choose,
// which is exactly the opposite of PortBinding.HostPort.
type HostService struct {
	Name     string
	HostPort int
	// ContainerPort is the port the name is expected to be reached on
	// from inside. Docker's ExtraHosts maps names to *addresses*, not to
	// ports, so a ContainerPort differing from HostPort cannot be honored
	// by a hosts entry alone — CreateAndStart surfaces that rather than
	// silently resolving the name to a port nothing listens on.
	ContainerPort int
}

// ExtraHosts renders the /etc/hosts entries for a set of host services,
// always including HostGatewayAlias itself. Exported so the compose path
// (internal/compose) produces byte-identical entries to the
// single-container path rather than reimplementing the convention.
func ExtraHosts(services []HostService) []string {
	out := make([]string, 0, len(services)+1)
	out = append(out, HostGatewayAlias+":"+dockerHostGateway)
	for _, s := range services {
		// Every alias points at the same address — the host — because that
		// is what a hosts entry can express. The port is the caller's to
		// use when dialing; see HostService.ContainerPort.
		out = append(out, s.Name+":"+dockerHostGateway)
	}
	return out
}

// ValidateHostServices rejects the declarations Docker's ExtraHosts
// cannot actually honor, so the failure names the declaration instead of
// appearing later as a connection refused inside the container.
//
// The remappable case (container port != host port) is the one worth
// spelling out: a hosts entry maps a *name* to an *address*, with no
// port component anywhere, so "reach host 5432 as db:6000" is not
// something this mechanism can express. Saying so is better than
// accepting it and resolving db to a host where nothing serves 6000.
func ValidateHostServices(services []HostService) error {
	seen := make(map[string]bool, len(services))
	for _, s := range services {
		if s.Name == "" {
			return fmt.Errorf("engine: host service with no name")
		}
		if s.Name == HostGatewayAlias {
			return fmt.Errorf("engine: host service %q shadows the built-in host alias — pick another name", s.Name)
		}
		if seen[s.Name] {
			return fmt.Errorf("engine: duplicate host service name %q", s.Name)
		}
		seen[s.Name] = true
		if s.ContainerPort != 0 && s.ContainerPort != s.HostPort {
			return fmt.Errorf("engine: host service %q: container port %d differs from host port %d — a hosts entry maps a name to an address, not to a port, so the container reaches this service on %d or not at all",
				s.Name, s.ContainerPort, s.HostPort, s.HostPort)
		}
	}
	return nil
}

// ResourceLimits mirrors config.Resources, translated to the units Docker
// wants, so this package does not need to import internal/config (engine
// stays a pure Docker-facing layer — docs/architecture.md §12.4). A nil
// field means "no limit" for that dimension, same as config.Resources.
type ResourceLimits struct {
	MemoryBytes int64 `json:"memory_bytes,omitempty"` // 0 means unset
	NanoCPUs    int64 `json:"nano_cpus,omitempty"`    // 0 means unset
	PIDs        int64 `json:"pids,omitempty"`         // 0 means unset
}

// CreateSpec is everything CreateAndStart needs to provision one
// instance's container. Every field is required unless noted; the
// zero-value Env/PortBindings are valid (no extra env, no published
// ports), everything else describes a real, mandatory mount or label.
type CreateSpec struct {
	// InstanceID becomes the claudio.instance.id label and the container
	// name (claudio-<InstanceID>) — see docs/architecture.md §9.1: attach
	// resolves containers by this exact name.
	InstanceID string
	RepoURL    string // claudio.repo label
	CreatedAt  int64  // claudio.created_at label, Unix seconds

	Image string
	// Cmd overrides the image's own entrypoint/cmd. Empty means "use
	// whatever the image declares" — the real claudio/base image already
	// keeps itself alive via tini + tail -f /dev/null (image/entrypoint.sh),
	// so production callers leave this unset; it exists so a test or a
	// non-Claudio image (e.g. a plain "alpine" used to exercise mounts or
	// port bindings in isolation) can supply a long-running command.
	Cmd []string

	// RepoRoot is the host path to the repo root (docs/architecture.md
	// §5.1) — mounted whole at /repo, per Appendix B: mounting only the
	// worktree breaks git's gitdir pointers inside the container.
	RepoRoot string
	// WorktreeDir is the host path to this session's worktree, used only
	// to compute the container-relative WorkingDir under /repo — it is
	// not mounted separately from RepoRoot.
	WorktreeDir string

	// HomeDir is the host path bind-mounted to /home/agent, making
	// transcripts and settings survive a container rebuild
	// (docs/architecture.md §5.1, ROD-99's restart-resumes-the-session
	// design — verified working in ROD-99).
	HomeDir string

	Ports     []PortBinding
	Resources ResourceLimits

	// HostServices are host-side services this container may reach by
	// name (ROD-128). Opt-in per service: an undeclared name does not
	// resolve, which is what keeps §7.4's sandbox posture honest — the
	// route to the host exists, but nothing tells the agent where to
	// find a service it was not given.
	HostServices []HostService

	// PublishAllInterfaces binds published ports to 0.0.0.0 instead of
	// the default 127.0.0.1 — `claudio create --publish-all-interfaces`
	// (docs/architecture.md §6.2). Off by default and deliberately opt-in
	// per instance: a sandboxed agent's dev server on 0.0.0.0 is reachable
	// by anything on the local network, which §7.4 treats as a sandbox
	// hole rather than a convenience.
	PublishAllInterfaces bool

	// Env is passed through verbatim — the caller decides what credential
	// to inject (CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY, per §8.1);
	// this package does not know how credentials are obtained (that's
	// ROD-108's credential broker).
	Env map[string]string
}

const (
	bindIPLoopback = "127.0.0.1" // never 0.0.0.0 by default — docs/architecture.md §6.2/§7.4
	bindIPAll      = "0.0.0.0"   // only via CreateSpec.PublishAllInterfaces
)

// CreateAndStart creates and starts a container for one Claudio instance,
// mounting the repo root and home directory, publishing the given ports
// on 127.0.0.1 (or every interface, if spec.PublishAllInterfaces is set),
// applying resource limits, and stamping the three
// claudio.* labels that make the container recoverable from Docker alone
// if state.db is lost (docs/architecture.md §10.1, ROD-99's adopt).
//
// The container is not started as a child of this process — nothing here
// waits on it or ties its lifetime to the CLI invocation, matching
// docs/architecture.md's "containers are not children of the CLI or
// daemon" requirement (ROD-99).
func CreateAndStart(ctx context.Context, host string, spec CreateSpec) (containerID string, err error) {
	cli, err := newClient(ctx, host)
	if err != nil {
		return "", err
	}
	defer cli.Close()

	containerWorktreeDir, err := ContainerWorkdir(spec.RepoRoot, spec.WorktreeDir)
	if err != nil {
		return "", err
	}

	portBindings, exposedPorts, err := toDockerPorts(spec.Ports, spec.PublishAllInterfaces)
	if err != nil {
		return "", err
	}

	if err := ValidateHostServices(spec.HostServices); err != nil {
		return "", err
	}

	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}

	config := &container.Config{
		Image:        spec.Image,
		Cmd:          spec.Cmd,
		Env:          env,
		WorkingDir:   containerWorktreeDir,
		ExposedPorts: exposedPorts,
		Labels: map[string]string{
			LabelInstanceID: spec.InstanceID,
			LabelRepo:       spec.RepoURL,
			LabelCreatedAt:  fmt.Sprintf("%d", spec.CreatedAt),
		},
	}

	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: spec.RepoRoot, Target: "/repo"},
			{Type: mount.TypeBind, Source: spec.HomeDir, Target: "/home/agent"},
		},
		PortBindings: portBindings,
		ExtraHosts:   ExtraHosts(spec.HostServices),
		Resources: container.Resources{
			Memory:   spec.Resources.MemoryBytes,
			NanoCPUs: spec.Resources.NanoCPUs,
			PidsLimit: func() *int64 {
				if spec.Resources.PIDs == 0 {
					return nil
				}
				return &spec.Resources.PIDs
			}(),
		},
		// Sandbox posture, docs/architecture.md §7.4: no capabilities beyond
		// what the toolchain needs, no privilege escalation via setuid
		// binaries. Read-only rootfs and network egress policy are
		// separately scoped (they need per-toolchain tmpfs allowlisting and
		// a filtering proxy respectively) and are not this issue's concern.
		CapDrop:       []string{"ALL"},
		SecurityOpt:   []string{"no-new-privileges"},
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
	}

	containerName := "claudio-" + spec.InstanceID
	resp, err := cli.ContainerCreate(ctx, config, hostConfig, &network.NetworkingConfig{}, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("engine: create container %s: %w", containerName, err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("engine: start container %s: %w", containerName, err)
	}

	return resp.ID, nil
}

// ContainerWorkdir computes /repo/worktrees/<name> from the host
// worktree path, per docs/architecture.md Appendix B: the container
// mounts the whole repo root at /repo, and the worktree's position under
// it must be preserved verbatim (worktrees/<name>) for the relative
// gitdir pointers rewritten by internal/repo.AddWorktree to resolve.
// Exported so callers running something else inside the container after
// creation (e.g. core's post_create hook, docs/architecture.md §5.1) can
// compute the same working directory CreateAndStart itself used, rather
// than duplicating this computation or guessing.
func ContainerWorkdir(repoRoot, worktreeDir string) (string, error) {
	rel, err := filepath.Rel(repoRoot, worktreeDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("engine: worktree %s is not under repo root %s", worktreeDir, repoRoot)
	}
	return "/repo/" + filepath.ToSlash(rel), nil
}

// ParseMemory converts a config.Resources.Memory-style string ("6g",
// "512m") into bytes for ResourceLimits.MemoryBytes, using the same
// binary-unit parsing Docker's own CLI uses (go-units), so a value that
// works in `docker run -m` means the same thing here.
func ParseMemory(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := units.RAMInBytes(s)
	if err != nil {
		return 0, fmt.Errorf("engine: parse memory %q: %w", s, err)
	}
	return n, nil
}

// NanoCPUs converts a whole-CPU count (config.Resources.CPUs) into the
// units Docker's API wants (10^-9 CPUs).
func NanoCPUs(cpus int) int64 {
	return int64(cpus) * 1_000_000_000
}

func toDockerPorts(bindings []PortBinding, publishAllInterfaces bool) (nat.PortMap, nat.PortSet, error) {
	bindIP := bindIPLoopback
	if publishAllInterfaces {
		bindIP = bindIPAll
	}
	portMap := make(nat.PortMap, len(bindings))
	portSet := make(nat.PortSet, len(bindings))
	for _, b := range bindings {
		port, err := nat.NewPort("tcp", fmt.Sprintf("%d", b.ContainerPort))
		if err != nil {
			return nil, nil, fmt.Errorf("engine: invalid container port %d: %w", b.ContainerPort, err)
		}
		portMap[port] = []nat.PortBinding{{HostIP: bindIP, HostPort: fmt.Sprintf("%d", b.HostPort)}}
		portSet[port] = struct{}{}
	}
	return portMap, portSet, nil
}
