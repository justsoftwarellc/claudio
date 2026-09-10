// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package compose

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidateServiceNames(t *testing.T) {
	t.Run("ordinary names pass", func(t *testing.T) {
		if err := ValidateServiceNames([]string{"db", "cache", "web"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty passes", func(t *testing.T) {
		if err := ValidateServiceNames(nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("agent is refused", func(t *testing.T) {
		err := ValidateServiceNames([]string{"db", AgentServiceName})
		if err == nil {
			t.Fatal("a service named agent was accepted")
		}
		if !errors.Is(err, ErrAgentServiceNameCollision) {
			t.Errorf("error does not wrap the sentinel: %v", err)
		}
		// The message has to tell the user what to do about it — this is
		// the only signal they get before an instance that would look
		// healthy while being silently broken.
		if !strings.Contains(err.Error(), "rename") {
			t.Errorf("error does not say how to fix it: %v", err)
		}
	})
}

// ROD-131's third and worst symptom: Compose merges a base file's
// list-valued keys into the override rather than replacing them, so a
// base service named `agent` donated its ports: to Claudio's agent
// container — publishing it on 0.0.0.0 with no --publish-all-interfaces.
// The agent block must therefore carry an explicit empty !override list,
// not merely omit the key.
func TestGenerateOverrideAgentCarriesExplicitEmptyPorts(t *testing.T) {
	out, err := GenerateOverride("claudio_x_net", []string{"db"},
		[]PortRewrite{{Service: "db", ContainerPort: 5432, HostPort: 43000}},
		AgentSpec{
			InstanceID: "x", Image: "claudio/base",
			RepoRoot: "/r", WorktreeDir: "/r/worktrees/x", HomeDir: "/r/worktrees/x.home",
		})
	if err != nil {
		t.Fatalf("GenerateOverride: %v", err)
	}

	// Asserted against the raw document, because the point is the literal
	// `!override []` reaching Compose — a typed round-trip would happily
	// report nil for both "absent" and "explicitly empty", which are the
	// two cases this distinguishes.
	var raw map[string]interface{}
	if err := yaml.Unmarshal(out, &raw); err != nil {
		t.Fatalf("parse override: %v", err)
	}
	services := raw["services"].(map[string]interface{})
	agent := services[AgentServiceName].(map[string]interface{})

	ports, present := agent["ports"]
	if !present {
		t.Fatalf("agent has no ports: key — a base file's ports would merge into it:\n%s", out)
	}
	if list, ok := ports.([]interface{}); !ok || len(list) != 0 {
		t.Errorf("agent ports = %v, want an empty list", ports)
	}
	if !strings.Contains(string(out), "!override []") {
		t.Errorf("agent ports list is not tagged !override, so it would append rather than replace:\n%s", out)
	}
}

// The sidecar side must keep behaving as before: a rewrite renders a
// tagged list, and a service without one emits no key at all.
func TestGenerateOverrideSidecarPortsUnchanged(t *testing.T) {
	out, err := GenerateOverride("claudio_x_net", []string{"db", "cache"},
		[]PortRewrite{{Service: "db", ContainerPort: 5432, HostPort: 43000}},
		AgentSpec{
			InstanceID: "x", Image: "claudio/base",
			RepoRoot: "/r", WorktreeDir: "/r/worktrees/x", HomeDir: "/r/worktrees/x.home",
		})
	if err != nil {
		t.Fatalf("GenerateOverride: %v", err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(out, &raw); err != nil {
		t.Fatalf("parse override: %v", err)
	}
	services := raw["services"].(map[string]interface{})

	db := services["db"].(map[string]interface{})
	list, ok := db["ports"].([]interface{})
	if !ok || len(list) != 1 || list[0] != "43000:5432" {
		t.Errorf("db ports = %v, want [43000:5432]", db["ports"])
	}

	cache := services["cache"].(map[string]interface{})
	if _, present := cache["ports"]; present {
		t.Errorf("cache emitted a ports key with no rewrite: %v", cache["ports"])
	}
}
