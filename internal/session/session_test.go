// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package session

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func dockerAvailable(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping docker-backed test in -short mode")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon not reachable")
	}
}

// tmuxContainer starts a real container running tmux and returns its
// ID, cleaning it up on test completion. Uses the real claudio/base
// image (which already has tmux installed) rather than apk-installing
// it into alpine per test run — skips cleanly if the image was never
// built locally, matching the convention in
// cmd/claudio/create_destroy_live_test.go.
func tmuxContainer(t *testing.T) string {
	t.Helper()
	if err := exec.Command("docker", "image", "inspect", "claudio/base:dev").Run(); err != nil {
		t.Skip("claudio/base:dev image not built locally — build it with `docker build -t claudio/base:dev image/` to run this test")
	}

	out, err := exec.Command("docker", "run", "-d", "--rm", "--entrypoint", "tail",
		"claudio/base:dev", "-f", "/dev/null").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	containerID := strings.TrimSpace(string(out))
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", containerID).Run() })

	if out, err := exec.Command("docker", "exec", containerID,
		"tmux", "new-session", "-d", "-s", SessionName, "-c", "/tmp").CombinedOutput(); err != nil {
		t.Fatalf("start tmux session: %v: %s", err, out)
	}
	return containerID
}

func TestSendKeysWritesToThePane(t *testing.T) {
	dockerAvailable(t)
	containerID := tmuxContainer(t)
	ctx := context.Background()

	if err := SendKeys(ctx, "", containerID, "echo hello-from-sendkeys"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}

	// tmux needs a moment to actually execute the injected command before
	// capture-pane sees its output.
	time.Sleep(300 * time.Millisecond)

	pane, err := CapturePane(ctx, "", containerID)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if !strings.Contains(pane, "hello-from-sendkeys") {
		t.Errorf("captured pane does not contain the injected command's output: %q", pane)
	}
}

func TestSendKeysEscapesSingleQuotes(t *testing.T) {
	dockerAvailable(t)
	containerID := tmuxContainer(t)
	ctx := context.Background()

	// The tricky case shellQuote exists for: text containing a single
	// quote must not break out of the sh -c string SendKeys builds.
	if err := SendKeys(ctx, "", containerID, "echo it's-a-test"); err != nil {
		t.Fatalf("SendKeys with an embedded single quote: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	pane, err := CapturePane(ctx, "", containerID)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if !strings.Contains(pane, "it's-a-test") {
		t.Errorf("captured pane does not contain the quoted command's output: %q", pane)
	}
}

func TestCapturePaneUnknownContainerErrors(t *testing.T) {
	dockerAvailable(t)
	_, err := CapturePane(context.Background(), "", "does-not-exist")
	if err == nil {
		t.Fatal("expected an error capturing a pane from a nonexistent container")
	}
}

func TestSendKeysNoSessionErrors(t *testing.T) {
	// A container with no tmux session at all — RunInContainer's exec
	// still succeeds (tmux itself runs), but tmux's own exit code is
	// nonzero for "no such session," which SendKeys must surface as an
	// error rather than silently reporting success.
	dockerAvailable(t)
	if err := exec.Command("docker", "image", "inspect", "claudio/base:dev").Run(); err != nil {
		t.Skip("claudio/base:dev image not built locally")
	}
	out, err := exec.Command("docker", "run", "-d", "--rm", "--entrypoint", "tail",
		"claudio/base:dev", "-f", "/dev/null").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	containerID := strings.TrimSpace(string(out))
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", containerID).Run() })

	err = SendKeys(context.Background(), "", containerID, "echo hi")
	if err == nil {
		t.Fatal("expected an error sending keys to a container with no tmux session")
	}
}
