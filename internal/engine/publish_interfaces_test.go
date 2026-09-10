// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package engine

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCreateAndStartPublishAllInterfaces is the opt-out half of ROD-98's
// bind rule — TestCreateAndStartPublishesPorts covers the 127.0.0.1
// default. Asserted against Docker's own inspect output rather than the
// nat.PortMap this package builds, for the same reason that test gives:
// what matters is the binding Docker actually applied.
func TestCreateAndStartPublishAllInterfaces(t *testing.T) {
	dockerAvailable(t)
	imageAvailable(t, "alpine")

	instanceID := "publish-all-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	hostPort := 43511 + int(time.Now().UnixNano()%400)
	repoRoot := t.TempDir()

	containerID, err := CreateAndStart(context.Background(), "", CreateSpec{
		InstanceID:           instanceID,
		RepoURL:              "git@github.com:acme/web.git",
		Image:                "alpine",
		Cmd:                  []string{"sleep", "60"},
		RepoRoot:             repoRoot,
		WorktreeDir:          repoRoot,
		HomeDir:              t.TempDir(),
		Ports:                []PortBinding{{ContainerPort: 80, HostPort: hostPort}},
		PublishAllInterfaces: true,
	})
	if err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", containerID).Run() })

	inspectOut := run(t, "", "docker", "inspect", "--format",
		`{{(index (index .NetworkSettings.Ports "80/tcp") 0).HostIp}}`, containerID)
	if got := strings.TrimSpace(inspectOut); got != "0.0.0.0" {
		t.Fatalf("published HostIp = %q, want 0.0.0.0 with PublishAllInterfaces set", got)
	}
}

// TestToDockerPortsBindIP pins which address each mode requests, without
// needing a Docker daemon — the live tests above confirm Docker honors
// it, this one confirms the mapping from the flag is not inverted.
func TestToDockerPortsBindIP(t *testing.T) {
	for _, tc := range []struct {
		name                string
		publishAllInterface bool
		wantIP              string
	}{
		{"default is loopback only", false, "127.0.0.1"},
		{"opt-out publishes on all interfaces", true, "0.0.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			portMap, portSet, err := toDockerPorts([]PortBinding{{ContainerPort: 3000, HostPort: 43001}}, tc.publishAllInterface)
			if err != nil {
				t.Fatalf("toDockerPorts: %v", err)
			}
			if len(portSet) != 1 {
				t.Errorf("exposed ports = %v, want exactly one", portSet)
			}
			bindings := portMap["3000/tcp"]
			if len(bindings) != 1 {
				t.Fatalf("port bindings for 3000/tcp = %+v, want exactly one", bindings)
			}
			if bindings[0].HostIP != tc.wantIP {
				t.Errorf("HostIP = %q, want %q", bindings[0].HostIP, tc.wantIP)
			}
			if bindings[0].HostPort != "43001" {
				t.Errorf("HostPort = %q, want \"43001\"", bindings[0].HostPort)
			}
		})
	}
}
