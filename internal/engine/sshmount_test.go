// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/mount"
)

func TestMountsAddsNoSSHKeysByDefault(t *testing.T) {
	got := mounts(CreateSpec{RepoRoot: "/r", HomeDir: "/h"})
	if len(got) != 2 {
		t.Errorf("mounts() = %+v, want exactly the repo and home mounts when no key is listed", got)
	}
}

func TestMountsAddsEachSSHKeyReadOnlyByBaseName(t *testing.T) {
	got := mounts(CreateSpec{RepoRoot: "/r", HomeDir: "/h", SSHKeys: []string{"/host/.ssh/id_ed25519", "/elsewhere/work_key"}})
	if len(got) != 4 {
		t.Fatalf("mounts() = %+v, want repo, home and two keys", got)
	}
	want := []mount.Mount{
		{Type: mount.TypeBind, Source: "/host/.ssh/id_ed25519", Target: "/opt/claudio/ssh-keys/id_ed25519", ReadOnly: true},
		{Type: mount.TypeBind, Source: "/elsewhere/work_key", Target: "/opt/claudio/ssh-keys/work_key", ReadOnly: true},
	}
	for i, w := range want {
		if got[2+i] != w {
			t.Errorf("mounts()[%d] = %+v, want %+v", 2+i, got[2+i], w)
		}
	}
}

// The mount as Docker actually applies it: a listed key is readable
// inside, a file sitting next to it on the host is not there at all,
// and the container cannot write to the operator's key.
func TestCreateAndStartMountsOnlyListedSSHKeysReadOnly(t *testing.T) {
	dockerAvailable(t)
	const image = "alpine:3"
	imageAvailable(t, image)

	// Under $HOME rather than t.TempDir(): Docker Desktop only shares
	// the user's home with its VM by default, and /var/folders is not
	// guaranteed to be.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.MkdirTemp(home, ".claudio-sshmount-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })

	repoRoot := filepath.Join(base, "repo")
	worktreeDir := filepath.Join(repoRoot, "worktrees", "w")
	homeDir := filepath.Join(base, "home")
	sshDir := filepath.Join(base, "ssh")
	for _, d := range []string{worktreeDir, homeDir, sshDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"id_test", "id_unlisted"} {
		if err := os.WriteFile(filepath.Join(sshDir, name), []byte("not-a-real-key\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	instanceID := "sshmount-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	containerID, err := CreateAndStart(t.Context(), "", CreateSpec{
		InstanceID:  instanceID,
		RepoURL:     "file:///unused",
		CreatedAt:   time.Now().Unix(),
		Image:       image,
		Cmd:         []string{"sleep", "300"},
		RepoRoot:    repoRoot,
		WorktreeDir: worktreeDir,
		HomeDir:     homeDir,
		SSHKeys:     []string{filepath.Join(sshDir, "id_test")},
	})
	if err != nil {
		t.Fatalf("CreateAndStart: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", containerID).Run() })

	got := strings.TrimSpace(run(t, "", "docker", "exec", containerID, "cat", "/opt/claudio/ssh-keys/id_test"))
	if got != "not-a-real-key" {
		t.Errorf("key read inside the container = %q, want the host file's content", got)
	}

	listing := strings.Fields(run(t, "", "docker", "exec", containerID, "ls", "/opt/claudio/ssh-keys"))
	if len(listing) != 1 || listing[0] != "id_test" {
		t.Errorf("keys visible in the container = %v, want only the listed id_test", listing)
	}

	out, err := exec.Command("docker", "exec", containerID, "sh", "-c", "echo x >> /opt/claudio/ssh-keys/id_test").CombinedOutput()
	if err == nil {
		t.Errorf("writing to a mounted key succeeded, want it refused (read-only mount)")
	} else if !strings.Contains(string(out), "Read-only file system") {
		t.Errorf("write was refused with %q, want a read-only filesystem error", out)
	}
}
