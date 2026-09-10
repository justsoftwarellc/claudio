// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package compose

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// file is the minimal shape this package reads from a repo's own compose
// file — just enough to enumerate declared ports for rewriting
// (docs/architecture.md §6.4: "rewrites every published port"). Deliberately
// not a full compose-spec model (compose-go was considered and rejected —
// see this package's own doc — in favor of shelling out to `docker
// compose` for everything orchestration-related); this package only ever
// needs to read the ports: list per service, never interpret build
// contexts, volumes, depends_on, or any other key.
type file struct {
	Services map[string]service `yaml:"services"`
}

type service struct {
	// Ports entries can be a bare container port ("5432"), "host:container"
	// or "host:container/proto" per Compose's short syntax — the long
	// (mapping) syntax exists too but is rare enough in sidecar compose
	// files that phase 1 only supports the short form; an unsupported
	// entry is skipped rather than erroring, matching this package's
	// stance that port rewriting is best-effort convenience, not a
	// strict compose-spec implementation.
	Ports []string `yaml:"ports"`
}

// DeclaredPort is one port a repo's compose file publishes, before
// rewriting — the service that declares it, and the container-side port
// number a host port must be allocated for.
type DeclaredPort struct {
	Service       string
	ContainerPort int
}

// LoadServiceNames returns every service the compose file declares, in
// sorted order — the set of services that must join the per-instance
// network, which is deliberately *not* the same set as
// LoadDeclaredPorts' (ROD-129).
//
// The distinction is the bug this exists to fix. LoadDeclaredPorts
// answers "what needs a host port allocated," so a service with no
// `ports:` key is correctly absent from it. Using that as the list of
// services to attach to the network meant an internal-only sidecar —
// the ordinary shape for a Postgres or Redis nothing publishes — never
// got a `networks:` entry, stayed on Compose's implicit `_default`
// network, and so could not be reached by name from the agent at all,
// contradicting §6.4's central promise.
//
// Existence and publishing are separate questions; this answers the
// first one.
func LoadServiceNames(composeFilePath string) ([]string, error) {
	data, err := os.ReadFile(composeFilePath)
	if err != nil {
		return nil, fmt.Errorf("compose: read %s: %w", composeFilePath, err)
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("compose: parse %s: %w", composeFilePath, err)
	}
	return sortedKeys(f.Services), nil
}

// LoadDeclaredPorts parses composeFilePath (as returned by
// FindComposeFile) and returns every port each service publishes, in
// service-then-declaration order — the allocator's input for "rewrite
// every published port to a daemon-allocated host port"
// (docs/architecture.md §6.4). Returns nil, nil for a compose file with
// no services or no ports: not every repo's compose file publishes
// anything host-reachable (an internal-only sidecar the agent reaches by
// service name needs no host port at all).
func LoadDeclaredPorts(composeFilePath string) ([]DeclaredPort, error) {
	data, err := os.ReadFile(composeFilePath)
	if err != nil {
		return nil, fmt.Errorf("compose: read %s: %w", composeFilePath, err)
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("compose: parse %s: %w", composeFilePath, err)
	}

	var out []DeclaredPort
	for _, name := range sortedKeys(f.Services) {
		for _, raw := range f.Services[name].Ports {
			containerPort, ok := parseContainerPort(raw)
			if !ok {
				continue
			}
			out = append(out, DeclaredPort{Service: name, ContainerPort: containerPort})
		}
	}
	return out, nil
}

// parseContainerPort extracts the container-side port from one of
// Compose's short-syntax port entries: a bare port ("5432"), a
// host:container pair ("5433:5432"), or either form with a trailing
// "/tcp"/"/udp". The host side (if present) is discarded outright — this
// package always reallocates it (docs/architecture.md §6.4's whole
// reason the allocator must rewrite rather than pass through).
func parseContainerPort(raw string) (int, bool) {
	raw, _, _ = strings.Cut(raw, "/") // drop /tcp, /udp
	parts := strings.Split(raw, ":")
	last := parts[len(parts)-1]
	n, err := strconv.Atoi(strings.TrimSpace(last))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func sortedKeys(m map[string]service) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
