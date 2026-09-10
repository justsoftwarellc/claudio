package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYML(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, ".claudio.yml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAddPortCreatesFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := AddPortToRepoConfig(dir, "manual-8080", 8080); err != nil {
		t.Fatalf("AddPortToRepoConfig: %v", err)
	}

	cfg, err := LoadRepoConfig(filepath.Join(dir, ".claudio.yml"))
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if len(cfg.Ports) != 1 || cfg.Ports[0].Container != 8080 || cfg.Ports[0].Name != "manual-8080" {
		t.Errorf("Ports = %+v, want one entry manual-8080/8080", cfg.Ports)
	}
}

func TestAddPortAppendsToExistingPorts(t *testing.T) {
	dir := t.TempDir()
	writeYML(t, dir, "ports:\n  - name: web\n    container: 3000\n")

	if err := AddPortToRepoConfig(dir, "manual-8080", 8080); err != nil {
		t.Fatalf("AddPortToRepoConfig: %v", err)
	}

	cfg, err := LoadRepoConfig(filepath.Join(dir, ".claudio.yml"))
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if len(cfg.Ports) != 2 {
		t.Fatalf("Ports = %+v, want 2 entries", cfg.Ports)
	}
	if cfg.Ports[0].Container != 3000 || cfg.Ports[1].Container != 8080 {
		t.Errorf("Ports = %+v, want 3000 preserved then 8080 appended", cfg.Ports)
	}
}

// The file belongs to the user: an --add must not silently drop keys this
// package's structs do not model, nor the user's comments. A naive
// unmarshal/remarshal through RepoConfig would lose both.
func TestAddPortPreservesUnknownKeysAndComments(t *testing.T) {
	dir := t.TempDir()
	writeYML(t, dir, `# top comment
post_create:
  - npm install
image:
  base: node:22-slim
  apt:
    - ripgrep
`)

	if err := AddPortToRepoConfig(dir, "manual-8080", 8080); err != nil {
		t.Fatalf("AddPortToRepoConfig: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, ".claudio.yml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{"# top comment", "npm install", "node:22-slim", "ripgrep", "container: 8080"} {
		if !strings.Contains(got, want) {
			t.Errorf("rewritten file lost %q:\n%s", want, got)
		}
	}

	cfg, err := LoadRepoConfig(filepath.Join(dir, ".claudio.yml"))
	if err != nil {
		t.Fatalf("LoadRepoConfig after rewrite: %v", err)
	}
	if len(cfg.PostCreate) != 1 || cfg.Image.Base != "node:22-slim" {
		t.Errorf("decoded = %+v, want post_create and image intact", cfg)
	}
}

func TestAddPortRejectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeYML(t, dir, "ports:\n  - name: web\n    container: 3000\n")

	err := AddPortToRepoConfig(dir, "manual-3000", 3000)
	if !errors.Is(err, ErrPortAlreadyDeclared) {
		t.Fatalf("AddPortToRepoConfig for an already-declared port = %v, want ErrPortAlreadyDeclared", err)
	}

	// The rejected add must leave the file exactly as it was.
	raw, _ := os.ReadFile(filepath.Join(dir, ".claudio.yml"))
	if got := string(raw); got != "ports:\n  - name: web\n    container: 3000\n" {
		t.Errorf("file changed by a rejected add:\n%s", got)
	}
}

// An explicit `ports:` with nothing under it decodes as null, not an
// empty list — appending to it must still produce a valid sequence.
func TestAddPortIntoEmptyPortsKey(t *testing.T) {
	dir := t.TempDir()
	writeYML(t, dir, "ports:\n")

	if err := AddPortToRepoConfig(dir, "manual-8080", 8080); err != nil {
		t.Fatalf("AddPortToRepoConfig: %v", err)
	}
	cfg, err := LoadRepoConfig(filepath.Join(dir, ".claudio.yml"))
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if len(cfg.Ports) != 1 || cfg.Ports[0].Container != 8080 {
		t.Errorf("Ports = %+v, want the single appended entry", cfg.Ports)
	}
}
