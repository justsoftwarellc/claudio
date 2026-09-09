package core

import (
	"context"
	"fmt"

	"github.com/rodrigomorales/claudio/internal/compose"
	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/coreerr"
	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/portdetect"
	"github.com/rodrigomorales/claudio/internal/store"
	"gopkg.in/yaml.v3"
)

// composeFilesFor reconstructs the compose.Files an already-provisioned
// instance's project was built from, from nothing but its stored row —
// stop/start/restart/destroy only have the store.Instance, never the
// CreateParams a fresh `create` had, so this recomputes the same paths
// provisionCompose wrote to (<worktreeDir>.compose-override.yml, and
// <worktreeDir>.compose-synthesized.yml when the repo declared services:
// but shipped no compose file of its own) rather than persisting them
// anywhere new. inst.RepoRoot/WorktreeDir's compose file (if the repo
// has one) is re-detected the same way provisioning found it the first
// time — cheap (one os.Stat per candidate name) and avoids storing a
// third path just to shave that lookup.
func composeFilesFor(inst store.Instance) compose.Files {
	overridePath := inst.WorktreeDir + ".compose-override.yml"
	if base := compose.FindComposeFile(inst.WorktreeDir); base != "" {
		return compose.Files{Base: base, Override: overridePath}
	}
	return compose.Files{Base: inst.WorktreeDir + ".compose-synthesized.yml", Override: overridePath}
}

// needsCompose reports whether this instance is a compose project rather
// than a single container (docs/architecture.md §6.4): the repo ships a
// docker-compose.yml/compose.yaml of its own, or its .claudio.yml
// declares services: without one. composeFilePath is "" for the
// synthesized-only case — nothing to layer an override on top of.
func needsCompose(worktreeDir string, repoCfg config.RepoConfig) (composeFilePath string, use bool) {
	if p := compose.FindComposeFile(worktreeDir); p != "" {
		return p, true
	}
	return "", len(repoCfg.Services) > 0
}

// allocateComposePorts reserves a host port for every port the repo's
// own compose file publishes (docs/architecture.md §6.4: "rewrites
// every published port to a daemon-allocated host port" — two instances
// of the same compose file would otherwise both want the same host
// port). Returns []resolvedPort — the same shape allocatePorts returns
// for the single-container path — so provisionContainer's port
// bookkeeping (progress reporting, the final store.PortMapping list)
// doesn't need a second notion of "an allocated port." composeFilePath
// empty means a pure .claudio.yml services: synthesis with no ports to
// allocate at all (config.Service is internal-only by design — see its
// doc), so this returns nil, nil in that case.
func allocateComposePorts(ctx context.Context, st CreateStore, composeFilePath, id string, params CreateParams) ([]resolvedPort, error) {
	if composeFilePath == "" {
		return nil, nil
	}
	declared, err := compose.LoadDeclaredPorts(composeFilePath)
	if err != nil {
		return nil, coreerr.Wrap(coreerr.InvalidInput, "compose: load declared ports", err)
	}
	out := make([]resolvedPort, 0, len(declared))
	for _, d := range declared {
		hostPort, err := st.AllocatePort(ctx, id, d.ContainerPort, d.Service, store.PortDeclared, nil, params.PortRangeLow, params.PortRangeHigh)
		if err != nil {
			op := fmt.Sprintf("allocate port for compose service %s (container %d)", d.Service, d.ContainerPort)
			return nil, wrapAllocatePort(op, err, params.PortRangeLow, params.PortRangeHigh)
		}
		out = append(out, resolvedPort{
			Resolved: portdetect.Resolved{
				ServiceName: d.Service,
				Container:   d.ContainerPort,
				Source:      store.PortDeclared,
				Expose:      true,
			},
			HostPort: hostPort,
		})
	}
	return out, nil
}

