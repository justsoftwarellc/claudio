// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package compose

import (
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
	"gopkg.in/yaml.v3"
)

// decodeServiceNetworks pulls each service's networks: list out of a
// generated override, so assertions read against the document Compose
// would actually consume rather than a substring of YAML.
func decodeServiceNetworks(t *testing.T, out []byte) map[string][]string {
	t.Helper()
	var doc struct {
		Services map[string]struct {
			Networks []string `yaml:"networks"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parse generated override: %v\n%s", err, out)
	}
	got := make(map[string][]string, len(doc.Services))
	for name, svc := range doc.Services {
		got[name] = svc.Networks
	}
	return got
}

func specForNetworkTest() AgentSpec {
	return AgentSpec{
		InstanceID:  "brave-otter",
		RepoURL:     "local:example",
		Image:       "claudio/base",
		RepoRoot:    "/workspace/repo",
		WorktreeDir: "/workspace/repo/worktrees/brave-otter",
		HomeDir:     "/workspace/repo/worktrees/brave-otter.home",
	}
}

// ROD-129: a sidecar that publishes nothing still has to join the
// per-instance network, or it lands on Compose's implicit _default and
// the agent cannot resolve it by name. The old code derived this list
// from allocated ports, so such a service was omitted entirely — the
// override looked well-formed, it simply had no block for the service.
func TestGenerateOverrideAttachesPortlessSidecarToNetwork(t *testing.T) {
	const network = "claudio_brave-otter_net"

	// "cache" publishes nothing, so it contributes no PortRewrite —
	// exactly the shape that used to disappear.
	out, err := GenerateOverride(
		network,
		[]string{"cache", "db"},
		[]PortRewrite{{Service: "db", ContainerPort: 5432, HostPort: 43001}},
		specForNetworkTest(),
	)
	if err != nil {
		t.Fatalf("GenerateOverride: %v", err)
	}

	networks := decodeServiceNetworks(t, out)

	cache, ok := networks["cache"]
	if !ok {
		t.Fatalf("override has no block for the portless service 'cache' — it would stay on _default:\n%s", out)
	}
	if len(cache) != 1 || cache[0] != network {
		t.Errorf("cache networks = %v, want [%s]", cache, network)
	}

	// The publishing service must be unaffected by the fix.
	if db := networks["db"]; len(db) != 1 || db[0] != network {
		t.Errorf("db networks = %v, want [%s]", db, network)
	}
	if agent := networks[AgentServiceName]; len(agent) != 1 || agent[0] != network {
		t.Errorf("agent networks = %v, want [%s]", agent, network)
	}
}

// The agent and a portless sidecar must land on the same network — that
// shared network is the entire mechanism by which name resolution works.
func TestGenerateOverridePutsAgentAndPortlessSidecarTogether(t *testing.T) {
	const network = "claudio_brave-otter_net"
	out, err := GenerateOverride(network, []string{"cache"}, nil, specForNetworkTest())
	if err != nil {
		t.Fatalf("GenerateOverride: %v", err)
	}

	networks := decodeServiceNetworks(t, out)
	agent, cache := networks[AgentServiceName], networks["cache"]
	if len(agent) == 0 || len(cache) == 0 {
		t.Fatalf("agent=%v cache=%v, want both on a network:\n%s", agent, cache, out)
	}
	if agent[0] != cache[0] {
		t.Errorf("agent is on %q but cache is on %q — the agent cannot resolve it", agent[0], cache[0])
	}
}

// A portless sidecar gets a network but no host route: ROD-128's
// host_services are the agent's alone.
func TestGenerateOverridePortlessSidecarGetsNoHostRoute(t *testing.T) {
	spec := specForNetworkTest()
	spec.HostServices = []engine.HostService{{Name: "api", HostPort: 8080, ContainerPort: 8080}}

	out, err := GenerateOverride("claudio_brave-otter_net", []string{"cache"}, nil, spec)
	if err != nil {
		t.Fatalf("GenerateOverride: %v", err)
	}

	var doc struct {
		Services map[string]struct {
			ExtraHosts []string `yaml:"extra_hosts"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parse override: %v", err)
	}
	if hosts := doc.Services["cache"].ExtraHosts; len(hosts) != 0 {
		t.Errorf("portless sidecar got extra_hosts %v, want none", hosts)
	}
}
