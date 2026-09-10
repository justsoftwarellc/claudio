// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGlobal(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoadGlobalConfigEditor(t *testing.T) {
	path := writeGlobal(t, "editor: code\n")
	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if cfg.Editor != "code" {
		t.Fatalf("editor not parsed: %q", cfg.Editor)
	}
}

// Unset is the honest default: Defaults() must not invent an editor,
// because `claudio open` distinguishes "none configured" from "the
// configured one is missing" and those need different messages.
func TestGlobalConfigEditorDefaultsToUnset(t *testing.T) {
	cfg, err := LoadGlobalConfig(filepath.Join(t.TempDir(), "config.yml"))
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if cfg.Editor != "" {
		t.Fatalf("expected no default editor, got %q", cfg.Editor)
	}
	if Defaults().Editor != "" {
		t.Fatalf("Defaults() should not set an editor, got %q", Defaults().Editor)
	}
}

// Setting an editor must not disturb the rest of the machine's config.
// This is the regression the yaml.Node edit exists for: a round-trip
// through the GlobalConfig struct would rewrite these keys (and
// materialize Defaults() as explicit ones) as a side effect.
func TestSetGlobalEditorPreservesOtherKeysAndComments(t *testing.T) {
	path := writeGlobal(t, `# my machine
workspace_root: /data/claudio

resources:
  memory: 12g
  cpus: 8

ports:
  range: [44000, 44999]
  bind: 127.0.0.1
`)
	if err := SetGlobalEditor(path, "nvim"); err != nil {
		t.Fatalf("SetGlobalEditor: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(raw), "# my machine") {
		t.Errorf("comment was dropped:\n%s", raw)
	}

	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("LoadGlobalConfig after set: %v", err)
	}
	if cfg.Editor != "nvim" {
		t.Errorf("editor = %q, want nvim", cfg.Editor)
	}
	if cfg.WorkspaceRoot != "/data/claudio" {
		t.Errorf("workspace_root = %q, want /data/claudio", cfg.WorkspaceRoot)
	}
	if cfg.Resources.Memory == nil || *cfg.Resources.Memory != "12g" {
		t.Errorf("resources.memory not preserved: %+v", cfg.Resources)
	}
	if cfg.Resources.CPUs == nil || *cfg.Resources.CPUs != 8 {
		t.Errorf("resources.cpus not preserved: %+v", cfg.Resources)
	}
	if cfg.Ports.Range != [2]int{44000, 44999} {
		t.Errorf("ports.range not preserved: %+v", cfg.Ports)
	}
}

// Changing the editor replaces the value rather than appending a second
// `editor:` key — a duplicate would make the file's meaning depend on
// which one the decoder happened to read last.
func TestSetGlobalEditorReplacesExisting(t *testing.T) {
	path := writeGlobal(t, "editor: vim\nworkspace_root: /data/claudio\n")
	if err := SetGlobalEditor(path, "code"); err != nil {
		t.Fatalf("SetGlobalEditor: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if n := strings.Count(string(raw), "editor:"); n != 1 {
		t.Fatalf("expected exactly one editor key, got %d:\n%s", n, raw)
	}
	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if cfg.Editor != "code" {
		t.Fatalf("editor = %q, want code", cfg.Editor)
	}
	if cfg.WorkspaceRoot != "/data/claudio" {
		t.Fatalf("workspace_root = %q, want /data/claudio", cfg.WorkspaceRoot)
	}
}

// `claudio config set editor` is reachable before anything has created
// ~/.claudio, so both the file and its directory may be absent.
func TestSetGlobalEditorCreatesMissingFileAndDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yml")
	if err := SetGlobalEditor(path, "zed"); err != nil {
		t.Fatalf("SetGlobalEditor: %v", err)
	}
	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if cfg.Editor != "zed" {
		t.Fatalf("editor = %q, want zed", cfg.Editor)
	}
	// The defaults must still apply to a file that only names an editor.
	if cfg.Ports.Range != Defaults().Ports.Range {
		t.Fatalf("defaults lost: %+v", cfg.Ports)
	}
}

// A config.yml that is already broken must be reported as such, not
// silently appended to — burying the real parse error under a write.
func TestSetGlobalEditorRefusesUnparseableFile(t *testing.T) {
	path := writeGlobal(t, "workspace_rooot: /typo\n")
	err := SetGlobalEditor(path, "code")
	if err == nil {
		t.Fatal("expected an error for a config with an unknown key")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("error should name the parse failure, got: %v", err)
	}
}
