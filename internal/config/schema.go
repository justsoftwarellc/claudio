// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

// Package config implements Claudio's own configuration schema — not
// devcontainer.json's. See docs/architecture.md §12.4 for why: forwardPorts
// cannot express a service name or expose:false, and hostRequirements
// states the opposite of what Claudio needs (a minimum the host must meet,
// versus a ceiling the instance may not exceed).
//
// Conventions, deliberate and enforced by the decoder: snake_case
// throughout, every list-shaped key is always a list, unknown keys are a
// decode error rather than silently ignored.
package config

// Resources is shared verbatim between the global and repo schemas, which
// is what makes the three-layer resolution in §12.3 legible: the repo
// states a need, the machine states a limit, the same field name means the
// same thing in both places.
type Resources struct {
	Memory *string `yaml:"memory,omitempty"` // e.g. "6g"; parsed at apply time
	CPUs   *int    `yaml:"cpus,omitempty"`
	PIDs   *int    `yaml:"pids,omitempty"`
}

// Merge returns a new Resources with fields from override taking
// precedence over r wherever override sets them. Used to apply the
// global -> repo -> local layering in order.
func (r Resources) Merge(override Resources) Resources {
	out := r
	if override.Memory != nil {
		out.Memory = override.Memory
	}
	if override.CPUs != nil {
		out.CPUs = override.CPUs
	}
	if override.PIDs != nil {
		out.PIDs = override.PIDs
	}
	return out
}

// Port is one entry in the repo config's `ports:` list. Unlike
// devcontainer's forwardPorts (a bare int array), this can name a service
// and mark it container-internal via Expose=false.
type Port struct {
	Name      string `yaml:"name"`
	Container int    `yaml:"container"`
	Expose    *bool  `yaml:"expose,omitempty"` // nil means true (default: forwarded)
}

func (p Port) Exposed() bool {
	return p.Expose == nil || *p.Expose
}

// HostService is one entry in the repo config's `host_services:` list —
// a service *already running on the host* that this instance should be
// able to reach (ROD-128). It is the inbound counterpart to Port, and
// deliberately a separate key rather than a variant of `ports:`:
//
//   - `ports:` publishes container -> host, and its host side is never
//     the user's to choose (§6.2's first-free-in-range allocation is what
//     lets two instances of one repo coexist).
//   - `host_services:` is the opposite direction, and its host side is
//     the *only* thing the user can meaningfully name — the port some
//     other container or process is already listening on. Nothing is
//     allocated, so nothing can collide.
//
// Folding this into `ports:` would have meant a `host:container` form on
// a key whose own CLI flag deliberately rejects exactly that syntax.
//
// Name becomes a hostname inside the container, so the app reaches
// `db:5432` the same way §6.4 already promises for compose sidecars.
type HostService struct {
	Name      string `yaml:"name"`
	Host      int    `yaml:"host"`
	Container *int   `yaml:"container,omitempty"` // nil means "same as Host"
}

// ContainerPort is the port Name resolves to inside the container.
// Defaulting to Host keeps the common case ("it's on 5432, let me reach
// it on 5432") free of ceremony, while still allowing a repo whose code
// hardcodes a different port to remap without editing the code.
func (h HostService) ContainerPort() int {
	if h.Container == nil {
		return h.Host
	}
	return *h.Container
}

// Service is a sidecar declared without a docker-compose.yml — see
// docs/architecture.md §6.4 and ROD-106. Synthesized into the per-instance
// compose project alongside anything the repo's own compose file declares.
type Service struct {
	Name      string            `yaml:"name"`
	Image     string            `yaml:"image"`
	Env       map[string]string `yaml:"env,omitempty"`
	Resources Resources         `yaml:"resources,omitempty"`
}

// Image describes how the agent's container image is built. Node-only for
// phase 1 (ROD-96); apt/npm_global let a repo extend it without Claudio
// maintaining a matrix of prebuilt toolchain images.
type Image struct {
	Base       string   `yaml:"base,omitempty"` // default: node:22-slim
	Apt        []string `yaml:"apt,omitempty"`
	NpmGlobal  []string `yaml:"npm_global,omitempty"`
	Dockerfile string   `yaml:"dockerfile,omitempty"` // escape hatch, built FROM Base
}

