// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"os/exec"
	"testing"

	"github.com/rodrigomorales/claudio/internal/store"
)

func TestAddPortReservesInStoreOnly(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	hostPort, err := AddPort(context.Background(), s, result.InstanceID, 9229, 43000, 43999)
	if err != nil {
		t.Fatalf("AddPort: %v", err)
	}
	if hostPort == 0 {
		t.Fatal("AddPort returned host port 0")
	}

	mappings, err := s.PortMappings(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("PortMappings: %v", err)
	}
	found := false
	for _, m := range mappings {
		if m.ContainerPort == 9229 {
			found = true
			if m.Source != store.PortManual {
				t.Errorf("Source = %s, want %s", m.Source, store.PortManual)
			}
			if m.HostPort != hostPort {
				t.Errorf("HostPort = %d, want %d", m.HostPort, hostPort)
			}
		}
	}
	if !found {
		t.Errorf("PortMappings = %+v, want a mapping for container port 9229", mappings)
	}
}

func TestRemovePortReleasesReservation(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	if _, err := AddPort(context.Background(), s, result.InstanceID, 9229, 43000, 43999); err != nil {
		t.Fatalf("AddPort: %v", err)
	}

	if err := RemovePort(context.Background(), s, result.InstanceID, 9229); err != nil {
		t.Fatalf("RemovePort: %v", err)
	}

	mappings, err := s.PortMappings(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("PortMappings: %v", err)
	}
	for _, m := range mappings {
		if m.ContainerPort == 9229 {
			t.Errorf("PortMappings still has container port 9229 after RemovePort: %+v", m)
		}
	}
}

func TestRemovePortUnknownPortErrors(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	err = RemovePort(context.Background(), s, result.InstanceID, 59999)
	if err == nil {
		t.Fatal("expected error removing a port that was never mapped")
	}
}
