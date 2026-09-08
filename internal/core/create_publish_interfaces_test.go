package core

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/portdetect"
)

// publishedHostIP reads back the address Docker actually bound one of an
// instance's published ports on. Asserting against Docker rather than
// against CreateParams is the point: this test exists to catch
// PublishAllInterfaces being dropped somewhere between core and
// engine.CreateSpec, which a struct-level assertion would not notice.
func publishedHostIP(t *testing.T, containerID string, containerPort int) string {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "--format",
		`{{range $p, $conf := .NetworkSettings.Ports}}{{$p}}={{(index $conf 0).HostIp}} {{end}}`,
		containerID).CombinedOutput()
	if err != nil {
		t.Fatalf("docker inspect: %v: %s", err, out)
	}
	want := strconv.Itoa(containerPort) + "/tcp"
	for _, field := range strings.Fields(string(out)) {
		name, ip, ok := strings.Cut(field, "=")
		if ok && name == want {
			return ip
		}
	}
	t.Fatalf("container %s publishes no mapping for %d: %s", containerID, containerPort, out)
	return ""
}

// manualPort is the --ports equivalent for a test that needs one
// predictable published port regardless of what detection finds in the
// fixture repo (which has only a README).
func manualPort(container int) []portdetect.Manual {
	return []portdetect.Manual{{Container: container}}
}

// TestCreateInstanceBindsLoopbackByDefault and its --publish-all-
// interfaces counterpart below cover ROD-98's "bind to 127.0.0.1 by
// default, never 0.0.0.0" through the full create path, since the flag
// crosses four layers (cmd -> client -> core.CreateParams ->
// engine.CreateSpec) and a dropped field would leave the default silently
// correct while the opt-out silently did nothing.
func TestCreateInstanceBindsLoopbackByDefault(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)

	params := baseCreateParams(t, newLocalOriginRepo(t))
	params.ManualPorts = manualPort(3000)

	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	if got := publishedHostIP(t, result.ContainerID, 3000); got != "127.0.0.1" {
		t.Errorf("published HostIp = %q, want 127.0.0.1 (the default, never 0.0.0.0)", got)
	}
}

func TestCreateInstancePublishAllInterfaces(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)

	params := baseCreateParams(t, newLocalOriginRepo(t))
	params.ManualPorts = manualPort(3000)
	params.PublishAllInterfaces = true

	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	if got := publishedHostIP(t, result.ContainerID, 3000); got != "0.0.0.0" {
		t.Errorf("published HostIp = %q, want 0.0.0.0 with PublishAllInterfaces set", got)
	}
}

func TestCreateInstancePublishAllInterfacesLeavesAllocationAlone(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)

	params := baseCreateParams(t, newLocalOriginRepo(t))
	params.ManualPorts = manualPort(3000)
	params.PublishAllInterfaces = true

	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	// The flag changes only the bind address, never which host port the
	// allocator hands out — that still comes from ports.range.
	mappings, err := s.PortMappings(context.Background(), result.InstanceID)
	if err != nil {
		t.Fatalf("PortMappings: %v", err)
	}
	if len(mappings) == 0 {
		t.Fatal("no port mappings recorded")
	}
	for _, m := range mappings {
		if m.HostPort < params.PortRangeLow || m.HostPort > params.PortRangeHigh {
			t.Errorf("host port %d outside ports.range %d-%d", m.HostPort, params.PortRangeLow, params.PortRangeHigh)
		}
	}
}
