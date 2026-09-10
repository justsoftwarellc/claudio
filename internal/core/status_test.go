// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"context"
	"os/exec"
	"testing"
)

func TestGetInstanceViewMergesLiveContainerState(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	result, err := createForTest(t, s, baseCreateParams(t, repoURL))
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	view, err := GetInstanceView(context.Background(), s, "", result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstanceView: %v", err)
	}
	if !view.ContainerRunning {
		t.Error("ContainerRunning = false, want true for a just-created container")
	}
	if view.ContainerStatus == "" {
		t.Error("ContainerStatus is empty, want Docker's live status string")
	}
	if view.ID != result.InstanceID {
		t.Errorf("ID = %q, want %q", view.ID, result.InstanceID)
	}
}

func TestGetInstanceViewUnknownIDErrors(t *testing.T) {
	s := openTestStore(t)
	_, err := GetInstanceView(context.Background(), s, "", "does-not-exist")
	if err == nil {
		t.Fatal("expected error for a nonexistent instance")
	}
}
