// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/engine"
)

// resolveHostServices merges the repo's own `host_services:` declarations
// with the per-instance ones from `claudio create --host-service`
// (ROD-128), returning them in a stable order.
//
// The layering matches every other config axis in docs/architecture.md
// §12.3 — the repo states what the project needs, the local flag wins —
// but the *reason* is sharper here than for resources. A host service is
// a hole in §7.4's sandbox boundary, and the person opening it should be
// the person at the keyboard, not a file that arrived with a clone. A
// repo declaring `host_services: [db: 5432]` is a convenience for its own
// author; the flag is what lets someone reviewing an unfamiliar repo
// point that name somewhere else, or (by declining to pass it) notice
// that the repo wanted host access at all.
//
// Local entries override by name rather than being appended, so
// --host-service db:6000 redirects the repo's `db` instead of producing
// two declarations of one hostname that the loader would then reject.
func resolveHostServices(repo []config.HostService, local []config.HostService) []engine.HostService {
	byName := make(map[string]config.HostService, len(repo)+len(local))
	for _, hs := range repo {
		byName[hs.Name] = hs
	}
	for _, hs := range local {
		byName[hs.Name] = hs
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	// Sorted, not declaration-ordered: this feeds ExtraHosts, which feeds
	// a container's /etc/hosts and the generated compose override. A
	// map-ranged order would make both differ run to run for identical
	// input, turning any diff of the override file into noise.
	sort.Strings(names)

	out := make([]engine.HostService, 0, len(names))
	for _, name := range names {
		hs := byName[name]
		out = append(out, engine.HostService{
			Name:          hs.Name,
			HostPort:      hs.Host,
			ContainerPort: hs.ContainerPort(),
		})
	}
	return out
}

// ParseHostServiceFlag parses one `--host-service` value into the same
// shape .claudio.yml produces, so the two layers merge without a second
// notion of what a host service is.
//
// Accepted forms:
//
//	name:host            — reachable inside as name:host
//	name:host:container  — accepted only when container == host
//
// The three-part form exists to give a precise error rather than to
// enable remapping: users reach for `host:container` here by analogy
// with docker -p, and being told why it cannot work is more useful than
// a parse error that looks like a typo. See engine.ValidateHostServices.
func ParseHostServiceFlag(entry string) (config.HostService, error) {
	parts := strings.Split(entry, ":")
	switch len(parts) {
	case 2, 3:
	default:
		return config.HostService{}, fmt.Errorf("invalid --host-service entry %q: expected name:port (e.g. db:5432)", entry)
	}

	name := parts[0]
	if name == "" {
		return config.HostService{}, fmt.Errorf("invalid --host-service entry %q: missing service name before the port", entry)
	}
	if name == engine.HostGatewayAlias {
		return config.HostService{}, fmt.Errorf("invalid --host-service entry %q: %s is Claudio's built-in name for the host — pick another", entry, engine.HostGatewayAlias)
	}

	host, err := parsePortField(parts[1])
	if err != nil {
		return config.HostService{}, fmt.Errorf("invalid --host-service entry %q: %w", entry, err)
	}

	hs := config.HostService{Name: name, Host: host}
	if len(parts) == 3 {
		container, err := parsePortField(parts[2])
		if err != nil {
			return config.HostService{}, fmt.Errorf("invalid --host-service entry %q: %w", entry, err)
		}
		if container != host {
			return config.HostService{}, fmt.Errorf("invalid --host-service entry %q: a host service is reached by name at the port it already listens on, so the container port cannot differ from %d — Claudio adds a hostname, not a port forward (use %s:%d)", entry, host, name, host)
		}
		hs.Container = &container
	}
	return hs, nil
}

// parsePortField parses one port field of a --host-service value,
// applying the same 1-65535 bound config.LoadRepoConfig applies to the
// declared form — the flag and the file must not disagree about what
// counts as a valid port.
func parsePortField(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("%d is not a valid TCP port (1-65535)", n)
	}
	return n, nil
}
