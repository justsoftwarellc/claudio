// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package compose

import (
	"os"
	"testing"
)

func writeCompose(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/docker-compose.yml"
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}
	return path
}

// The regression ROD-129 is about: a service with no ports: key is
// absent from LoadDeclaredPorts (correctly — it needs no allocation) but
// must still be enumerated here, because it still has to join the
// per-instance network to be reachable by name.
func TestLoadServiceNamesIncludesServicesWithNoPorts(t *testing.T) {
	path := writeCompose(t, `services:
  cache:
    image: redis:7-alpine
  db:
    image: postgres:16
    ports:
      - "5432:5432"
`)

	names, err := LoadServiceNames(path)
	if err != nil {
		t.Fatalf("LoadServiceNames: %v", err)
	}
	want := []string{"cache", "db"}
	if len(names) != len(want) {
		t.Fatalf("LoadServiceNames = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, names[i], want[i])
		}
	}

	// The contrast that makes the bug concrete: the ports-based view sees
	// only db, so it can never be the source of truth for the network.
	declared, err := LoadDeclaredPorts(path)
	if err != nil {
		t.Fatalf("LoadDeclaredPorts: %v", err)
	}
	if len(declared) != 1 || declared[0].Service != "db" {
		t.Fatalf("LoadDeclaredPorts = %+v, want just db", declared)
	}
}

func TestLoadServiceNamesEmptyAndMissing(t *testing.T) {
	t.Run("no services key", func(t *testing.T) {
		names, err := LoadServiceNames(writeCompose(t, "version: \"3\"\n"))
		if err != nil {
			t.Fatalf("LoadServiceNames: %v", err)
		}
		if len(names) != 0 {
			t.Errorf("got %v, want none", names)
		}
	})

	t.Run("unreadable path is an error", func(t *testing.T) {
		if _, err := LoadServiceNames(t.TempDir() + "/does-not-exist.yml"); err == nil {
			t.Error("LoadServiceNames on a missing file returned nil error")
		}
	})

	t.Run("malformed yaml is an error", func(t *testing.T) {
		if _, err := LoadServiceNames(writeCompose(t, "services: [oh: no\n")); err == nil {
			t.Error("LoadServiceNames on malformed YAML returned nil error")
		}
	})
}

// Order must be stable: this feeds the generated override, and a
// map-ranged order would make the file differ run to run for identical
// input, turning any diff of it into noise.
func TestLoadServiceNamesIsStablyOrdered(t *testing.T) {
	path := writeCompose(t, `services:
  zulu:
    image: alpine
  alpha:
    image: alpine
  mike:
    image: alpine
`)
	first, err := LoadServiceNames(path)
	if err != nil {
		t.Fatalf("LoadServiceNames: %v", err)
	}
	if first[0] != "alpha" || first[2] != "zulu" {
		t.Fatalf("expected sorted order, got %v", first)
	}
	for i := 0; i < 20; i++ {
		again, err := LoadServiceNames(path)
		if err != nil {
			t.Fatalf("LoadServiceNames: %v", err)
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("ordering not stable on run %d: %v vs %v", i, again, first)
			}
		}
	}
}
