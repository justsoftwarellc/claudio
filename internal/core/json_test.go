// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"encoding/json"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/portdetect"
	"github.com/rodrigomorales/claudio/internal/store"
)

// This file pins JSON serialization for the structs that cross
// client.Client (docs/architecture.md §12.4: phase 2's HTTP layer
// serializes these as request/response bodies verbatim). Each test
// round-trips a populated value and checks specific wire keys rather
// than just "it marshals without error" — the actual key names are the
// contract a phase-2 HTTP client would code against.

func TestCreateParamsJSONRoundTrip(t *testing.T) {
	name := "auth-refactor"
	original := CreateParams{
		RepoURL:     "git@github.com:acme/web.git",
		NewBranch:   "feat/auth",
		Name:        &name,
		ManualPorts: []portdetect.Manual{{ServiceName: "debug", Container: 9229}},
		Env:         map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "test-token"},
		Resources:   engine.ResourceLimits{MemoryBytes: 6 << 30, NanoCPUs: 4_000_000_000, PIDs: 512},
		CleanOnFail: true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Spot-check the actual wire keys — this is what a phase-2 HTTP
	// client would code against, so the key names themselves are the
	// contract, not just "it round-trips."
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}
	for _, key := range []string{"repo_url", "new_branch", "name", "manual_ports", "env", "resources", "clean_on_fail"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("marshaled JSON missing key %q; got keys %v", key, keysOf(raw))
		}
	}

	var decoded CreateParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.RepoURL != original.RepoURL {
		t.Errorf("RepoURL = %q, want %q", decoded.RepoURL, original.RepoURL)
	}
	if decoded.Name == nil || *decoded.Name != name {
		t.Errorf("Name = %v, want %q", decoded.Name, name)
	}
	if len(decoded.ManualPorts) != 1 || decoded.ManualPorts[0].Container != 9229 {
		t.Errorf("ManualPorts = %+v, want one entry with Container 9229", decoded.ManualPorts)
	}
	if decoded.Resources.MemoryBytes != original.Resources.MemoryBytes {
		t.Errorf("Resources.MemoryBytes = %d, want %d", decoded.Resources.MemoryBytes, original.Resources.MemoryBytes)
	}
}

func TestCreateResultJSONRoundTrip(t *testing.T) {
	original := CreateResult{
		InstanceID:  "brave-otter",
		Branch:      "claudio/brave-otter",
		WorktreeDir: "/repo/worktrees/brave-otter",
		ContainerID: "abc123",
		Ports: []store.PortMapping{
			{InstanceID: "brave-otter", ContainerPort: 3000, HostPort: 43001, ServiceName: "web", Source: store.PortDetected},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}
	for _, key := range []string{"instance_id", "branch", "worktree_dir", "container_id", "ports"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("marshaled JSON missing key %q; got keys %v", key, keysOf(raw))
		}
	}

	var decoded CreateResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.InstanceID != original.InstanceID {
		t.Errorf("InstanceID = %q, want %q", decoded.InstanceID, original.InstanceID)
	}
	if len(decoded.Ports) != 1 || decoded.Ports[0].HostPort != 43001 {
		t.Errorf("Ports = %+v, want one mapping with HostPort 43001", decoded.Ports)
	}
}

// TestInstanceViewJSONPromotesEmbeddedFields pins the specific behavior
// called out in InstanceView's doc comment: store.Instance is embedded
// untagged, so encoding/json promotes its fields to this struct's own
// top level in the output rather than nesting them under an "Instance"
// key. This is the one easy-to-get-wrong case in this file — an
// accidental json:"instance" tag on the embed would silently break it.
func TestInstanceViewJSONPromotesEmbeddedFields(t *testing.T) {
	view := InstanceView{
		Instance: store.Instance{
			ID:           "brave-otter",
			RepoURL:      "git@github.com:acme/web.git",
			DesiredState: store.StateRunning,
		},
		ContainerRunning: true,
		ContainerStatus:  "running",
		Ports: []store.PortMapping{
			{ContainerPort: 3000, HostPort: 43001},
		},
	}

	data, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map: %v", err)
	}

	if _, nested := raw["Instance"]; nested {
		t.Error(`marshaled JSON has a nested "Instance" key — the embed should be promoted to top level`)
	}
	for _, key := range []string{"id", "repo_url", "desired_state", "container_running", "container_status", "ports"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("marshaled JSON missing promoted/own key %q; got keys %v", key, keysOf(raw))
		}
	}

	var decoded InstanceView
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.ID != view.ID {
		t.Errorf("decoded.ID (promoted from embedded Instance) = %q, want %q", decoded.ID, view.ID)
	}
	if decoded.ContainerStatus != view.ContainerStatus {
		t.Errorf("ContainerStatus = %q, want %q", decoded.ContainerStatus, view.ContainerStatus)
	}
}

func TestUntrackedContainerJSONRoundTrip(t *testing.T) {
	original := UntrackedContainer{
		ContainerID:   "abc123",
		ContainerName: "claudio-wise-heron",
		RepoURL:       "git@github.com:acme/web.git",
		CreatedAt:     1234567890,
		Running:       true,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded UntrackedContainer
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != original {
		t.Errorf("decoded = %+v, want %+v", decoded, original)
	}
}

func TestRuntimeViewJSONRoundTrip(t *testing.T) {
	original := RuntimeView{
		Profile:         "orbstack",
		OperatingSystem: "OrbStack",
		ServerVersion:   "27.0.1",
		MemTotalBytes:   16 << 30,
		NCPU:            8,
		DockerHost:      "unix:///var/run/docker.sock",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded RuntimeView
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != original {
		t.Errorf("decoded = %+v, want %+v", decoded, original)
	}
}

func TestDestroyParamsJSONRoundTrip(t *testing.T) {
	original := DestroyParams{
		IDOrName:      "brave-otter",
		KeepWorkspace: true,
		DockerHost:    "unix:///var/run/docker.sock",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded DestroyParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != original {
		t.Errorf("decoded = %+v, want %+v", decoded, original)
	}
}

func keysOf(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
