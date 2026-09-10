// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

// Package core holds Claudio's actual operations. It is deliberately
// I/O-free: no printing, no reading stdin, no os.Exit — every function
// takes a context and typed request struct, returns a typed response
// and error. This is what lets the phase-2 daemon (docs/architecture.md
// §12.4) wrap the same functions behind an HTTP handler without a
// rewrite: cmd/claudio owns all terminal I/O, core owns behavior.
package core

import "github.com/rodrigomorales/claudio/internal/store"

// InstanceView merges a store.Instance (intent) with what Docker reports
// right now (reality) — see docs/architecture.md §10.1. Nothing
// observable is cached in the store; this struct is assembled fresh on
// every ListInstances call.
//
// json:",inline" on the embedded store.Instance is a gopkg.in/yaml.v3
// tag, not encoding/json's — encoding/json has no inline directive and
// instead promotes an embedded struct's fields automatically when it has
// no json tag of its own. store.Instance's own fields already carry
// their tags, so leaving the embed untagged here is what makes them
// appear at this struct's top level in the JSON output rather than
// nested under an "Instance" key.
type InstanceView struct {
	store.Instance

	// ContainerRunning and ContainerStatus are read live from Docker, not
	// from the store's desired_state. Empty/false if there is no matching
	// container (e.g. still PENDING, or the container was removed).
	ContainerRunning bool   `json:"container_running"`
	ContainerStatus  string `json:"container_status,omitempty"` // Docker's own status string: "running", "exited", ...
	OOMKilled        bool   `json:"oom_killed"`                 // surfaced distinctly — see ROD-112: a bare "stopped" is a worse failure mode than an explained one.

	Ports []store.PortMapping `json:"ports"`

	// HostServices are the inbound declarations (ROD-128) — host-side
	// services this instance can reach by name. Read from the worktree's
	// .claudio.yml rather than the store, because unlike Ports they are
	// not allocations: nothing is reserved, so there is no reservation to
	// record or release, and the file is the single source of truth that
	// a restart re-derives them from.
	//
	// Reported separately from Ports rather than merged into it with a
	// direction flag: they are reached by *name*, have no allocated host
	// port, and share none of Ports' lifecycle. Folding both into one
	// list would give every consumer a struct where half the fields are
	// meaningless depending on a discriminator.
	HostServices []HostServiceView `json:"host_services,omitempty"`

	// Sidecars is non-empty only for a compose-project instance
	// (Instance.IsCompose()) — docs/architecture.md §6.4: "Sidecar
	// health is surfaced in claudio status ... diagnosable without
	// dropping to docker ps." Empty for an ordinary single-container
	// instance, and best-effort for a compose instance: a failure to
	// query compose's own state must not fail the whole status view (see
	// GetInstanceView's doc for the same stance on the agent container's
	// own live state).
	Sidecars []SidecarState `json:"sidecars,omitempty"`
}

// SidecarState is one compose-project service's live state, for
// InstanceView.Sidecars — the agent service itself is excluded (its
// state is already ContainerRunning/ContainerStatus/OOMKilled above, so
// a caller doesn't see the agent container described twice under two
// different shapes).
type SidecarState struct {
	Service string `json:"service"`
	State   string `json:"state"`
	Health  string `json:"health,omitempty"`
}

// UntrackedContainer is a container carrying Claudio's labels with no
// matching row in the store — DB lost, deleted out of band, restored
// from backup. Flagged for the user to `adopt` or `forget`; never
// auto-resolved. See docs/architecture.md §10.1 and ROD-99.
type UntrackedContainer struct {
	ContainerID   string `json:"container_id"`
	ContainerName string `json:"container_name"`
	RepoURL       string `json:"repo_url,omitempty"`   // from the claudio.repo label, if present
	CreatedAt     int64  `json:"created_at,omitempty"` // from the claudio.created_at label, if present
	Running       bool   `json:"running"`
}

type RuntimeView struct {
	Profile         string `json:"profile"`
	OperatingSystem string `json:"operating_system"`
	ServerVersion   string `json:"server_version"`
	MemTotalBytes   int64  `json:"mem_total_bytes"`
	NCPU            int    `json:"ncpu"`
	DockerHost      string `json:"docker_host,omitempty"`
}

// HostServiceView is one resolved host service, for InstanceView.
type HostServiceView struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}
