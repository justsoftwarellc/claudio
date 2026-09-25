// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/config"
)

func write(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadInstancesMissingFileIsNotAnError(t *testing.T) {
	ids, path, err := config.LoadInstances(t.TempDir())
	if err != nil {
		t.Fatalf("LoadInstances: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("ids = %v, want none", ids)
	}
	// An empty path is what tells a caller there is no file to name in a
	// message; returning one that doesn't exist would print a lie.
	if path != "" {
		t.Errorf("path = %q, want empty for a missing file", path)
	}
}

func TestLoadInstancesMalformedIsAnError(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "instances: [unclosed\n")

	if _, _, err := config.LoadInstances(dir); err == nil {
		t.Fatal("LoadInstances succeeded on malformed YAML, want an error")
	}
}

// Config-only and instances-only are both valid states of one file, and
// neither may erase the other.
func TestAppendInstancePreservesConfig(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "image:\n  apt: [libpq-dev]\npost_create:\n  - npm ci\n")

	if err := config.AppendInstance(dir, "a3f9c2"); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadRepoConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Image.Apt) != 1 || cfg.Image.Apt[0] != "libpq-dev" {
		t.Errorf("image.apt = %v, want [libpq-dev]", cfg.Image.Apt)
	}
	if len(cfg.PostCreate) != 1 || cfg.PostCreate[0] != "npm ci" {
		t.Errorf("post_create = %v, want [npm ci]", cfg.PostCreate)
	}
	if len(cfg.Instances) != 1 || cfg.Instances[0] != "a3f9c2" {
		t.Errorf("instances = %v, want [a3f9c2]", cfg.Instances)
	}
}

// The file is user-authored, so an edit must not silently reformat it.
// A struct round-trip would drop every comment here.
func TestAppendInstanceKeepsComments(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "# why this repo needs libpq\nimage:\n  apt: [libpq-dev]\n")

	if err := config.AppendInstance(dir, "a3f9c2"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# why this repo needs libpq") {
		t.Errorf("comment lost on append:\n%s", data)
	}
}

func TestAppendInstanceIsIdempotentAndOrdered(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"first1", "second", "first1"} {
		if err := config.AppendInstance(dir, id); err != nil {
			t.Fatal(err)
		}
	}

	ids, _, err := config.LoadInstances(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "first1" || ids[1] != "second" {
		t.Errorf("ids = %v, want [first1 second] in creation order", ids)
	}
}

func TestAppendInstanceGitignoresTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := config.AppendInstance(dir, "a3f9c2"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), config.FileName) {
		t.Errorf(".gitignore does not cover %s:\n%s", config.FileName, data)
	}
}

// A .gitignore whose last line lacks a newline would otherwise get the
// entry glued onto it, ignoring the wrong path and not ignoring ours.
func TestAppendInstanceGitignoreWithoutTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("dist"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.AppendInstance(dir, "a3f9c2"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == config.FileName {
			return
		}
	}
	t.Errorf("%s not on a line of its own:\n%q", config.FileName, data)
}

// Removing the last id must clear the key but keep the file: it also
// holds config that unlinking an instance has no business destroying.
func TestRemoveInstanceLastIDKeepsFileAndConfig(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "image:\n  apt: [libpq-dev]\n")
	if err := config.AppendInstance(dir, "a3f9c2"); err != nil {
		t.Fatal(err)
	}

	if err := config.RemoveInstance(dir, "a3f9c2"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s was deleted along with its last instance: %v", config.FileName, err)
	}
	cfg, err := config.LoadRepoConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Instances) != 0 {
		t.Errorf("instances = %v, want none", cfg.Instances)
	}
	if len(cfg.Image.Apt) != 1 {
		t.Errorf("image.apt = %v, want [libpq-dev] preserved", cfg.Image.Apt)
	}
}

func TestRemoveInstanceUnknownIDIsNoop(t *testing.T) {
	dir := t.TempDir()
	if err := config.AppendInstance(dir, "a3f9c2"); err != nil {
		t.Fatal(err)
	}

	// destroy calls this for every instance, including ones never linked.
	if err := config.RemoveInstance(dir, "nothere"); err != nil {
		t.Fatalf("RemoveInstance for an unlisted id: %v", err)
	}
	if err := config.RemoveInstance(t.TempDir(), "a3f9c2"); err != nil {
		t.Fatalf("RemoveInstance with no file: %v", err)
	}

	ids, _, err := config.LoadInstances(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 {
		t.Errorf("ids = %v, want [a3f9c2] untouched", ids)
	}
}

func TestFindInstancesWalksUpFromSubdirectory(t *testing.T) {
	root := t.TempDir()
	if err := config.AppendInstance(root, "a3f9c2"); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	dir, ids, err := config.FindInstances(sub)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "a3f9c2" {
		t.Errorf("ids = %v, want [a3f9c2]", ids)
	}
	// EvalSymlinks: t.TempDir is under /var, a symlink to /private/var.
	want, _ := filepath.EvalSymlinks(root)
	got, _ := filepath.EvalSymlinks(dir)
	if got != want {
		t.Errorf("dir = %q, want %q", got, want)
	}
}

func TestFindInstancesNearestWins(t *testing.T) {
	root := t.TempDir()
	if err := config.AppendInstance(root, "parent"); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.AppendInstance(sub, "child1"); err != nil {
		t.Fatal(err)
	}

	_, ids, err := config.FindInstances(sub)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "child1" {
		t.Errorf("ids = %v, want [child1] — the nearest file wins", ids)
	}
}

// New with the merge: a subdirectory may carry config without ever
// having had an instance. Stopping the walk there would shadow a parent
// that does have one.
func TestFindInstancesSkipsConfigWithNoInstances(t *testing.T) {
	root := t.TempDir()
	if err := config.AppendInstance(root, "parent"); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "service")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, sub, "image:\n  apt: [libpq-dev]\n")

	dir, ids, err := config.FindInstances(sub)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "parent" {
		t.Errorf("ids = %v (dir %s), want [parent] — a config-only file must not stop the walk", ids, dir)
	}
}

func TestFindInstancesNoFileAnywhere(t *testing.T) {
	dir, ids, err := config.FindInstances(t.TempDir())
	if err != nil {
		t.Fatalf("FindInstances: %v", err)
	}
	if dir != "" || len(ids) != 0 {
		t.Errorf("dir = %q ids = %v, want empty for a directory Claudio never ran in", dir, ids)
	}
}
