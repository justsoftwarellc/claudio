// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateInstanceCopiesEnvFile(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	hostEnvFile := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(hostEnvFile, []byte("API_KEY=abc123\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	params := baseCreateParams(t, repoURL)
	params.EnvFile = hostEnvFile
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	got, err := os.ReadFile(filepath.Join(result.WorktreeDir, ".claudio", "env"))
	if err != nil {
		t.Fatalf("read copied env file: %v", err)
	}
	if string(got) != "API_KEY=abc123\n" {
		t.Errorf("copied content = %q, want %q", got, "API_KEY=abc123\n")
	}
}

func TestCreateInstanceExcludesClaudioDirFromGit(t *testing.T) {
	dockerAvailable(t)
	s := openTestStore(t)
	repoURL := newLocalOriginRepo(t)

	params := baseCreateParams(t, repoURL)
	result, err := createForTest(t, s, params)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", result.ContainerID).Run() })

	inst, err := s.GetInstance(t.Context(), result.InstanceID)
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	excludePath := filepath.Join(inst.RepoRoot, "main-clone", ".git", "info", "exclude")
	data, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read %s: %v", excludePath, err)
	}
	if !strings.Contains(string(data), ".claudio/") {
		t.Errorf("%s = %q, want it to contain \".claudio/\"", excludePath, data)
	}
}
