// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package core

import (
	"strings"
	"testing"

	"github.com/rodrigomorales/claudio/internal/config"
	"github.com/rodrigomorales/claudio/internal/engine"
)

func intPtr(n int) *int { return &n }

func TestParseHostServiceFlag(t *testing.T) {
	tests := []struct {
		name    string
		entry   string
		want    config.HostService
		wantErr string
	}{
		{name: "name and port", entry: "db:5432", want: config.HostService{Name: "db", Host: 5432}},
		{
			// The redundant three-part form is accepted so that a user
			// mirroring `docker -p` syntax is not stopped by a parse error
			// when what they wrote is actually unambiguous.
			name:  "explicit matching container port",
			entry: "db:5432:5432",
			want:  config.HostService{Name: "db", Host: 5432, Container: intPtr(5432)},
		},
		{
			name:    "remap is refused with the reason",
			entry:   "db:5432:6000",
			wantErr: "cannot differ",
		},
		{
			name:    "reserved host alias",
			entry:   "host.docker.internal:5432",
			wantErr: "built-in name for the host",
		},
		{name: "missing port", entry: "db", wantErr: "expected name:port"},
		{name: "empty name", entry: ":5432", wantErr: "missing service name"},
		{name: "non-numeric port", entry: "db:abc", wantErr: "not a number"},
		{name: "port out of range", entry: "db:70000", wantErr: "not a valid TCP port"},
		{name: "port zero", entry: "db:0", wantErr: "not a valid TCP port"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseHostServiceFlag(tc.entry)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseHostServiceFlag(%q) = %+v, want error containing %q", tc.entry, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseHostServiceFlag(%q) error = %v, want it to contain %q", tc.entry, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseHostServiceFlag(%q) unexpected error: %v", tc.entry, err)
			}
			if got.Name != tc.want.Name || got.Host != tc.want.Host {
				t.Errorf("ParseHostServiceFlag(%q) = %+v, want %+v", tc.entry, got, tc.want)
			}
			if (got.Container == nil) != (tc.want.Container == nil) {
				t.Errorf("ParseHostServiceFlag(%q) Container = %v, want %v", tc.entry, got.Container, tc.want.Container)
			}
		})
	}
}

// The flag layer must win over the repo's own declaration by name rather
// than being appended to it — appending would produce two entries for one
// hostname, which LoadRepoConfig rejects outright, so an append bug would
// surface as "your config is invalid" on a config the user never wrote.
func TestResolveHostServicesLocalOverridesRepoByName(t *testing.T) {
	repo := []config.HostService{
		{Name: "db", Host: 5432},
		{Name: "cache", Host: 6379},
	}
	local := []config.HostService{{Name: "db", Host: 15432}}

	got := resolveHostServices(repo, local)

	want := []engine.HostService{
		{Name: "cache", HostPort: 6379, ContainerPort: 6379},
		{Name: "db", HostPort: 15432, ContainerPort: 15432},
	}
	if len(got) != len(want) {
		t.Fatalf("resolveHostServices returned %d entries (%+v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Order must be stable for identical input: this feeds /etc/hosts and the
// generated compose override, and a map-ranged order would make both
// differ run to run, turning any diff of the override into noise.
func TestResolveHostServicesIsStablyOrdered(t *testing.T) {
	repo := []config.HostService{
		{Name: "zulu", Host: 9000},
		{Name: "alpha", Host: 9001},
		{Name: "mike", Host: 9002},
	}

	first := resolveHostServices(repo, nil)
	for i := 0; i < 20; i++ {
		again := resolveHostServices(repo, nil)
		for j := range first {
			if first[j] != again[j] {
				t.Fatalf("ordering is not stable: run %d entry %d = %+v, first run had %+v", i, j, again[j], first[j])
			}
		}
	}
	if first[0].Name != "alpha" || first[2].Name != "zulu" {
		t.Errorf("expected name-sorted order, got %+v", first)
	}
}

func TestResolveHostServicesDefaultsContainerPortToHost(t *testing.T) {
	got := resolveHostServices([]config.HostService{{Name: "db", Host: 5432}}, nil)
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].ContainerPort != 5432 {
		t.Errorf("ContainerPort = %d, want it to default to the host port 5432", got[0].ContainerPort)
	}
}

func TestResolveHostServicesEmpty(t *testing.T) {
	if got := resolveHostServices(nil, nil); len(got) != 0 {
		t.Errorf("resolveHostServices(nil, nil) = %+v, want empty", got)
	}
}
