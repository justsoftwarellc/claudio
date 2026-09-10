// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/engine"
	"github.com/rodrigomorales/claudio/internal/store"
)

func TestAdoptContainerReconstructsRowAndReReservesPorts(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)

	name := "claudio-test-adopt-" + strings.ReplaceAll(t.Name(), "/", "-")
	out, err := exec.Command("docker", "run", "-d", "--rm",
		"-p", "127.0.0.1::80",
		"--label", engine.LabelInstanceID+"=adopted-1",
		"--label", engine.LabelRepo+"=git@github.com:acme/adopted.git",
		"--label", engine.LabelCreatedAt+"=999",
		"--name", name,
		"alpine", "sleep", "60").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	containerID := trimNewline(string(out))
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", containerID).Run() })

	instanceID, err := AdoptContainer(context.Background(), s, "", containerID, 999)
	if err != nil {
		t.Fatalf("AdoptContainer: %v", err)
	}
	if instanceID != "adopted-1" {
		t.Fatalf("instanceID = %q, want adopted-1 (from the container's own label)", instanceID)
	}

	inst, err := s.GetInstance(context.Background(), "adopted-1")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.RepoURL != "git@github.com:acme/adopted.git" {
		t.Errorf("RepoURL = %q, want the label's repo URL", inst.RepoURL)
	}

	mappings, err := s.PortMappings(context.Background(), "adopted-1")
	if err != nil {
		t.Fatalf("PortMappings: %v", err)
	}
	if len(mappings) != 1 || mappings[0].ContainerPort != 80 {
		t.Fatalf("PortMappings = %+v, want one mapping for container port 80", mappings)
	}
	if mappings[0].Source != store.PortManual {
		t.Errorf("Source = %s, want %s (adopted ports are recorded as manual, not re-detected)", mappings[0].Source, store.PortManual)
	}
}

func TestAdoptContainerUnknownIDErrors(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)

	_, err := AdoptContainer(context.Background(), s, "", "does-not-exist", 0)
	if err == nil {
		t.Fatal("expected error adopting a nonexistent container")
	}
}

func TestForgetContainerRemovesIt(t *testing.T) {
	dockerAvailable(t)

	name := "claudio-test-forget-" + strings.ReplaceAll(t.Name(), "/", "-")
	out, err := exec.Command("docker", "run", "-d",
		"--label", engine.LabelInstanceID+"=forget-me",
		"--name", name,
		"alpine", "sleep", "60").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	containerID := trimNewline(string(out))

	if err := ForgetContainer(context.Background(), "", containerID); err != nil {
		exec.Command("docker", "rm", "-f", containerID).Run()
		t.Fatalf("ForgetContainer: %v", err)
	}

	inspectErr := exec.Command("docker", "inspect", containerID).Run()
	if inspectErr == nil {
		t.Fatal("container still exists after ForgetContainer")
	}
}
