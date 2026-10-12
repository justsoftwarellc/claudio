// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package imagebuild

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSSHOffersMountedKeysInBaseImage covers what the base image owes
// an instance whose remote is git@host:path, booted through the real
// entrypoint with a key mounted the way engine.CreateAndStart mounts
// the ones `ssh_keys:` lists.
//
// Each assertion is one way this failed or would have: the image
// shipped no ssh at all ("cannot run ssh: No such file or directory" on
// the first `git fetch`); a key not named id_ed25519/id_rsa is never
// tried unless something names it; and with no known_hosts and no
// terminal, ssh's default first-connection prompt fails every fetch.
func TestSSHOffersMountedKeysInBaseImage(t *testing.T) {
	dockerAvailable(t)
	baseTag := uniqueTag("claudio-test/base-for-ssh")
	t.Cleanup(func() { removeImage(t, baseTag) })
	if err := buildBaseAs(context.Background(), baseTag, BuildArgsForHost(os.Getuid(), os.Getgid())); err != nil {
		t.Fatalf("build base: %v", err)
	}

	// Under $HOME rather than t.TempDir(): Docker Desktop only shares
	// the user's home with its VM by default.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.MkdirTemp(home, ".claudio-ssh-image-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	homeDir := filepath.Join(base, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Deliberately not one of ssh's default key names.
	key := filepath.Join(base, "work_key")
	if err := os.WriteFile(key, []byte("not-a-real-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	boot := func(t *testing.T, mountKey bool) string {
		t.Helper()
		args := []string{"run", "-d", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
			"-e", "CLAUDE_CODE_OAUTH_TOKEN=test-not-a-real-token",
			"-v", homeDir + ":/home/agent", "-w", "/home/agent"}
		if mountKey {
			args = append(args, "-v", key+":/opt/claudio/ssh-keys/work_key:ro")
		}
		out, err := exec.Command("docker", append(args, baseTag)...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker run: %v: %s", err, out)
		}
		id := strings.TrimSpace(string(out))
		t.Cleanup(func() { exec.Command("docker", "rm", "-f", id).Run() })
		// The entrypoint creates the tmux session last, after the ssh
		// wiring, so its existence means the wiring has run.
		waitOut, err := exec.Command("docker", "exec", id, "bash", "-c",
			"for i in $(seq 50); do tmux has-session -t claude 2>/dev/null && exit 0; sleep 0.2; done; exit 1").CombinedOutput()
		if err != nil {
			logs, _ := exec.Command("docker", "logs", id).CombinedOutput()
			t.Fatalf("entrypoint never reached its tmux session: %v: %s\n%s", err, waitOut, logs)
		}
		return id
	}
	// `ssh -G` prints the config ssh would actually use for a host.
	sshConfig := func(t *testing.T, id string) string {
		t.Helper()
		out, err := exec.Command("docker", "exec", id, "ssh", "-G", "github.com").CombinedOutput()
		if err != nil {
			t.Fatalf("ssh -G in the base image: %v: %s", err, out)
		}
		return string(out)
	}

	withKey := sshConfig(t, boot(t, true))
	if !strings.Contains(withKey, "identityfile /opt/claudio/ssh-keys/work_key\n") {
		t.Errorf("ssh does not offer the mounted key; ssh -G says:\n%s", withKey)
	}
	if !strings.Contains(withKey, "stricthostkeychecking accept-new\n") {
		t.Errorf("ssh would prompt on a first connection, which git cannot answer; ssh -G says:\n%s", withKey)
	}

	// Same home/, key no longer listed: it must stop being offered
	// rather than linger from the previous boot.
	withoutKey := sshConfig(t, boot(t, false))
	if strings.Contains(withoutKey, "/opt/claudio/ssh-keys") {
		t.Errorf("ssh still names a key that is no longer mounted; ssh -G says:\n%s", withoutKey)
	}
}
