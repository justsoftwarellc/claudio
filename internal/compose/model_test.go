package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func writeComposeFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadDeclaredPortsHandlesShortSyntaxForms verified against a real
// compose file exercising Compose's short port syntax: a bare container
// port, a host:container pair (host side must be discarded — this
// package always reallocates it), and a service with no ports: section
// at all (an internal-only sidecar).
func TestLoadDeclaredPortsHandlesShortSyntaxForms(t *testing.T) {
	path := writeComposeFile(t, `
services:
  db:
    image: postgres:16
    ports:
      - "5432:5432"
  cache:
    image: redis:7
    ports:
      - "6379"
  internal:
    image: alpine
`)
	got, err := LoadDeclaredPorts(path)
	if err != nil {
		t.Fatalf("LoadDeclaredPorts: %v", err)
	}
	want := []DeclaredPort{
		{Service: "cache", ContainerPort: 6379},
		{Service: "db", ContainerPort: 5432},
	}
	if len(got) != len(want) {
		t.Fatalf("LoadDeclaredPorts = %+v, want %+v", got, want)
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
			}
		}
		if !found {
			t.Errorf("LoadDeclaredPorts = %+v, missing %+v", got, w)
		}
	}
}

func TestLoadDeclaredPortsIgnoresProtocolSuffix(t *testing.T) {
	path := writeComposeFile(t, `
services:
  dns:
    image: alpine
    ports:
      - "53:53/udp"
`)
	got, err := LoadDeclaredPorts(path)
	if err != nil {
		t.Fatalf("LoadDeclaredPorts: %v", err)
	}
	if len(got) != 1 || got[0].ContainerPort != 53 {
		t.Errorf("LoadDeclaredPorts = %+v, want one entry for container port 53", got)
	}
}

func TestLoadDeclaredPortsNoServicesOrPortsReturnsNil(t *testing.T) {
	path := writeComposeFile(t, "services: {}\n")
	got, err := LoadDeclaredPorts(path)
	if err != nil {
		t.Fatalf("LoadDeclaredPorts: %v", err)
	}
	if got != nil {
		t.Errorf("LoadDeclaredPorts = %+v, want nil", got)
	}
}

func TestLoadDeclaredPortsMissingFileErrors(t *testing.T) {
	_, err := LoadDeclaredPorts(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected an error for a missing compose file")
	}
}