// provisionCompose is provisionContainer's compose-project counterpart
// to engine.CreateAndStart: generates the override file this package's
// compose.GenerateOverride produces from ports (already allocated by
// allocateComposePorts) and runs `docker compose up -d` for the whole
// project — sidecars and the agent container together, on one
// per-instance network.
//
// composeFilePath is "" when the repo has no compose file of its own
// (a pure .claudio.yml services: synthesis, docs/architecture.md's
// "A repo may also declare services... without shipping a compose file
// at all") — in that case every declared service comes from repoCfg.Services
// and is written directly into the generated file rather than layered as
// an override, since there is no base file to layer over.
func provisionCompose(ctx context.Context, st CreateStore, composeFilePath string, id, repoURL, repoRoot, worktreeDir string, createdAt int64, params CreateParams, image string, cmd []string, resources engine.ResourceLimits, repoCfg config.RepoConfig, homeDir string, ports []resolvedPort) (containerID string, project string, err error) {
	project = compose.ProjectName(id)
	network := project + "_net"

	var sidecarNames []string
	var rewrites []compose.PortRewrite
	seen := make(map[string]bool)
	for _, p := range ports {
		seen[p.ServiceName] = true
		rewrites = append(rewrites, compose.PortRewrite{Service: p.ServiceName, ContainerPort: p.Container, HostPort: p.HostPort})
	}
	for name := range seen {
		sidecarNames = append(sidecarNames, name)
	}

	// .claudio.yml services: are internal-only by design (reached by
	// service name — config.Service has no ports field, see its doc) so
	// they need no allocation, only a name to attach to the network.
	for _, svc := range repoCfg.Services {
		sidecarNames = append(sidecarNames, svc.Name)
	}

	override, err := compose.GenerateOverride(network, sidecarNames, rewrites, compose.AgentSpec{
		InstanceID:  id,
		RepoURL:     repoURL,
		CreatedAt:   createdAt,
		Image:       image,
		Cmd:         cmd,
		RepoRoot:    repoRoot,
		WorktreeDir: worktreeDir,
		HomeDir:     homeDir,
		Resources:   resources,
		Env:         params.Env,
	})
	if err != nil {
		return "", "", coreerr.Wrap(coreerr.Internal, "compose: generate override", err)
	}

	// Synthesized-only services need a base file too — GenerateOverride's
	// output already declares them (as sidecarNames with no compose file
	// of their own to inherit an image from), but a service needs its
	// own image declared somewhere; synthesize that as this project's
	// "base" so `docker compose -f synthesized -f override` has an image
	// for each. When a real compose file exists, it already supplies
	// every image, so no synthesized base is needed for those.
	var baseFilePath string
	if composeFilePath != "" {
		baseFilePath = composeFilePath
	} else if len(repoCfg.Services) > 0 {
		synthesized, err := generateSynthesizedBase(repoCfg.Services)
		if err != nil {
			return "", "", coreerr.Wrap(coreerr.Internal, "compose: generate synthesized services", err)
		}
		baseFilePath = worktreeDir + ".compose-synthesized.yml"
		if err := compose.WriteOverride(baseFilePath, synthesized); err != nil {
			return "", "", coreerr.Wrap(coreerr.Internal, "compose: write synthesized services", err)
		}
	}

	overridePath := worktreeDir + ".compose-override.yml"
	if err := compose.WriteOverride(overridePath, override); err != nil {
		return "", "", coreerr.Wrap(coreerr.Internal, "compose: write override", err)
	}

	files := compose.Files{Base: baseFilePath, Override: overridePath}
	if out, err := compose.Up(ctx, params.DockerHost, project, files); err != nil {
		return "", "", coreerr.Wrap(coreerr.ProvisionFailed, "compose: up", fmt.Errorf("%w: %s", err, out))
	}

	containerID, err = engine.InspectContainerIDByName(ctx, params.DockerHost, "claudio-"+id)
	if err != nil {
		return "", "", coreerr.Wrap(coreerr.Internal, "compose: find agent container", err)
	}
	return containerID, project, nil
}

// synthesizedFile/synthesizedService are the minimal shape needed to
// declare a .claudio.yml services: entry (docs/architecture.md §6.4) as
// a standalone compose file this package writes and layers the override
// on top of — unlike GenerateOverride's output, this is not itself an
// override (there is no repo compose file to merge over in this case),
// so it needs no !override tags and no networks: block of its own
// (network attachment for these services still comes from the override
// layer, same as any other sidecar).
type synthesizedFile struct {
	Services map[string]synthesizedService `yaml:"services"`
}

type synthesizedService struct {
	Image       string            `yaml:"image"`
	Environment map[string]string `yaml:"environment,omitempty"`
	MemLimit    string            `yaml:"mem_limit,omitempty"`
	CPUs        string            `yaml:"cpus,omitempty"`
}

func generateSynthesizedBase(services []config.Service) ([]byte, error) {
	f := synthesizedFile{Services: make(map[string]synthesizedService, len(services))}
	for _, svc := range services {
		s := synthesizedService{Image: svc.Image, Environment: svc.Env}
		if svc.Resources.Memory != nil {
			mem, err := engine.ParseMemory(*svc.Resources.Memory)
			if err != nil {
				return nil, fmt.Errorf("service %s: %w", svc.Name, err)
			}
			s.MemLimit = fmt.Sprintf("%db", mem)
		}
		if svc.Resources.CPUs != nil {
			s.CPUs = fmt.Sprintf("%d", *svc.Resources.CPUs)
		}
		f.Services[svc.Name] = s
	}
	out, err := yaml.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("marshal synthesized services: %w", err)
	}
	return out, nil
}
