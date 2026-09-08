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
