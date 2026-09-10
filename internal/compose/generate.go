// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package compose

import (
	"fmt"
	"os"

	"github.com/rodrigomorales/claudio/internal/engine"
	"gopkg.in/yaml.v3"
)

// ProjectName is the `docker compose -p <name>` project name for
// instance id — docs/architecture.md §6.4: "docker compose -p claudio_<id>
// namespaces everything, so N instances of the same repo coexist."
// Exported so callers (core, and the CLI's `claudio logs`) derive the
// exact same name from just the instance ID without re-deriving the
// "claudio_" prefix convention independently.
func ProjectName(instanceID string) string {
	return "claudio_" + instanceID
}

// AgentServiceName is the name the agent container is given inside the
// generated compose project — what a sidecar's own network alias would
// use to reach it, and what `docker compose exec`/`logs` addresses it as.
const AgentServiceName = "agent"

// PortRewrite is one DeclaredPort paired with the host port
// store.AllocatePort assigned it — the allocator's output, and this
// package's input for building the override's ports: rewrite.
type PortRewrite struct {
	Service       string
	ContainerPort int
	HostPort      int
}

// AgentSpec is everything the generated override's agent service needs —
// deliberately the same shape as engine.CreateSpec (this package's
// single-container equivalent), so provisionContainer's existing
// resolution (image, resources, env, mounts) feeds both paths without
// having two independent notions of "what an agent container looks
// like."
type AgentSpec struct {
	InstanceID string
	RepoURL    string
	CreatedAt  int64

	Image string
	// Cmd overrides the image's own entrypoint/cmd — empty means "use
	// whatever the image declares," the same contract as
	// engine.CreateSpec.Cmd, and for the same reason: production's real
	// claudio/base image keeps itself alive on its own (image/entrypoint.sh),
	// so this only exists for a test or non-Claudio image that needs an
	// explicit long-running command.
	Cmd []string

	RepoRoot    string
	WorktreeDir string
	HomeDir     string

	Resources engine.ResourceLimits
	Env       map[string]string
}

// composeFile is the override document this package writes — the
// minimal shape `docker compose -f <repo's compose file> -f
// <this file>` needs. Fields absent from the Go zero value are omitted
// via omitempty, so a project with no sidecar ports produces a
// minimal override rather than an empty-but-present ports: [] on every
// service.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Networks map[string]composeNetwork `yaml:"networks,omitempty"`
}

type composeNetwork struct {
	Name string `yaml:"name,omitempty"`
}

type composeService struct {
	Image         string   `yaml:"image,omitempty"`
	ContainerName string   `yaml:"container_name,omitempty"`
	Networks      []string `yaml:"networks,omitempty"`
	// Ports uses overrideStringList, not []string: Compose's default
	// merge behavior for a list-valued key is to *append* across -f
	// files, not replace — verified empirically, layering a rewritten
	// "43091:5432" over the repo's own "5432:5432" published both
	// simultaneously rather than replacing it, which defeats the entire
	// point of rewriting (docs/architecture.md §6.4: "two instances of
	// the same compose file would both want host 5432"). Compose's
	// `!override` YAML tag on a sequence node replaces the base's list
	// outright instead of appending to it — this is that tag.
	Ports       overrideStringList `yaml:"ports,omitempty"`
	Environment map[string]string  `yaml:"environment,omitempty"`
	Volumes     []string           `yaml:"volumes,omitempty"`
	WorkingDir  string             `yaml:"working_dir,omitempty"`
	Command     []string           `yaml:"command,omitempty"`
	Labels      map[string]string  `yaml:"labels,omitempty"`
	CapDrop     []string           `yaml:"cap_drop,omitempty"`
	SecurityOpt []string           `yaml:"security_opt,omitempty"`
	MemLimit    string             `yaml:"mem_limit,omitempty"`
	CPUs        string             `yaml:"cpus,omitempty"`
	PidsLimit   int64              `yaml:"pids_limit,omitempty"`
	Restart     string             `yaml:"restart,omitempty"`
}

// overrideStringList marshals as a YAML sequence tagged !override, so
// Compose replaces the base file's same-keyed list instead of appending
// to it (see composeService.Ports's doc for why that distinction
// matters here). A nil/empty list marshals as an empty sequence, which
// yaml.v3's omitempty still elides since len(o) == 0 satisfies IsZero
// for a slice-kind field regardless of the custom marshaler.
type overrideStringList []string

func (o overrideStringList) MarshalYAML() (interface{}, error) {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!override"}
	for _, s := range o {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: s})
	}
	return node, nil
}

