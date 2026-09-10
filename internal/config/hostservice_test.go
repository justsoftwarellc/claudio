// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package config

import (
	"os"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	if contents != "" {
		if err := os.WriteFile(dir+"/.claudio.yml", []byte(contents), 0o644); err != nil {
			t.Fatalf("write .claudio.yml: %v", err)
		}
	}
	return dir
}

func TestLoadRepoConfigHostServices(t *testing.T) {
	dir := writeConfig(t, `host_services:
  - name: db
    host: 5432
  - name: api
    host: 8080
    container: 8080
`)
	cfg, err := LoadRepoConfig(dir + "/.claudio.yml")
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if len(cfg.HostServices) != 2 {
		t.Fatalf("got %d host services, want 2", len(cfg.HostServices))
	}
	if cfg.HostServices[0].ContainerPort() != 5432 {
		t.Errorf("db ContainerPort() = %d, want it to default to host 5432", cfg.HostServices[0].ContainerPort())
	}
	if cfg.HostServices[1].Container == nil || *cfg.HostServices[1].Container != 8080 {
		t.Errorf("api Container = %v, want an explicit 8080", cfg.HostServices[1].Container)
	}
}

func TestLoadRepoConfigHostServiceValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "missing name",
			yaml:    "host_services:\n  - host: 5432\n",
			wantErr: "missing required field 'name'",
		},
		{
			name:    "missing host",
			yaml:    "host_services:\n  - name: db\n",
			wantErr: "missing required field 'host'",
		},
		{
			// Two entries for one name would resolve to whichever Docker
			// wrote first, silently ignoring the second — this is the only
			// place the user can still be told which name collided.
			name:    "duplicate name",
			yaml:    "host_services:\n  - name: db\n    host: 5432\n  - name: db\n    host: 6379\n",
			wantErr: "duplicate name",
		},
		{
			name:    "host port out of range",
			yaml:    "host_services:\n  - name: db\n    host: 70000\n",
			wantErr: "not a valid TCP port",
		},
		{
			name:    "container port out of range",
			yaml:    "host_services:\n  - name: db\n    host: 5432\n    container: 99999\n",
			wantErr: "not a valid TCP port",
		},
		{
			name:    "unknown key inside an entry",
			yaml:    "host_services:\n  - name: db\n    host: 5432\n    bogus: 1\n",
			wantErr: "field bogus not found",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeConfig(t, tc.yaml)
			_, err := LoadRepoConfig(dir + "/.claudio.yml")
			if err == nil {
				t.Fatalf("LoadRepoConfig accepted %q, want error containing %q", tc.yaml, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// A --host-service flag must survive `claudio restart`, which re-derives
// everything from this file and never sees the original flags. This is
// the same disappearing-declaration bug ROD-123 fixed for `ports --add`.
func TestAddHostServiceToRepoConfigCreatesFile(t *testing.T) {
	dir := t.TempDir()
	if err := AddHostServiceToRepoConfig(dir, HostService{Name: "db", Host: 5432}); err != nil {
		t.Fatalf("AddHostServiceToRepoConfig: %v", err)
	}

	cfg, err := LoadRepoConfig(dir + "/.claudio.yml")
	if err != nil {
		t.Fatalf("LoadRepoConfig after write: %v", err)
	}
	if len(cfg.HostServices) != 1 || cfg.HostServices[0].Name != "db" || cfg.HostServices[0].Host != 5432 {
		t.Fatalf("round-trip produced %+v, want one db:5432 entry", cfg.HostServices)
	}
}

// The file belongs to the user: adding a host service must not disturb
// keys this Go type does not model, nor the ones it does.
func TestAddHostServiceToRepoConfigPreservesOtherKeys(t *testing.T) {
	dir := writeConfig(t, `ports:
  - name: web
    container: 3000
post_create:
  - npm ci
`)
	if err := AddHostServiceToRepoConfig(dir, HostService{Name: "db", Host: 5432}); err != nil {
		t.Fatalf("AddHostServiceToRepoConfig: %v", err)
	}

	cfg, err := LoadRepoConfig(dir + "/.claudio.yml")
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if len(cfg.Ports) != 1 || cfg.Ports[0].Name != "web" {
		t.Errorf("ports were disturbed: %+v", cfg.Ports)
	}
	if len(cfg.PostCreate) != 1 || cfg.PostCreate[0] != "npm ci" {
		t.Errorf("post_create was disturbed: %+v", cfg.PostCreate)
	}
	if len(cfg.HostServices) != 1 {
		t.Errorf("host_services = %+v, want one entry", cfg.HostServices)
	}
}

// Unlike a duplicate port, a repeated name is a redirect the user asked
// for: the flag layer outranks the repo's own declaration. Appending
// instead would produce a file LoadRepoConfig then rejects as invalid.
func TestAddHostServiceToRepoConfigReplacesSameName(t *testing.T) {
	dir := writeConfig(t, "host_services:\n  - name: db\n    host: 5432\n")
	if err := AddHostServiceToRepoConfig(dir, HostService{Name: "db", Host: 15432}); err != nil {
		t.Fatalf("AddHostServiceToRepoConfig: %v", err)
	}

	cfg, err := LoadRepoConfig(dir + "/.claudio.yml")
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if len(cfg.HostServices) != 1 {
		t.Fatalf("got %d entries (%+v), want the existing one replaced", len(cfg.HostServices), cfg.HostServices)
	}
	if cfg.HostServices[0].Host != 15432 {
		t.Errorf("host = %d, want the flag's 15432 to win", cfg.HostServices[0].Host)
	}
}

// An explicit `host_services:` with no entries parses as a null scalar
// rather than an empty sequence; appending to it must still work.
func TestAddHostServiceToRepoConfigEmptyList(t *testing.T) {
	dir := writeConfig(t, "host_services:\n")
	if err := AddHostServiceToRepoConfig(dir, HostService{Name: "db", Host: 5432}); err != nil {
		t.Fatalf("AddHostServiceToRepoConfig: %v", err)
	}
	cfg, err := LoadRepoConfig(dir + "/.claudio.yml")
	if err != nil {
		t.Fatalf("LoadRepoConfig: %v", err)
	}
	if len(cfg.HostServices) != 1 {
		t.Fatalf("got %+v, want one entry", cfg.HostServices)
	}
}

// A container port equal to the host port must not be written out: it is
// noise in a file the user owns, and re-reading must be equivalent.
func TestAddHostServiceToRepoConfigOmitsRedundantContainerPort(t *testing.T) {
	dir := t.TempDir()
	if err := AddHostServiceToRepoConfig(dir, HostService{Name: "db", Host: 5432}); err != nil {
		t.Fatalf("AddHostServiceToRepoConfig: %v", err)
	}
	data, err := os.ReadFile(dir + "/.claudio.yml")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(data), "container:") {
		t.Errorf("wrote a redundant container: key:\n%s", data)
	}
}
