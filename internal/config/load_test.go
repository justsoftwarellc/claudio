// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".claudio.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoadRepoConfigMissingFileIsNotAnError(t *testing.T) {
	cfg, err := LoadRepoConfig(filepath.Join(t.TempDir(), ".claudio.yml"))
	if err != nil {
		t.Fatalf("expected no error for absent file, got %v", err)
	}
	if len(cfg.Ports) != 0 {
		t.Fatalf("expected zero-value config, got %+v", cfg)
	}
}

func TestLoadRepoConfigValid(t *testing.T) {
	path := writeTemp(t, `
image:
  base: node:22-slim
  apt: [postgresql-client]

ports:
  - name: web
    container: 3000
  - name: db
    container: 5432
    expose: false

post_create:
  - npm ci

resources:
  memory: 10g
`)
	cfg, err := LoadRepoConfig(path)
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if cfg.Image.Base != "node:22-slim" {
		t.Fatalf("image.base not parsed: %+v", cfg.Image)
	}
	if len(cfg.Ports) != 2 || cfg.Ports[0].Name != "web" || cfg.Ports[0].Exposed() != true {
		t.Fatalf("ports not parsed correctly: %+v", cfg.Ports)
	}
	if cfg.Ports[1].Exposed() {
		t.Fatalf("expected db port to be non-exposed (expose:false)")
	}
	if len(cfg.PostCreate) != 1 || cfg.PostCreate[0] != "npm ci" {
		t.Fatalf("post_create not parsed: %+v", cfg.PostCreate)
	}
	if *cfg.Resources.Memory != "10g" {
		t.Fatalf("resources.memory not parsed: %+v", cfg.Resources)
	}
}

func TestLoadRepoConfigRejectsUnknownKey(t *testing.T) {
	path := writeTemp(t, `
image:
  base: node:22-slim
  bogus_key: true
`)
	_, err := LoadRepoConfig(path)
	if err == nil {
		t.Fatalf("expected an error for unknown key 'bogus_key', got nil")
	}
	if !strings.Contains(err.Error(), "bogus_key") {
		t.Fatalf("expected error to name the unknown key, got: %v", err)
	}
}

func TestLoadRepoConfigRejectsPortMissingName(t *testing.T) {
	path := writeTemp(t, `
ports:
  - container: 3000
`)
	_, err := LoadRepoConfig(path)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("expected error naming missing 'name' field, got %v", err)
	}
}

func TestLoadRepoConfigRejectsServiceMissingImage(t *testing.T) {
	path := writeTemp(t, `
services:
  - name: db
`)
	_, err := LoadRepoConfig(path)
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("expected error naming missing 'image' field, got %v", err)
	}
}

func TestLoadGlobalConfigDefaultsWhenAbsent(t *testing.T) {
	cfg, err := LoadGlobalConfig(filepath.Join(t.TempDir(), "config.yml"))
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if *cfg.Resources.Memory != "6g" || *cfg.Resources.CPUs != 4 {
		t.Fatalf("expected built-in defaults, got %+v", cfg.Resources)
	}
	if cfg.Ports.Range != [2]int{43000, 43999} {
		t.Fatalf("expected default port range, got %v", cfg.Ports.Range)
	}
	if cfg.Ports.Bind != "127.0.0.1" {
		t.Fatalf("expected default bind 127.0.0.1, got %s", cfg.Ports.Bind)
	}
}

func TestLoadGlobalConfigOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(`
resources:
  memory: 8g
ports:
  range: [50000, 50099]
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("LoadGlobalConfig: %v", err)
	}
	if *cfg.Resources.Memory != "8g" {
		t.Fatalf("expected overridden memory 8g, got %s", *cfg.Resources.Memory)
	}
	if *cfg.Resources.CPUs != 4 {
		t.Fatalf("expected cpus to keep default 4 when not overridden, got %d", *cfg.Resources.CPUs)
	}
	if cfg.Ports.Range != [2]int{50000, 50099} {
		t.Fatalf("expected overridden port range, got %v", cfg.Ports.Range)
	}
	if cfg.Ports.Bind != "127.0.0.1" {
		t.Fatalf("expected bind to keep default when not overridden, got %s", cfg.Ports.Bind)
	}
}

// post_start is the hook that survives a restart (ROD-127). Parsed
// alongside post_create rather than instead of it — a repo that both
// installs dependencies and runs a server declares both.
func TestLoadRepoConfigParsesPostStart(t *testing.T) {
	path := writeTemp(t, `
post_create:
  - npm ci

post_start:
  - npm start
  - npm run worker
`)
	cfg, err := LoadRepoConfig(path)
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if len(cfg.PostCreate) != 1 || cfg.PostCreate[0] != "npm ci" {
		t.Fatalf("post_create not parsed: %+v", cfg.PostCreate)
	}
	if len(cfg.PostStart) != 2 || cfg.PostStart[0] != "npm start" || cfg.PostStart[1] != "npm run worker" {
		t.Fatalf("post_start not parsed: %+v", cfg.PostStart)
	}
}

// The package's unknown-key strictness has to cover the new key too:
// `poststart` or `post-start` is a typo that must say so rather than
// silently never starting the user's server.
func TestLoadRepoConfigRejectsMisspelledPostStart(t *testing.T) {
	for _, key := range []string{"poststart", "post-start", "on_start"} {
		t.Run(key, func(t *testing.T) {
			path := writeTemp(t, key+":\n  - npm start\n")
			if _, err := LoadRepoConfig(path); err == nil {
				t.Fatalf("expected %q to be rejected as an unknown key", key)
			}
		})
	}
}