// GenerateOverride builds the compose override docs/architecture.md
// §6.4 describes: every sidecar in the repo's own compose file joins
// networkName with its published ports rewritten to rewrites' host
// ports, and the agent container is added as an extra service
// (AgentServiceName) on the same network, configured the same way
// engine.CreateAndStart would configure a single-container instance
// (mounts, resources, sandbox posture, labels).
//
// networkName is the single per-instance network every service and the
// agent join — docs/architecture.md's `claudio_<id>`, but passed in
// rather than derived here so this function stays agnostic of the
// naming convention itself (ProjectName already owns that).
//
// sidecarServiceNames must list every service name the repo's own
// compose file (or the synthesized one, for the no-compose-file path)
// declares — this function only ever adds network/port overrides for
// services it's told about, since generating a compose override that
// silently invents or drops a service would fight the repo's own
// compose file rather than layer on top of it.
func GenerateOverride(networkName string, sidecarServiceNames []string, rewrites []PortRewrite, agent AgentSpec) ([]byte, error) {
	portsByService := make(map[string][]string)
	for _, r := range rewrites {
		portsByService[r.Service] = append(portsByService[r.Service],
			fmt.Sprintf("%d:%d", r.HostPort, r.ContainerPort))
	}

	services := make(map[string]composeService, len(sidecarServiceNames)+1)
	for _, name := range sidecarServiceNames {
		services[name] = composeService{
			Networks: []string{networkName},
			Ports:    portsByService[name],
		}
	}

	containerWorkdir, err := engine.ContainerWorkdir(agent.RepoRoot, agent.WorktreeDir)
	if err != nil {
		return nil, err
	}
	services[AgentServiceName] = composeService{
		Image:         agent.Image,
		ContainerName: "claudio-" + agent.InstanceID,
		Networks:      []string{networkName},
		Environment:   agent.Env,
		Command:       agent.Cmd,
		WorkingDir:    containerWorkdir,
		Volumes: []string{
			agent.RepoRoot + ":/repo",
			agent.HomeDir + ":/home/agent",
		},
		Labels: map[string]string{
			engine.LabelInstanceID: agent.InstanceID,
			engine.LabelRepo:       agent.RepoURL,
			engine.LabelCreatedAt:  fmt.Sprintf("%d", agent.CreatedAt),
		},
		// Sandbox posture matches engine.CreateAndStart exactly
		// (docs/architecture.md §7.4) — the compose path must not be a
		// weaker sandbox than the single-container path just because it
		// takes a different route to the same container.
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges"},
		Restart:     "no",
		MemLimit:    dockerMemLimit(agent.Resources.MemoryBytes),
		CPUs:        dockerCPUs(agent.Resources.NanoCPUs),
		PidsLimit:   agent.Resources.PIDs,
	}

	doc := composeFile{
		Services: services,
		Networks: map[string]composeNetwork{
			networkName: {Name: networkName},
		},
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("compose: marshal override: %w", err)
	}
	return out, nil
}

// WriteOverride writes the override document to path (sibling to the
// worktree, following the same "generated artifact, never checked in"
// convention provisionContainer's homeDir uses for /home/agent's bind
// source — see create.go's doc on that).
func WriteOverride(path string, contents []byte) error {
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return fmt.Errorf("compose: write override %s: %w", path, err)
	}
	return nil
}

// dockerMemLimit renders bytes into the string form Compose's mem_limit
// key wants ("6442450944b" — Compose accepts a plain byte count with a
// "b" suffix, avoiding a second unit-parsing round-trip through
// something like "6g" that would have to agree with engine.ParseMemory's
// own rounding). 0 (unset) renders as "", which Compose treats as no
// limit — matching engine.ResourceLimits' own "0 means unset" contract.
func dockerMemLimit(bytes int64) string {
	if bytes == 0 {
		return ""
	}
	return fmt.Sprintf("%db", bytes)
}

// dockerCPUs renders nanoCPUs (engine.ResourceLimits' unit) into the
// decimal CPU count Compose's cpus key wants ("4", "0.5"). 0 renders as
// "", meaning no limit.
func dockerCPUs(nanoCPUs int64) string {
	if nanoCPUs == 0 {
		return ""
	}
	whole := nanoCPUs / 1_000_000_000
	frac := nanoCPUs % 1_000_000_000
	if frac == 0 {
		return fmt.Sprintf("%d", whole)
	}
	return fmt.Sprintf("%g", float64(nanoCPUs)/1_000_000_000)
}