// RepoConfig is <repo>/.claudio.yml — what the project needs. Versioned,
// shared, committed. Every field optional.
//
// The two hooks are deliberately split by lifetime, not by ordering
// (ROD-127). A Claudio restart *replaces* the container rather than
// restarting a process inside it, so "what has to exist on disk" and
// "what has to be running" have genuinely different schedules:
//
//   - PostCreate runs once, at create only, and its exit status gates
//     provisioning. For work that persists in the worktree — `npm ci`,
//     migrations, code generation. Re-running it on every container
//     recreation would be wasteful, which is why StartInstance skips it.
//   - PostStart runs on every provision, create and start alike, and is
//     launched detached. For work that dies with the container — a dev
//     server, a worker, a queue consumer. Without it there is no way to
//     bring such a process back after `claudio restart`, which is the
//     hole this pair closes.
type RepoConfig struct {
	Image Image  `yaml:"image,omitempty"`
	Ports []Port `yaml:"ports,omitempty"`
	// HostServices are services already running on the host that this
	// instance may reach (ROD-128). Opt-in per service, never a blanket
	// route to the host — see §7.4 on why the sandbox boundary is not
	// widened by default.
	HostServices []HostService `yaml:"host_services,omitempty"`
	Services     []Service     `yaml:"services,omitempty"`
	PostCreate   []string      `yaml:"post_create,omitempty"`
	PostStart    []string      `yaml:"post_start,omitempty"`
	Resources    Resources     `yaml:"resources,omitempty"`
}

// PortsConfig is the global port-allocation policy (ROD-98).
type PortsConfig struct {
	Range [2]int `yaml:"range,omitempty"` // [low, high]; default [43000, 43999]
	Bind  string `yaml:"bind,omitempty"`  // default 127.0.0.1; never 0.0.0.0 by default
}

// RuntimeConfig lets docker_host be pinned; empty means the default
// endpoint (ROD-95 runtime detection probes it either way).
type RuntimeConfig struct {
	DockerHost string `yaml:"docker_host,omitempty"`
}

// GlobalConfig is ~/.claudio/config.yml — what this machine allows. It is
// the base layer of the resolution in §12.3; RepoConfig and a future local
// override sit on top of it.
type GlobalConfig struct {
	WorkspaceRoot string        `yaml:"workspace_root,omitempty"` // default ~/.claudio
	Resources     Resources     `yaml:"resources,omitempty"`
	Ports         PortsConfig   `yaml:"ports,omitempty"`
	Runtime       RuntimeConfig `yaml:"runtime,omitempty"`
	// Editor is the binary `claudio open` launches against a worktree
	// (ROD-130) — a bare name resolved on PATH, or an absolute path.
	//
	// Global-only, and deliberately not a RepoConfig field: which editor
	// a human uses is a property of the person and the machine, not of
	// the project, so it must not arrive with a clone. That is the same
	// split the package doc draws between "the repo states a need" and
	// "the machine states a limit".
	//
	// A bare binary rather than a command string: the value is handed to
	// exec.LookPath and invoked as `<editor> <worktree>`, so there is no
	// splitting or quoting layer to get wrong on a path with spaces.
	// Editors wanting flags (`code -n`) are the case this trades away.
	//
	// Empty means unset, which is not an error until `claudio open` is
	// actually run — see cmdOpen on why the requirement bites at first
	// use rather than at install or on every command.
	Editor string `yaml:"editor,omitempty"`
}

// Defaults returns the global config's built-in defaults before any file
// is read. These are the numbers measured on the reference machine
// (docs/architecture.md §7.4 / ROD-112): 6 GB / 4 CPUs / 512 PIDs against
// an OrbStack VM cap of 15.7 GB, not the host's full RAM.
func Defaults() GlobalConfig {
	mem := "6g"
	cpus := 4
	pids := 512
	return GlobalConfig{
		WorkspaceRoot: "~/.claudio",
		Resources: Resources{
			Memory: &mem,
			CPUs:   &cpus,
			PIDs:   &pids,
		},
		Ports: PortsConfig{
			Range: [2]int{43000, 43999},
			Bind:  "127.0.0.1",
		},
	}
}
