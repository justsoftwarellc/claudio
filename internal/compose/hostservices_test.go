// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package compose

import (
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
	"gopkg.in/yaml.v3"
)

func agentSpecForTest(hostServices []engine.HostService) AgentSpec {
	return AgentSpec{
		InstanceID:   "brave-otter",
		RepoURL:      "local:example",
		Image:        "claudio/base",
		RepoRoot:     "/workspace/repo",
		WorktreeDir:  "/workspace/repo/worktrees/brave-otter",
		HomeDir:      "/workspace/repo/worktrees/brave-otter.home",
		HostServices: hostServices,
	}
}

// decodeAgentExtraHosts pulls the agent service's extra_hosts out of a
// generated override, so assertions read against the document Compose
// would actually consume rather than against a substring of YAML.
func decodeAgentExtraHosts(t *testing.T, out []byte) []string {
	t.Helper()
	var doc struct {
		Services map[string]struct {
			ExtraHosts []string `yaml:"extra_hosts"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parse generated override: %v\n%s", err, out)
	}
	agent, ok := doc.Services[AgentServiceName]
	if !ok {
		t.Fatalf("generated override has no %q service:\n%s", AgentServiceName, out)
	}
	return agent.ExtraHosts
}

// The compose path and the single-container path must produce identical
// entries — an instance's networking should not depend on whether its
// repo happens to ship a compose file.
func TestGenerateOverrideHostServicesMatchEnginePath(t *testing.T) {
	services := []engine.HostService{
		{Name: "db", HostPort: 5432, ContainerPort: 5432},
		{Name: "cache", HostPort: 6379, ContainerPort: 6379},
	}

	out, err := GenerateOverride("claudio_brave-otter", nil, nil, agentSpecForTest(services))
	if err != nil {
		t.Fatalf("GenerateOverride: %v", err)
	}

	got := decodeAgentExtraHosts(t, out)
	want := engine.ExtraHosts(services)
	if len(got) != len(want) {
		t.Fatalf("extra_hosts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("extra_hosts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// Even with nothing declared the gateway alias is present, so the name
// resolves the same way on every runtime.
func TestGenerateOverrideAlwaysCarriesGatewayAlias(t *testing.T) {
	out, err := GenerateOverride("claudio_brave-otter", nil, nil, agentSpecForTest(nil))
	if err != nil {
		t.Fatalf("GenerateOverride: %v", err)
	}
	got := decodeAgentExtraHosts(t, out)
	if len(got) != 1 || got[0] != engine.HostGatewayAlias+":host-gateway" {
		t.Errorf("extra_hosts = %v, want just the gateway alias", got)
	}
}

// Sidecars get no host route: they are the services the repo already
// declares, and widening the boundary for them was never asked for.
func TestGenerateOverrideDoesNotGiveSidecarsHostAccess(t *testing.T) {
	out, err := GenerateOverride(
		"claudio_brave-otter",
		[]string{"db"},
		nil,
		agentSpecForTest([]engine.HostService{{Name: "api", HostPort: 8080, ContainerPort: 8080}}),
	)
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
	if hosts := doc.Services["db"].ExtraHosts; len(hosts) != 0 {
		t.Errorf("sidecar db got extra_hosts %v, want none", hosts)
	}
}

// An invalid declaration must fail here rather than producing an
// override Compose accepts and a container that resolves a name to a
// host serving nothing.
func TestGenerateOverrideRejectsInvalidHostService(t *testing.T) {
	_, err := GenerateOverride("claudio_brave-otter", nil, nil,
		agentSpecForTest([]engine.HostService{{Name: "db", HostPort: 5432, ContainerPort: 6000}}))
	if err == nil {
		t.Fatal("GenerateOverride accepted a remapped host service, want an error")
	}
	if !strings.Contains(err.Error(), "not to a port") {
		t.Errorf("error = %v, want it to explain the port cannot differ", err)
	}
}
