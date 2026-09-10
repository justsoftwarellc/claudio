package core

import (
	"context"
	"os/exec"
	"testing"
)

// The workflow --add exists for: an app is started inside the container
// on a port nobody declared at create time, `ports --add` records it, and
// a restart publishes it. Before AddPort declared the port in the
// worktree's .claudio.yml this failed — a restart re-derives its ports
// from that file, and stopping releases every store reservation, so the
// restart the CLI tells the user to run was itself what dropped the port.
func TestAddPortSurvivesRestart(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	ctx := context.Background()
	repoURL := newLocalOriginRepo(t)
	params := baseCreateParams(t, repoURL)

	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	hostPort, err := AddPort(ctx, s, result.InstanceID, 8080, params.PortRangeLow, params.PortRangeHigh)
	if err != nil {
		t.Fatalf("AddPort: %v", err)
	}
	if hostPort == 0 {
		t.Fatal("AddPort returned host port 0")
	}

	restarted, err := RestartInstance(ctx, s, params.DockerHost, params, result.InstanceID, false, nil)
	if err != nil {
		t.Fatalf("RestartInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", restarted.ContainerID).Run() })

	for _, p := range restarted.Ports {
		if p.ContainerPort == 8080 {
			return // published again after the restart
		}
	}
	t.Fatalf("container port 8080 gone after restart; ports = %+v", restarted.Ports)
}
